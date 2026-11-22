package main

import (
	"context"
	"errors"
	"path"
	"slices"
	"time"

	"github.com/prusa3d/prusa-connect/sdk/storageapi"
)

// Bounds on one scan, so a storage with millions of files cannot hold the
// module for ever. A plan cut short says so.
const (
	maxWalked     = 500_000
	maxPlanPaths  = 3_000
	maxUsageItems = 200
)

// lister is the part of the storage client the scan needs, so a test can
// stand in a fake.
type lister interface {
	List(ctx context.Context, params storageapi.ListParams) (storageapi.Listing, error)
}

// entry is one file or folder, with its full path inside the storage.
type entry struct {
	Path  string
	Dir   bool
	Size  int64
	MTime time.Time
}

// walker lists folders and counts what it read. Principal is the person the
// listing is for, and empty for the module's own work: with a person, the
// server lists with that person's rights.
type walker struct {
	files     lister
	storage   string
	principal string
	walked    int
	cut       bool
}

func (w *walker) list(ctx context.Context, dir string) ([]entry, error) {
	var out []entry

	cursor := ""

	for {
		page, err := w.files.List(ctx, storageapi.ListParams{Principal: w.principal, Storage: w.storage, Path: dir, Cursor: cursor})
		if err != nil {
			return out, err
		}

		for _, e := range page.Entries {
			w.walked++
			out = append(out, entry{
				Path: joinPath(dir, e.Name), Dir: e.Dir, Size: e.Size, MTime: time.Unix(e.MTime, 0),
			})
		}

		if page.Next == "" {
			return out, nil
		}

		if w.walked >= maxWalked {
			w.cut = true

			return out, nil
		}

		cursor = page.Next
	}
}

// walk calls visit for every file under dir.
func (w *walker) walk(ctx context.Context, dir string, visit func(entry)) error {
	entries, err := w.list(ctx, dir)
	if err != nil {
		return err
	}

	for _, e := range entries {
		if w.walked >= maxWalked {
			w.cut = true

			return nil
		}

		if e.Dir {
			if err = w.walk(ctx, e.Path, visit); err != nil && !errors.Is(err, storageapi.ErrNoFile) {
				return err
			}

			continue
		}

		visit(e)
	}

	return nil
}

func joinPath(dir, name string) string {
	if dir == "" {
		return name
	}

	return dir + "/" + name
}

// unit is what a rule keeps or deletes as a whole: one file, or one folder
// with everything in it.
type unit struct {
	Path   string    `json:"path"`
	Size   int64     `json:"size"`
	Files  int       `json:"files"`
	Newest time.Time `json:"newest"`
}

// RulePlan is what one rule would delete right now.
type RulePlan struct {
	RuleID      string    `json:"rule_id"`
	Name        string    `json:"name"`
	Target      string    `json:"target"`
	Storage     string    `json:"storage,omitempty"`
	Device      string    `json:"device,omitempty"`
	Path        string    `json:"path"`
	Enabled     bool      `json:"enabled"`
	Units       int       `json:"units"`
	Files       int       `json:"files"`
	Size        int64     `json:"size"`
	Oldest      time.Time `json:"oldest,omitzero"`
	DeleteUnits int       `json:"delete_units"`
	DeleteFiles int       `json:"delete_files"`
	DeleteSize  int64     `json:"delete_size"`
	Delete      []string  `json:"delete"`
	Truncated   bool      `json:"truncated,omitempty"`
	// Lazy is a printer rule the overview does not scan, because reading a
	// printer is slow and occupies it. The page asks for that rule alone.
	Lazy  bool   `json:"lazy,omitempty"`
	Error string `json:"error,omitempty"`
}

// planRule scans the place a rule covers and lists what it would delete, for
// the module itself (principal empty) or for a person.
func planRule(ctx context.Context, files lister, rule Rule, principal string, now time.Time) RulePlan {
	plan := RulePlan{
		RuleID: rule.ID, Name: rule.Name, Target: targetStorage, Storage: rule.Storage, Path: rule.Path,
		Enabled: rule.Enabled, Delete: []string{},
	}

	w := &walker{files: files, storage: rule.Storage, principal: principal}

	units, err := collect(ctx, w, rule)
	if err != nil && !errors.Is(err, storageapi.ErrNoFile) {
		plan.Error = err.Error()

		return plan
	}

	fillPlan(&plan, units, rule, now)

	plan.Truncated = plan.Truncated || w.cut

	return plan
}

