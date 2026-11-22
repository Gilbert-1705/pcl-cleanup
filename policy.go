package main

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/prusa3d/prusa-connect/sdk/accessapi"
)

// Two layers keep a rule from deleting more than its author could delete by
// hand.
//
//  1. The operator's lists: allowed_storages and allowed_devices name where
//     rules may point at all. A rule outside them is refused on save and
//     skipped by every run. Only someone who may configure the extension
//     changes them.
//  2. The author's own rights: saving a changed rule asks the server whether
//     the person saving it may delete there (write_storage on storages,
//     operate on the printer). The rule remembers that person, and an
//     automatic run asks again for them before it deletes anything, because
//     the API token it deletes with may hold more rights than they do.
//
// A rule nobody has saved since 0.3.0 (the defaults, or rules from an older
// version) has no author, so it never runs automatically until somebody saves
// it.

var (
	// errNotAllowed is a rule outside the operator's lists.
	errNotAllowed = errors.New("not allowed by the extension's settings")
	// errForbidden is a person who may not delete where the rule points.
	errForbidden = errors.New("the person may not delete there")
	// errNobody is a request without a person behind it.
	errNobody = errors.New("the request carries no person")
)

// The questions this module asks. Both are listed under checks: in
// module.yaml; the module never takes either action as itself.
const (
	actionWriteStorage = "write_storage"
	actionOperate      = "operate"
	objectResourceKind = "resource_kind"
	objectDevice       = "device"
	kindStorage        = "storage"
	anyEntry           = "*"
)

// checker is the part of the access client the policy needs, so a test can
// stand in a fake.
type checker interface {
	Check(ctx context.Context, params accessapi.CheckParams) (accessapi.Decision, error)
}

// allowedIn reports whether a list from the settings lets name in. "*" lets
// everything in; an empty list nothing.
func allowedIn(list []string, name string) bool {
	return slices.Contains(list, anyEntry) || slices.Contains(list, name)
}

// placeAllowed checks a rule against the operator's lists.
func (s Settings) placeAllowed(rule Rule) error {
	if rule.printer() {
		if !allowedIn(s.AllowedDevices, rule.Device) {
			return fmt.Errorf("%w: the printer %s is not in allowed_devices", errNotAllowed, rule.Device)
		}

		return nil
	}

	if !allowedIn(s.AllowedStorages, rule.Storage) {
		return fmt.Errorf("%w: the storage %s is not in allowed_storages", errNotAllowed, rule.Storage)
	}

	return nil
}

// question is what to ask the server about a person and a rule.
func question(person string, rule Rule) accessapi.CheckParams {
	if rule.printer() {
		return accessapi.CheckParams{Subject: person, Action: actionOperate, ObjectType: objectDevice, ObjectID: rule.Device}
	}

	// write_storage is granted on the storage kind as a whole in Prusa Connect
	// Local v0.1.0-beta.1, not per storage. allowed_storages narrows it.
	return accessapi.CheckParams{Subject: person, Action: actionWriteStorage, ObjectType: objectResourceKind, ObjectID: kindStorage}
}

// mayDelete asks the server whether person may delete where rule points.
func mayDelete(ctx context.Context, access checker, person string, rule Rule) error {
	if strings.TrimSpace(person) == "" {
		// An empty subject would ask about the module itself.
		return errNobody
	}

	decision, err := access.Check(ctx, question(person, rule))
	if err != nil {
		return fmt.Errorf("the server could not say whether %s may delete there: %w", person, err)
	}

	if !decision.Allowed {
		return fmt.Errorf("%w: %s", errForbidden, person)
	}

	return nil
}

// adopt merges the rules a person saves with the rules kept so far. A rule
// that is unchanged keeps its author and is not checked again. A new or
// changed rule has to pass the operator's lists and the person's rights, and
// gets the person as its author.
func adopt(ctx context.Context, access checker, settings Settings, person string, kept, saved []Rule) ([]Rule, error) {
	before := map[string]Rule{}
	for _, rule := range kept {
		before[rule.ID] = rule
	}

	out := make([]Rule, len(saved))

	for i, rule := range saved {
		if old, ok := before[rule.ID]; ok && sameRule(old, rule) {
			rule.SavedBy = old.SavedBy
			out[i] = rule

			continue
		}

		if err := settings.placeAllowed(rule); err != nil {
			return nil, fmt.Errorf("%s: %w", rule.Name, err)
		}

		if err := mayDelete(ctx, access, person, rule); err != nil {
			return nil, fmt.Errorf("%s: %w", rule.Name, err)
		}

		rule.SavedBy = person
		out[i] = rule
	}

	return out, nil
}

// mayRunAutomatically says whether an automatic run deletes for a rule now,
// and if not, why. It asks for the rule's author, who is usually not on the
// page at that moment.
func mayRunAutomatically(ctx context.Context, access checker, settings Settings, rule Rule) error {
	switch {
	case rule.printer():
		return errors.New("printer rules only run by hand")
	case rule.SavedBy == "":
		return errors.New("nobody has saved this rule since 0.3.0, so it only runs by hand until it is saved once")
	}

	if err := settings.placeAllowed(rule); err != nil {
		return err
	}

	return mayDelete(ctx, access, rule.SavedBy, rule)
}
