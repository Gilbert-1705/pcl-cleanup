package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/prusa3d/prusa-connect/sdk/deviceapi"
	"github.com/prusa3d/prusa-connect/sdk/routesapi"
)

// Regression tests for the findings of the audit of 26.11.2026 (SEC-0002 to
// SEC-0005 and SEC-0011 to SEC-0013).

func overviewFor(t *testing.T, h http.Handler, person string) State {
	t.Helper()

	rec := call(t, h, person, "GET", "/overview", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("overview for %s: %d %s", person, rec.Code, rec.Body)
	}

	var state State
	if err := json.Unmarshal(rec.Body.Bytes(), &state); err != nil {
		t.Fatal(err)
	}

	return state
}

// printerHistory is autoPrinter after one dry run, which writes the files it
// would delete into the history, and with a reason why the rule was skipped.
func printerHistory(t *testing.T) (*module, http.Handler, *fakeAccess) {
	t.Helper()

	m, _, access := autoPrinter(t, nil)

	runs := m.runAll(context.Background(), bySchedule)
	if len(runs) != 1 || len(runs[0].Deleted) == 0 || runs[0].Device != "dev-1" {
		t.Fatalf("the dry run wrote %+v", runs)
	}

	m.mu.Lock()
	m.skipped = map[string]string{"usb": "the printer Core One is printing"}
	m.mu.Unlock()

	return m, m.routes(), access
}

// SEC-0002: a person who may open the extension but not observe the printer
// learns nothing about it.
func TestTheOverviewShowsAPrinterOnlyToWhoMayObserveIt(t *testing.T) {
	_, h, access := printerHistory(t)

	anna := overviewFor(t, h, "user:anna")
	if !hasPrinterDetails(anna) {
		t.Fatalf("anna, who may observe and operate dev-1, does not see it: %+v", anna)
	}

	for _, name := range []string{"user:gast", ""} {
		state := overviewFor(t, h, name)
		assertNothingAboutThePrinter(t, name, state)
	}

	// When the server cannot answer, nothing is shown either.
	access.mu.Lock()
	access.fail = errors.New("unreachable")
	access.mu.Unlock()

	assertNothingAboutThePrinter(t, "anna while the server is unreachable", overviewFor(t, h, "user:anna"))
}

func hasPrinterDetails(state State) bool {
	var rule, files, author, skipped bool

	for _, r := range state.Rules {
		if r.ID == "usb" && r.Device == "dev-1" && !r.Hidden {
			rule = true
			author = r.SavedBy == "user:anna"
		}
	}

	for _, run := range state.History {
		files = files || (len(run.Deleted) > 0 && run.Device == "dev-1")
	}

	_, skipped = state.Skipped["usb"]

	return rule && files && author && skipped && len(state.AllowedDevices) == 1
}

func assertNothingAboutThePrinter(t *testing.T, who string, state State) {
	t.Helper()

	raw, _ := json.Marshal(state)
	for _, secret := range []string{"dev-1", "teil-", "Core One", "user:anna", `"usb"` + `,"path"`} {
		if strings.Contains(string(raw), secret) {
			t.Errorf("%s sees %q: %s", who, secret, raw)
		}
	}

	for _, r := range state.Rules {
		if r.ID == "usb" && (!r.Hidden || r.Device != "" || r.Path != "" || r.CopyStorage != "" || r.Pattern != "") {
			t.Errorf("%s sees the printer rule %+v", who, r)
		}

		if r.SavedBy != "" {
			t.Errorf("%s sees who saved %s: %s", who, r.ID, r.SavedBy)
		}
	}

	for _, run := range state.History {
		if len(run.Deleted) > 0 || run.Device != "" {
			t.Errorf("%s sees the files of a printer run: %+v", who, run)
		}
	}

	if len(state.Skipped) > 0 || len(state.AllowedDevices) > 0 {
		t.Errorf("%s sees skipped %v and devices %v", who, state.Skipped, state.AllowedDevices)
	}
}

