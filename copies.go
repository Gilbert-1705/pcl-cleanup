package main

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/prusa3d/prusa-connect/sdk/storageapi"
)

// A printer rule deletes a print file off a printer only while the same file
// lies in one of the server's storages, so nothing is lost that cannot be
// sent again. "The same" is the long name, case aside, and the size in bytes:
// both have to match. And the copy may not be much newer than the file on the
// printer: a real copy was uploaded before it was sent to the printer, so a
// copy that appeared later is more likely another file of the same name and
// size than the one the print came from. copyTolerance allows for printer
// clocks and FAT times without a time zone.
//
// The module protocol gives no checksum of a file on a printer, and no way to
// read one, so the content itself cannot be compared. Whoever may write to
// copy_storage can therefore make a file there look like a copy; README
// says so. The printer reports the long name as DisplayName, and a
// FAT short name such as PRINT~1.BGC as Name; the short name is used only when
// there is no long one.
//
// The copies are read with the rights of the person the plan is for. When
// they cannot be read in full, nothing is deleted: a copy that was not read
// would look missing, and so would keep a file, but a scan cut short is
// reported anyway so the plan never looks complete when it is not.

// errCopiesCut is a copy storage that was read only in part.
var errCopiesCut = errors.New("the storage with the copies was read only in part, so nothing is deleted")

// copyTolerance is how much newer than the file on the printer a copy may be.
const copyTolerance = 24 * time.Hour

// copyKey is what a print file and its copy have to share.
type copyKey struct {
	name string
	size int64
}

func keyOf(name string, size int64) copyKey {
	return copyKey{name: strings.ToLower(path.Base(strings.TrimSpace(name))), size: size}
}

// copySet lists the files under CopyPath in CopyStorage, for person, with
// the oldest modification time of each name and size. A copy without a time
// is left out: it could not be compared with the file on the printer.
func copySet(ctx context.Context, files lister, rule Rule, person string) (map[copyKey]time.Time, error) {
	w := &walker{files: files, storage: rule.CopyStorage, principal: person}
	set := map[copyKey]time.Time{}

	err := w.walk(ctx, rule.CopyPath, func(e entry) {
		if e.Size <= 0 || e.MTime.IsZero() {
			return
		}

		key := keyOf(e.Path, e.Size)
		if old, ok := set[key]; !ok || e.MTime.Before(old) {
			set[key] = e.MTime
		}
	})

	switch {
	case errors.Is(err, storageapi.ErrNoFile):
		return nil, fmt.Errorf("the folder %q with the copies does not exist in the storage %s", rule.CopyPath, rule.CopyStorage)
	case err != nil:
		return nil, fmt.Errorf("the copies in the storage %s could not be read: %w", rule.CopyStorage, err)
	case w.cut:
		return nil, errCopiesCut
	}

	return set, nil
}

// markCopies flags the units that have no copy on the server, so fillPlan
// keeps them. It reads the copies only if the plan would delete anything at
// all, because listing a storage can take a while.
func markCopies(ctx context.Context, files lister, rule Rule, person string, units []unit, now time.Time) error {
	if !rule.needsCopy() || !anyCandidate(units, rule, now) {
		return nil
	}

	set, err := copySet(ctx, files, rule, person)
	if err != nil {
		return err
	}

	for i := range units {
		copied, ok := set[keyOf(units[i].Name, units[i].Size)]
		if units[i].Size <= 0 || !ok || !copyFits(copied, units[i].Newest) {
			units[i].NoCopy = true
		}
	}

	return nil
}

// copyFits reports whether a copy written at copied can be the one a file
// on the printer, written at printed, came from.
func copyFits(copied, printed time.Time) bool {
	return !copied.IsZero() && !printed.IsZero() && !copied.After(printed.Add(copyTolerance))
}

// anyCandidate reports whether the age and count limits alone would delete
// a unit. units have to be in plan order (sortUnits).
func anyCandidate(units []unit, rule Rule, now time.Time) bool {
	cutoff := rule.cutoff(now)

	for i, u := range units {
		if i >= rule.KeepNewest && u.Newest.Before(cutoff) {
			return true
		}
	}

	return false
}

// maxFolders bounds the folders the page is offered at one level.
const maxFolders = 500

// Folders are the folders one level below Path in a storage.
type Folders struct {
	Storage   string   `json:"storage"`
	Path      string   `json:"path"`
	Folders   []string `json:"folders"`
	Truncated bool     `json:"truncated,omitempty"`
}

func foldersIn(ctx context.Context, files lister, storage, dir, person string) (Folders, error) {
	if strings.TrimSpace(person) == "" {
		return Folders{}, errNobody
	}

	w := &walker{files: files, storage: storage, principal: person}

	entries, err := w.list(ctx, dir)
	if err != nil {
		return Folders{}, fmt.Errorf("the folder %q in the storage %s could not be read: %w", dir, storage, err)
	}

	out := Folders{Storage: storage, Path: dir, Folders: []string{}, Truncated: w.cut}

	for _, e := range entries {
		if !e.Dir {
			continue
		}

		if len(out.Folders) == maxFolders {
			out.Truncated = true

			break
		}

		out.Folders = append(out.Folders, e.Path)
	}

	return out, nil
}
