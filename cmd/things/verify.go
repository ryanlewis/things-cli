package main

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/ryanlewis/things-cli/internal/db"
	"github.com/ryanlewis/things-cli/internal/model"
	"github.com/ryanlewis/things-cli/internal/output"
)

// repeatingDocsURL documents which attributes Things refuses to update on
// repeating items.
const repeatingDocsURL = "https://culturedcode.com/things/support/articles/2803573/"

// Read-back tuning. Things gives write commands no callback (issue #19), so
// the only confirmation available is re-reading the database until the change
// lands. Vars rather than consts so tests can shrink them. verifyTimeout is
// only the fallback for a Deps built without --verify-timeout's value; see
// (*Deps).readBackTimeout.
var (
	verifyTimeout  = 5 * time.Second
	verifyInterval = 100 * time.Millisecond
	verifySleep    = time.Sleep
)

// verifyPause waits for the next read-back poll, but never past deadline, so
// a read-back gives up at the budget it was given rather than up to one
// interval later — and a budget shorter than the interval is not stretched
// to it.
func verifyPause(deadline time.Time) {
	verifySleep(min(verifyInterval, max(time.Until(deadline), 0)))
}

// pollUntil runs step in rounds until it reports done, returns an error, or
// is called with expired set. expired is read once per round, before step
// runs, so everything step judges in a round is judged against the same
// clock; a round run with expired set is the last. Between rounds it waits
// with verifyPause.
func pollUntil(budget time.Duration, step func(expired bool) (done bool, err error)) error {
	deadline := time.Now().Add(budget)
	for {
		expired := !time.Now().Before(deadline)
		done, err := step(expired)
		if err != nil || done || expired {
			return err
		}
		verifyPause(deadline)
	}
}

// checkRepeating refuses a write that Things would silently drop. Things
// rejects status, when, deadline and duplicate changes on repeating tasks and
// projects without reporting an error, so an attempt would look like success.
// blocked lists the attributes this command is about to change; it is empty
// when nothing restricted was requested.
func checkRepeating(task *model.Task, blocked []string) error {
	if !task.Repeating || len(blocked) == 0 {
		return nil
	}
	// "task", not "to-do": the word the JSON and the lookup errors use, so a
	// refusal reads the same way as the error beside it (issue #245).
	kind := "task"
	if task.Type == model.TypeProject {
		kind = "project"
	}
	return fmt.Errorf("%q is a repeating %s — Things does not allow %s to be changed on repeating %ss and drops the request silently (%s). Change it in the Things app instead",
		task.Title, kind, strings.Join(blocked, ", "), kind, repeatingDocsURL)
}

// restrictedEdits names the requested attributes Things refuses on repeating
// items, in the order they appear in the docs.
func restrictedEdits(when, deadline *string, complete, cancel, duplicate bool) []string {
	var blocked []string
	if when != nil {
		blocked = append(blocked, "when")
	}
	if deadline != nil {
		blocked = append(blocked, "deadline")
	}
	if complete {
		blocked = append(blocked, "completed")
	}
	if cancel {
		blocked = append(blocked, "canceled")
	}
	if duplicate {
		blocked = append(blocked, "duplicate")
	}
	return blocked
}

// statusWant pairs an item with the status a write asked Things to move it to.
//
// edit marks the read-back for an `edit` / `project edit` write that changes
// fields, which also waits for the item's modification date to differ from
// since. Things bumps it on every write it applies, so it covers every field
// an edit can change — comparing fields one by one could not, since `--when
// "next friday"` or `--append-notes` give nothing exact to compare against.
// Any change counts, not only a later date: an item last modified on a device
// whose clock runs ahead of this Mac's gets an earlier date from this write.
// since is nil when the item had no modification date before the write; any
// date then counts.
//
// The one exception is the checklist: Things leaves the to-do's modification
// date alone when only its checklist rows change. When watchChecklist is set,
// the edit also counts as landed once the item's checklist differs from
// checklist, as read before the write.
type statusWant struct {
	uuid  string
	title string
	want  model.Status
	edit  bool
	since *time.Time

	watchChecklist bool
	checklist      []model.ChecklistItem
}

// landed reports whether current shows the change w asked for.
func (w statusWant) landed(current *model.Task) bool {
	if current.Status != w.want {
		return false
	}
	if !w.edit {
		return true
	}
	return current.ModificationDate != nil && (w.since == nil || !current.ModificationDate.Equal(*w.since))
}

