// Command cleanup keeps the storages of Prusa Connect Local from filling
// up with camera pictures: snapshots, timelapses and the pictures other
// extensions leave behind.
//
// Rules say which folder of which storage keeps how much. The page shows what
// each rule would delete and which active rules are due, and deletes with the
// signed-in person's rights. The module protocol cannot delete in a storage,
// so storage rules never run on their own; see ROADMAP.md. Rules on a
// printer's own storage run by hand (printer.go), and on the schedule only
// when the operator turns that on (auto_printer.go), for each rule only while
// the person who saved it may still delete there (policy.go).
package main

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"os/signal"
	"path"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/prusa3d/prusa-connect/sdk/accessapi"
	"github.com/prusa3d/prusa-connect/sdk/deviceapi"
	"github.com/prusa3d/prusa-connect/sdk/extension"
	"github.com/prusa3d/prusa-connect/sdk/routesapi"
	"github.com/prusa3d/prusa-connect/sdk/storageapi"
	"github.com/prusa3d/prusa-connect/sdk/wire"
	"github.com/prusa3d/prusa-connect/sdk/wirerpc"
)

const (
	name    = "cleanup"
	release = extension.Version("0.5.0")
)

//go:embed openapi.json
var openAPI []byte

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", name, err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	line, err := wire.Handshake{
		Protocol: wire.ProtocolMajor,
		MinMinor: wire.MinMinor,
		MaxMinor: wire.MaxMinor,
		Address:  wire.StdioAddress,
	}.Line()
	if err != nil {
		return err
	}

	if _, err = os.Stdout.WriteString(line); err != nil {
		return err
	}

	m := &module{}
	methods := wirerpc.Methods{}

	conn, err := wirerpc.NewConn(stdio{}, wirerpc.Options{Role: wirerpc.RoleChild, Methods: methods})
	if err != nil {
		return err
	}

	m.files = storageapi.NewClient(conn)
	m.printers = deviceapi.NewClient(conn)
	m.access = accessapi.NewClient(conn)

	methods[wirerpc.MethodInfo] = func(context.Context, wirerpc.Call) (any, error) {
		return routesapi.Describe(wirerpc.Info{
			Name: name, Version: release, Contract: wirerpc.Contract,
			Serves: []wirerpc.Method{wirerpc.MethodConfigure},
		}, openAPI)
	}
	methods[wirerpc.MethodShutdown] = func(context.Context, wirerpc.Call) (any, error) {
		stop()

		return nil, nil
	}
	methods[wirerpc.MethodConfigure] = wirerpc.ConfigureGranted(
		func(_ context.Context, held Settings, grants wirerpc.Grants) error {
			if err := held.check(); err != nil {
				return err
			}

			if grants.Data == "" {
				return fmt.Errorf("%w: the module was installed without its data directory; install it with -approve", errSettings)
			}

			return m.start(ctx, held, grants.Data)
		})
	methods[routesapi.MethodServe] = routesapi.Serve(m.routes())

	fmt.Fprintf(os.Stderr, "%s %s is serving on stdio as pid %d\n", name, release, os.Getpid())

	if err = conn.Serve(ctx); err != nil && ctx.Err() == nil {
		return err
	}

	m.wg.Wait()

	return nil
}

type module struct {
	once sync.Once
	wg   sync.WaitGroup

	files    lister
	printers devices
	access   checker
	settings Settings
	rules    rulesFile
	history  *history

	// saving keeps two saves of the rules from running into each other: each
	// one loads, checks and writes the list as a whole.
	saving sync.Mutex

	mu      sync.Mutex
	usage   map[string]Usage
	nextRun time.Time
	running bool
	// skipped is why the last automatic run left a rule out, by rule id.
	skipped map[string]string
}

