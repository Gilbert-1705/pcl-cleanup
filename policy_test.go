package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prusa3d/prusa-connect/sdk/accessapi"
	"github.com/prusa3d/prusa-connect/sdk/deviceapi"
	"github.com/prusa3d/prusa-connect/sdk/routesapi"
)

// fakeAccess answers access.check from a table keyed
// "subject action object_type/object_id" and keeps the questions asked.
type fakeAccess struct {
	mu    sync.Mutex
	allow map[string]bool
	asked []string
	fail  error
}

func (f *fakeAccess) Check(_ context.Context, p accessapi.CheckParams) (accessapi.Decision, error) {
	key := p.Subject + " " + p.Action + " " + p.ObjectType + "/" + p.ObjectID

	f.mu.Lock()
	defer f.mu.Unlock()

	f.asked = append(f.asked, key)
	if f.fail != nil {
		return accessapi.Decision{}, f.fail
	}

	return accessapi.Decision{Allowed: f.allow[key]}, nil
}

// call sends one request to the module's routes as the host does, with the
// principal the host authorized.
func call(t *testing.T, h http.Handler, principal, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()

	path, query, _ := strings.Cut(target, "?")

	r, err := routesapi.Request{Method: method, Path: path, Query: query, Body: []byte(body), Principal: principal}.HTTP(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)

	return rec
}

const snapshotRule = `{"id":"s","name":"Snapshots","enabled":true,"storage":"local","path":"camera","mode":"files","pattern":"*.jpg","keep_days":14}`

func newModule(t *testing.T, settings Settings, access *fakeAccess) (*module, http.Handler) {
	t.Helper()

	m := &module{files: storageWith(), access: access, printers: fakePrinterOne()}
	if err := m.start(context.Background(), settings, t.TempDir()); err != nil {
		t.Fatal(err)
	}

	return m, m.routes()
}

func TestSavingARuleNeedsThePersonsOwnRightToDelete(t *testing.T) {
	access := &fakeAccess{allow: map[string]bool{"user:anna write_storage resource_kind/storage": true}}
	m, h := newModule(t, Settings{AutoInterval: "0s", AllowedStorages: []string{"local"}}, access)

	if rec := call(t, h, "user:ben", "PUT", "/rules", "["+snapshotRule+"]"); rec.Code != http.StatusForbidden {
		t.Fatalf("a person without write_storage saved a rule: %d %s", rec.Code, rec.Body)
	}

	if rec := call(t, h, "", "PUT", "/rules", "["+snapshotRule+"]"); rec.Code != http.StatusForbidden {
		t.Fatalf("a request without a person saved a rule: %d %s", rec.Code, rec.Body)
	}

	for _, q := range access.asked {
		if strings.HasPrefix(q, " ") {
			t.Fatalf("the module asked about itself: %q", q)
		}
	}

	if rec := call(t, h, "user:anna", "PUT", "/rules", "["+snapshotRule+"]"); rec.Code != http.StatusOK {
		t.Fatalf("anna could not save: %d %s", rec.Code, rec.Body)
	}

	rules, err := m.rules.load()
	if err != nil || len(rules) != 1 || rules[0].SavedBy != "user:anna" {
		t.Fatalf("saved %+v %v", rules, err)
	}
}

func TestAnUnchangedRuleKeepsItsAuthorAndAForgedAuthorIsIgnored(t *testing.T) {
	access := &fakeAccess{allow: map[string]bool{"user:anna write_storage resource_kind/storage": true}}
	m, h := newModule(t, Settings{AutoInterval: "0s", AllowedStorages: []string{"local"}}, access)

	if rec := call(t, h, "user:anna", "PUT", "/rules", "["+snapshotRule+"]"); rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}

	// Ben may not delete, but he only adds nothing and claims the rule.
	forged := strings.Replace(snapshotRule, `"id":"s"`, `"id":"s","saved_by":"user:ben"`, 1)
	if rec := call(t, h, "user:ben", "PUT", "/rules", "["+forged+"]"); rec.Code != http.StatusOK {
		t.Fatalf("an unchanged list was refused: %d %s", rec.Code, rec.Body)
	}

	rules, _ := m.rules.load()
	if rules[0].SavedBy != "user:anna" {
		t.Fatalf("the author became %q", rules[0].SavedBy)
	}

	// Changing the rule makes it Ben's, and Ben may not.
	changed := strings.Replace(snapshotRule, `"keep_days":14`, `"keep_days":1`, 1)
	if rec := call(t, h, "user:ben", "PUT", "/rules", "["+changed+"]"); rec.Code != http.StatusForbidden {
		t.Fatalf("ben changed anna's rule: %d %s", rec.Code, rec.Body)
	}
}

