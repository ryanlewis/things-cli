package main

import (
	"maps"
	"time"

	"github.com/ryanlewis/things-cli/internal/db"
	"github.com/ryanlewis/things-cli/internal/model"
	"github.com/ryanlewis/things-cli/internal/things"
)

type EditCmd struct {
	Task string `arg:"" required:"" help:"Task title, UUID, or numeric index from last list."`

	Title *string `help:"Replace title."`

	Notes        *string `help:"Replace notes."`
	PrependNotes *string `help:"Prepend text to notes." name:"prepend-notes"`
	AppendNotes  *string `help:"Append text to notes." name:"append-notes"`

	When     *string `help:"Schedule: today|tomorrow|evening|anytime|someday, YYYY-MM-DD, HH:MM, YYYY-MM-DD@HH:MM, RFC3339, or empty to clear."`
	Deadline *string `help:"Deadline date (YYYY-MM-DD) or empty to clear."`

	Tags    *string `help:"Replace all tags (comma-separated)."`
	AddTags *string `help:"Add tags (comma-separated)." name:"add-tags"`

	Checklist        *string `help:"Replace checklist items (newline-separated)."`
	PrependChecklist *string `help:"Prepend checklist items (newline-separated)." name:"prepend-checklist"`
	AppendChecklist  *string `help:"Append checklist items (newline-separated)." name:"append-checklist"`

	List      *string `help:"Move to list/project by name."`
	ListID    *string `help:"Move to list/project by UUID." name:"list-id"`
	Heading   *string `help:"Set heading within project by name."`
	HeadingID *string `help:"Set heading by UUID." name:"heading-id"`

	Complete  bool `help:"Mark the task as completed." xor:"status"`
	Cancel    bool `help:"Mark the task as canceled." xor:"status"`
	Duplicate bool `help:"Duplicate the task before applying edits."`
	Reveal    bool `help:"Reveal the task in Things after editing."`

	TagFlags
}

func (c *EditCmd) Run(d *Deps) error {
	database, err := d.Database()
	if err != nil {
		return err
	}
	task, err := resolveTask(d, c.Task, database)
	if err != nil {
		return err
	}
	// resolveTask returns projects too, and things:///update cannot address
	// one: Things answers a project id with a modal "does not exist" dialog
	// and changes nothing (issue #189). Refuse before anything is opened
	// rather than routing to update-project, which would silently drop the
	// task-only flags (checklist, list, heading).
	if task.Type == model.TypeProject {
		return &wrongKindError{
			Token: "not a task",
			Kind:  "project",
			Query: c.Task,
			UUID:  task.UUID,
			Title: task.Title,
			Retry: "things project edit",
		}
	}
	if err := checkRepeating(task, restrictedEdits(c.When, c.Deadline, c.Complete, c.Cancel, c.Duplicate)); err != nil {
		return err
	}
	// After checkRepeating: no point warning about tags on an edit Things
	// is going to refuse anyway.
	if err := verifyTagStrings(d, c.TagFlags, c.Tags, c.AddTags); err != nil {
		return err
	}

	token := authToken(d, database)
	update := func() error {
		return things.UpdateTask(things.UpdateParams{
			UpdateCommon: things.UpdateCommon{
				ID:           task.UUID,
				AuthToken:    token,
				Title:        c.Title,
				Notes:        c.Notes,
				PrependNotes: c.PrependNotes,
				AppendNotes:  c.AppendNotes,
				When:         c.When,
				Deadline:     c.Deadline,
				Tags:         c.Tags,
				AddTags:      c.AddTags,
				Completed:    c.Complete,
				Canceled:     c.Cancel,
				Duplicate:    c.Duplicate,
				Reveal:       c.Reveal,
			},
			Checklist:        expandNewlinesPtr(c.Checklist),
			PrependChecklist: expandNewlinesPtr(c.PrependChecklist),
			AppendChecklist:  expandNewlinesPtr(c.AppendChecklist),
			List:             c.List,
			ListID:           c.ListID,
			Heading:          c.Heading,
			HeadingID:        c.HeadingID,
		})
	}
	changed := c.changesFields() && !c.certainNoOp(task)
	return applyEdit(d, database, task, changed, c.Complete, c.Cancel, c.Duplicate, update)
}

// certainNoOp reports whether every field flag set on the edit provably
// leaves the task as it is, so there is no modification to wait for.
func (c *EditCmd) certainNoOp(task *model.Task) bool {
	return !c.uncoveredSet() && c.covered().unchanged(task)
}

// changesFields reports whether the edit sets any attribute besides the status.
func (c *EditCmd) changesFields() bool {
	return c.covered().set() || c.uncoveredSet()
}