func (m *module) start(ctx context.Context, settings Settings, dir string) error {
	var err error

	m.once.Do(func() {
		m.settings = settings
		m.rules = rulesFile{path: dataPath(dir, "regeln.json")}
		m.history = loadHistory(dataPath(dir, "verlauf.json"))
		m.usage = map[string]Usage{}
		m.skipped = map[string]string{}

		if _, err = m.rules.load(); err != nil {
			err = fmt.Errorf("regeln.json in the data directory: %w", err)

			return
		}

		if settings.scheduled() {
			m.wg.Add(1)

			go func() {
				defer m.wg.Done()
				m.schedule(ctx)
			}()
		}

		if settings.interval > 0 && !settings.PrinterAuto {
			fmt.Fprintf(os.Stderr, "%s: auto_interval is set but printer_auto is off, so nothing runs on its own; storage rules run from the page\n", name)
		}
	})

	return err
}

// schedule runs every active printer rule each auto_interval, starting a few
// minutes after the module starts, when the server has settled.
func (m *module) schedule(ctx context.Context) {
	next := time.Now().Add(5 * time.Minute)

	for {
		m.mu.Lock()
		m.nextRun = next
		m.mu.Unlock()

		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Until(next)):
		}

		m.runAll(ctx, bySchedule)

		next = time.Now().Add(m.settings.interval)
	}
}

func (m *module) runAll(ctx context.Context, by string) []RunRecord {
	m.mu.Lock()
	if m.running {
		m.mu.Unlock()

		return nil
	}

	m.running = true
	m.mu.Unlock()

	defer func() {
		m.mu.Lock()
		m.running = false
		m.mu.Unlock()
	}()

	rules, err := m.rules.load()
	if err != nil {
		record := RunRecord{At: time.Now(), By: by, Rule: "*", Error: err.Error()}
		m.history.add(record)

		return []RunRecord{record}
	}

	var out []RunRecord

	skipped := map[string]string{}

	for _, rule := range rules {
		// Storage rules run from the page only: the module protocol cannot
		// delete in a storage (ROADMAP.md).
		if !rule.Enabled || !rule.printer() {
			continue
		}

		if err := mayRunAutomatically(ctx, m.access, m.settings, rule); err != nil {
			skipped[rule.ID] = err.Error()
			fmt.Fprintf(os.Stderr, "%s: %s: skipped: %v\n", name, rule.Name, err)

			continue
		}

		record, skip := runPrinter(ctx, m.printers, m.files, rule, m.settings, by, time.Now())
		if skip != nil {
			skipped[rule.ID] = skip.Error()
			fmt.Fprintf(os.Stderr, "%s: %s: skipped: %v\n", name, rule.Name, skip)

			continue
		}

		if record.Units == 0 && record.Error == "" {
			continue
		}

		m.history.add(record)
		out = append(out, record)
		fmt.Fprintf(os.Stderr, "%s: %s: %d files, %d bytes, dry run %v %s\n", name, rule.Name, record.Units, record.Freed, record.DryRun, record.Error)
	}

	m.mu.Lock()
	m.skipped = skipped
	m.mu.Unlock()

	m.forgetUsage()

	return out
}

func (m *module) forgetUsage() {
	m.mu.Lock()
	m.usage = map[string]Usage{}
	m.mu.Unlock()
}

// State is what the page reads first.
type State struct {
	Rules []Rule `json:"rules"`
	// RulesVersion names the stored rules. PUT /rules has to send it back in
	// If-Match, so a save made on an older list is refused.
	RulesVersion string      `json:"rules_version"`
	Interval     string      `json:"interval"`
	NextRun      time.Time   `json:"next_run,omitzero"`
	Running      bool        `json:"running"`
	History      []RunRecord `json:"history"`
	// Skipped is why the last automatic run left a rule out, by rule id.
	Skipped map[string]string `json:"skipped,omitempty"`
	// AllowedStorages and AllowedDevices are the operator's lists, so the page
	// offers only what may be saved.
	AllowedStorages []string `json:"allowed_storages"`
	AllowedDevices  []string `json:"allowed_devices"`
	// PrinterAuto says printer rules may be active; PrinterAutomatic that they
	// also run, because auto_interval is set.
	PrinterAuto      bool   `json:"printer_auto"`
	PrinterAutomatic bool   `json:"printer_automatic"`
	PrinterAutoMax   int    `json:"printer_auto_max"`
	DryRun           bool   `json:"printer_auto_dry_run"`
	Error            string `json:"error,omitempty"`
}

