package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// Rule says what may go in one place of one storage.
//
// Mode "files" judges every file under Path on its own, by when it last
// changed. Mode "folders" judges the folders Depth levels below Path as one
// unit each, by the newest file inside: a timelapse run, for example, is one
// folder with a video, a cover and a description, and goes as a whole.
//
// Target "printer" points the rule at a printer's own storage (a USB drive or
// its internal disk) instead of one of the server's storages. Such a rule
// judges print files one by one, only in the folder it names, and runs by
// hand unless the operator turned on printer_auto: see printer.go and
// auto_printer.go.
//
// A printer rule only deletes a file whose copy lies in one of the server's
// storages: same long name (case aside) and same size, in CopyStorage under
// CopyPath (empty: the whole storage). SkipCopyCheck turns that off for a rule
// run by hand; it is off by default, so a rule saved without the field checks.
// Files without a copy still count among the newest KeepNewest and are never
// deleted. See copies.go.
//
// SavedBy is the principal who last saved the rule in a changed form. The
// server sets it, never the page. An automatic run deletes for a rule only
// while that person may still delete in its storage themselves.
type Rule struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Enabled    bool   `json:"enabled"`
	Target     string `json:"target,omitempty"`
	Storage    string `json:"storage,omitempty"`
	Device     string `json:"device,omitempty"`
	Path       string `json:"path"`
	Mode       string `json:"mode"`
	Depth      int    `json:"depth,omitempty"`
	Pattern    string `json:"pattern,omitempty"`
	KeepDays   int    `json:"keep_days"`
	KeepNewest int    `json:"keep_newest,omitempty"`
	// Printer rules only, see above.
	CopyStorage   string `json:"copy_storage,omitempty"`
	CopyPath      string `json:"copy_path,omitempty"`
	SkipCopyCheck bool   `json:"skip_copy_check,omitempty"`
	SavedBy       string `json:"saved_by,omitempty"`
}

const (
	modeFiles     = "files"
	modeFolders   = "folders"
	targetStorage = "storage"
	targetPrinter = "printer"
	maxRules      = 32
	maxDeviceID   = 128
	maxStorageID  = 64
)

// printFileSuffixes are the only files a printer rule may name. A printer's
// storage holds the prints themselves, and a rule there never reaches
// anything else, such as firmware or settings files.
var printFileSuffixes = []string{".bgcode", ".gcode"}

// sameRule reports whether two rules say the same thing, apart from who saved
// them. A rule that is unchanged keeps its author when another person saves
// the list.
func sameRule(a, b Rule) bool {
	a.SavedBy, b.SavedBy = "", ""

	return a == b
}

// printer reports whether the rule points at a printer's storage.
func (r Rule) printer() bool { return r.Target == targetPrinter }

// needsCopy reports whether the rule deletes only files with a copy on the
// server.
func (r Rule) needsCopy() bool { return r.printer() && !r.SkipCopyCheck }

var errRule = errors.New("the rule is not usable")

// defaultRules are what a fresh install starts with. The timelapse rule points
// at zeitraffer/, the folder pcl-timelapse writes to by default.
func defaultRules() []Rule {
	return []Rule{
		{
			ID: "camera-snapshots", Name: "Camera snapshots", Enabled: true,
			Storage: "local", Path: "camera", Mode: modeFiles, Pattern: "*.jpg", KeepDays: 14,
		},
		{
			ID: "timelapses", Name: "Timelapses", Enabled: true,
			Storage: "local", Path: "zeitraffer", Mode: modeFolders, Depth: 2, KeepDays: 180, KeepNewest: 10,
		},
		{
			ID: "printwatch", Name: "PrintWatch snapshots", Enabled: false,
			Storage: "local", Path: "printwatch", Mode: modeFiles, Pattern: "*.jpg", KeepDays: 7,
		},
	}
}

// check validates a rule and puts its path into the one form the storage
// takes. A rule never reaches the root of a storage: that would put every
// print file in reach of an age limit.
func (r *Rule) check() error {
	r.ID = strings.TrimSpace(r.ID)
	r.Name = strings.TrimSpace(r.Name)
	r.Target = strings.TrimSpace(r.Target)
	r.Storage = strings.TrimSpace(r.Storage)
	r.Device = strings.TrimSpace(r.Device)
	r.Path = strings.Trim(path.Clean("/"+strings.TrimSpace(r.Path)), "/")
	r.Pattern = strings.TrimSpace(r.Pattern)
	r.CopyStorage = strings.TrimSpace(r.CopyStorage)
	r.CopyPath = strings.Trim(path.Clean("/"+strings.TrimSpace(r.CopyPath)), "/")

	if r.Target == "" {
		r.Target = targetStorage
	}

	switch {
	case r.ID == "" || len(r.ID) > 64:
		return fmt.Errorf("%w: id is empty or longer than 64", errRule)
	case r.Name == "" || len(r.Name) > 80:
		return fmt.Errorf("%w: %s: the name is empty or longer than 80", errRule, r.ID)
	case len(r.SavedBy) > 256:
		return fmt.Errorf("%w: %s: saved_by is longer than 256", errRule, r.ID)
	case r.Target == targetPrinter:
		return r.checkPrinter()
	case r.Target != targetStorage:
		return fmt.Errorf("%w: %s: target is %q and has to be storage or printer", errRule, r.ID, r.Target)
	case r.Storage == "":
		return fmt.Errorf("%w: %s: no storage is named", errRule, r.ID)
	case r.Device != "":
		return fmt.Errorf("%w: %s: a storage rule names no printer", errRule, r.ID)
	case r.CopyStorage != "" || r.CopyPath != "" || r.SkipCopyCheck:
		return fmt.Errorf("%w: %s: only a printer rule checks for copies", errRule, r.ID)
	case r.Path == "" || r.Path == ".":
		return fmt.Errorf("%w: %s: the path is the root of the storage, and a rule has to name a folder", errRule, r.ID)
	case r.KeepDays < 1 || r.KeepDays > 3650:
		return fmt.Errorf("%w: %s: keep_days has to be 1 to 3650", errRule, r.ID)
	case r.KeepNewest < 0 || r.KeepNewest > 100000:
		return fmt.Errorf("%w: %s: keep_newest has to be 0 or more", errRule, r.ID)
	}

	switch r.Mode {
	case modeFiles:
		r.Depth = 0
	case modeFolders:
		if r.Depth < 1 || r.Depth > 5 {
			return fmt.Errorf("%w: %s: depth has to be 1 to 5 for folders", errRule, r.ID)
		}
	default:
		return fmt.Errorf("%w: %s: mode is %q and has to be files or folders", errRule, r.ID, r.Mode)
	}

	if r.Pattern != "" {
		if _, err := path.Match(r.Pattern, "x"); err != nil {
			return fmt.Errorf("%w: %s: the pattern %q is not a pattern", errRule, r.ID, r.Pattern)
		}
	}

	return nil
}

