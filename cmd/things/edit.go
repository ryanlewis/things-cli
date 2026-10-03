package main

import (
	"maps"
	"time"

	"github.com/ryanlewis/things-cli/internal/db"
	"github.com/ryanlewis/things-cli/internal/model"
	"github.com/ryanlewis/things-cli/internal/things"
)

// commonEditFlags are the field flags `edit` and `project edit` share. They
// come before each command's own field flags in --help, and editStatusFlags
// after them, so the two structs keep the flags in their documented order.
type commonEditFlags struct {
	Title *string `help:"Replace title."`

	Notes        *string `help:"Replace notes."`
	PrependNotes *string `help:"Prepend text to notes." name:"prepend-notes"`
	AppendNotes  *string `help:"Append text to notes." name:"append-notes"`

	When     *string `help:"Schedule: today|tomorrow|evening|anytime|someday, YYYY-MM-DD, HH:MM, YYYY-MM-DD@HH:MM, RFC3339, or empty to clear."`
	Deadline *string `help:"Deadline date (YYYY-MM-DD) or empty to clear."`

	Tags    *string `help:"Replace all tags (comma-separated)."`
	AddTags *string `help:"Add tags (comma-separated)." name:"add-tags"`
}

// editStatusFlags are the status and action flags `edit` and `project edit`
// share, with the tag policy flags. Each command names its item with
// set:"item=..." on the embed, so the help reads "Mark the task as completed."
type editStatusFlags struct {
	Complete  bool `help:"Mark the ${item} as completed." xor:"status"`
	Cancel    bool `help:"Mark the ${item} as canceled." xor:"status"`
	Duplicate bool `help:"Duplicate the ${item} before applying edits."`
	Reveal    bool `help:"Reveal the ${item} in Things after editing."`

	TagFlags
}

type EditCmd struct {
	Task string `arg:"" required:"" help:"Task title, UUID, or numeric index from last list."`

	commonEditFlags `embed:""`

	Checklist        *string `help:"Replace checklist items (newline-separated)."`
	PrependChecklist *string `help:"Prepend checklist items (newline-separated)." name:"prepend-checklist"`
	AppendChecklist  *string `help:"Append checklist items (newline-separated)." name:"append-checklist"`

	List      *string `help:"Move to list/project by name."`
	ListID    *string `help:"Move to list/project by UUID." name:"list-id"`
	Heading   *string `help:"Set heading within project by name."`
	HeadingID *string `help:"Set heading by UUID." name:"heading-id"`

	editStatusFlags `embed:"" set:"item=task"`
}

func (c *EditCmd) Run(d *Deps) error {
	return runEdit(d, c.Task, taskEdit, &c.commonEditFlags, &c.editStatusFlags, c.ownFieldsSet(), func(u things.UpdateCommon) error {
		return things.UpdateTask(things.UpdateParams{
			UpdateCommon:     u,
			Checklist:        expandNewlinesPtr(c.Checklist),
			PrependChecklist: expandNewlinesPtr(c.PrependChecklist),
			AppendChecklist:  expandNewlinesPtr(c.AppendChecklist),
			List:             c.List,
			ListID:           c.ListID,
			Heading:          c.Heading,
			HeadingID:        c.HeadingID,
		})
	})
}

// ownFieldsSet reports whether any of this command's own field flags is set.
// None of them is in coveredFields; runEdit adds the shared ones. Every field
// flag belongs either here, in commonEditFlags.uncoveredSet, or in covered,
// so a new one cannot be missed by changesFields and still pass certainNoOp.
func (c *EditCmd) ownFieldsSet() bool {
	return anySet(c.Checklist, c.PrependChecklist, c.AppendChecklist, c.List, c.ListID, c.Heading, c.HeadingID)
}

// editKind is what differs between `edit` and `project edit` before the
// write: which item type the command addresses, and the wrongKindError for
// the other one.
type editKind struct {
	project bool
	token   string
	other   string
	retry   string
}

var (
	// things:///update cannot address a project: Things answers a project id
	// with a modal "does not exist" dialog and changes nothing (issue #189).
	// Refuse before anything is opened rather than routing to update-project,
	// which would silently drop the task-only flags (checklist, list,
	// heading).
	taskEdit = editKind{project: false, token: "not a task", other: "project", retry: "things project edit"}
	// The mirror: a to-do would go to things:///update-project, which cannot
	// address one (issue #191). Same structured error, so an agent can branch
	// on the token in either direction rather than string-matching the
	// message.
	projectEdit = editKind{project: true, token: "not a project", other: "task", retry: "things edit"}
)