func (m *module) routes() http.Handler {
	mux := http.NewServeMux()

	// Not /state: the host keeps /api/v1/modules/<namespace>/state for the
	// module's records and answers it itself.
	mux.HandleFunc("GET /overview", func(w http.ResponseWriter, r *http.Request) {
		if m.history == nil {
			answer(w, http.StatusServiceUnavailable, map[string]string{"error": "the module is still starting"})

			return
		}

		rules, err := m.rules.load()
		state := State{
			AllowedStorages: m.settings.AllowedStorages,
			PrinterAuto:     m.settings.PrinterAuto, PrinterAutomatic: m.settings.printerAutomatic(),
			PrinterAutoMax: m.settings.printerMax, DryRun: m.settings.dryRun,
		}

		if err != nil {
			state.Error = err.Error()
		} else {
			state.RulesVersion = rulesVersion(rules)
		}

		if m.settings.interval > 0 {
			state.Interval = m.settings.interval.String()
		}

		m.mu.Lock()
		state.NextRun, state.Running = m.nextRun, m.running
		skipped := map[string]string{}
		for id, why := range m.skipped {
			skipped[id] = why
		}
		m.mu.Unlock()

		// Only what the person may see (visibility.go).
		newViewer(r.Context(), m.access, routesapi.PrincipalOf(r)).
			overview(&state, rules, m.history.list(), skipped, m.settings.AllowedDevices)

		answer(w, http.StatusOK, state)
	})

	mux.HandleFunc("PUT /rules", func(w http.ResponseWriter, r *http.Request) {
		if err := wantJSON(r); err != nil {
			refuse(w, http.StatusUnsupportedMediaType, err)

			return
		}

		raw, err := io.ReadAll(io.LimitReader(r.Body, 256<<10))
		if err != nil {
			refuse(w, http.StatusBadRequest, err)

			return
		}

		var rules []Rule
		if err = json.Unmarshal(raw, &rules); err != nil {
			refuse(w, http.StatusBadRequest, fmt.Errorf("the body is not a list of rules: %w", err))

			return
		}

		// One save at a time, against the list it was made on.
		m.saving.Lock()
		defer m.saving.Unlock()

		kept, err := m.rules.load()
		if err != nil {
			// A broken file is replaced, but every rule is then new and checked.
			kept = nil
		}

		switch match := ifMatch(r); {
		case match == "":
			refuse(w, http.StatusPreconditionRequired, errors.New("send the rules_version of the overview in If-Match"))

			return
		case kept != nil && match != rulesVersion(kept):
			refuse(w, http.StatusPreconditionFailed, errors.New("the rules changed since they were read; load them again"))

			return
		}

		rules = keepHidden(rules, kept)

		if err = refuseUnused(rules); err != nil {
			refuse(w, http.StatusUnprocessableEntity, err)

			return
		}

		if err = checkRules(rules); err != nil {
			refuse(w, http.StatusUnprocessableEntity, err)

			return
		}

		rules, err = adopt(r.Context(), m.access, m.settings, routesapi.PrincipalOf(r), kept, rules)

		switch {
		case errors.Is(err, errNotAllowed):
			refuse(w, http.StatusUnprocessableEntity, err)

			return
		case errors.Is(err, errForbidden), errors.Is(err, errNobody):
			refuse(w, http.StatusForbidden, err)

			return
		case err != nil:
			refuse(w, http.StatusBadGateway, err)

			return
		}

		if err = m.rules.save(rules); err != nil {
			refuse(w, http.StatusInternalServerError, err)

			return
		}

		view := newViewer(r.Context(), m.access, routesapi.PrincipalOf(r))
		shown := make([]Rule, 0, len(rules))

		for _, rule := range rules {
			shown = append(shown, view.rule(rule))
		}

		answer(w, http.StatusOK, map[string]any{"rules": shown, "rules_version": rulesVersion(rules)})
	})

	mux.HandleFunc("GET /plan", func(w http.ResponseWriter, r *http.Request) {
		rules, err := m.rules.load()
		if err != nil {
			refuse(w, http.StatusInternalServerError, err)

			return
		}

		only := r.URL.Query().Get("rule")
		person := routesapi.PrincipalOf(r)
		view := newViewer(r.Context(), m.access, person)
		plans := []RulePlan{}

		for _, rule := range rules {
			if only != "" && rule.ID != only {
				continue
			}

			// A printer the person may not see is not read for them, and its
			// rule says nothing about it (visibility.go).
			if rule.printer() && !view.observes(rule.Device) {
				plans = append(plans, RulePlan{
					RuleID: rule.ID, Name: rule.Name, Target: rule.Target, Delete: []string{}, Error: errHiddenText,
				})

				continue
			}

			plan := m.planFor(r.Context(), rule, person, only != "")
			if only == "" {
				// The overview carries the counts; the paths come per rule.
				plan.Delete = nil
			}

			plans = append(plans, plan)
		}

		answer(w, http.StatusOK, map[string]any{"plans": plans})
	})

	mux.HandleFunc("GET /usage", func(w http.ResponseWriter, r *http.Request) {
		storage := strings.TrimSpace(r.URL.Query().Get("storage"))
		if storage == "" {
			refuse(w, http.StatusBadRequest, fmt.Errorf("storage names no storage"))

			return
		}

		if !allowedIn(m.settings.AllowedStorages, storage) {
			refuse(w, http.StatusUnprocessableEntity, fmt.Errorf("%w: the storage %s is not in allowed_storages", errNotAllowed, storage))

			return
		}

		// Listed with the person's rights, so the cache is per person.
		person := routesapi.PrincipalOf(r)
		key := person + "\x00" + storage

		m.mu.Lock()
		cached, ok := m.usage[key]
		m.mu.Unlock()

		if ok && r.URL.Query().Get("refresh") == "" && time.Since(cached.Scanned) < 10*time.Minute {
			answer(w, http.StatusOK, cached)

			return
		}

		usage, err := usageOf(r.Context(), m.files, storage, person, time.Now())
		if err != nil {
			refuse(w, http.StatusBadGateway, err)

			return
		}

		m.mu.Lock()
		m.usage[key] = usage
		m.mu.Unlock()

		answer(w, http.StatusOK, usage)
	})

	// The page picks the folder with the copies of a printer rule from here:
	// the folders one level below path, listed with the person's rights.
	mux.HandleFunc("GET /folders", func(w http.ResponseWriter, r *http.Request) {
		storage := strings.TrimSpace(r.URL.Query().Get("storage"))
		dir := strings.Trim(path.Clean("/"+r.URL.Query().Get("path")), "/")

		if !allowedIn(m.settings.AllowedStorages, storage) || storage == "" {
			refuse(w, http.StatusUnprocessableEntity, fmt.Errorf("%w: the storage %q is not in allowed_storages", errNotAllowed, storage))

			return
		}

		folders, err := foldersIn(r.Context(), m.files, storage, dir, routesapi.PrincipalOf(r))
		if errors.Is(err, errNobody) {
			refuse(w, http.StatusForbidden, err)

			return
		}

		if err != nil {
			refuse(w, http.StatusBadGateway, err)

			return
		}

		answer(w, http.StatusOK, folders)
	})

	// The page deletes with the person's own rights and reports what it did,
	// so the history covers both kinds of run.
	mux.HandleFunc("POST /runs", func(w http.ResponseWriter, r *http.Request) {
		if err := wantJSON(r); err != nil {
			refuse(w, http.StatusUnsupportedMediaType, err)

			return
		}

		var record RunRecord
		if err := json.NewDecoder(io.LimitReader(r.Body, 16<<10)).Decode(&record); err != nil {
			refuse(w, http.StatusBadRequest, err)

			return
		}

		if err := record.clean(); err != nil {
			refuse(w, http.StatusUnprocessableEntity, err)

			return
		}

		if rules, err := m.rules.load(); err == nil && !slices.ContainsFunc(rules, func(rule Rule) bool { return rule.Name == record.Rule }) {
			refuse(w, http.StatusUnprocessableEntity, fmt.Errorf("there is no rule named %q", record.Rule))

			return
		}

		record.At = time.Now()
		record.By = byHand + routesapi.PrincipalOf(r)
		m.history.add(record)
		m.forgetUsage()

		answer(w, http.StatusCreated, record)
	})

	mux.HandleFunc("POST /auto/run", func(w http.ResponseWriter, r *http.Request) {
		if err := wantJSON(r); err != nil {
			refuse(w, http.StatusUnsupportedMediaType, err)

			return
		}

		if !m.settings.scheduled() {
			refuse(w, http.StatusConflict, fmt.Errorf("the automatic printer run needs auto_interval and printer_auto in the settings"))

			return
		}

		answer(w, http.StatusOK, map[string]any{"runs": m.runAll(r.Context(), byRequest)})
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})

	return mux
}

