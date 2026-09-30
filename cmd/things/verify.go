package main

import (
	"fmt"
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
// lands. Vars rather than consts so tests can shrink them.
var (
	verifyTimeout  = 10 * time.Second
	verifyInterval = 100 * time.Millisecond
	verifySleep    = time.Sleep
)

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
type statusWant struct {
	uuid  string
	title string
	want  model.Status
	edit  bool
	since *time.Time
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
// surfaced once the deadline passes.
func verifyStatuses(database *db.DB, wants []statusWant, budget time.Duration) []statusResult {
	results := make([]statusResult, len(wants))
	pending := make([]int, len(wants))
	for i := range wants {
		pending[i] = i
	}

	deadline := time.Now().Add(budget)
	for len(pending) > 0 {
		// Read the clock once per round so every item in it is judged against
		// the same deadline.
		expired := !time.Now().Before(deadline)

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
			switch {
			case err != nil:
				if expired {
					results[i].err = fmt.Errorf("verifying status change: %w", err)
					continue
				}
			case current == nil:
				results[i].err = fmt.Errorf("verifying status change: %s no longer exists in the Things database", w.uuid)
				continue
			case w.landed(current):
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
		if expired {
			// Every item was judged in this round; anything still listed would
			// only spin.
			break
		}
		if len(pending) > 0 {
			verifySleep(verifyInterval)
		}
	}
	return results
}

// verifyStatus re-reads a single item until its status matches want, and
// reports an error if it never does.
func verifyStatus(database *db.DB, task *model.Task, want model.Status) error {
	return verifyStatuses(database, []statusWant{{uuid: task.UUID, title: task.Title, want: want}}, verifyTimeout)[0].err
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
	return verifyStatus(database, task, want)
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
// stay as it was. An edit that asks for nothing new (no field flags, and any
// status it names is already the item's) has nothing to wait for; the item
// is printed as it stands. A status-only edit waits for the status alone, the
// same check `complete` and `cancel` make.
func applyEdit(d *Deps, database *db.DB, task *model.Task, changed, complete, cancel, duplicate bool, update func() error) error {
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
		}}, verifyTimeout)[0]
		if res.err != nil {
			return res.err
		}
		current = res.task
	}
	items, err := database.GetChecklistItems(current.UUID)
	if err != nil {
		return err
	}
	return output.PrintTaskWithChecklist(d.Stdout, current, items, d.JSON)
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
		return output.Print(d.Stdout, unconfirmedEdit{UUID: task.UUID, Title: task.Title, Reason: reason}, true)
	}
	_, err := fmt.Fprintf(d.Stdout, "%s: %q (%s)\n", msg, task.Title, task.UUID)
	return err
}
