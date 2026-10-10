package main

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/ryanlewis/things-cli/internal/clock"
	"github.com/ryanlewis/things-cli/internal/db"
	"github.com/ryanlewis/things-cli/internal/model"
	"github.com/ryanlewis/things-cli/internal/output"
)

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
	things  string // `things` with the flags this run needs, for the edit that moves a misfiled item
}

// fails reports whether c, a created item's verdict, makes the import fail:
// the item may be missing (see mayBeMissing), or it was saved without its
// completion-date.
func (c importCreated) fails() bool {
	return c.mayBeMissing() || c.Reason == dateDropped || c.Reason == whenMisfiled
}

// mayBeMissing reports whether c says a created item may not be there: it
// never appeared, or a dated item's row could pass for it and fewer new items
// appeared than the items that could claim them. These are the items to
// search for, and re-run if they are missing. A shares-dated-title item with
// enough new items for every claimant is there, though which of them it is
// cannot be told, so it does not fail the import.
func (c importCreated) mayBeMissing() bool {
	return c.Reason == reasonNotFound || (c.Reason == reasonSharesDatedTitle && (c.Present == nil || !*c.Present))
}

// The reasons a created item is not confirmed, as an unconfirmed add gives
// them; unconfirmedMsg has the line each prints.
const (
	reasonNoVerify   = "no-verify"  // the read-back was skipped
	reasonUnreadable = "unreadable" // the database could not be read
	reasonAmbiguous  = "ambiguous"  // more than one new item has its title
	reasonNotFound   = "not-found"  // no new item has its title
	// Import only: an item the payload dates back is not checked.
	reasonCreationDate = "creation-date"
	// Import only: a dated item's row could pass for the item.
	reasonSharesDatedTitle = "shares-dated-title"
)

// dateDropped is the verdict on a created item saved without the
// completion-date the payload gives it. The item is there, so the import
// fails without asking for it to be created again.
const dateDropped = "completion-date-dropped"

// whenMisfiled is the verdict on a created item Things filed somewhere other
// than its keyword or date `when` puts it, as an add's misfiled error. The
// item is there, so the import fails without asking for it to be created
// again.
const whenMisfiled = "misfiled"

// whenIgnored is the verdict on a created item whose `when` is a free phrase
// Things did not understand: the item was given no start date. As an add with
// such a --when, the item is there and the import does not fail over it.
const whenIgnored = "when"

// missing is the created items that are not known to be there: the ones to
// search for and re-run.
func (e *importVerifyError) missing() []importCreated {
	var out []importCreated
	for _, c := range e.created {
		if c.mayBeMissing() {
			out = append(out, c)
		}
	}
	return out
}

// withReason is each created item whose verdict is one of reasons, in
// payload order.
func (e *importVerifyError) withReason(reasons ...string) []importCreated {
	var out []importCreated
	for _, c := range e.created {
		if slices.Contains(reasons, c.Reason) {
			out = append(out, c)
		}
	}
	return out
}

