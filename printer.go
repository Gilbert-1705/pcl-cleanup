package main

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/prusa3d/prusa-connect/sdk/deviceapi"
)

// A printer rule cleans print files off a printer's own storage, a USB drive
// or its internal disk. It differs from a storage rule on purpose:
//
//   - By hand, it lists the printer for the person on the page, with their
//     rights. Only the automatic run (auto_printer.go, off by default) reads
//     it as the module itself, which then needs the operator role on that
//     printer from the operator.
//   - It reads only the folder it names, not the folders below: a printer on
//     Buddy firmware answers one command at a time and takes 12 to 15 seconds
//     to list a folder of about 300 files.
//   - It reads nothing while the printer prints, and it never offers the
//     file of the current or the last print.
//   - A file whose date the printer did not report (0) is never offered.
//   - By default it deletes only files whose copy lies in one of the server's
//     storages (copies.go). Files without one still count among the newest.
//   - The page deletes the files one by one with the person's rights.

// devices is the part of the device client a printer plan needs, so a test
// can stand in a fake.
type devices interface {
	Get(ctx context.Context, params deviceapi.GetParams) (deviceapi.Device, error)
	Jobs(ctx context.Context, params deviceapi.JobsParams) (deviceapi.Jobs, error)
	Files(ctx context.Context, params deviceapi.FilesParams) (deviceapi.Listing, error)
	Command(ctx context.Context, params deviceapi.CommandParams) (deviceapi.Result, error)
}

// errPrinting is a printer that is busy with a print.
var errPrinting = errors.New("the printer is printing; nothing is read or deleted until the print has ended")

// planPrinter lists what a printer rule would delete right now, read for
// the person on the page. A request without a person reads nothing.
func planPrinter(ctx context.Context, printers devices, files lister, rule Rule, person string, now time.Time) RulePlan {
	if strings.TrimSpace(person) == "" {
		plan := emptyPrinterPlan(rule)
		plan.Error = errNobody.Error()

		return plan
	}

	return printerPlan(ctx, printers, files, rule, person, now)
}

func emptyPrinterPlan(rule Rule) RulePlan {
	return RulePlan{
		RuleID: rule.ID, Name: rule.Name, Target: targetPrinter, Device: rule.Device, Path: rule.Path,
		Enabled: rule.Enabled, Delete: []string{}, CopyCheck: rule.needsCopy(),
	}
}

// printerPlan reads the printer and the copies for principal: a person, or
// the module itself when empty (only the automatic run).
func printerPlan(ctx context.Context, printers devices, files lister, rule Rule, person string, now time.Time) RulePlan {
	plan := emptyPrinterPlan(rule)

	units, err := printerFiles(ctx, printers, rule, person)
	if err != nil {
		plan.Error = err.Error()

		return plan
	}

	sortUnits(units)

	if err = markCopies(ctx, files, rule, person, units, now); err != nil {
		// Without the copies, the plan still shows what lies on the printer, but
		// deletes nothing.
		fillPlan(&plan, units, rule, now)
		plan.Error = err.Error()
		plan.Delete, plan.deleteSizes, plan.deleteNames = []string{}, nil, nil
		plan.DeleteUnits, plan.DeleteFiles, plan.DeleteSize = 0, 0, 0

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

			// What gets deleted is f.Path, so it, not only the name, has to be
			// a print file directly in the rule's folder.
			if !printPathIn(f.Path, rule.Path) {
				continue
			}

			if ok, _ := path.Match(pattern, name); !ok {
				continue
			}

			units = append(units, unit{Path: f.Path, Name: name, Size: f.Size, Files: 1, Newest: time.Unix(f.Modified, 0)})
		}

		offset += len(page.Items)
		if len(page.Items) == 0 || offset >= page.Total || offset >= maxWalked {
			break
		}
	}

	return units, nil
}

// printPathIn reports whether p, a path the printer reported, is a print file
// directly in dir: not in a folder below it, not elsewhere, and with a print
// file ending or its FAT short form. Case is ignored, as FAT ignores it.
func printPathIn(p, dir string) bool {
	lower := strings.ToLower(strings.TrimSpace(p))
	if lower == "" || path.Clean(lower) != lower || strings.Contains(lower, "\\") {
		return false
	}

	if !strings.EqualFold(path.Dir(lower), strings.ToLower(dir)) {
		return false
	}

	for _, suffix := range printPathSuffixes {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}

	return false
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
// head of the list whatever its age, everything older than keep_days goes,
// except a printer file without a copy on the server, which stays.
func fillPlan(plan *RulePlan, units []unit, rule Rule, now time.Time) {
	sortUnits(units)

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

		if u.NoCopy {
			plan.KeptNoCopy++
			plan.KeptNoCopySize += u.Size

			if len(plan.KeptNoCopyList) < maxPlanPaths {
				plan.KeptNoCopyList = append(plan.KeptNoCopyList, u.Name)
			}

			continue
		}

		plan.DeleteUnits++
		plan.DeleteFiles += u.Files
		plan.DeleteSize += u.Size

		if len(plan.Delete) < maxPlanPaths {
			plan.Delete = append(plan.Delete, u.Path)
			plan.deleteSizes = append(plan.deleteSizes, u.Size)
			plan.deleteNames = append(plan.deleteNames, u.Name)
		} else {
			plan.Truncated = true
		}
	}
}