// SEC-0002: asking for the plan of a printer rule tells nothing either.
func TestThePlanOfAHiddenPrinterRuleSaysNothing(t *testing.T) {
	_, h, _ := printerHistory(t)

	rec := call(t, h, "user:gast", "GET", "/plan?rule=usb", "")
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "dev-1") || !strings.Contains(rec.Body.String(), errHiddenText) {
		t.Fatalf("gast got %d %s", rec.Code, rec.Body)
	}
}

// SEC-0002: a run written before 0.5.0 names files but no printer, so its
// files are shown to nobody.
func TestAnOldRunWithFilesButNoPrinterShowsNoFiles(t *testing.T) {
	m, h, _ := printerHistory(t)
	m.history.add(RunRecord{At: now, By: "automatisch", Rule: "USB", Deleted: []string{"alt.bgcode"}})

	for _, run := range overviewFor(t, h, "user:anna").History {
		if len(run.Deleted) > 0 && run.Device == "" {
			t.Fatalf("anna sees the files of an old run: %+v", run)
		}
	}
}

// SEC-0002: a person who may change only the storage rules saves the list
// they were shown, with the printer rule hidden, and the printer rule stays
// as it was.
func TestAHiddenRuleSurvivesASaveByWhoCannotSeeIt(t *testing.T) {
	m, h, access := printerHistory(t)
	access.mu.Lock()
	access.allow["user:gast write_storage resource_kind/storage"] = true
	access.mu.Unlock()

	shown := overviewFor(t, h, "user:gast").Rules

	for i := range shown {
		if shown[i].ID == "usb" && !shown[i].Hidden {
			t.Fatalf("the printer rule is not hidden: %+v", shown[i])
		}

		if !shown[i].Hidden {
			shown[i].KeepDays = 99
		}
	}

	body, _ := json.Marshal(shown)
	if rec := call(t, h, "user:gast", "PUT", "/rules", string(body)); rec.Code != http.StatusOK {
		t.Fatalf("gast could not save: %d %s", rec.Code, rec.Body)
	}

	stored, err := m.rules.load()
	if err != nil {
		t.Fatal(err)
	}

	for _, r := range stored {
		if r.ID == "usb" && (r.Device != "dev-1" || r.SavedBy != "user:anna" || !r.Enabled || r.Hidden) {
			t.Fatalf("the printer rule changed: %+v", r)
		}
	}
}

// SEC-0005: removing a rule takes the right to delete where it points.
func TestRemovingARuleNeedsTheRightToDeleteThere(t *testing.T) {
	m, h, access := printerHistory(t)

	if rec := call(t, h, "user:gast", "PUT", "/rules", "[]"); rec.Code != http.StatusForbidden {
		t.Fatalf("gast removed every rule: %d %s", rec.Code, rec.Body)
	}

	// May delete in the storages, but not on the printer: still no.
	access.mu.Lock()
	access.allow["user:gast write_storage resource_kind/storage"] = true
	access.mu.Unlock()

	if rec := call(t, h, "user:gast", "PUT", "/rules", "[]"); rec.Code != http.StatusForbidden {
		t.Fatalf("gast removed the printer rule: %d %s", rec.Code, rec.Body)
	}

	if rules, _ := m.rules.load(); len(rules) == 0 {
		t.Fatal("the rules are gone")
	}

	if rec := call(t, h, "user:anna", "PUT", "/rules", "[]"); rec.Code != http.StatusOK {
		t.Fatalf("anna could not remove her rules: %d %s", rec.Code, rec.Body)
	}
}

