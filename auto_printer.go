package main

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/prusa3d/prusa-connect/sdk/connectorapi"
	"github.com/prusa3d/prusa-connect/sdk/deviceapi"
)

// The automatic run for printer rules is off unless the operator turns on
// printer_auto. Every one of these has to hold before it deletes a file:
//
//   - printer_auto is on and auto_interval is set (cleaner.go);
//   - the printer is in allowed_devices and the copy storage in
//     allowed_storages, and the rule is active and checks for copies;
//   - the person who saved the rule may still operate the printer
//     (access.check, policy.go);
//   - the module itself may read and operate the printer: it has no person
//     behind it, so the host answers for its own instance, which holds
//     nothing until the operator grants it the operator role on that
//     printer. That role would also let it start a print; this code only
//     lists, reads the jobs and sends delete_file;
//   - the printer is connected and not printing, read fresh, and the copies
//     were read in full;
//   - before each file, the printer is still not printing and the file is
//     not the one of the current or the last print.
//
// A run deletes at most printer_auto_max files per rule, the oldest first, and
// writes each one into the history. In a dry run (the default) it deletes
// nothing and writes down what it would have deleted.

// verbDeleteFile is the command that removes one file from a printer's
// storage. The host checks it like the HTTP route: storage-prefixed path, no
// "..", operate on the printer.
const verbDeleteFile = string(connectorapi.VerbDeleteFile)

// runPrinter applies one active printer rule as the module itself. A rule
// that cannot even be planned (printing, away, no grant, copies unreadable)
// comes back as skip, which the page shows next to the rule: on a schedule of
// an hour it would otherwise push the real runs out of the history.
func runPrinter(
	ctx context.Context, printers devices, files lister, rule Rule, settings Settings, by string, now time.Time,
) (record RunRecord, skip error) {
	record = RunRecord{At: now, By: by, Rule: rule.Name, Device: rule.Device, DryRun: settings.dryRun}

	plan := printerPlan(ctx, printers, files, rule, "", now)
	if plan.Error != "" {
		return record, errors.New(plan.Error)
	}

	if !plan.CopyCheck {
		return record, errors.New("an automatic printer rule has to check for copies")
	}

	// The plan lists the files to delete newest first; the oldest go first.
	order := make([]int, 0, len(plan.Delete))
	for i := len(plan.Delete) - 1; i >= 0; i-- {
		order = append(order, i)
	}

	if len(order) > settings.printerMax {
		order = order[:settings.printerMax]
		record.Partial = true
	}

	record.Partial = record.Partial || plan.Truncated

	for _, i := range order {
		file := plan.Delete[i]

		if settings.dryRun {
			record.Units++
			record.Files++
			record.Freed += plan.deleteSizes[i]
			record.Deleted = append(record.Deleted, plan.deleteNames[i])

			continue
		}

		if err := stillFree(ctx, printers, rule.Device, file, plan.deleteNames[i]); err != nil {
			record.Error = err.Error()
			record.Partial = true

			break
		}

		result, err := printers.Command(ctx, deviceapi.CommandParams{
			ID: rule.Device, Verb: verbDeleteFile, Args: map[string]string{"path": file},
		})

		switch {
		case err != nil:
			record.Error = fmt.Sprintf("%s could not be deleted: %v", plan.deleteNames[i], err)
		case !result.OK:
			record.Error = fmt.Sprintf("the printer refused to delete %s: %s", plan.deleteNames[i], result.MachineReason)
		}

		if record.Error != "" {
			record.Partial = true

			break
		}

		record.Units++
		record.Files++
		record.Freed += plan.deleteSizes[i]
		record.Deleted = append(record.Deleted, plan.deleteNames[i])
	}

	return record, nil
}

// errHeld is a file that became the one of the current or the last print
// after the plan was made.
var errHeld = errors.New("a print started or ended since the printer was read; the run stops and reads again next time")

// stillFree asks the jobs again right before a file goes: a print may have
// started since the printer was read, perhaps of this very file.
func stillFree(ctx context.Context, printers devices, device, file, name string) error {
	jobs, err := printers.Jobs(ctx, deviceapi.JobsParams{ID: device})
	if err != nil {
		return fmt.Errorf("the prints could not be read again: %w", err)
	}

	if jobs.Current != nil && jobs.Current.Ended == nil {
		return errPrinting
	}

	held := jobFiles(jobs)
	if held[strings.ToLower(file)] || held[strings.ToLower(path.Base(file))] || held[strings.ToLower(name)] {
		return errHeld
	}

	return nil
}