// checklistChanged reports whether items differs from the checklist w saw
// before the write. It compares each row's uuid and title in order: append
// and prepend add rows, and a replace writes new ones even for the same
// titles.
func (w statusWant) checklistChanged(items []model.ChecklistItem) bool {
	return !slices.EqualFunc(items, w.checklist, func(a, b model.ChecklistItem) bool {
		return a.UUID == b.UUID && a.Title == b.Title
	})
}

// statusResult is the outcome for one item of a batch read-back. err is nil
// when the change landed. got is the status actually seen on the final read,
// which exists only when the item was read successfully and simply never
// changed — observed says whether it does, since a read error or a deleted row
// leaves nothing to report. task is the item as last read once the change
// landed, so a caller can print it without a second query.
type statusResult struct {
	err      error
	got      model.Status
	observed bool
	task     *model.Task
}

// verifyStatuses re-reads each item until its status matches the one requested,
// and reports per item whether the change landed. The returned slice is
// parallel to wants; a nil err means that item is confirmed. Without this a
// write Things ignored is indistinguishable from one it applied.
//
// One budget covers the whole batch and items are polled in rounds, so an
// import asking for ten completions waits the same as a single one rather than
// ten times as long. Things applies a payload's items together, so a round is
// also the shape that matches how the changes actually show up.
//
// A failed read is retried rather than returned: Things is writing to the same
// database while we poll, and a transient SQLITE_BUSY there must not turn a
// write that landed into a reported failure. A read that keeps failing is
// surfaced once the deadline passes. A row that is missing is treated the same
// way, since Things may not have finished writing it.
func verifyStatuses(database *db.DB, wants []statusWant, budget time.Duration) []statusResult {
	results := make([]statusResult, len(wants))
	pending := make([]int, len(wants))
	for i := range wants {
		pending[i] = i
	}

	// pollUntil reads the clock once per round, so every item in it is
	// judged against the same deadline.
	_ = pollUntil(budget, func(expired bool) (bool, error) {
		if len(pending) == 0 {
			return true, nil
		}
		// One query per round for every item still pending, rather than one
		// per item per round (issue #167).
		uuids := make([]string, len(pending))
		for n, i := range pending {
			uuids[n] = wants[i].uuid
		}
		found, err := database.GetTasksByUUIDs(uuids)

		var next []int
		for _, i := range pending {
			w := wants[i]
			current := found[w.uuid]
			readErr := err
			landed := readErr == nil && current != nil && w.landed(current)
			if !landed && readErr == nil && current != nil && w.watchChecklist && current.Status == w.want {
				var items []model.ChecklistItem
				if items, readErr = database.GetChecklistItems(w.uuid); readErr == nil {
					landed = w.checklistChanged(items)
				}
			}
			switch {
			case readErr != nil:
				if expired {
					results[i].err = fmt.Errorf("verifying status change: %w", readErr)
					continue
				}
			case current == nil:
				// Things writes the database while we read it, so a row can
				// be missing for a moment: retry like a failed read.
				if expired {
					results[i].err = fmt.Errorf("verifying status change: %s no longer exists in the Things database", w.uuid)
					continue
				}
			case landed:
				results[i].task = current
				continue
			case expired && current.Status == w.want:
				// Only an edit can get here: the status is right but the
				// modification date never moved.
				results[i] = statusResult{
					err: fmt.Errorf("edit did not apply: %q (%s) was not modified within %s. Either Things dropped the command — check that Things3 is running — or every value in the edit was one the item already had, which Things does not record as a change. Run `things show %s` to see which before retrying; do not retry blindly",
						w.title, w.uuid, budget, w.uuid),
					got:      current.Status,
					observed: true,
				}
				continue
			case expired:
				results[i] = statusResult{
					err: fmt.Errorf("status change did not apply: %q (%s) is still %s after %s. Things accepted the command and then dropped it silently — check that Things3 is running, or make the change in the app",
						w.title, w.uuid, current.Status, budget),
					got:      current.Status,
					observed: true,
				}
				continue
			}
			next = append(next, i)
		}
		pending = next
		// An expired round judged every item, so pending is empty and
		// pollUntil stops.
		return len(pending) == 0, nil
	})
	return results
}