// detailLines renders each of items as a line of the plain-text error: its
// path and why it fails.
func detailLines(items []importCreated) []string {
	var lines []string
	for _, it := range items {
		lines = append(lines, fmt.Sprintf("  %s: %s", it.Path, it.detail))
	}
	return lines
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
		lines := detailLines(missing)
		cause := "The rest of the import was still applied. Things may have dropped an item that did not appear (check that Things3 is running), or may be slow to save."
		if len(e.items) == e.total && !slices.ContainsFunc(e.created, func(c importCreated) bool {
			return c.Reason != reasonCreationDate && (c.Reason != reasonNotFound || len(c.Candidates) > 0)
		}) {
			// Nothing the payload creates appeared (an item given a
			// creation-date is not looked for, so it says nothing either
			// way) and no status change applied, which is also what a
			// payload Things rejected with an error looks like.
			cause = "None of them appeared: Things may have rejected the whole payload (look for an error in the Things window), may not be running, or may be slow to save."
		}
		parts = append(parts, fmt.Sprintf("%d of %d created items did not appear or cannot be confirmed (each line says why). %s Run `%s <title>` for each before re-running the import with only these items; do not retry blindly:\n%s",
			len(missing), len(e.created), cause, e.search, strings.Join(lines, "\n")))
	}
	dropped := detailLines(e.withReason(dateDropped))
	if len(dropped) > 0 {
		parts = append(parts, fmt.Sprintf("%d created items were saved without the completion-date the payload gives. They are there, so do not import them again; set the date in the Things app:\n%s",
			len(dropped), strings.Join(dropped, "\n")))
	}
	misfiled := detailLines(e.withReason(whenMisfiled))
	if len(misfiled) > 0 {
		parts = append(parts, fmt.Sprintf("%d created items were not filed where their when puts them. They are there, so do not import them again; move each with `%s edit <uuid> --when ...` (`%s project edit` for a project) if it matters:\n%s",
			len(misfiled), e.things, e.things, strings.Join(misfiled, "\n")))
	}
	if len(e.created) > len(missing)+len(dropped)+len(misfiled) {
		lines := []string{"The other created items:"}
		for _, it := range e.created {
			if !it.fails() {
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
// with a `creation-date` is not checked at all: reason "creation-date". Nor
// is one that shares its type and title with such an item whose date falls
// in the read-back window, when one of its new items is also filed where that
// item goes: reason "shares-dated-title", with candidates listing its new
// items. A confirmed item the payload closes with a completion-date that was
// saved with another stopDate is turned back with reason
// "completion-date-dropped", keeping its uuid. A confirmed item its `when`
// did not file is turned back with reason "misfiled", or "when" for a free
// phrase Things did not understand, keeping its uuid and saying in landed
// where it is.
type importCreated struct {
	Path       string   `json:"path"`
	Kind       string   `json:"kind"`
	Title      string   `json:"title"`
	UUID       string   `json:"uuid,omitempty"`
	Confirmed  bool     `json:"confirmed"`
	Reason     string   `json:"reason,omitempty"`
	Candidates []string `json:"candidates,omitempty"`
	// Present is given on a shares-dated-title item only: true when as many
	// new items appeared as the items that could claim them, so it is there
	// (the import does not fail over it), false when it may be missing.
	Present *bool `json:"present,omitempty"`
	// Landed is given on a misfiled or when item only: where Things filed it.
	Landed string `json:"landed,omitempty"`

	detail string // why a failing item is missing or unconfirmed, for the plain-text error
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
			created[i] = importCreated{Path: c.path, Kind: c.typ.String(), Title: c.shownTitle(), Reason: reasonNoVerify}
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
	sent := clock.Now()
	if err := write(); err != nil {
		return err
	}
	budget := d.readBackTimeout()
	deadline := time.Now().Add(budget)

	created := readBackCreates(d, database, plan.creates, snap, snapErr, sent, budget)
	failures, total := verifyImportStatuses(database, plan, max(time.Until(deadline), 0), budget)

	// The failures go in the error rather than straight to stderr: under
	// --json the error is rendered as one object on stdout (issue #152) and
	// anything written to stderr never reaches the reader, so a summary
	// pointing at a list printed elsewhere would name detail the consumer
	// cannot see. Every created item's verdict goes in with them, since the
	// success list is not printed beside an error.
	if len(failures) > 0 || slices.ContainsFunc(created, importCreated.fails) {
		things := strings.Join(thingsCmd(d), " ")
		return &importVerifyError{
			items: failures, total: total, created: created,
			search: things + " search", things: things,
		}
	}
	return printImportCreated(d, created)
}

// readBackCreates finds each item the import created, the way applyAdd finds
// the one an add created (see findCreated), and returns a verdict per item in
// payload order. Each item must also be filed where the payload puts it (see
// resolveImportDests). Items with the same type, title and destination are
// counted together:
// when as many new items appear as the payload asked for, they are confirmed
// and paired with the payload's items in the order Things saved them. More is
// ambiguous and fewer is not found, both naming the new items that did
// appear. A snapshot or read-back that could not read the database at all
// leaves every item unconfirmed as unreadable, with a warning. An item the
// payload gives a creation-date is not looked for, and is reported as not
// checked. When that date is inside the read-back window its row can land
// among the new items, so an item without one whose new items include one
// that also fits where a dated item with its type and title is filed is not
// confirmed, rather than risk confirming it with the dated item's row, and
// names the new items it could be. It fails the import unless a new item
// appeared for each item that could claim one, when all of them are there.
func readBackCreates(d *Deps, database *db.DB, creates []importCreate, snap createdSnapshot, snapErr error, sent time.Time, budget time.Duration) []importCreated {
	if len(creates) == 0 {
		return nil
	}
	start := time.Now()
	// dated is where each dated item whose row may land among the new items
	// is filed, by type and title.
	dated := map[createdKey][]createdDest{}
	for _, c := range creates {
		if c.dated && c.landsInWindow(snap.since) {
			dated[c.key()] = append(dated[c.key()], c.dest)
		}
	}
	want := map[createdWant]int{}
	for _, c := range creates {
		if !c.dated {
			want[c.want()]++
		}
	}
	var found map[createdWant][]model.Task
	err := snapErr
	if err == nil && len(want) > 0 {
		var readOK bool
		found, readOK, err = findCreated(database, snap, want, budget)
		if readOK {
			err = nil
		}
	}

	out := make([]importCreated, len(creates))
	paired := map[createdWant]int{}
	for i, c := range creates {
		out[i] = importCreated{Path: c.path, Kind: c.typ.String(), Title: c.shownTitle()}
		w := c.want()
		matches := found[w]
		uuids := taskUUIDs(matches)
		sharesDated := slices.ContainsFunc(matches, func(t model.Task) bool {
			return slices.ContainsFunc(dated[c.key()], func(dst createdDest) bool { return dst.fits(t) })
		})
		switch n := want[w]; {
		case c.dated:
			out[i].Reason = reasonCreationDate
		case err != nil && len(dated[c.key()]) > 0:
			// With no rows to see where the dated item went, it may be
			// any new item with the title.
			out[i].Reason, out[i].Present = reasonSharesDatedTitle, new(bool)
			out[i].detail = fmt.Sprintf("an item the payload gives a creation-date is also a %s titled %q, and the database could not be read to count the new items with that title, so the item may exist but cannot be confirmed", c.typ, c.title)
		case err != nil:
			out[i].Reason = reasonUnreadable
		case sharesDated:
			out[i].Reason, out[i].Candidates = reasonSharesDatedTitle, uuids
			// Each item without a creation-date whose new items overlap
			// these, and each dated item that fits one of them, may hold
			// one of the new items they share. When there are enough for
			// every item that could claim them, all are there, and only
			// which is which is unknown.
			rows, claimants := sharedRows(c, creates, found)
			// A dated item can hold one of the rows only when its
			// creation-date is inside the window the rows were read from:
			// datedSlack lets an earlier one mark this item, but its row
			// is never among them.
			for _, other := range creates {
				if other.dated && other.key() == c.key() && !other.datedAt.Before(snap.since) && slices.ContainsFunc(rows, other.dest.fits) {
					claimants++
				}
			}
			present := len(rows) >= claimants
			out[i].Present = &present
			out[i].detail = fmt.Sprintf("an item the payload gives a creation-date is also a %s titled %q, and only %s with that title appeared for the %d items that could be filed there, so this item may exist as one of them but cannot be confirmed (%s)", c.typ, c.title, plural(len(rows), "new item"), claimants, strings.Join(uuids, ", "))
		case len(matches) == n && n > 1 && !distinctCreationDates(matches):
			// Two saved at the same instant cannot be paired with the
			// payload's items by order, so neither is confirmed.
			out[i].Reason, out[i].Candidates = reasonAmbiguous, uuids
		case len(matches) == n:
			out[i].Confirmed = true
			out[i].UUID = uuids[paired[w]]
			paired[w]++
		case len(matches) > n:
			out[i].Reason, out[i].Candidates = reasonAmbiguous, uuids
		case len(matches) == 0:
			out[i].Reason = reasonNotFound
			out[i].detail = fmt.Sprintf("no new %s titled %q appeared within %s", c.typ, c.title, budget)
		default:
			out[i].Reason, out[i].Candidates = reasonNotFound, uuids
			out[i].detail = fmt.Sprintf("only %d of %d new %ss titled %q appeared within %s (%s)", len(matches), n, c.typ, c.title, budget, strings.Join(uuids, ", "))
		}
	}
	unconfirmShared(out, creates, found)
	checkClosedAt(database, out, creates, found, max(budget-time.Since(start), 0))
	checkWhens(out, creates, found, sent)
	if err != nil && len(want) > 0 && !d.dbWarned {
		fmt.Fprintf(d.errOut(), "warning: cannot read the Things database to confirm the items the import created: %v\n", err)
	}
	return out
}

// sharedRows returns the new items that c, an item without a creation-date,
// competes for with the other items without one of its type and title, and
// how many such items compete for them, c included. An item competes when
// its new items overlap the ones gathered so far, which brings its own new
// items in, so the sets grow until nothing more overlaps: when A shares a
// row with B, and B with C, all three compete for the rows of all three.
func sharedRows(c importCreate, creates []importCreate, found map[createdWant][]model.Task) ([]model.Task, int) {
	var rows []model.Task
	inRows := map[string]bool{}
	add := func(tasks []model.Task) {
		for _, t := range tasks {
			if !inRows[t.UUID] {
				inRows[t.UUID] = true
				rows = append(rows, t)
			}
		}
	}
	add(found[c.want()])
	competes := make([]bool, len(creates))
	for grew := true; grew; {
		grew = false
		for j, other := range creates {
			if competes[j] || other.dated || other.key() != c.key() {
				continue
			}
			theirs := found[other.want()]
			if other.want() != c.want() && !slices.ContainsFunc(theirs, func(t model.Task) bool { return inRows[t.UUID] }) {
				continue
			}
			competes[j], grew = true, true
			add(theirs)
		}
	}
	claimants := 0
	for _, ok := range competes {
		if ok {
			claimants++
		}
	}
	return rows, claimants
}

// unconfirmShared turns back every confirmation whose new item another
// created item can also claim. An item whose destination is loose, unchecked
// or matched by a title several lists may carry, fits a row filed for another
// item with the same kind and title, so the two can claim the same row: a row
// two items were both confirmed with, or a row a loose item was confirmed
// with that fits another item's destination too, confirms neither. They are reported ambiguous with every new item either
// could be, or not-found when fewer new items appeared than the created items
// that could claim them, since then at least one of those never appeared.
func unconfirmShared(out []importCreated, creates []importCreate, found map[createdWant][]model.Task) {
	holds := func(j int, uuid string) bool {
		return !creates[j].dated && slices.ContainsFunc(found[creates[j].want()], func(t model.Task) bool { return t.UUID == uuid })
	}
	shared := make([]bool, len(out))
	for i, c := range out {
		if !c.Confirmed {
			continue
		}
		for j := range out {
			if j == i || creates[j].want() == creates[i].want() || !holds(j, c.UUID) {
				continue
			}
			if creates[i].dest.loose() || (out[j].Confirmed && out[j].UUID == c.UUID) {
				shared[i] = true
				break
			}
		}
	}
	verdicts := map[int]importCreated{}
	for i, c := range out {
		if !shared[i] {
			continue
		}
		var rows []model.Task
		for _, matches := range found {
			if !slices.ContainsFunc(matches, func(t model.Task) bool { return t.UUID == c.UUID }) {
				continue
			}
			for _, t := range matches {
				if !slices.ContainsFunc(rows, func(r model.Task) bool { return r.UUID == t.UUID }) {
					rows = append(rows, t)
				}
			}
		}
		// Oldest first, as the other candidate lists are.
		slices.SortFunc(rows, func(a, b model.Task) int {
			if a.CreationDate != nil && b.CreationDate != nil {
				if n := a.CreationDate.Compare(*b.CreationDate); n != 0 {
					return n
				}
			}
			return strings.Compare(a.UUID, b.UUID)
		})
		candidates := taskUUIDs(rows)
		claimants := 0
		for j := range out {
			if slices.ContainsFunc(candidates, func(uuid string) bool { return holds(j, uuid) }) {
				claimants++
			}
		}
		v := importCreated{Path: c.Path, Kind: c.Kind, Title: c.Title, Reason: reasonAmbiguous, Candidates: candidates}
		if len(rows) < claimants {
			v.Reason = reasonNotFound
			v.detail = fmt.Sprintf("only %d new %ss titled %q appeared for the %d created items that could be filed there (%s)", len(rows), c.Kind, c.Title, claimants, strings.Join(candidates, ", "))
		}
		verdicts[i] = v
	}
	for i, v := range verdicts {
		out[i] = v
	}
}

// checkClosedAt turns back each confirmation of an item the payload closes
// with a completion-date whose row was saved with another stopDate, keeping
// its uuid: the item is there, without its date. A row with no stopDate yet
// has not settled, so it is read again within budget, and one still without
// it is left confirmed rather than judged. Things keeps the date to the
// fraction of a second; a second's slack costs nothing.
func checkClosedAt(database *db.DB, out []importCreated, creates []importCreate, found map[createdWant][]model.Task, budget time.Duration) {
	stops := map[string]*time.Time{}
	var pending []string
	for i, c := range creates {
		if !out[i].Confirmed || c.closedAt.IsZero() {
			continue
		}
		idx := slices.IndexFunc(found[c.want()], func(t model.Task) bool { return t.UUID == out[i].UUID })
		if idx < 0 {
			continue
		}
		stops[out[i].UUID] = found[c.want()][idx].StopDate
		if stops[out[i].UUID] == nil {
			pending = append(pending, out[i].UUID)
		}
	}
	if len(pending) > 0 {
		_ = pollUntil(budget, func(bool) (bool, error) {
			rows, err := database.GetTasksByUUIDs(pending)
			if err != nil {
				return false, nil
			}
			settled := true
			for _, uuid := range pending {
				if t := rows[uuid]; t != nil && t.StopDate != nil {
					stops[uuid] = t.StopDate
				} else {
					settled = false
				}
			}
			return settled, nil
		})
	}
	for i, c := range creates {
		stop, ok := stops[out[i].UUID]
		if !ok || c.closedAt.IsZero() || stop == nil || stop.Sub(c.closedAt).Abs() < time.Second {
			continue
		}
		out[i].Confirmed, out[i].Reason = false, dateDropped
		out[i].detail = fmt.Sprintf("%s %q (%s) was saved with completion date %s, not %s", c.typ, c.title, out[i].UUID, stop.UTC().Format(time.RFC3339Nano), c.closedAt.UTC().Format(time.RFC3339Nano))
	}
}

// checkWhens turns back each confirmation of an item whose `when` did not
// file it, sent at sent, keeping its uuid, as applyAdd does for --when: a
// keyword or date that did not land is "misfiled", and a free phrase that
// left the item with no start date is "when". Things files a to-do whose
// `when` it applies with a deadline already past in the Inbox, measured on
// 9 Oct 2026, and nothing in the import says so, so only the read-back can
// tell. An item the payload completes or cancels is not checked, nor one in
// the items of a project it completes or cancels: Things was not measured
// filing a closed item by its when.
func checkWhens(out []importCreated, creates []importCreate, found map[createdWant][]model.Task, sent time.Time) {
	now := clock.Now()
	for i, c := range creates {
		if !out[i].Confirmed || c.closed || c.to.parentClosed || strings.TrimSpace(c.when) == "" {
			continue
		}
		idx := slices.IndexFunc(found[c.want()], func(t model.Task) bool { return t.UUID == out[i].UUID })
		if idx < 0 {
			continue
		}
		row := &found[c.want()][idx]
		check := &whenCheck{value: c.when, sent: sent}
		switch check.verdict(row, now) {
		case whenNotFiled:
			out[i].Confirmed, out[i].Reason = false, whenMisfiled
		case whenNotUnderstood:
			out[i].Confirmed, out[i].Reason = false, whenIgnored
		default:
			continue
		}
		out[i].Landed = describeStart(row)
		out[i].detail = fmt.Sprintf("%s %q (%s) was created, but its when %q did not file it there: it is %s", c.typ, c.title, out[i].UUID, c.when, out[i].Landed)
	}
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
	case c.UUID != "" && c.Landed != "":
		return fmt.Sprintf("%s: %s %q (%s), %s", unconfirmedMsg[c.Reason], c.Path, c.Title, c.UUID, c.Landed)
	case c.UUID != "":
		return fmt.Sprintf("%s: %s %q (%s)", unconfirmedMsg[c.Reason], c.Path, c.Title, c.UUID)
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

// jsonItems renders the unapplied status changes, then the created items
// that never appeared, for the --json error payload.
func (e *importVerifyError) jsonItems() []jsonErrorItem {
	missing := append(e.missing(), e.withReason(dateDropped, whenMisfiled)...)
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
			ID:         it.UUID,
			Title:      it.Title,
			Confirmed:  new(bool),
			Reason:     it.Reason,
			Candidates: append([]string(nil), it.Candidates...),
			Present:    it.Present,
			Landed:     it.Landed,
		})
	}
	return out
}
