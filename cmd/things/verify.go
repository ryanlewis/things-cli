package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/ryanlewis/things-cli/internal/db"
	"github.com/ryanlewis/things-cli/internal/model"
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
// rejects status, when, deadline and duplicate changes on repeating to-dos and
// projects without reporting an error, so an attempt would look like success.
// blocked lists the attributes this command is about to change; it is empty
// when nothing restricted was requested.
func checkRepeating(task *model.Task, blocked []string) error {
	if !task.Repeating || len(blocked) == 0 {
		return nil
	}
	kind := "to-do"
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

// verifyStatus re-reads the item until its status matches want, and reports an
// error if it never does. Without this a write Things ignored is
// indistinguishable from one it applied.
//
// A failed read is retried rather than returned: Things is writing to the same
// database while we poll, and a transient SQLITE_BUSY there must not turn a
// write that landed into a reported failure. A read that keeps failing is
// surfaced once the deadline passes.
func verifyStatus(database *db.DB, task *model.Task, want model.Status) error {
	deadline := time.Now().Add(verifyTimeout)
	for {
		current, err := database.GetTaskByUUID(task.UUID)
		switch {
		case err != nil:
			if !time.Now().Before(deadline) {
				return fmt.Errorf("verifying status change: %w", err)
			}
		case current == nil:
			return fmt.Errorf("verifying status change: %s no longer exists in the Things database", task.UUID)
		case current.Status == want:
			return nil
		case !time.Now().Before(deadline):
			return fmt.Errorf("status change did not apply: %q (%s) is still %s after %s. Things accepted the command and then dropped it silently — check that Things3 is running, or make the change in the app",
				task.Title, task.UUID, current.Status, verifyTimeout)
		}
		verifySleep(verifyInterval)
	}
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

// applyEditStatusWrite runs an `edit` / `project edit` update and, when it
// asked for a status transition, confirms the transition landed. A --duplicate
// edit is exempt: Things applies the attributes to the copy, so the original's
// status is expected to stay put.
func applyEditStatusWrite(d *Deps, database *db.DB, task *model.Task, complete, cancel, duplicate bool, update func() error) error {
	want := model.StatusOpen
	switch {
	case complete:
		want = model.StatusCompleted
	case cancel:
		want = model.StatusCancelled
	}
	if want == model.StatusOpen || duplicate {
		return update()
	}
	return applyStatusWrite(d, database, task, want, update)
}