// SEC-0005: a save names the list it was made on; a save made on an older
// list, or on none, is refused.
func TestASaveOnAnOldListIsRefused(t *testing.T) {
	_, h, _ := printerHistory(t)

	version := versionFor(t, h, "user:anna")
	rules := overviewFor(t, h, "user:anna").Rules
	body, _ := json.Marshal(rules)

	put := func(match string) int {
		headers := map[string][]string{"Content-Type": {"application/json"}}
		if match != "" {
			headers["If-Match"] = []string{match}
		}

		return send(t, h, routesapi.Request{
			Method: "PUT", Path: "/rules", Body: body, Principal: "user:anna", Headers: headers,
		}).Code
	}

	if code := put(""); code != http.StatusPreconditionRequired {
		t.Fatalf("a save without If-Match: %d", code)
	}

	// Two people read the same list. The first one changes a rule.
	first := append([]Rule{}, rules...)
	first[0].KeepDays++
	body, _ = json.Marshal(first)

	if code := put(`"` + version + `"`); code != http.StatusOK {
		t.Fatalf("the first save: %d", code)
	}

	// The second one saves what they read, which would undo the change.
	body, _ = json.Marshal(rules)

	if code := put(`"` + version + `"`); code != http.StatusPreconditionFailed {
		t.Fatalf("a second save on the same old list: %d", code)
	}

	if code := put(`W/"` + versionFor(t, h, "user:anna") + `"`); code != http.StatusOK {
		t.Fatalf("a save on the current list: %d", code)
	}
}

// SEC-0011: a folder rule takes no pattern; one saved before 0.5.0 still loads.
func TestAFolderRuleTakesNoPattern(t *testing.T) {
	m, h := newModule(t, Settings{AutoInterval: "0s", AllowedStorages: []string{"local"}}, &fakeAccess{allow: map[string]bool{
		"user:anna write_storage resource_kind/storage": true,
	}})

	// The same rules without the pattern are saved: only the pattern is refused.
	clean, _ := json.Marshal(defaultRules())
	if rec := call(t, h, "user:anna", "PUT", "/rules", string(clean)); rec.Code != http.StatusOK {
		t.Fatalf("the default rules: %d %s", rec.Code, rec.Body)
	}

	rules := defaultRules()
	for i := range rules {
		if rules[i].Mode == modeFolders {
			rules[i].Pattern = "*.mp4"
		}
	}

	body, _ := json.Marshal(rules)
	if rec := call(t, h, "user:anna", "PUT", "/rules", string(body)); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("a folder rule with a pattern: %d %s", rec.Code, rec.Body)
	}

	raw, _ := json.Marshal(rules)
	if err := os.WriteFile(m.rules.path, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	loaded, err := m.rules.load()
	if err != nil {
		t.Fatalf("an old folder rule with a pattern does not load: %v", err)
	}

	for _, r := range loaded {
		if r.Mode == modeFolders && r.Pattern != "" {
			t.Fatalf("the pattern stayed: %+v", r)
		}
	}
}

// SEC-0004: what gets deleted is the path the printer reports, so the path
// itself has to be a print file directly in the rule's folder.
func TestOnlyAPrintFileDirectlyInTheFolderIsOffered(t *testing.T) {
	cases := map[string]bool{
		"usb/teil.bgcode":       true,
		"usb/TEIL~1.BGC":        true,
		"USB/Halter.GCODE":      true,
		"usb/HALTER~1.GCO":      true,
		"usb/sub/teil.bgcode":   false,
		"usb/settings.ini":      false,
		"usb/firmware.bbf":      false,
		"other/teil.bgcode":     false,
		"usb/../usb/x.bgcode":   false,
		"usb/teil.bgcode.ini":   false,
		"":                      false,
		"usb\\teil.bgcode":      false,
		"usb/teil.bgcode/x.gco": false,
	}

	for p, want := range cases {
		if got := printPathIn(p, "usb"); got != want {
			t.Errorf("%q: %v, want %v", p, got, want)
		}
	}

	printers := &fakePrinters{device: deviceapi.Device{ID: "dev-1", Name: "Core One", Connected: true}}
	printers.files = []deviceapi.File{
		{Path: "usb/sub/settings.ini", Name: "SETTIN~1.INI", DisplayName: "harmlos.bgcode", Size: 1, Modified: daysAgo(400).Unix()},
		{Path: "usb/echt.bgcode", Name: "echt.bgcode", Size: 1, Modified: daysAgo(400).Unix()},
	}

	rule := printerRule()
	rule.KeepNewest, rule.SkipCopyCheck, rule.CopyStorage, rule.CopyPath = 0, true, "", ""

	plan := planPrinter(context.Background(), printers, &fakeCopies{sizes: map[string]int64{}}, rule, "user:anna", now)
	if plan.Error != "" || len(plan.Delete) != 1 || plan.Delete[0] != "usb/echt.bgcode" {
		t.Fatalf("plan %+v", plan)
	}
}

