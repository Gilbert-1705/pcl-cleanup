package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"
)

func checked(t *testing.T, s Settings) Settings {
	t.Helper()

	if err := s.check(); err != nil {
		t.Fatal(err)
	}

	return s
}

func ptr[T any](v T) *T { return &v }

// autoPrinter is a module with printer_auto on, the copies of every print
// file in place, and an active printer rule saved by anna, who may operate
// the printer.
func autoPrinter(t *testing.T, change func(*Settings)) (*module, *fakePrinters, *fakeAccess) {
	t.Helper()

	settings := Settings{
		AutoInterval: "0s", AllowedStorages: []string{"local"}, AllowedDevices: []string{"dev-1"},
		PrinterAuto: true,
	}
	if change != nil {
		change(&settings)
	}

	// anna may also delete in the storages: saving only the printer rule
	// removes the default rules.
	access := &fakeAccess{allow: map[string]bool{
		"user:anna operate device/dev-1": true, "user:anna write_storage resource_kind/storage": true,
		"user:anna observe device/dev-1": true,
	}}
	m, h := newModule(t, checked(t, settings), access)

	printers := usbPrinter()
	m.printers, m.files = printers, copiesOf(printers)

	rule := printerRule()
	rule.Enabled = true

	body, _ := json.Marshal([]Rule{rule})
	if rec := call(t, h, "user:anna", "PUT", "/rules", string(body)); rec.Code != http.StatusOK {
		t.Fatalf("anna could not save an active printer rule: %d %s", rec.Code, rec.Body)
	}

	printers.as = nil

	return m, printers, access
}

func TestTheSettingsOfThePrinterRunDefaultToSafe(t *testing.T) {
	s := checked(t, Settings{AutoInterval: "0s"})
	if s.PrinterAuto || s.printerMax != 50 || !s.dryRun || s.printerAutomatic() {
		t.Fatalf("defaults: auto %v, max %d, dry run %v", s.PrinterAuto, s.printerMax, s.dryRun)
	}

	s = checked(t, Settings{AutoInterval: "6h", PrinterAuto: true, PrinterAutoMax: ptr(10), PrinterAutoDryRun: ptr(false)})
	if !s.printerAutomatic() || !s.scheduled() || s.printerMax != 10 || s.dryRun {
		t.Fatalf("set: %+v", s)
	}

	for _, max := range []int{0, -1, 501} {
		s := Settings{AutoInterval: "0s", PrinterAutoMax: ptr(max)}
		if err := s.check(); err == nil {
			t.Errorf("printer_auto_max %d was accepted", max)
		}
	}
}

func TestAnActivePrinterRuleNeedsPrinterAuto(t *testing.T) {
	access := &fakeAccess{allow: map[string]bool{"user:anna operate device/dev-1": true}}
	settings := checked(t, Settings{AutoInterval: "0s", AllowedStorages: []string{"local"}, AllowedDevices: []string{"dev-1"}})
	_, h := newModule(t, settings, access)

	rule := printerRule()
	rule.Enabled = true
	body, _ := json.Marshal([]Rule{rule})

	if rec := call(t, h, "user:anna", "PUT", "/rules", string(body)); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("an active printer rule without printer_auto: %d %s", rec.Code, rec.Body)
	}
}

func TestADryRunDeletesNothingAndWritesDownTheOldest(t *testing.T) {
	m, printers, _ := autoPrinter(t, nil)

	runs := m.runAll(context.Background(), bySchedule)
	if len(runs) != 1 {
		t.Fatalf("runs %+v", runs)
	}

	run := runs[0]
	if !run.DryRun || run.Units != 50 || len(run.Deleted) != 50 || !run.Partial || len(printers.deleted) != 0 {
		t.Fatalf("dry run: %+v, deleted %d", run, len(printers.deleted))
	}

	if long, _ := teil(249); run.Deleted[0] != long {
		t.Fatalf("the oldest did not come first: %s", run.Deleted[0])
	}

	if got := m.history.list(); len(got) != 1 || !got[0].DryRun || len(got[0].Deleted) != 50 {
		t.Fatalf("history %+v", got)
	}
}

func TestARunDeletesTheOldestUpToTheLimitAsTheModuleItself(t *testing.T) {
	m, printers, _ := autoPrinter(t, func(s *Settings) {
		s.PrinterAutoDryRun, s.PrinterAutoMax = ptr(false), ptr(5)
	})

	run := m.runAll(context.Background(), bySchedule)[0]
	if run.Error != "" || run.DryRun || run.Units != 5 || run.Freed != 5000 || !run.Partial {
		t.Fatalf("run %+v", run)
	}

	for i, d := range []int{249, 248, 247, 246, 245} {
		long, short := teil(d)
		if printers.deleted[i] != short || run.Deleted[i] != long {
			t.Fatalf("delete %d: %s / %s", i, printers.deleted[i], run.Deleted[i])
		}
	}

	for _, as := range printers.as {
		if !strings.HasSuffix(as, ":") {
			t.Fatalf("the automatic run called the printer as a person: %s", as)
		}
	}
}