func TestTheOperatorsListIsTheOuterLimit(t *testing.T) {
	access := &fakeAccess{allow: map[string]bool{"user:anna write_storage resource_kind/storage": true}}
	_, h := newModule(t, Settings{AutoInterval: "0s", AllowedStorages: []string{"archive"}}, access)

	if rec := call(t, h, "user:anna", "PUT", "/rules", "["+snapshotRule+"]"); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("a rule outside allowed_storages was answered %d %s", rec.Code, rec.Body)
	}

	if rec := call(t, h, "user:anna", "GET", "/usage?storage=local", ""); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("usage outside allowed_storages was answered %d", rec.Code)
	}

	if !allowedIn([]string{"*"}, "anything") || allowedIn(nil, "local") {
		t.Fatal("the * entry or the empty list is read wrong")
	}
}

func TestTheAutomaticRunDeletesOnlyForAnAuthorWhoStillMay(t *testing.T) {
	var deleted []string

	var mu sync.Mutex

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Paths []string }
		_ = json.NewDecoder(r.Body).Decode(&body)

		mu.Lock()
		deleted = append(deleted, body.Paths...)
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	access := &fakeAccess{allow: map[string]bool{"user:anna write_storage resource_kind/storage": true}}
	settings := Settings{AutoInterval: "6h", APIToken: "pcl_x", CoreURL: server.URL, AllowedStorages: []string{"local"}}

	if err := settings.check(); err != nil {
		t.Fatal(err)
	}

	m, h := newModule(t, settings, access)

	// The defaults have no author: nothing runs, and the page is told why.
	m.runAll(context.Background(), "automatisch")

	if len(deleted) != 0 || len(m.skipped) != 2 {
		t.Fatalf("defaults ran: %d deleted, skipped %v", len(deleted), m.skipped)
	}

	if rec := call(t, h, "user:anna", "PUT", "/rules", "["+snapshotRule+"]"); rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}

	m.runAll(context.Background(), "automatisch")

	// The run plans with the real clock, the fixture is dated by now, so count
	// what a plan right now would delete.
	rules, _ := m.rules.load()
	want := planRule(context.Background(), storageWith(), rules[0], "", time.Now()).DeleteUnits

	if want < 15 || len(deleted) != want || len(m.skipped) != 0 {
		t.Fatalf("anna's rule: %d deleted, want %d, skipped %v", len(deleted), want, m.skipped)
	}

	for _, p := range deleted {
		if !strings.HasPrefix(p, "camera/") || !strings.HasSuffix(p, ".jpg") {
			t.Fatalf("deleted %s", p)
		}
	}

	// Anna loses the right: the rule is skipped, nothing more is deleted.
	access.mu.Lock()
	access.allow = map[string]bool{}
	access.mu.Unlock()

	deleted = nil
	m.runAll(context.Background(), "automatisch")

	if len(deleted) != 0 || !strings.Contains(m.skipped["s"], "may not delete") {
		t.Fatalf("after anna lost the right: %d deleted, skipped %v", len(deleted), m.skipped)
	}

	// The server cannot answer: skipped as well, never deleted.
	access.fail = errors.New("unreachable")
	m.runAll(context.Background(), "automatisch")

	if len(deleted) != 0 || m.skipped["s"] == "" {
		t.Fatalf("with no answer: %d deleted, skipped %v", len(deleted), m.skipped)
	}
}

func TestTheHistoryOnlyTakesRunsOfExistingRules(t *testing.T) {
	access := &fakeAccess{}
	_, h := newModule(t, Settings{AutoInterval: "0s", AllowedStorages: []string{"local"}}, access)

	for body, want := range map[string]int{
		`{"rule":"Kamera-Snapshots","units":3,"files":3,"freed":3000}`: http.StatusCreated,
		`{"rule":"Erfunden","units":3}`:                                http.StatusUnprocessableEntity,
		`{"rule":"Kamera-Snapshots","units":-1}`:                       http.StatusUnprocessableEntity,
		`{"rule":""}`:                                                  http.StatusUnprocessableEntity,
	} {
		if rec := call(t, h, "user:anna", "POST", "/runs", body); rec.Code != want {
			t.Errorf("%s: %d, want %d", body, rec.Code, want)
		}
	}

	long := RunRecord{Rule: "x", Error: strings.Repeat("ä", 400)}
	if err := long.clean(); err != nil || len(long.Error) > maxRecordError+5 || !strings.HasSuffix(long.Error, "…") {
		t.Fatalf("a long error was kept as %d bytes, %v", len(long.Error), err)
	}
}

