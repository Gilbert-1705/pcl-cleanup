package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/prusa3d/prusa-connect/sdk/storageapi"
)

// fakeCopies is a server storage with files of given sizes. It records who
// it listed for, can fail, and can be endless to cut a scan.
type fakeCopies struct {
	mu      sync.Mutex
	sizes   map[string]int64
	fail    error
	endless bool
	calls   int
	asked   []string
}

func (f *fakeCopies) List(_ context.Context, p storageapi.ListParams) (storageapi.Listing, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls++
	f.asked = append(f.asked, p.Principal+" "+p.Storage+":"+p.Path)

	if f.fail != nil {
		return storageapi.Listing{}, f.fail
	}

	if f.endless {
		out := storageapi.Listing{Next: "more"}
		for i := range 1000 {
			out.Entries = append(out.Entries, storageapi.ListEntry{Name: fmt.Sprintf("f%d.bgcode", i), Size: 1})
		}

		return out, nil
	}

	prefix := p.Path
	if prefix != "" {
		prefix += "/"
	}

	children := map[string]storageapi.ListEntry{}
	found := p.Path == ""

	for file, size := range f.sizes {
		if !strings.HasPrefix(file, prefix) {
			continue
		}

		found = true
		head, _, deeper := strings.Cut(strings.TrimPrefix(file, prefix), "/")

		if deeper {
			children[head] = storageapi.ListEntry{Name: head, Dir: true}
		} else {
			children[head] = storageapi.ListEntry{Name: head, Size: size, MTime: now.Unix()}
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

	out := storageapi.Listing{}
	for _, n := range names {
		out.Entries = append(out.Entries, children[n])
	}

	return out, nil
}

// copiesOf holds a copy of every print file on the printer under drucke/,
// some in a subfolder, as Connect Local keeps them.
func copiesOf(printers *fakePrinters) *fakeCopies {
	f := &fakeCopies{sizes: map[string]int64{}}

	for i, file := range printers.files {
		if file.Folder || file.DisplayName == "" {
			continue
		}

		dir := "drucke"
		if i%3 == 0 {
			dir = "drucke/halter"
		}

		f.sizes[dir+"/"+file.DisplayName] = file.Size
	}

	return f
}

// usbPrinter is fakePrinterOne after a print of letzter.bgcode, so only the
// 250 dated print files are judged.
func usbPrinter() *fakePrinters {
	f := fakePrinterOne()
	f.last = "usb/letzter.bgcode"

	return f
}

// teil names the printer file of day d in fakePrinterOne.
func teil(d int) (long, short string) {
	return fmt.Sprintf("teil-%04d-halter.bgcode", d), fmt.Sprintf("usb/TEIL%04d.BGC", d)
}

func TestOnlyFilesWithTheSameNameAndSizeOnTheServerAreDeleted(t *testing.T) {
	printers := usbPrinter()
	copies := copiesOf(printers)

	drop := func(d int) {
		long, _ := teil(d)
		for p := range copies.sizes {
			if strings.HasSuffix(p, "/"+long) {
				delete(copies.sizes, p)
			}
		}
	}

	// Day 100: no copy at all. Day 101: same name, another size. Day 102: the
	// copy in upper case, which still counts. Day 103: the copy outside
	// copy_path. Day 104: a copy under the short name only.
	for _, d := range []int{100, 101, 102, 103, 104} {
		drop(d)
	}

	long, _ := teil(101)
	copies.sizes["drucke/"+long] = 999

	long, _ = teil(102)
	copies.sizes["drucke/"+strings.ToUpper(long)] = 1000

	long, _ = teil(103)
	copies.sizes["anderswo/"+long] = 1000

	copies.sizes["drucke/TEIL0104.BGC"] = 1000

	plan := planPrinter(context.Background(), printers, copies, printerRule(), "user:anna", now)
	if plan.Error != "" {
		t.Fatal(plan.Error)
	}

	if !plan.CopyCheck || plan.DeleteUnits != 219-4 || plan.KeptNoCopy != 4 || plan.KeptNoCopySize != 4000 {
		t.Fatalf("delete %d, kept without copy %d (%d bytes), check %v", plan.DeleteUnits, plan.KeptNoCopy, plan.KeptNoCopySize, plan.CopyCheck)
	}

	for _, d := range []int{100, 101, 103, 104} {
		long, short := teil(d)
		if slices.Contains(plan.Delete, short) || !slices.Contains(plan.KeptNoCopyList, long) {
			t.Errorf("day %d without a matching copy: deleted %v, listed as kept %v", d,
				slices.Contains(plan.Delete, short), slices.Contains(plan.KeptNoCopyList, long))
		}
	}

	if _, short := teil(102); !slices.Contains(plan.Delete, short) {
		t.Error("a copy that differs only in case did not count")
	}

	for _, who := range copies.asked {
		if !strings.HasPrefix(who, "user:anna local:drucke") {
			t.Fatalf("the copies were listed as %q", who)
		}
	}
}

func TestFilesWithoutACopyCountAmongTheNewestAndOlderOnesDoNotMoveUp(t *testing.T) {
	printers := usbPrinter()
	copies := copiesOf(printers)

	// The 20 newest files came onto the drive another way (straight from the
	// slicer) and have no copy on the server.
	for d := range 20 {
		long, _ := teil(d)
		for p := range copies.sizes {
			if strings.HasSuffix(p, "/"+long) {
				delete(copies.sizes, p)
			}
		}
	}

	rule := printerRule()
	rule.KeepDays, rule.KeepNewest = 0, 10

	if err := rule.check(); err != nil {
		t.Fatal(err)
	}

	plan := planPrinter(context.Background(), printers, copies, rule, "user:anna", now)
	if plan.Error != "" {
		t.Fatal(plan.Error)
	}

	// Days 0 to 9 are the newest ten, days 10 to 19 stay for lack of a copy,
	// and days 20 to 249 go: the older files do not take the place of the
	// ones without a copy.
	if plan.DeleteUnits != 230 || plan.KeptNoCopy != 10 {
		t.Fatalf("delete %d, kept without copy %d", plan.DeleteUnits, plan.KeptNoCopy)
	}

	for d := range 20 {
		if _, short := teil(d); slices.Contains(plan.Delete, short) {
			t.Fatalf("day %d would be deleted", d)
		}
	}
}

func TestWithoutAllTheCopiesNothingIsDeleted(t *testing.T) {
	for name, copies := range map[string]*fakeCopies{
		"unreadable": {fail: errors.New("boom")},
		"cut short":  {endless: true},
		"no folder":  {sizes: map[string]int64{"anderswo/x.bgcode": 1}},
	} {
		plan := planPrinter(context.Background(), usbPrinter(), copies, printerRule(), "user:anna", now)

		if plan.Error == "" || plan.DeleteUnits != 0 || len(plan.Delete) != 0 || plan.Units != 250 {
			t.Errorf("%s: error %q, delete %d %v, units %d", name, plan.Error, plan.DeleteUnits, len(plan.Delete), plan.Units)
		}
	}

	if plan := planPrinter(context.Background(), usbPrinter(), &fakeCopies{endless: true}, printerRule(), "user:anna", now); !strings.Contains(plan.Error, errCopiesCut.Error()) {
		t.Errorf("a cut scan says %q", plan.Error)
	}
}

func TestTheCopiesAreReadOnlyWhenSomethingWouldGo(t *testing.T) {
	copies := &fakeCopies{fail: errors.New("must not be asked")}

	rule := printerRule()
	rule.KeepNewest = 1000

	plan := planPrinter(context.Background(), usbPrinter(), copies, rule, "user:anna", now)
	if plan.Error != "" || copies.calls != 0 || plan.DeleteUnits != 0 {
		t.Fatalf("error %q, calls %d", plan.Error, copies.calls)
	}

	// Without the copy check, the old behaviour stays, for a rule run by hand.
	rule = printerRule()
	rule.SkipCopyCheck, rule.CopyStorage, rule.CopyPath = true, "", ""

	if err := rule.check(); err != nil {
		t.Fatal(err)
	}

	plan = planPrinter(context.Background(), usbPrinter(), copies, rule, "user:anna", now)
	if plan.Error != "" || copies.calls != 0 || plan.DeleteUnits != 219 || plan.CopyCheck {
		t.Fatalf("without the check: error %q, calls %d, delete %d", plan.Error, copies.calls, plan.DeleteUnits)
	}
}

func TestKeepDaysZeroIsOnlyForPrintersThatKeepTheNewest(t *testing.T) {
	rule := printerRule()
	rule.KeepDays, rule.KeepNewest = 0, 10

	if err := rule.check(); err != nil {
		t.Errorf("printer, keep_days 0, keep_newest 10: %v", err)
	}

	rule.KeepNewest = 0
	if err := rule.check(); err == nil {
		t.Error("printer, keep_days 0, keep_newest 0 was accepted")
	}

	storage := Rule{ID: "s", Name: "S", Storage: "local", Path: "camera", Mode: modeFiles, KeepDays: 0, KeepNewest: 10}
	if err := storage.check(); err == nil {
		t.Error("a storage rule with keep_days 0 was accepted")
	}
}

func TestThePrinterRuleNeedsAnAllowedCopyStorage(t *testing.T) {
	access := &fakeAccess{allow: map[string]bool{"user:anna operate device/dev-1": true}}
	settings := Settings{AutoInterval: "0s", AllowedStorages: []string{"local"}, AllowedDevices: []string{"dev-1"}}
	m, h := newModule(t, settings, access)
	m.files = copiesOf(usbPrinter())

	for name, change := range map[string]func(*Rule){
		"no copy storage":     func(r *Rule) { r.CopyStorage = "" },
		"copy storage not ok": func(r *Rule) { r.CopyStorage = "nas" },
	} {
		rule := printerRule()
		change(&rule)
		body, _ := json.Marshal([]Rule{rule})

		if rec := call(t, h, "user:anna", "PUT", "/rules", string(body)); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: answered %d %s", name, rec.Code, rec.Body)
		}
	}

	// A printer rule saved by 0.3.0 has no copy storage. It still loads, and
	// its plan deletes nothing until it is saved again.
	old := printerRule()
	old.CopyStorage, old.CopyPath, old.SavedBy = "", "", "user:anna"

	if err := m.rules.save([]Rule{old}); err != nil {
		t.Fatal(err)
	}

	if _, err := m.rules.load(); err != nil {
		t.Fatalf("a 0.3.0 printer rule does not load: %v", err)
	}

	rec := call(t, h, "user:anna", "GET", "/plan?rule=usb", "")

	var one struct{ Plans []RulePlan }
	if err := json.Unmarshal(rec.Body.Bytes(), &one); err != nil || len(one.Plans) != 1 {
		t.Fatalf("plan: %s", rec.Body)
	}

	if p := one.Plans[0]; !strings.Contains(p.Error, "copy_storage") || p.DeleteUnits != 0 || len(p.Delete) != 0 {
		t.Fatalf("the old rule plans %+v", p)
	}
}

func TestThePageIsOfferedTheFoldersOfAnAllowedStorage(t *testing.T) {
	access := &fakeAccess{}
	_, h := newModule(t, Settings{AutoInterval: "0s", AllowedStorages: []string{"local"}}, access)

	rec := call(t, h, "user:anna", "GET", "/folders?storage=local&path=zeitraffer", "")

	var got Folders
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || rec.Code != http.StatusOK {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}

	if got.Path != "zeitraffer" || !slices.Equal(got.Folders, []string{"zeitraffer/core-one"}) {
		t.Fatalf("folders %+v", got)
	}

	if rec := call(t, h, "user:anna", "GET", "/folders?storage=nas", ""); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("a storage outside the list: %d", rec.Code)
	}

	if rec := call(t, h, "", "GET", "/folders?storage=local", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("without a person: %d", rec.Code)
	}
}