// checkPrinter validates a rule on a printer's storage. Its path starts with
// the storage the printer reported (usb, local), and that storage itself may
// be the folder, because that is where print files usually lie. What keeps
// such a rule narrow is the rest: print files only, one folder, by hand
// unless printer_auto is on (policy.go), and by default only files with a copy
// on the server; an active one always checks for copies.
//
// keep_days may be 0 on a printer, for "keep only the newest N whatever their
// age", as long as keep_newest keeps at least one.
func (r *Rule) checkPrinter() error {
	switch {
	case r.Device == "" || len(r.Device) > maxDeviceID:
		return fmt.Errorf("%w: %s: a printer rule names the printer by its id", errRule, r.ID)
	case r.Storage != "":
		return fmt.Errorf("%w: %s: a printer rule names no server storage", errRule, r.ID)
	case r.Path == "" || r.Path == ".":
		return fmt.Errorf("%w: %s: the path has to start with the printer's storage, such as usb", errRule, r.ID)
	case r.Mode != modeFiles:
		return fmt.Errorf("%w: %s: a printer rule judges files one by one, so mode has to be files", errRule, r.ID)
	case r.Enabled && r.SkipCopyCheck:
		return fmt.Errorf("%w: %s: an active printer rule always checks for copies", errRule, r.ID)
	case r.KeepDays < 0 || r.KeepDays > 3650:
		return fmt.Errorf("%w: %s: keep_days has to be 0 to 3650 on a printer", errRule, r.ID)
	case r.KeepNewest < 0 || r.KeepNewest > 100000:
		return fmt.Errorf("%w: %s: keep_newest has to be 0 or more", errRule, r.ID)
	case r.KeepDays == 0 && r.KeepNewest < 1:
		return fmt.Errorf("%w: %s: with keep_days 0, keep_newest has to keep at least one file", errRule, r.ID)
	case r.SkipCopyCheck && (r.CopyStorage != "" || r.CopyPath != ""):
		return fmt.Errorf("%w: %s: a rule without the copy check names no copy storage", errRule, r.ID)
	case len(r.CopyStorage) > maxStorageID:
		return fmt.Errorf("%w: %s: copy_storage is longer than %d", errRule, r.ID, maxStorageID)
	}

	// A missing copy_storage is not refused here, so a printer rule saved by
	// 0.3.0 still loads. placeAllowed refuses it on save, plan and run.

	r.Depth = 0

	if _, err := path.Match(r.Pattern, "x"); err != nil || !printPattern(r.Pattern) {
		return fmt.Errorf("%w: %s: a printer rule needs a pattern for print files, such as *.bgcode or *.gcode", errRule, r.ID)
	}

	return nil
}

// printPattern reports whether a pattern can only match print files: it ends
// in one of printFileSuffixes written out, so no wildcard reaches the
// extension. The file name is matched in lower case (see printer.go).
func printPattern(pattern string) bool {
	lower := strings.ToLower(pattern)

	for _, suffix := range printFileSuffixes {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}

	return false
}

func checkRules(rules []Rule) error {
	if len(rules) > maxRules {
		return fmt.Errorf("%w: %d rules, the limit is %d", errRule, len(rules), maxRules)
	}

	seen := map[string]bool{}

	for i := range rules {
		if err := rules[i].check(); err != nil {
			return err
		}

		if seen[rules[i].ID] {
			return fmt.Errorf("%w: the id %s is used twice", errRule, rules[i].ID)
		}

		seen[rules[i].ID] = true
	}

	return nil
}

// cutoff is the moment before which a unit counts as old.
func (r Rule) cutoff(now time.Time) time.Time {
	return now.Add(-time.Duration(r.KeepDays) * 24 * time.Hour)
}

// rulesFile keeps the rules in the module's data directory.
type rulesFile struct{ path string }

func (f rulesFile) load() ([]Rule, error) {
	raw, err := os.ReadFile(f.path)
	if errors.Is(err, os.ErrNotExist) {
		return defaultRules(), nil
	}

	if err != nil {
		return nil, err
	}

	var rules []Rule
	if err = json.Unmarshal(raw, &rules); err != nil {
		return nil, err
	}

	return rules, checkRules(rules)
}

func (f rulesFile) save(rules []Rule) error {
	raw, err := json.MarshalIndent(rules, "", "  ")
	if err != nil {
		return err
	}

	tmp := f.path + ".tmp"
	if err = os.WriteFile(tmp, raw, 0o640); err != nil {
		return err
	}

	return os.Rename(tmp, f.path)
}

func dataPath(dir, name string) string { return filepath.Join(dir, name) }
