package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/prusa3d/prusa-connect/sdk/storageapi"
)

var now = time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)

func daysAgo(d int) time.Time { return now.Add(-time.Duration(d) * 24 * time.Hour) }

// fakeStorage holds files by path and lists them a page at a time, as the
// host does.
type fakeStorage struct {
	files map[string]time.Time
	page  int
}

func (f *fakeStorage) List(_ context.Context, p storageapi.ListParams) (storageapi.Listing, error) {
	children := map[string]storageapi.ListEntry{}
	prefix := p.Path
	if prefix != "" {
		prefix += "/"
	}

	found := p.Path == ""

	for file, mtime := range f.files {
		if !strings.HasPrefix(file, prefix) {
			continue
		}

		found = true
		rest := strings.TrimPrefix(file, prefix)
		head, _, deeper := strings.Cut(rest, "/")

		if deeper {
			children[head] = storageapi.ListEntry{Name: head, Dir: true}
		} else {
			children[head] = storageapi.ListEntry{Name: head, Size: 1000, MTime: mtime.Unix()}
		}
	}

	if !found {
		return storageapi.Listing{}, storageapi.ErrNoFile
	}

	names := make([]string, 0, len(children))
	for n := range children {
		names = append(names, n)
	}

	sort.Strings(names)

	start := 0
	if p.Cursor != "" {
		start = slices.Index(names, p.Cursor)
	}

	size := f.page
	if size == 0 {
		size = 1000
	}

	out := storageapi.Listing{}

	for i := start; i < len(names) && i < start+size; i++ {
		out.Entries = append(out.Entries, children[names[i]])
	}

	if start+size < len(names) {
		out.Next = names[start+size]
	}

	return out, nil
}

func storageWith() *fakeStorage {
	f := &fakeStorage{files: map[string]time.Time{}, page: 3}

	for d := range 30 {
		f.files[path.Join("camera/buddy3d", time.Unix(int64(d), 0).UTC().Format("150405")+".jpg")] = daysAgo(d)
	}

	f.files["camera/buddy3d/notiz.txt"] = daysAgo(100)
	f.files["modelle/halter.bgcode"] = daysAgo(400)

	for d, run := range []string{"2026-11-19_a", "2026-10-01_b", "2026-01-05_c", "2025-12-01_d"} {
		age := []int{1, 50, 319, 354}[d]
		f.files["zeitraffer/core-one/"+run+"/zeitraffer.mp4"] = daysAgo(age)
		f.files["zeitraffer/core-one/"+run+"/vorschau.jpg"] = daysAgo(age)
	}

	return f
}

func TestFilesOlderThanTheLimitAndMatchingThePatternAreDeleted(t *testing.T) {
	rule := Rule{ID: "s", Name: "Snapshots", Enabled: true, Storage: "local", Path: "camera", Mode: modeFiles,
		Pattern: "*.jpg", KeepDays: 14}
	if err := rule.check(); err != nil {
		t.Fatal(err)
	}

	plan := planRule(context.Background(), storageWith(), rule, now)

	if plan.Error != "" {
		t.Fatal(plan.Error)
	}

	// Days 15 to 29 are older than 14 days; the text file does not match.
	if plan.Units != 30 || plan.DeleteUnits != 15 || len(plan.Delete) != 15 {
		t.Fatalf("units %d, delete %d (%d paths)", plan.Units, plan.DeleteUnits, len(plan.Delete))
	}

	for _, p := range plan.Delete {
		if !strings.HasPrefix(p, "camera/buddy3d/") || !strings.HasSuffix(p, ".jpg") {
			t.Fatalf("would delete %s", p)
		}
	}
}

func TestFoldersGoAsAWholeAndTheNewestAreKept(t *testing.T) {
	rule := Rule{ID: "z", Name: "Zeitraffer", Enabled: true, Storage: "local", Path: "zeitraffer",
		Mode: modeFolders, Depth: 2, KeepDays: 30, KeepNewest: 2}
	if err := rule.check(); err != nil {
		t.Fatal(err)
	}

	plan := planRule(context.Background(), storageWith(), rule, now)

	want := []string{"zeitraffer/core-one/2026-01-05_c", "zeitraffer/core-one/2025-12-01_d"}
	if !slices.Equal(plan.Delete, want) || plan.DeleteFiles != 4 || plan.Units != 4 {
		t.Fatalf("delete %v (%d files of %d units)", plan.Delete, plan.DeleteFiles, plan.Units)
	}
}

