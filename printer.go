package main

import (
	"context"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/prusa3d/prusa-connect/sdk/deviceapi"
)

// A printer rule cleans print files off a printer's own storage, a USB drive
// or its internal disk. It differs from a storage rule on purpose:
//
//   - It lists the printer for the person on the page, with their rights, and
//     never as the module itself. The module holds no right on any printer.
//   - It reads only the folder it names, not the folders below: a printer on
//     Buddy firmware answers one command at a time and takes 12 to 15 seconds
//     to list a folder of about 300 files.
//   - It reads nothing while the printer prints, and it never offers the
//     file of the current or the last print.
//   - A file whose date the printer did not report (0) is never offered.
//   - The page deletes the files one by one with the person's rights. There is
//     no automatic run for printers.

// devices is the part of the device client a printer plan needs, so a test
// can stand in a fake.
type devices interface {
	Get(ctx context.Context, params deviceapi.GetParams) (deviceapi.Device, error)
	Jobs(ctx context.Context, params deviceapi.JobsParams) (deviceapi.Jobs, error)
	Files(ctx context.Context, params deviceapi.FilesParams) (deviceapi.Listing, error)
}

// errPrinting is a printer that is busy with a print.
var errPrinting = errors.New("the printer is printing; nothing is read or deleted until the print has ended")

// planPrinter lists what a printer rule would delete right now, read for
// person.
func planPrinter(ctx context.Context, printers devices, rule Rule, person string, now time.Time) RulePlan {
	plan := RulePlan{
		RuleID: rule.ID, Name: rule.Name, Target: targetPrinter, Device: rule.Device, Path: rule.Path,
		Enabled: false, Delete: []string{},
	}

	if strings.TrimSpace(person) == "" {
		plan.Error = errNobody.Error()

		return plan
	}

	units, err := printerFiles(ctx, printers, rule, person)
	if err != nil {
		plan.Error = err.Error()

		return plan
	}

	fillPlan(&plan, units, rule, now)

	return plan
}

// printerFiles reads the files a printer rule may judge.
func printerFiles(ctx context.Context, printers devices, rule Rule, person string) ([]unit, error) {
	device, err := printers.Get(ctx, deviceapi.GetParams{Principal: person, ID: rule.Device})
	if err != nil {
		return nil, fmt.Errorf("the printer %s could not be read: %w", rule.Device, err)
	}

	if !device.Connected {
		return nil, fmt.Errorf("the printer %s is not connected", printerName(device))
	}

	jobs, err := printers.Jobs(ctx, deviceapi.JobsParams{Principal: person, ID: rule.Device})
	if err != nil {
		return nil, fmt.Errorf("the prints of %s could not be read: %w", printerName(device), err)
	}

	if jobs.Current != nil && jobs.Current.Ended == nil {
		return nil, errPrinting
	}

	protected := jobFiles(jobs)
	pattern := strings.ToLower(rule.Pattern)

	var units []unit

	for offset := 0; ; {
		page, err := printers.Files(ctx, deviceapi.FilesParams{
			Principal: person, ID: rule.Device, Path: rule.Path, Limit: deviceapi.MaxPageLimit, Offset: offset,
		})
		if err != nil {
			return nil, fmt.Errorf("the folder %s on %s could not be read: %w", rule.Path, printerName(device), err)
		}

		if page.Entry != nil && !page.Entry.Folder {
			return nil, fmt.Errorf("%s on %s is a file, and a rule names a folder", rule.Path, printerName(device))
		}

		for _, f := range page.Items {
			name := strings.ToLower(f.DisplayName)
			if name == "" {
				name = strings.ToLower(f.Name)
			}

			if f.Folder || f.ReadOnly || f.Modified <= 0 || protected[strings.ToLower(f.Path)] || protected[name] {
				continue
			}

			if ok, _ := path.Match(pattern, name); !ok {
				continue
			}

			units = append(units, unit{Path: f.Path, Size: f.Size, Files: 1, Newest: time.Unix(f.Modified, 0)})
		}

		offset += len(page.Items)
		if len(page.Items) == 0 || offset >= page.Total || offset >= maxWalked {
			break
		}
	}

	return units, nil
}

// jobFiles are the files of the current and the last print, by path and by
// name, in lower case. The printer may report a path with the short FAT name
// and the job with the long one, so both are kept.
func jobFiles(jobs deviceapi.Jobs) map[string]bool {
	out := map[string]bool{}

	for _, job := range []*deviceapi.Job{jobs.Current, jobs.Last} {
		if job == nil {
			continue
		}

		for _, name := range []string{job.Path, job.DisplayName} {
			name = strings.ToLower(strings.TrimSpace(name))
			if name == "" {
				continue
			}

			out[name] = true
			out[path.Base(name)] = true
		}
	}

	return out
}

func printerName(device deviceapi.Device) string {
	if device.Name != "" {
		return device.Name
	}

	return device.ID
}

// fillPlan counts units into a plan: newest first, keep_newest protects the
// head of the list whatever its age, everything older than keep_days goes.
func fillPlan(plan *RulePlan, units []unit, rule Rule, now time.Time) {
	slices.SortFunc(units, func(a, b unit) int { return b.Newest.Compare(a.Newest) })

	cutoff := rule.cutoff(now)

	for i, u := range units {
		plan.Units++
		plan.Files += u.Files
		plan.Size += u.Size

		if plan.Oldest.IsZero() || u.Newest.Before(plan.Oldest) {
			plan.Oldest = u.Newest
		}

		if i < rule.KeepNewest || !u.Newest.Before(cutoff) {
			continue
		}

		plan.DeleteUnits++
		plan.DeleteFiles += u.Files
		plan.DeleteSize += u.Size

		if len(plan.Delete) < maxPlanPaths {
			plan.Delete = append(plan.Delete, u.Path)
		} else {
			plan.Truncated = true
		}
	}
}
