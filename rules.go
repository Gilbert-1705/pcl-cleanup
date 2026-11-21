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
type Rule struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Enabled    bool   `json:"enabled"`
	Storage    string `json:"storage"`
	Path       string `json:"path"`
	Mode       string `json:"mode"`
	Depth      int    `json:"depth,omitempty"`
	Pattern    string `json:"pattern,omitempty"`
	KeepDays   int    `json:"keep_days"`
	KeepNewest int    `json:"keep_newest,omitempty"`
}

const (
	modeFiles   = "files"
	modeFolders = "folders"
	maxRules    = 32
)

var errRule = errors.New("the rule is not usable")

// defaultRules are what a fresh install starts with.
func defaultRules() []Rule {
	return []Rule{
		{
			ID: "kamera-snapshots", Name: "Kamera-Snapshots", Enabled: true,
			Storage: "local", Path: "camera", Mode: modeFiles, Pattern: "*.jpg", KeepDays: 14,
		},
		{
			ID: "zeitraffer", Name: "Zeitraffer", Enabled: true,
			Storage: "local", Path: "zeitraffer", Mode: modeFolders, Depth: 2, KeepDays: 180, KeepNewest: 10,
		},
		{
			ID: "printwatch", Name: "PrintWatch-Snapshots", Enabled: false,
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
	r.Storage = strings.TrimSpace(r.Storage)
	r.Path = strings.Trim(path.Clean("/"+strings.TrimSpace(r.Path)), "/")
	r.Pattern = strings.TrimSpace(r.Pattern)

	switch {
	case r.ID == "" || len(r.ID) > 64:
		return fmt.Errorf("%w: id is empty or longer than 64", errRule)
	case r.Name == "" || len(r.Name) > 80:
		return fmt.Errorf("%w: %s: the name is empty or longer than 80", errRule, r.ID)
	case r.Storage == "":
		return fmt.Errorf("%w: %s: no storage is named", errRule, r.ID)
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