func TestAMissingFolderIsAnEmptyPlan(t *testing.T) {
	rule := Rule{ID: "p", Name: "PrintWatch", Enabled: true, Storage: "local", Path: "printwatch",
		Mode: modeFiles, KeepDays: 7}
	_ = rule.check()

	plan := planRule(context.Background(), storageWith(), rule, now)
	if plan.Error != "" || plan.Units != 0 {
		t.Fatalf("%+v", plan)
	}
}

func TestARuleCannotReachTheRootOfAStorage(t *testing.T) {
	for _, p := range []string{"", "/", ".", "../"} {
		rule := Rule{ID: "x", Name: "x", Storage: "local", Path: p, Mode: modeFiles, KeepDays: 1}
		if err := rule.check(); err == nil {
			t.Errorf("path %q was accepted", p)
		}
	}

	rule := Rule{ID: "x", Name: "x", Storage: "local", Path: "/camera/../camera/", Mode: modeFiles, KeepDays: 1}
	if err := rule.check(); err != nil || rule.Path != "camera" {
		t.Fatalf("%v %q", err, rule.Path)
	}
}

func TestTheDefaultsAreValid(t *testing.T) {
	if err := checkRules(defaultRules()); err != nil {
		t.Fatal(err)
	}
}

func TestUsageAddsUpEachTopLevelFolder(t *testing.T) {
	usage, err := usageOf(context.Background(), storageWith(), "local", now)
	if err != nil {
		t.Fatal(err)
	}

	if usage.Files != 31+1+8 || usage.Items[0].Path != "camera" || usage.Items[0].Files != 31 {
		t.Fatalf("%+v", usage)
	}
}

func TestTheDeleterSendsBatchesWithTheToken(t *testing.T) {
	var batches [][]string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/storages/local/deletions" || r.Header.Get("Authorization") != "Bearer pcl_x" {
			w.WriteHeader(http.StatusForbidden)

			return
		}

		var body struct{ Paths []string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		batches = append(batches, body.Paths)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	paths := make([]string, 450)
	for i := range paths {
		paths[i] = "camera/x/" + time.Unix(int64(i), 0).Format("150405") + ".jpg"
	}

	d := deleter{base: server.URL, token: "pcl_x", client: server.Client()}

	done, err := d.remove(context.Background(), "local", paths)
	if err != nil || done != 450 || len(batches) != 3 || len(batches[2]) != 50 {
		t.Fatalf("done %d, %d batches, %v", done, len(batches), err)
	}

	d.token = "wrong"
	if _, err = d.remove(context.Background(), "local", paths[:1]); err == nil {
		t.Fatal("a refusal was not reported")
	}
}

func TestTheRoutesServeStatePlanAndRules(t *testing.T) {
	m := &module{files: storageWith()}
	if err := m.start(context.Background(), Settings{AutoInterval: "0s"}, t.TempDir()); err != nil {
		t.Fatal(err)
	}

	h := m.routes()

	call := func(method, target, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(method, target, strings.NewReader(body)))

		return rec
	}

	if rec := call("GET", "/overview", ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), "kamera-snapshots") {
		t.Fatalf("state: %d %s", rec.Code, rec.Body)
	}

	if rec := call("PUT", "/rules", `[{"id":"a","name":"A","storage":"local","path":"","mode":"files","keep_days":3}]`); rec.Code != 422 {
		t.Fatalf("a rule on the root was answered %d", rec.Code)
	}

	if rec := call("PUT", "/rules", `[{"id":"a","name":"A","enabled":true,"storage":"local","path":"camera","mode":"files","keep_days":3}]`); rec.Code != 200 {
		t.Fatalf("rules: %d %s", rec.Code, rec.Body)
	}

	rec := call("GET", "/plan?rule=a", "")

	var plans struct{ Plans []RulePlan }
	if err := json.Unmarshal(rec.Body.Bytes(), &plans); err != nil || len(plans.Plans) != 1 || plans.Plans[0].DeleteUnits < 27 {
		t.Fatalf("plan: %s", rec.Body)
	}

	if rec = call("POST", "/auto/run", ""); rec.Code != http.StatusConflict {
		t.Fatalf("a run without a token was answered %d", rec.Code)
	}
}