// fakePrinters is one printer with a USB drive. Printing says whether a print
// is on the bed.
type fakePrinters struct {
	device   deviceapi.Device
	printing bool
	last     string
	files    []deviceapi.File
	read     int
}

func (f *fakePrinters) Get(_ context.Context, p deviceapi.GetParams) (deviceapi.Device, error) {
	if p.ID != f.device.ID {
		return deviceapi.Device{}, deviceapi.ErrNoDevice
	}

	return f.device, nil
}

func (f *fakePrinters) Jobs(_ context.Context, _ deviceapi.JobsParams) (deviceapi.Jobs, error) {
	var jobs deviceapi.Jobs

	if f.printing {
		jobs.Current = &deviceapi.Job{Path: "usb/druckt-gerade.bgcode", Started: now}
	}

	if f.last != "" {
		ended := now
		jobs.Last = &deviceapi.Job{Path: f.last, Started: now, Ended: &ended}
	}

	return jobs, nil
}

func (f *fakePrinters) Files(_ context.Context, p deviceapi.FilesParams) (deviceapi.Listing, error) {
	f.read++

	if p.Path != "usb" {
		return deviceapi.Listing{}, deviceapi.ErrInvalidQuery
	}

	end := min(p.Offset+p.Limit, len(f.files))

	return deviceapi.Listing{
		Entry: &deviceapi.File{Path: "usb", Folder: true},
		Items: f.files[p.Offset:end], Total: len(f.files), Limit: p.Limit, Offset: p.Offset,
	}, nil
}

func fakePrinterOne() *fakePrinters {
	f := &fakePrinters{device: deviceapi.Device{ID: "dev-1", Name: "Core One", Connected: true}}

	// The printer reports the short FAT name in Path and Name and the long
	// name in DisplayName.
	for d := range 250 {
		short := fmt.Sprintf("TEIL%04d.BGC", d)
		f.files = append(f.files, deviceapi.File{
			Path: "usb/" + short, Name: short, DisplayName: fmt.Sprintf("teil-%04d-halter.bgcode", d),
			Size: 1000, Modified: daysAgo(d).Unix(),
		})
	}

	f.files = append(f.files,
		deviceapi.File{Path: "usb/models", Name: "models", Folder: true},
		deviceapi.File{Path: "usb/firmware.bbf", Name: "firmware.bbf", Size: 1, Modified: daysAgo(900).Unix()},
		deviceapi.File{Path: "usb/ohne-datum.bgcode", Name: "ohne-datum.bgcode", Size: 1, Modified: 0},
		deviceapi.File{Path: "usb/geschuetzt.bgcode", Name: "geschuetzt.bgcode", Size: 1, Modified: daysAgo(900).Unix(), ReadOnly: true},
		deviceapi.File{Path: "usb/letzter.bgcode", Name: "letzter.bgcode", Size: 1, Modified: daysAgo(900).Unix()},
	)

	return f
}

func printerRule() Rule {
	return Rule{ID: "usb", Name: "USB", Target: targetPrinter, Device: "dev-1", Path: "usb", Mode: modeFiles,
		Pattern: "*.bgcode", KeepDays: 30, KeepNewest: 5}
}

func TestAPrinterRuleOffersOnlyOldPrintFiles(t *testing.T) {
	printers := fakePrinterOne()
	printers.last = "usb/letzter.bgcode"

	rule := printerRule()
	if err := rule.check(); err != nil {
		t.Fatal(err)
	}

	plan := planPrinter(context.Background(), printers, rule, "user:anna", now)
	if plan.Error != "" {
		t.Fatal(plan.Error)
	}

	// 250 dated print files over two pages; days 31 to 249 are older than 30
	// days (day 30 is the cutoff itself). Folders, firmware, files without a
	// date, read-only files and the last print stay.
	if plan.Units != 250 || plan.DeleteUnits != 219 || printers.read != 2 {
		t.Fatalf("units %d, delete %d, pages read %d", plan.Units, plan.DeleteUnits, printers.read)
	}

	for _, p := range plan.Delete {
		if !strings.HasPrefix(p, "usb/TEIL") {
			t.Fatalf("would delete %s", p)
		}
	}
}