// runEdit is the shared Run of `edit` and `project edit`: resolve ref, refuse
// the wrong kind of item and any change Things drops on repeating items,
// check the tags, then send the write and confirm it with applyEdit. ownSet
// says whether the command set any of its own field flags, none of which is
// in coveredFields. write sends the update, given the shared params with the
// item's id and the auth token filled in.
func runEdit(d *Deps, ref string, kind editKind, f *commonEditFlags, s *editStatusFlags, ownSet bool, write func(things.UpdateCommon) error) error {
	database, err := d.Database()
	if err != nil {
		return err
	}
	task, err := resolveTask(d, ref, database)
	if err != nil {
		return err
	}
	if (task.Type == model.TypeProject) != kind.project {
		return &wrongKindError{
			Token: kind.token,
			Kind:  kind.other,
			Query: ref,
			UUID:  task.UUID,
			Title: task.Title,
			Retry: kind.retry,
		}
	}
	if err := checkRepeating(task, restrictedEdits(f.When, f.Deadline, s.Complete, s.Cancel, s.Duplicate)); err != nil {
		return err
	}
	// After checkRepeating: no point warning about tags on an edit Things
	// is going to refuse anyway.
	if err := verifyTagStrings(d, s.TagFlags, f.Tags, f.AddTags); err != nil {
		return err
	}

	token := authToken(d, database)
	update := func() error {
		return write(things.UpdateCommon{
			ID:           task.UUID,
			AuthToken:    token,
			Title:        f.Title,
			Notes:        f.Notes,
			PrependNotes: f.PrependNotes,
			AppendNotes:  f.AppendNotes,
			When:         f.When,
			Deadline:     f.Deadline,
			Tags:         f.Tags,
			AddTags:      f.AddTags,
			Completed:    s.Complete,
			Canceled:     s.Cancel,
			Duplicate:    s.Duplicate,
			Reveal:       s.Reveal,
		})
	}
	uncovered := ownSet || f.uncoveredSet()
	changed := f.changesFields(uncovered) && !f.certainNoOp(task, uncovered, droppedTags(database, s.TagFlags, f.Tags, f.AddTags))
	return applyEdit(d, database, task, changed, s.Complete, s.Cancel, s.Duplicate, update)
}

// certainNoOp reports whether every field flag set on the edit provably
// leaves the item as it is, so there is no modification to wait for.
// uncovered says whether any field flag outside coveredFields is set, and
// dropped holds the folded tag names Things will drop (droppedTags).
func (f *commonEditFlags) certainNoOp(task *model.Task, uncovered bool, dropped map[string]struct{}) bool {
	return !uncovered && f.covered().unchanged(task, dropped)
}

// droppedTags returns the folded names in the --tags and --add-tags values
// that Things will drop because no such tag exists, so the no-op check can
// leave them out. --create-tags has made them by now, so none is dropped
// then; --strict-tags has already refused the edit. A failed lookup counts
// every tag as known, and the edit waits for its read-back as before.
func droppedTags(database *db.DB, flags TagFlags, values ...*string) map[string]struct{} {
	if flags.CreateTags {
		return nil
	}
	unknown, err := database.UnknownTags(splitTagValues(values...))
	if err != nil {
		return nil
	}
	dropped := make(map[string]struct{}, len(unknown))
	for _, t := range unknown {
		dropped[db.FoldTag(t)] = struct{}{}
	}
	return dropped
}

// changesFields reports whether the edit sets any attribute besides the
// status. uncovered says whether any field flag outside coveredFields is set.
func (f *commonEditFlags) changesFields(uncovered bool) bool {
	return f.covered().set() || uncovered
}

func (f *commonEditFlags) covered() coveredFields {
	return coveredFields{f.Title, f.Notes, f.Deadline, f.Tags, f.AddTags}
}

// uncoveredSet reports whether any shared field flag outside coveredFields is
// set. runEdit adds the command's own flags (ownFieldsSet).
func (f *commonEditFlags) uncoveredSet() bool {
	return anySet(f.PrependNotes, f.AppendNotes, f.When)
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
// touch its reminder, which the CLI does not read. Tags named in dropped do
// not exist in Things, which ignores them, so they count as no change.
func (f coveredFields) unchanged(task *model.Task, dropped map[string]struct{}) bool {
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
			if key := db.FoldTag(t); !isDropped(dropped, key) {
				want[key] = struct{}{}
			}
		}
		if !maps.Equal(want, have) {
			return false
		}
	}
	if f.addTags != nil {
		for _, t := range things.SplitTags(*f.addTags) {
			key := db.FoldTag(t)
			if _, ok := have[key]; !ok && !isDropped(dropped, key) {
				return false
			}
		}
	}
	if f.deadline != nil && !deadlineUnchanged(*f.deadline, task.Deadline) {
		return false
	}
	return true
}

func isDropped(dropped map[string]struct{}, key string) bool {
	_, ok := dropped[key]
	return ok
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
	return runStatusChange(d, c.Task, c.Yes, model.StatusCompleted)
}

type CancelCmd struct {
	Task string `arg:"" required:"" help:"Task title, UUID, or numeric index from last list."`
	ConfirmFlags
}

func (c *CancelCmd) Run(d *Deps) error {
	return runStatusChange(d, c.Task, c.Yes, model.StatusCancelled)
}

// statusChange is what `complete` or `cancel` needs for its status: the
// attribute name checkRepeating reports, the verb the project prompt uses,
// and the writes for a task and a project.
type statusChange struct {
	blockedWord  string
	verb         string
	taskWrite    func(uuid string) error
	projectWrite func(uuid string) error
}

var statusChanges = map[model.Status]statusChange{
	model.StatusCompleted: {"completed", "Complete", things.CompleteTask, things.CompleteProject},
	model.StatusCancelled: {"canceled", "Cancel", things.CancelTask, things.CancelProject},
}

// runStatusChange resolves ref and moves it to want, asking first when it is
// a project, then confirms the change landed.
func runStatusChange(d *Deps, ref string, yes bool, want model.Status) error {
	sc := statusChanges[want]
	database, err := d.Database()
	if err != nil {
		return err
	}
	task, err := resolveTask(d, ref, database)
	if err != nil {
		return err
	}
	if err := checkRepeating(task, []string{sc.blockedWord}); err != nil {
		return err
	}
	write := func() error { return sc.taskWrite(task.UUID) }
	if task.Type == model.TypeProject {
		if err := confirmProjectStatusChange(d, yes, sc.verb, task.Title); err != nil {
			return err
		}
		write = func() error { return sc.projectWrite(task.UUID) }
	}
	return applyStatusWrite(d, database, task, want, write)
}
