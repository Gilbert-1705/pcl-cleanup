package main

import (
	"context"
	"strings"

	"github.com/prusa3d/prusa-connect/sdk/accessapi"
)

// What the overview shows a person. The host lets anyone with view_resource
// on the extension read GET /overview, which says nothing about the printers.
// So everything that comes from a printer or names one is shown only to a
// person who may observe that printer, and who saved a rule only to a person
// who may change it. Every question that fails counts as no: nothing is shown
// that the server did not allow.
//
//   - A printer rule the person may not observe comes with its id, name,
//     target and state only, marked hidden. PUT /rules keeps such a rule as
//     it is stored (see keepHidden).
//   - A run on a printer the person may not observe, and a run that names
//     files but no printer (written before 0.5.0), comes without the file
//     names and without the error, which may name the printer.
//   - Why the last automatic run left a printer rule out is shown only with
//     the rule.
//   - saved_by comes only to a person who may delete where the rule points.
//   - Who ran a rule by hand comes only to that person and to a person who
//     may delete where the rule points; others read "hand" (byHandHidden).
//   - allowed_devices lists only printers the person may observe ("*" stays).

// actionObserve is what a person needs on a printer to see what is on it.
const actionObserve = "observe"

// errHiddenText stands in for anything about a printer the person may not
// see.
const errHiddenText = "hidden: you may not see this printer"

// byHandHidden is RunRecord.By of a run by hand for a reader who may not
// learn who ran it.
const byHandHidden = "hand"

// viewer answers, for one request, what a person may see. It asks the server
// once per printer and rule.
type viewer struct {
	ctx      context.Context
	access   checker
	person   string
	observed map[string]bool
	changes  map[string]bool
}

func newViewer(ctx context.Context, access checker, person string) *viewer {
	return &viewer{
		ctx: ctx, access: access, person: strings.TrimSpace(person),
		observed: map[string]bool{}, changes: map[string]bool{},
	}
}

// observes reports whether the person may see the printer device.
func (v *viewer) observes(device string) bool {
	if v.person == "" || device == "" || v.access == nil {
		return false
	}

	if seen, ok := v.observed[device]; ok {
		return seen
	}

	decision, err := v.access.Check(v.ctx, accessapi.CheckParams{
		Subject: v.person, Action: actionObserve, ObjectType: objectDevice, ObjectID: device,
	})
	seen := err == nil && decision.Allowed
	v.observed[device] = seen

	return seen
}

// mayChange reports whether the person may delete where rule points, the
// same question a save asks.
func (v *viewer) mayChange(rule Rule) bool {
	if may, ok := v.changes[rule.ID]; ok {
		return may
	}

	may := v.access != nil && v.person != "" && mayDelete(v.ctx, v.access, v.person, rule) == nil
	v.changes[rule.ID] = may

	return may
}

// who is RunRecord.By as the person may see it: a run by hand names who ran
// it only to that person and to a person who may change rule. rule is nil
// when no stored rule has the run's name any more.
func (v *viewer) who(by string, rule *Rule) string {
	ran, byHandOf := strings.CutPrefix(by, byHand)
	if !byHandOf {
		return by
	}

	if v.person != "" && ran == v.person {
		return by
	}

	if rule != nil && v.mayChange(*rule) {
		return by
	}

	return byHandHidden
}

// rule is rule as the person may see it.
func (v *viewer) rule(rule Rule) Rule {
	if rule.printer() && !v.observes(rule.Device) {
		return Rule{ID: rule.ID, Name: rule.Name, Enabled: rule.Enabled, Target: rule.Target, Hidden: true}
	}

	if rule.SavedBy != "" && !v.mayChange(rule) {
		rule.SavedBy = ""
	}

	return rule
}

// run is a history entry as the person may see it. rule is the stored rule
// of that name, or nil.
func (v *viewer) run(record RunRecord, rule *Rule) RunRecord {
	record.By = v.who(record.By, rule)

	if len(record.Deleted) == 0 && record.Device == "" {
		return record
	}

	if record.Device != "" && v.observes(record.Device) {
		return record
	}

	record.Deleted = nil
	record.Device = ""

	if record.Error != "" {
		record.Error = errHiddenText
	}

	return record
}

// overview fills the parts of state that depend on the person.
func (v *viewer) overview(state *State, rules []Rule, history []RunRecord, skipped map[string]string, devices []string) {
	visible := map[string]bool{}
	// A run names its rule by name, and names are unique (checkRules). Two
	// stored rules of one name, kept from before that check, name no rule:
	// who ran it is then shown to nobody but that person.
	byName := map[string]*Rule{}

	for i := range rules {
		if _, taken := byName[rules[i].Name]; taken {
			byName[rules[i].Name] = nil

			continue
		}

		byName[rules[i].Name] = &rules[i]
	}

	state.Rules = make([]Rule, 0, len(rules))
	for _, rule := range rules {
		shown := v.rule(rule)
		state.Rules = append(state.Rules, shown)
		visible[rule.ID] = !shown.Hidden
	}

	state.History = make([]RunRecord, 0, len(history))
	for _, record := range history {
		state.History = append(state.History, v.run(record, byName[record.Rule]))
	}

	for id, why := range skipped {
		if !visible[id] {
			continue
		}

		if state.Skipped == nil {
			state.Skipped = map[string]string{}
		}

		state.Skipped[id] = why
	}

	state.AllowedDevices = []string{}
	for _, device := range devices {
		if device == anyEntry || v.observes(device) {
			state.AllowedDevices = append(state.AllowedDevices, device)
		}
	}
}

// keepHidden replaces each rule the page sent back as hidden with the rule as
// it is stored, so a person who may not see a printer rule can still save the
// others. A hidden rule that is not stored any more is dropped. Nothing the
// page sent for it is kept.
func keepHidden(saved, kept []Rule) []Rule {
	stored := map[string]Rule{}
	for _, rule := range kept {
		stored[rule.ID] = rule
	}

	out := make([]Rule, 0, len(saved))

	for _, rule := range saved {
		if !rule.Hidden {
			out = append(out, rule)

			continue
		}

		if old, ok := stored[strings.TrimSpace(rule.ID)]; ok {
			out = append(out, old)
		}
	}

	return out
}
