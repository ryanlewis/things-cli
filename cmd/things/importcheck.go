package main

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/ryanlewis/things-cli/internal/db"
	"github.com/ryanlewis/things-cli/internal/model"
	"github.com/ryanlewis/things-cli/internal/output"
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
	creates []importCreate
}

// importUpdates collects every `operation: update` item in a Things JSON
// payload, at any depth. The payload has already been validated as JSON by
// the caller; anything unparseable yields no updates and the import proceeds
// as before, leaving Things to reject it.
func importUpdates(data []byte) []importUpdate {
	var updates []importUpdate
	walkImportPayload(data, func(path string, v map[string]any) {
		if op, _ := v["operation"].(string); op != "update" {
			return
		}
		id, _ := v["id"].(string)
		itemType, _ := v["type"].(string)
		attrs, _ := v["attributes"].(map[string]any)
		updates = append(updates, importUpdate{
			path:     path,
			itemType: strings.TrimSpace(itemType),
			id:       strings.TrimSpace(id),
			attrs:    attrs,
		})
	})
	return updates
}

// importCreate is one to-do or project an import payload creates. Headings
// and checklist items are created too, but are not read back: neither is
// reachable through the task lookups (see unresolvableItemTypes), and a
// checklist item is saved with its to-do.
type importCreate struct {
	path  string
	typ   model.TaskType
	title string // trimmed
	// dated is set when the payload gives the item a `creation-date`.
	// Things saves it with that date, so it never shows up among the items
	// created since the write and is not read back.
	dated bool
}

func (c importCreate) key() createdKey { return newCreatedKey(c.typ, c.title) }

// kind is the CLI's word for the item: "task" or "project".
func (c importCreate) kind() string {
	if c.typ == model.TypeProject {
		return "project"
	}
	return "task"
}

// importCreates collects every to-do and project in a Things JSON payload
// that is not an `operation: update`, at any depth: a project's `items`, and
// the `items` of a project the payload updates, create to-dos too. An item
// with no title is left out, since there is nothing to find it by.
func importCreates(data []byte) []importCreate {
	var creates []importCreate
	walkImportPayload(data, func(path string, v map[string]any) {
		if op, _ := v["operation"].(string); op != "" && op != "create" {
			return
		}
		var typ model.TaskType
		switch itemType, _ := v["type"].(string); strings.TrimSpace(itemType) {
		case "to-do":
			typ = model.TypeTask
		case "project":
			typ = model.TypeProject
		default:
			return
		}
		attrs, _ := v["attributes"].(map[string]any)
		title, _ := attrs["title"].(string)
		if title = strings.TrimSpace(title); title == "" {
			return
		}
		_, dated := attrs["creation-date"]
		creates = append(creates, importCreate{path: path, typ: typ, title: title, dated: dated})
	})
	return creates
}

// walkImportPayload calls visit for every object in a Things JSON payload
// with the path that locates it. Items nest — a project carries `items`, a
// to-do carries `checklist-items` — so this walks the whole decoded tree the
// way walkImportTags does.
func walkImportPayload(data []byte, visit func(path string, item map[string]any)) {
	var payload any
	if err := json.Unmarshal(data, &payload); err != nil {
		return
	}
	walkImportNode(payload, "", visit)
}