// SEC-0003: a copy that appeared well after the file on the printer is not
// taken as its copy.
func TestACopyMuchNewerThanThePrintFileDoesNotCount(t *testing.T) {
	printed := daysAgo(100)

	cases := []struct {
		name   string
		copied time.Time
		want   bool
	}{
		{"uploaded before the print file", printed.Add(-time.Hour), true},
		{"a few hours later, as a time zone shifts it", printed.Add(5 * time.Hour), true},
		{"two days later", printed.Add(48 * time.Hour), false},
		{"no time", time.Time{}, false},
	}

	for _, c := range cases {
		if got := copyFits(c.copied, printed); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}

	if copyFits(printed, time.Time{}) {
		t.Error("a print file without a time fits a copy")
	}

	// The same through a plan: one copy too new, one fine.
	printers := &fakePrinters{device: deviceapi.Device{ID: "dev-1", Name: "Core One", Connected: true}}
	printers.files = []deviceapi.File{
		{Path: "usb/a.bgcode", Name: "a.bgcode", Size: 10, Modified: printed.Unix()},
		{Path: "usb/b.bgcode", Name: "b.bgcode", Size: 10, Modified: printed.Unix()},
	}

	copies := &fakeCopies{
		sizes:  map[string]int64{"drucke/a.bgcode": 10, "drucke/b.bgcode": 10},
		mtimes: map[string]int64{"drucke/a.bgcode": now.Unix()},
	}

	rule := printerRule()
	rule.KeepNewest = 0

	plan := planPrinter(context.Background(), printers, copies, rule, "user:anna", now)
	if plan.Error != "" || len(plan.Delete) != 1 || plan.Delete[0] != "usb/b.bgcode" || plan.KeptNoCopy != 1 {
		t.Fatalf("plan %+v", plan)
	}
}

// SEC-0012: the routes that change something take JSON only, which a page of
// another site cannot send without the browser asking first.
func TestChangingRoutesTakeJSONOnly(t *testing.T) {
	_, h, _ := printerHistory(t)

	for _, route := range []struct{ method, path, body string }{
		{"POST", "/runs", `{"rule":"Camera snapshots","units":1}`},
		{"POST", "/auto/run", `{}`},
		{"PUT", "/rules", `[]`},
	} {
		for _, kind := range []string{"", "text/plain", "application/x-www-form-urlencoded", "multipart/form-data; boundary=x"} {
			headers := map[string][]string{"If-Match": {versionFor(t, h, "user:anna")}}
			if kind != "" {
				headers["Content-Type"] = []string{kind}
			}

			rec := send(t, h, routesapi.Request{
				Method: route.method, Path: route.path, Body: []byte(route.body), Principal: "user:anna", Headers: headers,
			})
			if rec.Code != http.StatusUnsupportedMediaType {
				t.Errorf("%s %s as %q: %d", route.method, route.path, kind, rec.Code)
			}
		}
	}
}

// SEC-0013: the history is written through a file of its own and a rename.
func TestTheHistoryIsWrittenWhole(t *testing.T) {
	dir := t.TempDir()
	h := loadHistory(filepath.Join(dir, "verlauf.json"))

	for i := range 3 {
		h.add(RunRecord{At: now, By: bySchedule, Rule: "r", Units: i})
	}

	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || entries[0].Name() != "verlauf.json" {
		t.Fatalf("files left: %v", entries)
	}

	if again := loadHistory(filepath.Join(dir, "verlauf.json")); len(again.list()) != 3 {
		t.Fatalf("history read back: %+v", again.list())
	}
}