// verifyStatus re-reads a single item until its status matches want, and
// reports an error if it never does.
func verifyStatus(database *db.DB, task *model.Task, want model.Status, budget time.Duration) error {
	return verifyStatuses(database, []statusWant{{uuid: task.UUID, title: task.Title, want: want}}, budget)[0].err
}

// applyStatusWrite runs a status-changing write and confirms it landed, unless
// verification is switched off with --no-verify.
func applyStatusWrite(d *Deps, database *db.DB, task *model.Task, want model.Status, write func() error) error {
	if err := write(); err != nil {
		return err
	}
	if d.NoVerify {
		return nil
	}
	return verifyStatus(database, task, want, d.readBackTimeout())
}

// applyEdit runs an `edit` / `project edit` update, waits for it to land, and
// prints the item as Things now holds it — the same output `things show`
// gives, so a caller has its confirmation without a second command. changed
// says whether the edit asked for anything beyond the status; complete and
// cancel ask for a status transition.
//
// The read-back is skipped, and the output says the edit is unconfirmed, in
// two cases: --no-verify, and --duplicate, where Things applies the edit to a
// new copy whose uuid the CLI never learns while the original is expected to
// stay as it was. An edit that asks for nothing new (no field flags, or only
// values the item certainly has already — see coveredFields — and any status
// it names is already the item's) has nothing to wait for; the item is
// printed as it stands. A status-only edit waits for the status alone, the
// same check `complete` and `cancel` make.
//
// checklist says whether the edit changes the checklist. The checklist is
// read before the write so the read-back can see it change; if that read
// fails, the read-back waits for the modification date alone.
func applyEdit(d *Deps, database *db.DB, task *model.Task, changed, checklist, complete, cancel, duplicate bool, update func() error) error {
	var before []model.ChecklistItem
	watchChecklist := false
	if checklist && changed && !duplicate && !d.NoVerify {
		var err error
		before, err = database.GetChecklistItems(task.UUID)
		watchChecklist = err == nil
	}
	if err := update(); err != nil {
		return err
	}
	switch {
	case duplicate:
		return printUnconfirmedEdit(d, task, "duplicate", "Sent to Things as a duplicate; the copy is not read back")
	case d.NoVerify:
		return printUnconfirmedEdit(d, task, "no-verify", "Sent to Things, not confirmed (--no-verify)")
	}

	want := task.Status
	switch {
	case complete:
		want = model.StatusCompleted
	case cancel:
		want = model.StatusCancelled
	}
	current := task
	if changed || want != task.Status {
		res := verifyStatuses(database, []statusWant{{
			uuid: task.UUID, title: task.Title, want: want, edit: changed, since: task.ModificationDate,
			watchChecklist: watchChecklist, checklist: before,
		}}, d.readBackTimeout())[0]
		if res.err != nil {
			return res.err
		}
		current = res.task
	}
	return printItem(d, database, current)
}

// unconfirmedEdit is the --json output for an edit that was sent but not read
// back. confirmed is always false, so a caller testing it cannot mistake this
// for the item object a confirmed edit prints.
type unconfirmedEdit struct {
	UUID      string `json:"uuid"`
	Title     string `json:"title"`
	Confirmed bool   `json:"confirmed"`
	Reason    string `json:"reason"`
}

// printUnconfirmedEdit says an edit went out without being read back, so exit
// 0 with nothing printed is never the answer for a write nobody checked.
func printUnconfirmedEdit(d *Deps, task *model.Task, reason, msg string) error {
	if d.JSON {
		return output.PrintJSON(d.Stdout, unconfirmedEdit{UUID: task.UUID, Title: task.Title, Reason: reason})
	}
	_, err := fmt.Fprintf(d.Stdout, "%s: %q (%s)\n", msg, task.Title, task.UUID)
	return err
}

// createdSlack widens the window applyAdd looks for the new item in, so a
// creationDate stored a fraction before the moment the write started is
// still inside it.
const createdSlack = time.Second

// addSettleRounds is how many more rounds applyAdd polls after the first one
// that finds the new item, so a second item with the same title saved a
// moment later is seen and reported as ambiguous rather than missed.
const addSettleRounds = 2

