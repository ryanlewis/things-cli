package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/ryanlewis/things-cli/internal/db"
	"github.com/ryanlewis/things-cli/internal/model"
)

// unresolvableItemTypes are the payload item types GetTaskByUUID will never
// return a row for, so the repeating check and the read-back skip them rather
// than warn that Things does not know an id it knows perfectly well.
//
// Checklist items live in their own table, not TMTask. Headings are TMTask
// rows but the lookups exclude the heading type (issue #146). Neither can
// repeat or carry a status, so nothing is lost by skipping them.
var unresolvableItemTypes = map[string]bool{
	"checklist-item": true,
	"heading":        true,
}

// importUpdate is one `operation: update` item found in an import payload,
// reduced to the parts the repeating check and the status read-back need.
type importUpdate struct {
	// path locates the item in the payload for error messages, e.g. `[2]` for
	// a top-level item or `[2].attributes.items[0]` for one nested in a
	// project.
	path     string
	itemType string
	id       string
	attrs    map[string]any
}

// resolvable reports whether the item names a row the CLI can look up. Items
// without an id are Things' problem to report, and checklist items and
// headings are not reachable through GetTaskByUUID.
func (u importUpdate) resolvable() bool {
	return u.id != "" && !unresolvableItemTypes[u.itemType]
}

// importPlan is what the pre-write pass learned about a payload: the update
// items in it, and the database rows they point at. Sharing the lookups means
// the read-back afterwards knows each item's title without querying again.
type importPlan struct {
	updates []importUpdate
	tasks   map[string]*model.Task // by id; a nil value means "not in the database"
}

// importUpdates collects every `operation: update` item in a Things JSON
// payload. Items nest — a project carries `items`, a to-do carries
// `checklist-items` — so this walks the whole decoded tree the way
// walkImportTags does. The payload has already been validated as JSON by the
// caller; anything unparseable yields no updates and the import proceeds as
// before, leaving Things to reject it.
func importUpdates(data []byte) []importUpdate {
	var payload any
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil
	}
	var updates []importUpdate
	walkImportUpdates(payload, "", &updates)
	return updates
}

func walkImportUpdates(node any, path string, updates *[]importUpdate) {
	switch v := node.(type) {
	case map[string]any:
		if op, _ := v["operation"].(string); op == "update" {
			id, _ := v["id"].(string)
			itemType, _ := v["type"].(string)
			attrs, _ := v["attributes"].(map[string]any)
			*updates = append(*updates, importUpdate{
				path:     path,
				itemType: strings.TrimSpace(itemType),
				id:       strings.TrimSpace(id),
				attrs:    attrs,
			})
		}
		// Walk keys in a fixed order: Go randomises map iteration, and the
		// order items are discovered in decides the order they are reported.
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			walkImportUpdates(v[key], path+"."+key, updates)
		}
	case []any:
		for i, child := range v {
			walkImportUpdates(child, fmt.Sprintf("%s[%d]", path, i), updates)
		}
	}
}

// restrictedImportAttrs names the attributes in an update item's `attributes`
// that Things refuses on a repeating item, in the order the docs list them. It
// is the payload-shaped counterpart of restrictedEdits, which does the same
// job for the `edit` flags.
//
// Presence is enough, whatever the value. The URL scheme docs say of both
// status fields that "this field cannot be updated on repeating to-dos" (and
// the same for repeating projects), so `"completed": false` is refused exactly
// like `"completed": true` — setting a repeating item to incomplete is as much
// an update of that field as completing it.
func restrictedImportAttrs(attrs map[string]any) []string {
	var blocked []string
	for _, name := range []string{"when", "deadline", "completed", "canceled"} {
		if _, ok := attrs[name]; ok {
			blocked = append(blocked, name)
		}
	}
	return blocked
}