func collect(ctx context.Context, w *walker, rule Rule) ([]unit, error) {
	var units []unit

	if rule.Mode == modeFiles {
		err := w.walk(ctx, rule.Path, func(e entry) {
			if rule.Pattern != "" {
				if ok, _ := path.Match(rule.Pattern, path.Base(e.Path)); !ok {
					return
				}
			}

			units = append(units, unit{Path: e.Path, Size: e.Size, Files: 1, Newest: e.MTime})
		})

		return units, err
	}

	// Folders: descend to the configured depth, then each folder there is one
	// unit, aged by the newest file inside it. Files lying at a shallower
	// level are left alone.
	level := []string{rule.Path}

	for range rule.Depth {
		var next []string

		for _, dir := range level {
			entries, err := w.list(ctx, dir)
			if err != nil {
				if errors.Is(err, storageapi.ErrNoFile) {
					continue
				}

				return units, err
			}

			for _, e := range entries {
				if e.Dir {
					next = append(next, e.Path)
				}
			}
		}

		level = next
	}

	for _, dir := range level {
		if w.cut {
			// The scan hit its bound. Folders not yet read stay out of the plan.
			break
		}

		u := unit{Path: dir}

		err := w.walk(ctx, dir, func(e entry) {
			u.Files++
			u.Size += e.Size

			if e.MTime.After(u.Newest) {
				u.Newest = e.MTime
			}
		})
		if err != nil && !errors.Is(err, storageapi.ErrNoFile) {
			return units, err
		}

		if w.cut {
			// This folder was read only in part, or not at all. Its newest file
			// may be among the entries not read, so its age is unknown: it is
			// neither kept nor deleted by this plan.
			break
		}

		if u.Files == 0 {
			// An empty folder is as old as it gets.
			u.Newest = time.Unix(0, 0)
		}

		units = append(units, u)
	}

	return units, nil
}

// UsageItem is one top-level entry of a storage and what it holds.
type UsageItem struct {
	Path   string    `json:"path"`
	Dir    bool      `json:"dir"`
	Size   int64     `json:"size"`
	Files  int       `json:"files"`
	Newest time.Time `json:"newest,omitzero"`
}

// Usage is how a storage's space is spread over its top-level entries.
type Usage struct {
	Storage   string      `json:"storage"`
	Size      int64       `json:"size"`
	Files     int         `json:"files"`
	Items     []UsageItem `json:"items"`
	Scanned   time.Time   `json:"scanned"`
	Truncated bool        `json:"truncated,omitempty"`
}

func usageOf(ctx context.Context, files lister, storage, principal string, now time.Time) (Usage, error) {
	w := &walker{files: files, storage: storage, principal: principal}

	top, err := w.list(ctx, "")
	if err != nil {
		return Usage{}, err
	}

	out := Usage{Storage: storage, Scanned: now, Items: []UsageItem{}}

	for _, e := range top {
		item := UsageItem{Path: e.Path, Dir: e.Dir}

		if e.Dir {
			err = w.walk(ctx, e.Path, func(f entry) {
				item.Files++
				item.Size += f.Size

				if f.MTime.After(item.Newest) {
					item.Newest = f.MTime
				}
			})
			if err != nil && !errors.Is(err, storageapi.ErrNoFile) {
				return out, err
			}
		} else {
			item.Files, item.Size, item.Newest = 1, e.Size, e.MTime
		}

		out.Size += item.Size
		out.Files += item.Files
		out.Items = append(out.Items, item)
	}

	slices.SortFunc(out.Items, func(a, b UsageItem) int {
		switch {
		case a.Size > b.Size:
			return -1
		case a.Size < b.Size:
			return 1
		}

		return 0
	})

	if len(out.Items) > maxUsageItems {
		out.Items = out.Items[:maxUsageItems]
	}

	out.Truncated = w.cut

	return out, nil
}
