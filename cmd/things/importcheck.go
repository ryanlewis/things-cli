package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/ryanlewis/things-cli/internal/db"
	"github.com/ryanlewis/things-cli/internal/model"
)

// checklistItemType is the one payload item type that does not live in TMTask.
// Checklist items have their own table and cannot repeat, so the repeating
// check and the read-back both skip them rather than look up an id that
// GetTaskByUUID could never resolve.
const checklistItemType = "checklist-item"

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
// without an id are Things' problem to report, and checklist items are not in
// TMTask.
func (u importUpdate) resolvable() bool {
	return u.id != "" && u.itemType != checklistItemType
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
func restrictedImportAttrs(attrs map[string]any) []string {
	var blocked []string
	for _, name := range []string{"when", "deadline"} {
		if _, ok := attrs[name]; ok {
			blocked = append(blocked, name)
		}
	}
	for _, name := range []string{"completed", "canceled"} {
		if requestsStatusChange(attrs, name) {
			blocked = append(blocked, name)
		}
	}
	return blocked
}

// requestsStatusChange reports whether a status attribute asks Things to move
// the item. An explicit `false` says "leave it as it is", which leaves Things
// nothing to drop, so it is not treated as a restricted edit. A non-boolean
// value is treated as a request: Things decides what to make of it, and
// guessing it means nothing would put the silent drop back.
func requestsStatusChange(attrs map[string]any, name string) bool {
	v, ok := attrs[name]
	if !ok || v == nil {
		return false
	}
	b, isBool := v.(bool)
	return !isBool || b
}

// wantedStatus reports the status an update item asks Things to move the item
// to. Only a literal `true` counts: anything else either leaves the status
// alone or is a payload error for Things to report. `completed` wins over
// `canceled` when a payload sets both, which is the same precedence `edit`
// applies to --complete over --cancel.
func wantedStatus(attrs map[string]any) (model.Status, bool) {
	if b, ok := attrs["completed"].(bool); ok && b {
		return model.StatusCompleted, true
	}
	if b, ok := attrs["canceled"].(bool); ok && b {
		return model.StatusCancelled, true
	}
	return model.StatusOpen, false
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

	var refusals, missing []string
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
		refusals = append(refusals, fmt.Sprintf("  %s (id %s): %q is a repeating %s — %s",
			u.path, u.id, task.Title, kind, strings.Join(blocked, ", ")))
	}

	for _, m := range missing {
		fmt.Fprintf(d.errOut(), "warning: %s is not in the Things database — Things will report this itself\n", m)
	}
	if len(refusals) == 0 {
		return plan, nil
	}
	return nil, fmt.Errorf("%d of %d update items change attributes Things does not allow on repeating items, and drops the request silently (%s). Nothing was sent to Things — fix these and run the import again, or make the changes in the Things app:\n%s",
		len(refusals), len(plan.updates), repeatingDocsURL, strings.Join(refusals, "\n"))
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
	var paths []string
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
		paths = append(paths, u.path)
	}
	if len(wants) == 0 {
		return nil
	}

	failed := 0
	for i, err := range verifyStatuses(database, wants, verifyTimeout) {
		if err == nil {
			continue
		}
		failed++
		fmt.Fprintf(d.errOut(), "import: %s: %v\n", paths[i], err)
	}
	if failed == 0 {
		return nil
	}
	return fmt.Errorf("%d of %d requested status changes did not apply (listed above). The rest of the import was still applied; re-run the import with only the failed items, or make the changes in the Things app",
		failed, len(wants))
}
