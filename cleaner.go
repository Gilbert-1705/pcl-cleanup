package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// deleteBatch is how many paths go into one deletion request.
const deleteBatch = 200

// historyLength is how many runs the page lists.
const historyLength = 30

// Settings is the module's configuration.
//
// AllowedStorages and AllowedDevices are the operator's outer limit: rules may
// only point at the storages and printers they name, "*" naming all. See
// policy.go.
//
// PrinterAuto lets printer rules run on the schedule too (auto_printer.go).
// PrinterAutoMax bounds the files one such run deletes per rule, and
// PrinterAutoDryRun, on unless set to false, has the run only write down what
// it would delete. Both are pointers so that a missing value, as after an
// update that did not ask for the new options, takes the safe default.
type Settings struct {
	CoreURL           string   `json:"core_url"`
	APIToken          string   `json:"api_token"`
	AutoInterval      string   `json:"auto_interval"`
	AllowedStorages   []string `json:"allowed_storages"`
	AllowedDevices    []string `json:"allowed_devices"`
	PrinterAuto       bool     `json:"printer_auto"`
	PrinterAutoMax    *int     `json:"printer_auto_max"`
	PrinterAutoDryRun *bool    `json:"printer_auto_dry_run"`

	interval   time.Duration
	printerMax int
	dryRun     bool
}

// Bounds of printer_auto_max.
const (
	defaultPrinterMax = 50
	highestPrinterMax = 500
)

var errSettings = errors.New("the cleanup settings are not usable")

func (s *Settings) check() error {
	every, err := time.ParseDuration(s.AutoInterval)
	if err != nil || (every != 0 && every < 10*time.Minute) {
		return fmt.Errorf("%w: auto_interval is %q and has to be 0 (off) or at least 10m", errSettings, s.AutoInterval)
	}

	s.interval = every
	if s.CoreURL != "" {
		if s.CoreURL, err = checkCoreURL(s.CoreURL); err != nil {
			return err
		}
	}

	var err2 error
	if s.AllowedStorages, err2 = cleanList("allowed_storages", s.AllowedStorages, 64); err2 != nil {
		return err2
	}

	if s.AllowedDevices, err2 = cleanList("allowed_devices", s.AllowedDevices, maxDeviceID); err2 != nil {
		return err2
	}

	s.printerMax = defaultPrinterMax
	if s.PrinterAutoMax != nil {
		s.printerMax = *s.PrinterAutoMax
	}

	if s.printerMax < 1 || s.printerMax > highestPrinterMax {
		return fmt.Errorf("%w: printer_auto_max is %d and has to be 1 to %d", errSettings, s.printerMax, highestPrinterMax)
	}

	s.dryRun = s.PrinterAutoDryRun == nil || *s.PrinterAutoDryRun

	return nil
}

// cleanList trims the entries of a list setting and refuses empty or overlong
// ones.
func cleanList(key string, list []string, longest int) ([]string, error) {
	out := make([]string, 0, len(list))

	for _, entry := range list {
		entry = strings.TrimSpace(entry)
		if entry == "" || len(entry) > longest {
			return nil, fmt.Errorf("%w: %s holds an empty entry or one longer than %d", errSettings, key, longest)
		}

		out = append(out, entry)
	}

	return out, nil
}

// automatic reports whether storage rules run on the schedule: they delete
// through the HTTP API with the token.
func (s Settings) automatic() bool {
	return s.interval > 0 && s.APIToken != "" && s.CoreURL != ""
}

// printerAutomatic reports whether printer rules run on the schedule. They
// need no token: they read and delete through the module protocol, as the
// module itself, which the operator has to make an operator of each printer.
func (s Settings) printerAutomatic() bool {
	return s.interval > 0 && s.PrinterAuto
}

// scheduled reports whether anything runs on the schedule.
func (s Settings) scheduled() bool { return s.automatic() || s.printerAutomatic() }

// RunRecord is one cleanup, by hand or on the schedule.
type RunRecord struct {
	At      time.Time `json:"at"`
	By      string    `json:"by"`
	Rule    string    `json:"rule"`
	Units   int       `json:"units"`
	Files   int       `json:"files"`
	Freed   int64     `json:"freed"`
	Error   string    `json:"error,omitempty"`
	Partial bool      `json:"partial,omitempty"`
	// Deleted names each file an automatic printer run deleted, or would
	// have deleted in a dry run.
	Deleted []string `json:"deleted,omitempty"`
	DryRun  bool     `json:"dry_run,omitempty"`
}

// Bounds on a run the page reports.
const (
	maxRecordRule  = 80
	maxRecordError = 500
)

// clean holds a run the page reports to the bounds the page itself can
// produce, so the history shows what happened and nothing longer. The page
// reports what it did with the person's own rights; the server checked those
// when it deleted.
func (r *RunRecord) clean() error {
	r.Rule = strings.TrimSpace(r.Rule)
	r.Error = strings.TrimSpace(r.Error)

	switch {
	case r.Rule == "" || len(r.Rule) > maxRecordRule:
		return errors.New("rule is empty or longer than 80")
	case r.Units < 0 || r.Files < 0 || r.Freed < 0:
		return errors.New("units, files and freed cannot be negative")
	}

	// Only the module writes the files of an automatic run.
	r.Deleted, r.DryRun = nil, false

	if len(r.Error) > maxRecordError {
		r.Error = strings.ToValidUTF8(r.Error[:maxRecordError], "") + " …"
	}

	return nil
}

type history struct {
	mu   sync.Mutex
	path string
	runs []RunRecord
}

func loadHistory(path string) *history {
	h := &history{path: path}

	if raw, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(raw, &h.runs)
	}

	return h
}

func (h *history) add(r RunRecord) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.runs = append([]RunRecord{r}, h.runs...)
	if len(h.runs) > historyLength {
		h.runs = h.runs[:historyLength]
	}

	if raw, err := json.MarshalIndent(h.runs, "", "  "); err == nil {
		_ = os.WriteFile(h.path, raw, 0o640)
	}
}

func (h *history) list() []RunRecord {
	h.mu.Lock()
	defer h.mu.Unlock()

	return append([]RunRecord{}, h.runs...)
}

// deleter removes entries through the server's own HTTP API, with the token
// the operator gave the module. The module protocol can write and read files
// but not delete them, so this is the only way to delete with nobody on the
// page.
type deleter struct {
	base   string
	token  string
	client *http.Client
}

func (d deleter) remove(ctx context.Context, storage string, paths []string) (int, error) {
	done := 0

	for start := 0; start < len(paths); start += deleteBatch {
		batch := paths[start:min(start+deleteBatch, len(paths))]

		body, _ := json.Marshal(map[string]any{"paths": batch})
		address := d.base + "/api/v1/storages/" + url.PathEscape(storage) + "/deletions"

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, address, bytes.NewReader(body))
		if err != nil {
			return done, err
		}

		req.Header.Set("Authorization", "Bearer "+d.token)
		req.Header.Set("Content-Type", "application/json")

		resp, err := d.client.Do(req)
		if err != nil {
			return done, err
		}

		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		resp.Body.Close()

		if resp.StatusCode/100 != 2 {
			return done, fmt.Errorf("the server answered %s: %s", resp.Status, strings.TrimSpace(string(detail)))
		}

		done += len(batch)
	}

	return done, nil
}
