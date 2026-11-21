package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/prusa3d/prusa-connect/sdk/storageapi"
)

// Regression: a scan that hits maxWalked must not age the folders it did not
// read in full. Before 0.2.0 they counted as empty and so as old.

// fakeTimelapses: zeitraffer/mark-i/run-NNNN/ with filesPerRun files each,
// all written an hour ago. Pages of 1000 entries.
type fakeTimelapses struct {
	runs, filesPerRun int
	mtime             int64
}

func (f fakeTimelapses) List(_ context.Context, p storageapi.ListParams) (storageapi.Listing, error) {
	var all []storageapi.ListEntry
	switch {
	case p.Path == "zeitraffer":
		all = []storageapi.ListEntry{{Name: "mark-i", Dir: true}}
	case p.Path == "zeitraffer/mark-i":
		for i := range f.runs {
			all = append(all, storageapi.ListEntry{Name: fmt.Sprintf("run-%04d", i), Dir: true})
		}
	case strings.HasPrefix(p.Path, "zeitraffer/mark-i/run-"):
		for i := range f.filesPerRun {
			all = append(all, storageapi.ListEntry{Name: fmt.Sprintf("f%05d.jpg", i), Size: 100, MTime: f.mtime})
		}
	default:
		return storageapi.Listing{}, storageapi.ErrNoFile
	}
	start, _ := strconv.Atoi(p.Cursor)
	end := min(start+1000, len(all))
	out := storageapi.Listing{Entries: all[start:end]}
	if end < len(all) {
		out.Next = strconv.Itoa(end)
	}
	return out, nil
}

func TestCutWalkNeverDeletesRecentFolders(t *testing.T) {
	now := time.Now()
	files := fakeTimelapses{runs: 700, filesPerRun: 1000, mtime: now.Add(-time.Hour).Unix()}
	rule := defaultRules()[1] // Zeitraffer: folders, depth 2, 180 days, keep 10
	if err := rule.check(); err != nil {
		t.Fatal(err)
	}
	plan := planRule(context.Background(), files, rule, now)
	t.Logf("units=%d delete_units=%d truncated=%v", plan.Units, plan.DeleteUnits, plan.Truncated)
	if plan.DeleteUnits != 0 || len(plan.Delete) != 0 {
		t.Fatalf("every run is an hour old, yet the plan deletes %d of them (first %v)", plan.DeleteUnits, plan.Delete[:min(3, len(plan.Delete))])
	}
}

func TestOldFoldersStillGoWithoutCut(t *testing.T) {
	now := time.Now()
	files := fakeTimelapses{runs: 30, filesPerRun: 5, mtime: now.Add(-200 * 24 * time.Hour).Unix()}
	rule := defaultRules()[1]
	if err := rule.check(); err != nil {
		t.Fatal(err)
	}
	plan := planRule(context.Background(), files, rule, now)
	if plan.Truncated || plan.Units != 30 || plan.DeleteUnits != 20 {
		t.Fatalf("units=%d delete=%d truncated=%v, want 30, 20 (keep_newest 10), false", plan.Units, plan.DeleteUnits, plan.Truncated)
	}
}

func TestCutWalkKeepsOldFoldersReadInFull(t *testing.T) {
	now := time.Now()
	files := fakeTimelapses{runs: 700, filesPerRun: 1000, mtime: now.Add(-200 * 24 * time.Hour).Unix()}
	rule := defaultRules()[1]
	if err := rule.check(); err != nil {
		t.Fatal(err)
	}
	plan := planRule(context.Background(), files, rule, now)
	t.Logf("units=%d delete_units=%d truncated=%v", plan.Units, plan.DeleteUnits, plan.Truncated)
	if !plan.Truncated || plan.Units == 0 || plan.Units >= 700 || plan.DeleteUnits != plan.Units-10 {
		t.Fatalf("units=%d delete=%d truncated=%v", plan.Units, plan.DeleteUnits, plan.Truncated)
	}
}