func (c *EditCmd) covered() coveredFields {
	return coveredFields{c.Title, c.Notes, c.Deadline, c.Tags, c.AddTags}
}

// uncoveredSet reports whether any field flag outside coveredFields is set.
// Every field flag belongs either here or in covered, so a new one cannot be
// missed by changesFields and still pass certainNoOp.
func (c *EditCmd) uncoveredSet() bool {
	return anySet(c.PrependNotes, c.AppendNotes, c.When, c.Checklist, c.PrependChecklist, c.AppendChecklist,
		c.List, c.ListID, c.Heading, c.HeadingID)
}

// coveredFields are the edit flags whose effect can be predicted exactly from
// the item as read, so an edit made only of values it already has is caught
// before the read-back. Things records no change for such an edit, and
// waiting for one would end in a false "did not apply" after the full budget.
type coveredFields struct {
	title, notes, deadline, tags, addTags *string
}

// set reports whether any covered flag was given.
func (f coveredFields) set() bool {
	return anySet(f.title, f.notes, f.deadline, f.tags, f.addTags)
}

// unchanged reports whether each flag that is set already matches task. It is
// deliberately narrow: a value whose outcome depends on how Things reads it
// counts as a change, and the edit waits for its read-back as before. --when
// is left out for that reason — even `today` on an item already in Today may
// touch its reminder, which the CLI does not read.
func (f coveredFields) unchanged(task *model.Task) bool {
	if f.title != nil && *f.title != task.Title {
		return false
	}
	if f.notes != nil && *f.notes != task.Notes {
		return false
	}
	// Tags compare the way Things matches them: case-insensitively, after
	// trimming (db.FoldTag).
	have := make(map[string]struct{}, len(task.Tags))
	for _, t := range task.Tags {
		have[db.FoldTag(t)] = struct{}{}
	}
	if f.tags != nil {
		want := make(map[string]struct{})
		for _, t := range things.SplitTags(*f.tags) {
			want[db.FoldTag(t)] = struct{}{}
		}
		if !maps.Equal(want, have) {
			return false
		}
	}
	if f.addTags != nil {
		for _, t := range things.SplitTags(*f.addTags) {
			if _, ok := have[db.FoldTag(t)]; !ok {
				return false
			}
		}
	}
	if f.deadline != nil && !deadlineUnchanged(*f.deadline, task.Deadline) {
		return false
	}
	return true
}

// deadlineUnchanged covers a literal date equal to the item's deadline, and a
// clear on an item that has none.
func deadlineUnchanged(value string, current *model.ThingsDate) bool {
	v, err := things.NormalizeDeadline(value)
	if err != nil {
		return false
	}
	if v == "" {
		return current == nil
	}
	date, err := time.ParseInLocation("2006-01-02", v, time.Local)
	return err == nil && current != nil && *current == model.ThingsDateFromTime(date)
}

// anySet reports whether any of the optional flags was given.
func anySet(flags ...*string) bool {
	for _, f := range flags {
		if f != nil {
			return true
		}
	}
	return false
}

type CompleteCmd struct {
	Task string `arg:"" required:"" help:"Task title, UUID, or numeric index from last list."`
	ConfirmFlags
}

func (c *CompleteCmd) Run(d *Deps) error {
	database, err := d.Database()
	if err != nil {
		return err
	}
	task, err := resolveTask(d, c.Task, database)
	if err != nil {
		return err
	}
	if err := checkRepeating(task, []string{"completed"}); err != nil {
		return err
	}
	write := func() error { return things.CompleteTask(task.UUID) }
	if task.Type == model.TypeProject {
		if err := confirmProjectStatusChange(d, c.Yes, "Complete", task.Title); err != nil {
			return err
		}
		write = func() error { return things.CompleteProject(task.UUID) }
	}
	return applyStatusWrite(d, database, task, model.StatusCompleted, write)
}

type CancelCmd struct {
	Task string `arg:"" required:"" help:"Task title, UUID, or numeric index from last list."`
	ConfirmFlags
}

func (c *CancelCmd) Run(d *Deps) error {
	database, err := d.Database()
	if err != nil {
		return err
	}
	task, err := resolveTask(d, c.Task, database)
	if err != nil {
		return err
	}
	if err := checkRepeating(task, []string{"canceled"}); err != nil {
		return err
	}
	write := func() error { return things.CancelTask(task.UUID) }
	if task.Type == model.TypeProject {
		if err := confirmProjectStatusChange(d, c.Yes, "Cancel", task.Title); err != nil {
			return err
		}
		write = func() error { return things.CancelProject(task.UUID) }
	}
	return applyStatusWrite(d, database, task, model.StatusCancelled, write)
}