// planFor plans one rule for the person on the page. A storage rule outside
// allowed_storages, or a printer rule outside allowed_devices, answers only
// that. A printer rule is read only when asked for alone (full), because
// reading a printer is slow and occupies it.
func (m *module) planFor(ctx context.Context, rule Rule, person string, full bool) RulePlan {
	if err := m.settings.placeAllowed(rule); err != nil {
		return RulePlan{
			RuleID: rule.ID, Name: rule.Name, Target: rule.Target, Storage: rule.Storage, Device: rule.Device,
			Path: rule.Path, Enabled: rule.Enabled, Delete: []string{}, Error: err.Error(),
		}
	}

	if !rule.printer() {
		return planRule(ctx, m.files, rule, person, time.Now())
	}

	if !full {
		return RulePlan{
			RuleID: rule.ID, Name: rule.Name, Target: targetPrinter, Device: rule.Device, Path: rule.Path,
			Delete: []string{}, Lazy: true,
		}
	}

	return planPrinter(ctx, m.printers, m.files, rule, person, time.Now())
}

func answer(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func refuse(w http.ResponseWriter, status int, err error) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"type": "about:blank", "status": status, "code": "cleanup/REFUSED",
		"title": "the request was refused", "detail": err.Error(), "params": map[string]any{}, "retryable": false,
	})
}

type stdio struct{}

func (stdio) Read(p []byte) (int, error)  { return os.Stdin.Read(p) }
func (stdio) Write(p []byte) (int, error) { return os.Stdout.Write(p) }
func (stdio) Close() error                { return os.Stdin.Close() }

// rulesVersion names a list of rules: the first 16 hex digits of the SHA-256
// of its JSON. Two saves made on the same list carry the same version, and
// the second one is refused (PUT /rules).
func rulesVersion(rules []Rule) string {
	raw, err := json.Marshal(rules)
	if err != nil {
		return ""
	}

	sum := sha256.Sum256(raw)

	return hex.EncodeToString(sum[:8])
}

// ifMatch is the version a request names in If-Match, without quotes and a
// weak marker.
func ifMatch(r *http.Request) string {
	value := strings.TrimSpace(r.Header.Get("If-Match"))
	value = strings.TrimPrefix(value, "W/")

	return strings.Trim(value, `"`)
}

// wantJSON refuses a request that does not say it sends JSON. A browser sends
// a form or text/plain to another site without asking first, but never
// application/json, so this keeps such requests out of the routes that change
// something.
func wantJSON(r *http.Request) error {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		return errors.New("send the body as application/json")
	}

	return nil
}