func walkImportNode(node any, path string, visit func(path string, item map[string]any)) {
	switch v := node.(type) {
	case map[string]any:
		visit(path, v)
		// Walk keys in a fixed order: Go randomises map iteration, and the
		// order items are discovered in decides the order they are reported.
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			walkImportNode(v[key], path+"."+key, visit)
		}
	case []any:
		for i, child := range v {
			walkImportNode(child, fmt.Sprintf("%s[%d]", path, i), visit)
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
	Path  string
	ID    string
	Title string
	// Kind is "task" or "project", the word Error() puts in the message. It
	// matches the `kind` an error payload carries, though jsonItems does not
	// emit it — the `items` array has no kind field (issue #245). The `import`
	// payload's own spelling of a task is "to-do", the format's word, not the
	// CLI's.
	Kind    string
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
// and some of the status changes it asked for did not take, or some of the
// items it created never appeared.
type importVerifyError struct {
	items []importVerifyItem
	total int // status changes the payload requested, landed or not

	// created is the verdict on every item the payload created, so the
	// confirmed ones keep their uuids when the import fails.
	created []importCreated
	search  string // the `things search` command, with the flags this run needs
}

// missing is the created items that never appeared: the ones to search for
// and re-run.
func (e *importVerifyError) missing() []importCreated {
	var out []importCreated
	for _, c := range e.created {
		if c.Reason == "not-found" {
			out = append(out, c)
		}
	}
	return out
}

func (e *importVerifyError) Error() string {
	var parts []string
	if len(e.items) > 0 {
		lines := make([]string, len(e.items))
		for i, it := range e.items {
			lines[i] = fmt.Sprintf("  %s: %v", it.Path, it.err)
		}
		parts = append(parts, fmt.Sprintf("%d of %d requested status changes did not apply. The rest of the import was still applied; re-run the import with only the failed items, or make the changes in the Things app:\n%s",
			len(e.items), e.total, strings.Join(lines, "\n")))
	}
	missing := e.missing()
	if len(missing) > 0 {
		lines := make([]string, len(missing))
		for i, it := range missing {
			lines[i] = fmt.Sprintf("  %s: %s", it.Path, it.detail)
		}
		parts = append(parts, fmt.Sprintf("%d of %d created items did not appear. The rest of the import was still applied. Things may have dropped them (check that Things3 is running), or may be slow to save. Run `%s <title>` for each before re-running the import with only these items; do not retry blindly:\n%s",
			len(missing), len(e.created), e.search, strings.Join(lines, "\n")))
	}
	if len(e.created) > len(missing) {
		lines := []string{"The other created items:"}
		for _, it := range e.created {
			if it.Reason != "not-found" {
				lines = append(lines, "  "+it.line())
			}
		}
		parts = append(parts, strings.Join(lines, "\n"))
	}
	return strings.Join(parts, "\n")
}

// importCreated is the read-back verdict on one item an import created, and
// one entry of the array `import --json` prints. A confirmed item carries the
// uuid it was found under; an unconfirmed one carries the reason, as an
// unconfirmed add does: "no-verify", "unreadable", "ambiguous" (candidates
// lists the new items with its title), or "not-found" (candidates lists the
// ones that did appear when fewer than the payload asked for did). An item
// with a `creation-date` is not checked at all: reason "creation-date".
type importCreated struct {
	Path       string   `json:"path"`
	Kind       string   `json:"kind"`
	Title      string   `json:"title"`
	UUID       string   `json:"uuid,omitempty"`
	Confirmed  bool     `json:"confirmed"`
	Reason     string   `json:"reason,omitempty"`
	Candidates []string `json:"candidates,omitempty"`

	detail string // why a not-found item is missing, for the plain-text error
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
	plan := &importPlan{updates: importUpdates(data), tasks: map[string]*model.Task{}, creates: importCreates(data)}

	// One query for the whole payload rather than one per item (issue #167).
	var ids []string
	for _, u := range plan.updates {
		if u.resolvable() {
			ids = append(ids, u.id)
		}
	}
	found, err := database.GetTasksByUUIDs(ids)
	if err != nil {
		return nil, fmt.Errorf("checking the payload against the Things database: %w", err)
	}
	// An id with no row maps to a nil task, which is what the loop below and
	// the read-back afterwards both read as "not in the database".
	for _, id := range ids {
		plan.tasks[id] = found[id]
	}

	var refusals []importRefusalItem
	var missing []string
	for _, u := range plan.updates {
		if !u.resolvable() {
			continue
		}
		task := plan.tasks[u.id]
		if task == nil {
			missing = append(missing, fmt.Sprintf("%s (id %s)", u.path, u.id))
			continue
		}
		blocked := restrictedImportAttrs(u.attrs)
		if len(blocked) == 0 || !task.Repeating {
			continue
		}
		kind := "task"
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

// applyImport sends write, the import, then reads it back: the items it
// created (readBackCreates) and the status changes it asked for
// (verifyImportStatuses). The whole import shares one timeout budget. Every
// created item is reported on stdout, confirmed or not; a created item that
// never appeared, or a status change that did not apply, makes the import
// fail with all of them named. Under --no-verify nothing is read back and
// every created item is reported unconfirmed.
func applyImport(d *Deps, database *db.DB, plan *importPlan, write func() error) error {
	if d.NoVerify {
		if err := write(); err != nil {
			return err
		}
		created := make([]importCreated, len(plan.creates))
		for i, c := range plan.creates {
			created[i] = importCreated{Path: c.path, Kind: c.kind(), Title: c.title, Reason: "no-verify"}
		}
		return printImportCreated(d, created)
	}

	var snap createdSnapshot
	var snapErr error
	var types []model.TaskType
	for _, c := range plan.creates {
		if !c.dated && !slices.Contains(types, c.typ) {
			types = append(types, c.typ)
		}
	}
	if len(types) > 0 {
		snap, snapErr = snapshotCreated(database, types)
	}
	if err := write(); err != nil {
		return err
	}
	budget := d.readBackTimeout()
	deadline := time.Now().Add(budget)

	created := readBackCreates(d, database, plan.creates, snap, snapErr, budget)
	failures, total := verifyImportStatuses(database, plan, max(time.Until(deadline), 0), budget)

	// The failures go in the error rather than straight to stderr: under
	// --json the error is rendered as one object on stdout (issue #152) and
	// anything written to stderr never reaches the reader, so a summary
	// pointing at a list printed elsewhere would name detail the consumer
	// cannot see. Every created item's verdict goes in with them, since the
	// success list is not printed beside an error.
	if len(failures) > 0 || slices.ContainsFunc(created, func(c importCreated) bool { return c.Reason == "not-found" }) {
		return &importVerifyError{
			items: failures, total: total, created: created,
			search: strings.Join(append(append([]string{"things"}, globalFlags(d)...), "search"), " "),
		}
	}
	return printImportCreated(d, created)
}

// readBackCreates finds each item the import created, the way applyAdd finds
// the one an add created (see findCreated), and returns a verdict per item in
// payload order. Items with the same type and title are counted together:
// when as many new items appear as the payload asked for, they are confirmed
// and paired with the payload's items in the order Things saved them. More is
// ambiguous and fewer is not found, both naming the new items that did
// appear. A snapshot or read-back that could not read the database at all
// leaves every item unconfirmed as unreadable, with a warning. An item the
// payload gives a creation-date is not looked for, and is reported as not
// checked.
func readBackCreates(d *Deps, database *db.DB, creates []importCreate, snap createdSnapshot, snapErr error, budget time.Duration) []importCreated {
	if len(creates) == 0 {
		return nil
	}
	want := map[createdKey]int{}
	for _, c := range creates {
		if !c.dated {
			want[c.key()]++
		}
	}
	var found map[createdKey][]model.Task
	err := snapErr
	if err == nil && len(want) > 0 {
		var readOK bool
		found, readOK, err = findCreated(database, snap, want, budget)
		if readOK {
			err = nil
		}
	}

	out := make([]importCreated, len(creates))
	paired := map[createdKey]int{}
	for i, c := range creates {
		out[i] = importCreated{Path: c.path, Kind: c.kind(), Title: c.title}
		key := c.key()
		matches := found[key]
		uuids := make([]string, len(matches))
		for n, t := range matches {
			uuids[n] = t.UUID
		}
		switch n := want[key]; {
		case c.dated:
			out[i].Reason = "creation-date"
		case err != nil:
			out[i].Reason = "unreadable"
		case len(matches) == n && n > 1 && !distinctCreationDates(matches):
			// Two saved at the same instant cannot be paired with the
			// payload's items by order, so neither is confirmed.
			out[i].Reason, out[i].Candidates = "ambiguous", uuids
		case len(matches) == n:
			out[i].Confirmed = true
			out[i].UUID = uuids[paired[key]]
			paired[key]++
		case len(matches) > n:
			out[i].Reason, out[i].Candidates = "ambiguous", uuids
		case len(matches) == 0:
			out[i].Reason = "not-found"
			out[i].detail = fmt.Sprintf("no new %s titled %q appeared within %s", c.kind(), c.title, budget)
		default:
			out[i].Reason, out[i].Candidates = "not-found", uuids
			out[i].detail = fmt.Sprintf("only %d of %d new %ss titled %q appeared within %s (%s)", len(matches), n, c.kind(), c.title, budget, strings.Join(uuids, ", "))
		}
	}
	if err != nil && len(want) > 0 && !d.dbWarned {
		fmt.Fprintf(d.errOut(), "warning: cannot read the Things database to confirm the items the import created: %v\n", err)
	}
	return out
}

// distinctCreationDates reports whether no two of tasks, oldest first, share a
// creation date, so their order is the order Things saved them in.
func distinctCreationDates(tasks []model.Task) bool {
	for i := 1; i < len(tasks); i++ {
		prev, cur := tasks[i-1].CreationDate, tasks[i].CreationDate
		if prev == nil || cur == nil || !cur.After(*prev) {
			return false
		}
	}
	return true
}

// line renders c the way the plain-text output reports it.
func (c importCreated) line() string {
	switch {
	case c.Confirmed:
		return fmt.Sprintf("Created and confirmed: %s %q (%s)", c.Path, c.Title, c.UUID)
	case len(c.Candidates) > 0:
		return fmt.Sprintf("%s: %s %q (%s)", unconfirmedMsg[c.Reason], c.Path, c.Title, strings.Join(c.Candidates, ", "))
	}
	return fmt.Sprintf("%s: %s %q", unconfirmedMsg[c.Reason], c.Path, c.Title)
}

// printImportCreated reports each created item, a line per item or, under
// --json, an array. An import that created nothing prints nothing.
func printImportCreated(d *Deps, created []importCreated) error {
	if len(created) == 0 {
		return nil
	}
	if d.JSON {
		return output.PrintJSON(d.Stdout, created)
	}
	for _, c := range created {
		if _, err := fmt.Fprintln(d.Stdout, c.line()); err != nil {
			return err
		}
	}
	return nil
}

// verifyImportStatuses re-reads every item the payload asked to complete or
// cancel and returns the ones whose status never changed, with the number of
// status changes requested. Things gives the import no per-item result, so
// this is the only way a silently dropped status change in a batch becomes
// visible.
//
// Every item is checked before anything is reported: a payload's later items
// are just as interesting as its first, so the read-back does not stop at the
// first failure. The batch polls for up to wait, what is left of the
// import's budget, and reports budget in its messages.
func verifyImportStatuses(database *db.DB, plan *importPlan, wait, budget time.Duration) ([]importVerifyItem, int) {
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
		return nil, 0
	}

	var failures []importVerifyItem
	for i, res := range verifyStatusesWithin(database, wants, wait, budget) {
		if res.err == nil {
			continue
		}
		failed := items[i]
		failed.Got, failed.Observed, failed.err = res.got, res.observed, res.err
		failures = append(failures, failed)
	}
	return failures, len(wants)
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

// jsonItems renders the unapplied status changes, then the created items
// that never appeared, for the --json error payload.
func (e *importVerifyError) jsonItems() []jsonErrorItem {
	missing := e.missing()
	out := make([]jsonErrorItem, len(e.items), len(e.items)+len(missing))
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
	for _, it := range missing {
		out = append(out, jsonErrorItem{
			Path:       it.Path,
			Title:      it.Title,
			Confirmed:  new(bool),
			Reason:     it.Reason,
			Candidates: append([]string(nil), it.Candidates...),
		})
	}
	return out
}