// applyAdd sends write, which creates an item of typ titled title, then finds
// that item in the database and prints it as `things show` does.
// things:///add returns no uuid (issue #19), so the item is found by what it
// must be: an item of typ with that title, created after the write started,
// that was not there before it. More than one such item visible together
// when the read-back settles — the same title added elsewhere at the same
// moment — is reported unconfirmed with the candidates rather than guessed.
// Under --no-verify, or when the database cannot be read, the write still
// goes out and the output says it is unconfirmed.
func applyAdd(d *Deps, typ model.TaskType, title string, write func() error) error {
	if d.NoVerify {
		if err := write(); err != nil {
			return err
		}
		return printUnconfirmedAdd(d, title, "no-verify", "Sent to Things, not confirmed (--no-verify)", nil)
	}
	unreadable := func(err error) error {
		// The tag check may have said the database cannot be read already;
		// the unconfirmed line below is enough on top of that.
		if !d.dbWarned {
			fmt.Fprintf(d.errOut(), "warning: cannot read the Things database to confirm the add: %v\n", err)
		}
		return printUnconfirmedAdd(d, title, "unreadable", "Sent to Things, not confirmed (database unreadable)", nil)
	}

	since := time.Now().Add(-createdSlack)
	database, err := d.Database()
	var existing []model.Task
	if err == nil {
		existing, err = database.TasksCreatedSince(typ, since)
	}
	if err != nil {
		if err := write(); err != nil {
			return err
		}
		return unreadable(err)
	}
	before := make(map[string]struct{}, len(existing))
	for _, t := range existing {
		before[t.UUID] = struct{}{}
	}

	if err := write(); err != nil {
		return err
	}
	budget := d.readBackTimeout()
	var found []model.Task
	var readErr error
	readOK := false
	matched := 0
	_ = pollUntil(budget, func(bool) (bool, error) {
		created, err := database.TasksCreatedSince(typ, since)
		if err != nil {
			// Things is writing while we poll; a busy read is retried, and
			// found keeps what the last good read saw.
			readErr = err
			return false, nil
		}
		readOK = true
		found = found[:0]
		for _, t := range created {
			if _, old := before[t.UUID]; !old && strings.TrimSpace(t.Title) == strings.TrimSpace(title) {
				found = append(found, t)
			}
		}
		if len(found) > 0 {
			matched++
		}
		return matched > addSettleRounds, nil
	})
	switch {
	case len(found) == 0 && !readOK:
		// Not one read-back read worked. A read that failed after others
		// had worked is only a busy moment: those reads saw no item.
		return unreadable(readErr)
	case len(found) == 0:
		kind := "to-do"
		if typ == model.TypeProject {
			kind = "project"
		}
		search := append(append([]string{"things"}, globalFlags(d)...), "search", shellQuote(strings.TrimSpace(title)))
		return fmt.Errorf("add not confirmed: no new %s titled %q appeared within %s. Things may have dropped it (check that Things3 is running), or it may be slow to save. Run `%s` before retrying; do not retry blindly",
			kind, title, budget, strings.Join(search, " "))
	case len(found) > 1:
		uuids := make([]string, len(found))
		for i, t := range found {
			uuids[i] = t.UUID
		}
		return printUnconfirmedAdd(d, title, "ambiguous", "Sent to Things, not confirmed (more than one new item has this title)", uuids)
	}
	return printItem(d, database, &found[0])
}

// unconfirmedAdd is the --json output for an add that was sent but not read
// back. There is no uuid: the add returns none, and finding it is the
// read-back. candidates lists the new items an ambiguous read-back found.
type unconfirmedAdd struct {
	Title      string   `json:"title"`
	Confirmed  bool     `json:"confirmed"`
	Reason     string   `json:"reason"`
	Candidates []string `json:"candidates,omitempty"`
}

// printUnconfirmedAdd is printUnconfirmedEdit for an add.
func printUnconfirmedAdd(d *Deps, title, reason, msg string, candidates []string) error {
	if d.JSON {
		return output.PrintJSON(d.Stdout, unconfirmedAdd{Title: title, Reason: reason, Candidates: candidates})
	}
	if len(candidates) > 0 {
		_, err := fmt.Fprintf(d.Stdout, "%s: %q (%s)\n", msg, title, strings.Join(candidates, ", "))
		return err
	}
	_, err := fmt.Fprintf(d.Stdout, "%s: %q\n", msg, title)
	return err
}