func TestAPrinterRuleReadsNothingWhilePrinting(t *testing.T) {
	printers := fakePrinterOne()
	printers.printing = true

	plan := planPrinter(context.Background(), printers, printerRule(), "user:anna", now)
	if !strings.Contains(plan.Error, "printing") || printers.read != 0 || len(plan.Delete) != 0 {
		t.Fatalf("while printing: %+v, read %d", plan.Error, printers.read)
	}

	printers.printing = false
	printers.device.Connected = false

	if plan = planPrinter(context.Background(), printers, printerRule(), "user:anna", now); !strings.Contains(plan.Error, "not connected") {
		t.Fatalf("away: %q", plan.Error)
	}

	if plan = planPrinter(context.Background(), printers, printerRule(), "", now); plan.Error == "" {
		t.Fatal("a plan without a person read the printer")
	}
}

func TestPrinterRulesAreNarrow(t *testing.T) {
	for name, change := range map[string]func(*Rule){
		"no printer":        func(r *Rule) { r.Device = "" },
		"any file":          func(r *Rule) { r.Pattern = "*" },
		"no pattern":        func(r *Rule) { r.Pattern = "" },
		"wildcard suffix":   func(r *Rule) { r.Pattern = "*.bgcod?" },
		"folders":           func(r *Rule) { r.Mode = modeFolders; r.Depth = 1 },
		"automatic":         func(r *Rule) { r.Enabled = true },
		"printer root":      func(r *Rule) { r.Path = "/" },
		"with a storage":    func(r *Rule) { r.Storage = "local" },
		"unknown target":    func(r *Rule) { r.Target = "cloud" },
		"storage w/ device": func(r *Rule) { r.Target = targetStorage; r.Storage = "local"; r.Device = "dev-1" },
	} {
		rule := printerRule()
		change(&rule)

		if err := rule.check(); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}

	for _, pattern := range []string{"*.bgcode", "*.gcode", "*.BGCODE", "teil-*.bgcode"} {
		rule := printerRule()
		rule.Pattern = pattern

		if err := rule.check(); err != nil {
			t.Errorf("%s was refused: %v", pattern, err)
		}
	}
}

func TestAPrinterRuleNeedsOperateOnThatPrinterAndIsNeverAutomatic(t *testing.T) {
	access := &fakeAccess{allow: map[string]bool{"user:anna operate device/dev-1": true}}
	settings := Settings{AutoInterval: "0s", AllowedStorages: []string{"local"}, AllowedDevices: []string{"dev-1"}}
	m, h := newModule(t, settings, access)

	body, _ := json.Marshal([]Rule{printerRule()})

	if rec := call(t, h, "user:ben", "PUT", "/rules", string(body)); rec.Code != http.StatusForbidden {
		t.Fatalf("ben saved a printer rule: %d %s", rec.Code, rec.Body)
	}

	if rec := call(t, h, "user:anna", "PUT", "/rules", string(body)); rec.Code != http.StatusOK {
		t.Fatalf("anna could not save: %d %s", rec.Code, rec.Body)
	}

	// The overview does not read the printer; asking for the rule alone does.
	rec := call(t, h, "user:anna", "GET", "/plan", "")

	var plans struct{ Plans []RulePlan }
	if err := json.Unmarshal(rec.Body.Bytes(), &plans); err != nil || len(plans.Plans) != 1 || !plans.Plans[0].Lazy {
		t.Fatalf("overview: %s", rec.Body)
	}

	rec = call(t, h, "user:anna", "GET", "/plan?rule=usb", "")

	var one struct{ Plans []RulePlan }
	if err := json.Unmarshal(rec.Body.Bytes(), &one); err != nil || len(one.Plans) != 1 || one.Plans[0].Lazy || one.Plans[0].DeleteUnits == 0 {
		t.Fatalf("one rule: %s", rec.Body)
	}

	rules, _ := m.rules.load()
	if err := mayRunAutomatically(context.Background(), access, settings, rules[0]); err == nil {
		t.Fatal("a printer rule may run automatically")
	}

	// Outside allowed_devices the rule is refused.
	m.settings.AllowedDevices = nil

	other := printerRule()
	other.ID, other.KeepDays = "usb2", 60
	body, _ = json.Marshal([]Rule{rules[0], other})

	if rec := call(t, h, "user:anna", "PUT", "/rules", string(body)); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("a rule outside allowed_devices was answered %d %s", rec.Code, rec.Body)
	}
}