// wantedStatus reports the status an update item asks Things to move the item
// to. Both status fields are two-way, per the `update` command's parameter
// table at repeatingDocsURL, which is worth quoting because the cross-status
// cases are the surprising ones:
//
//	completed: "Complete a to-do or set a to-do to incomplete. […] Setting
//	           completed=false on a canceled to-do will also mark it as
//	           incomplete."
//	canceled:  "Cancel a to-do or set a to-do to incomplete. […] Setting
//	           canceled=false on a completed to-do will also mark it as
//	           incomplete."
//
// So a literal `false` asks for `open` whichever status the item is in — the
// two sentences above cover the mismatched pairs explicitly — and a dropped
// reopen is as invisible as a dropped completion. The item's current status is
// therefore not needed here.
//
// `canceled` "Takes priority over `completed`", so it decides the outcome
// whenever it is present — with one exception. The two entries disagree about
// `canceled: false` alongside `completed: true`: `canceled` claims priority
// outright, while `completed` says it is ignored only "if `canceled` is also
// set to `true`". Rather than guess, that one combination is left unverified;
// guessing wrong would fail an import that Things applied correctly. It is
// still refused up front on a repeating target, where the outcome does not
// matter because neither field may be updated at all.
func wantedStatus(attrs map[string]any) (model.Status, bool) {
	completed, hasCompleted := attrs["completed"].(bool)
	canceled, hasCanceled := attrs["canceled"].(bool)
	switch {
	case hasCanceled && hasCompleted && !canceled && completed:
		return model.StatusOpen, false
	case hasCanceled:
		if canceled {
			return model.StatusCancelled, true
		}
		return model.StatusOpen, true
	case hasCompleted:
		if completed {
			return model.StatusCompleted, true
		}
		return model.StatusOpen, true
	}
	return model.StatusOpen, false
}

// importRefusalItem is one payload item the pre-write check refused, and one
// entry of the `items` array a --json consumer reads (issue #161).
type importRefusalItem struct {
	Path    string
	ID      string
	Title   string
	Kind    string // "to-do" or "project", as the message names it
	Blocked []string
}

// importRefusalError is the whole-payload refusal. It carries the offending
// items so --json can hand them over one by one instead of a consumer having
// to parse them back out of the message.
type importRefusalError struct {
	items []importRefusalItem
	total int // update items in the payload, offending or not
}

func (e *importRefusalError) Error() string {
	lines := make([]string, len(e.items))
	for i, it := range e.items {
		lines[i] = fmt.Sprintf("  %s (id %s): %q is a repeating %s — %s",
			it.Path, it.ID, it.Title, it.Kind, strings.Join(it.Blocked, ", "))
	}
	return fmt.Sprintf("%d of %d update items change attributes Things does not allow on repeating items, and drops the request silently (%s). Nothing was sent to Things — fix these and run the import again, or make the changes in the Things app:\n%s",
		len(e.items), e.total, repeatingDocsURL, strings.Join(lines, "\n"))
}

// importVerifyItem is one status change that never landed. wanted and got name
// the statuses either side of the failure; got is absent when there was
// nothing to observe, because the row could not be read or no longer exists.
type importVerifyItem struct {
	Path     string
	ID       string
	Title    string
	Wanted   model.Status
	Got      model.Status
	Observed bool

	err error // the read-back error, verbatim, for the plain-text message
}

// importVerifyError is a partially applied import: the payload reached Things,
// and some of the status changes it asked for did not take.
type importVerifyError struct {
	items []importVerifyItem
	total int // status changes the payload requested, landed or not
}

func (e *importVerifyError) Error() string {
	lines := make([]string, len(e.items))
	for i, it := range e.items {
		lines[i] = fmt.Sprintf("  %s: %v", it.Path, it.err)
	}
	return fmt.Sprintf("%d of %d requested status changes did not apply. The rest of the import was still applied; re-run the import with only the failed items, or make the changes in the Things app:\n%s",
		len(e.items), e.total, strings.Join(lines, "\n"))
}