func TestARunStopsWhenAPrintStarts(t *testing.T) {
	m, printers, _ := autoPrinter(t, func(s *Settings) { s.PrinterAutoDryRun = ptr(false) })
	printers.printAfter = 2

	run := m.runAll(context.Background(), bySchedule)[0]
	if len(printers.deleted) != 2 || run.Units != 2 || !strings.Contains(run.Error, "printing") || !run.Partial {
		t.Fatalf("run %+v, deleted %d", run, len(printers.deleted))
	}

	// Printing from the start: the printer is not even listed, and the rule is
	// skipped rather than filling the history.
	printers.deleted, printers.read = nil, 0

	if runs := m.runAll(context.Background(), bySchedule); len(runs) != 0 || len(printers.deleted) != 0 || printers.read != 0 {
		t.Fatalf("while printing: %+v", runs)
	}

	m.mu.Lock()
	why := m.skipped["usb"]
	m.mu.Unlock()

	if !strings.Contains(why, "printing") || len(m.history.list()) != 1 {
		t.Fatalf("skipped %q, history %d", why, len(m.history.list()))
	}
}

func TestARunStopsWhenThePrinterRefusesOrTheCopiesAreMissing(t *testing.T) {
	m, printers, _ := autoPrinter(t, func(s *Settings) { s.PrinterAutoDryRun = ptr(false) })
	printers.refuse = true

	run := m.runAll(context.Background(), bySchedule)[0]
	if run.Units != 0 || !strings.Contains(run.Error, "FILE_HELD") {
		t.Fatalf("refused: %+v", run)
	}

	printers.refuse = false
	m.files = &fakeCopies{fail: errors.New("boom")}

	runs := m.runAll(context.Background(), bySchedule)

	m.mu.Lock()
	why := m.skipped["usb"]
	m.mu.Unlock()

	if len(runs) != 0 || len(printers.deleted) != 0 || !strings.Contains(why, "copies") {
		t.Fatalf("without copies: %+v, skipped %q", runs, why)
	}
}

func TestARunSkipsARuleWhoseAuthorMayNoLongerOperate(t *testing.T) {
	for name, change := range map[string]func(*module, *fakeAccess){
		"author lost operate": func(_ *module, a *fakeAccess) { a.allow = map[string]bool{} },
		"server cannot say":   func(_ *module, a *fakeAccess) { a.fail = errors.New("unreachable") },
		"printer_auto off":    func(m *module, _ *fakeAccess) { m.settings.PrinterAuto = false },
		"not in allowed list": func(m *module, _ *fakeAccess) { m.settings.AllowedDevices = []string{"dev-2"} },
		"copies not allowed":  func(m *module, _ *fakeAccess) { m.settings.AllowedStorages = []string{"nas"} },
	} {
		m, printers, access := autoPrinter(t, func(s *Settings) { s.PrinterAutoDryRun = ptr(false) })
		change(m, access)

		runs := m.runAll(context.Background(), bySchedule)

		m.mu.Lock()
		why := m.skipped["usb"]
		m.mu.Unlock()

		if len(runs) != 0 || len(printers.deleted) != 0 || printers.read != 0 || why == "" {
			t.Errorf("%s: runs %d, deleted %d, read %d, skipped %q", name, len(runs), len(printers.deleted), printers.read, why)
		}
	}
}

func TestStorageRulesNeverRunOnTheSchedule(t *testing.T) {
	m, _, access := autoPrinter(t, nil)
	access.allow["user:anna write_storage resource_kind/storage"] = true

	rules, _ := m.rules.load()
	snap := Rule{ID: "s", Name: "Snapshots", Enabled: true, Storage: "local", Path: "camera", Mode: modeFiles, KeepDays: 14, SavedBy: "user:anna"}

	if err := m.rules.save(append(rules, snap)); err != nil {
		t.Fatal(err)
	}

	// An active storage rule with an author who may delete, and old files to
	// delete: the schedule still leaves it alone, without a word, because the
	// module protocol cannot delete in a storage.
	for _, run := range m.runAll(context.Background(), bySchedule) {
		if run.Rule == snap.Name {
			t.Fatalf("the storage rule ran: %+v", run)
		}
	}

	for _, run := range m.history.list() {
		if run.Rule == snap.Name {
			t.Fatalf("the storage rule is in the history: %+v", run)
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if why, ok := m.skipped["s"]; ok {
		t.Fatalf("the storage rule was reported as skipped: %q", why)
	}
}

func TestThePageSeesThePrinterRunSettings(t *testing.T) {
	m, _, _ := autoPrinter(t, func(s *Settings) { s.AutoInterval = "6h"; s.PrinterAutoMax = ptr(20) })
	m.settings.interval = 6 * time.Hour

	rec := call(t, m.routes(), "user:anna", "GET", "/overview", "")

	var state State
	if err := json.Unmarshal(rec.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}

	if !state.PrinterAuto || !state.PrinterAutomatic || state.PrinterAutoMax != 20 || !state.DryRun {
		t.Fatalf("state %+v", state)
	}

	if !slices.Contains(state.AllowedDevices, "dev-1") {
		t.Fatalf("devices %v", state.AllowedDevices)
	}
}