// prepareImport is the pre-write pass over an import payload. It refuses the
// whole import when any `operation: update` item would change an attribute
// Things drops silently on a repeating to-do or project — the same check
// `edit`, `complete` and `cancel` make, applied per item.
//
// The refusal is all-or-nothing on purpose: the URL scheme takes one payload
// and gives no per-item result, so there is no way to send the rest and report
// what was skipped. Refusing before anything is sent leaves the user with a
// payload they can fix and re-run.
func prepareImport(d *Deps, database *db.DB, data []byte) (*importPlan, error) {
	plan := &importPlan{updates: importUpdates(data), tasks: map[string]*model.Task{}}

	var refusals []importRefusalItem
	var missing []string
	for _, u := range plan.updates {
		if !u.resolvable() {
			continue
		}
		task, ok := plan.tasks[u.id]
		if !ok {
			var err error
			task, err = database.GetTaskByUUID(u.id)
			if err != nil {
				return nil, fmt.Errorf("checking %s (id %s) against the Things database: %w", u.path, u.id, err)
			}
			plan.tasks[u.id] = task
		}
		if task == nil {
			missing = append(missing, fmt.Sprintf("%s (id %s)", u.path, u.id))
			continue
		}
		blocked := restrictedImportAttrs(u.attrs)
		if len(blocked) == 0 || !task.Repeating {
			continue
		}
		kind := "to-do"
		if task.Type == model.TypeProject {
			kind = "project"
		}
		refusals = append(refusals, importRefusalItem{
			Path: u.path, ID: u.id, Title: task.Title, Kind: kind, Blocked: blocked,
		})
	}

	// Refuse first: the warnings below promise that Things will report the
	// unknown ids itself, which is only true when the payload is actually
	// sent. A refused import never reaches Things.
	if len(refusals) > 0 {
		return nil, &importRefusalError{items: refusals, total: len(plan.updates)}
	}

	for _, m := range missing {
		fmt.Fprintf(d.errOut(), "warning: %s is not in the Things database — Things will report this itself\n", m)
	}
	return plan, nil
}

// verifyImportStatuses re-reads every item the payload asked to complete or
// cancel and reports the ones whose status never changed. Things gives the
// import no per-item result, so this is the only way a silently dropped status
// change in a batch becomes visible.
//
// Every item is checked before anything is reported: a payload's later items
// are just as interesting as its first, so the read-back does not stop at the
// first failure. The whole batch shares one timeout budget.
func verifyImportStatuses(d *Deps, database *db.DB, plan *importPlan) error {
	if d.NoVerify {
		return nil
	}

	var wants []statusWant
	var items []importVerifyItem
	for _, u := range plan.updates {
		if !u.resolvable() {
			continue
		}
		// An id the pre-write pass could not find was already warned about;
		// Things reports it too, and reading it back would only repeat that.
		task := plan.tasks[u.id]
		if task == nil {
			continue
		}
		want, ok := wantedStatus(u.attrs)
		if !ok {
			continue
		}
		wants = append(wants, statusWant{uuid: u.id, title: task.Title, want: want})
		items = append(items, importVerifyItem{Path: u.path, ID: u.id, Title: task.Title, Wanted: want})
	}
	if len(wants) == 0 {
		return nil
	}

	// The failures go in the error rather than straight to stderr: under
	// --json the error is rendered as one object on stdout (issue #152) and
	// anything written to stderr never reaches the reader, so a summary
	// pointing at a list printed elsewhere would name detail the consumer
	// cannot see.
	var failures []importVerifyItem
	for i, res := range verifyStatuses(database, wants, verifyTimeout) {
		if res.err == nil {
			continue
		}
		failed := items[i]
		failed.Got, failed.Observed, failed.err = res.got, res.observed, res.err
		failures = append(failures, failed)
	}
	if len(failures) == 0 {
		return nil
	}
	return &importVerifyError{items: failures, total: len(wants)}
}

// jsonItems renders the refused items for the --json error payload. Blocked is
// copied rather than shared: the payload outlives the error only in tests, but
// aliasing a slice into the wire format invites a caller to mutate it.
func (e *importRefusalError) jsonItems() []jsonErrorItem {
	out := make([]jsonErrorItem, len(e.items))
	for i, it := range e.items {
		out[i] = jsonErrorItem{
			Path:    it.Path,
			ID:      it.ID,
			Title:   it.Title,
			Blocked: append([]string(nil), it.Blocked...),
		}
	}
	return out
}

// jsonItems renders the unapplied status changes for the --json error payload.
func (e *importVerifyError) jsonItems() []jsonErrorItem {
	out := make([]jsonErrorItem, len(e.items))
	for i, it := range e.items {
		out[i] = jsonErrorItem{
			Path:   it.Path,
			ID:     it.ID,
			Title:  it.Title,
			Wanted: it.Wanted.String(),
		}
		if it.Observed {
			out[i].Got = it.Got.String()
		}
	}
	return out
}
