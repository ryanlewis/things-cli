package main

import (
	"fmt"
	"maps"
	"strings"
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
	return runEdit(d, c.Task, taskEdit, &c.commonEditFlags, &c.editStatusFlags, c.checkOwn, c.checklistSet(), func(u things.UpdateCommon) error {
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

// checkOwn reports whether any of this command's own field flags may change
// the task. None of them is in coveredFields; runEdit adds the shared ones.
// Every field flag belongs either here or in commonEditFlags.covered, so a
// new one cannot be missed by changesFields and still pass certainNoOp. A
// move Things will drop (checkMove) does not count.
func (c *EditCmd) checkOwn(d *Deps, database *db.DB, task *model.Task) bool {
	return c.checkMove(d, database, task) || c.checklistSet()
}

// checkMove warns when Things will not move the task where the move flags
// say, sends a uuid given to --list as list-id, and reports whether the move
// flags may change the task. Checked in Things 3 with things:///update: a list
// or heading title is matched ignoring case but not surrounding space, and a
// uuid only as list-id. A list Things cannot find is ignored: the to-do stays
// where it is, or with a heading the heading is looked up as if it came
// alone. An unknown heading in a known list moves the to-do to the list with
// no heading. A heading alone is looked up in the to-do's own project, and
// left out when it is not there. A heading-id moves the to-do under that
// heading, whatever list or heading title comes with it, and one Things cannot
// find is ignored. A move to where the to-do already is changes nothing, and
// Things does not record it. Each of those is reported as no change. Headings
// are compared by folded title, so two in one project whose titles fold
// together are not told apart. A database that cannot be read gives no
// warning here, and the move counts as a change.
func (c *EditCmd) checkMove(d *Deps, database *db.DB, task *model.Task) bool {
	switch {
	case !anySet(c.List, c.ListID, c.Heading, c.HeadingID):
		return false
	case c.List != nil && c.ListID != nil, emptyID(c.ListID), emptyID(c.HeadingID):
		// Which list Things prefers, and what it does with an empty id,
		// were not checked.
		return true
	}
	heading := ""
	if c.Heading != nil {
		heading = *c.Heading
	}
	if c.HeadingID != nil {
		id := strings.TrimSpace(*c.HeadingID)
		ok, err := database.HeadingExists(id)
		switch {
		case err != nil:
			return true
		case ok:
			// Checked in Things 3: a known heading-id wins over a list or
			// heading title sent with it.
			return task.HeadingUUID != id
		case !anySet(c.List, c.ListID, c.Heading):
			fmt.Fprintf(d.errOut(), "warning: Things has no heading with id %q; the to-do will stay where it is\n", *c.HeadingID)
			return false
		}
		fmt.Fprintf(d.errOut(), "warning: Things has no heading with id %q; it will ignore --heading-id\n", *c.HeadingID)
	}
	// next is what Things does when it cannot find the list.
	next := "the to-do will stay where it is"
	if c.Heading != nil && task.ProjectUUID != "" {
		next = fmt.Sprintf("it will look for --heading %q in the to-do's own project", heading)
	}
	list, target, found := "", "", false
	switch {
	case c.List != nil:
		list = *c.List
		t, f, err := database.AddTarget(list, heading)
		switch {
		case err != nil:
			return true
		case t == "":
			fmt.Fprintf(d.errOut(), "warning: Things has no open project or area called %q; %s\n", list, next)
		case t == strings.TrimSpace(list):
			c.List, c.ListID = nil, &t
			fallthrough
		default:
			target, found = t, f
		}
	case c.ListID != nil:
		list = *c.ListID
		t, f, err := database.AddTarget(list, heading)
		switch {
		case err != nil:
			return true
		case t != strings.TrimSpace(list):
			// AddTarget also matches a title, which list-id does not.
			fmt.Fprintf(d.errOut(), "warning: Things has no open project or area with id %q; %s\n", list, next)
		default:
			target, found = t, f
		}
	}
	switch {
	case target != "" && found:
		return task.ProjectUUID != target || db.FoldCase(task.HeadingTitle) != db.FoldCase(heading)
	case target != "":
		if c.Heading != nil {
			fmt.Fprintf(d.errOut(), "warning: %q has no heading %q; Things will move the to-do there without a heading\n", list, heading)
		}
		inList := task.ProjectUUID == target || task.ProjectUUID == "" && task.AreaUUID == target
		return task.HeadingUUID != "" || !inList
	case c.Heading == nil:
		return false
	case task.ProjectUUID == "" && list != "":
		// The unknown list's warning has said the to-do stays put.
		return false
	case task.ProjectUUID == "":
		fmt.Fprintf(d.errOut(), "warning: --heading %q needs --list: the to-do is not in a project, so Things will leave it where it is\n", heading)
		return false
	}
	t, f, err := database.AddTarget(task.ProjectUUID, heading)
	switch {
	case err != nil || t == "":
		return true
	case !f:
		fmt.Fprintf(d.errOut(), "warning: %q has no heading %q; Things will leave the to-do where it is\n", task.ProjectTitle, heading)
		return false
	}
	return db.FoldCase(task.HeadingTitle) != db.FoldCase(heading)
}

// emptyID reports whether an id flag was given as blank.
func emptyID(id *string) bool {
	return id != nil && strings.TrimSpace(*id) == ""
}

// checklistSet reports whether any checklist flag is set.
func (c *EditCmd) checklistSet() bool {
	return anySet(c.Checklist, c.PrependChecklist, c.AppendChecklist)
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
// check the tags and the command's own flags, then send the write and confirm
// it with applyEdit. checkOwn warns about the command's own field flags, none
// of which is in coveredFields, and reports whether they may change the item.
// checklist says whether the command set a checklist flag, which only `edit`
// has. write sends the update, given the shared params with the item's id and
// the auth token filled in.
func runEdit(d *Deps, ref string, kind editKind, f *commonEditFlags, s *editStatusFlags, checkOwn func(*Deps, *db.DB, *model.Task) bool, checklist bool, write func(things.UpdateCommon) error) error {
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
	// The status flags go through the guard `complete` and `cancel` use. A
	// status the item already has is dropped from the write, and the rest of
	// the edit still goes; a switch between completed and cancelled is
	// refused whole.
	complete, cancel := s.Complete, s.Cancel
	var already model.Status
	if complete || cancel {
		want := model.StatusCompleted
		if cancel {
			want = model.StatusCancelled
		}
		same, err := checkClosed(task, want)
		if err != nil {
			return err
		}
		if same {
			already, complete, cancel = want, false, false
		}
	}
	if err := checkRepeating(task, restrictedEdits(f.When, f.Deadline, complete, cancel, s.Duplicate)); err != nil {
		return err
	}
	// After checkRepeating: no point warning about tags on an edit Things
	// is going to refuse anyway.
	unknown, err := verifyTagStrings(d, s.TagFlags, f.Tags, f.AddTags)
	if err != nil {
		return err
	}
	uncovered := checkOwn(d, database, task)

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
			Completed:    complete,
			Canceled:     cancel,
			Duplicate:    s.Duplicate,
			Reveal:       s.Reveal,
		})
	}
	// A reminder that cannot be read counts as one, so the edit waits.
	hasReminder := func() bool {
		ok, err := database.HasReminder(task.UUID)
		return ok || err != nil
	}
	changed := f.changesFields(uncovered) && !f.certainNoOp(task, uncovered, foldTags(unknown), hasReminder)
	if already != model.StatusOpen {
		if !changed && !s.Duplicate && !s.Reveal {
			fmt.Fprintf(d.errOut(), "note: %q is already %s; nothing sent\n", task.Title, already)
			return printItem(d, database, task)
		}
		fmt.Fprintf(d.errOut(), "note: %q is already %s; the other edits are sent without the status\n", task.Title, already)
	}
	return applyEdit(d, database, task, changed, checklist, complete, cancel, s.Duplicate, update)
}

// certainNoOp reports whether every field flag set on the edit provably
// leaves the item as it is, so there is no modification to wait for.
// uncovered says whether any field flag outside coveredFields is set,
// dropped holds the folded tag names Things will drop (foldTags), and
// hasReminder reads whether the item has a reminder (whenUnchanged).
func (f *commonEditFlags) certainNoOp(task *model.Task, uncovered bool, dropped map[string]struct{}, hasReminder func() bool) bool {
	return !uncovered && f.covered().unchanged(task, dropped, hasReminder)
}

// foldTags folds the names verifyTags says Things will drop, so the no-op
// check can leave them out. --create-tags has made them by then, so none is
// dropped; --strict-tags has already refused the edit. A failed lookup
// returns none, so every tag counts as known and the edit waits for its
// read-back as before.
func foldTags(unknown []string) map[string]struct{} {
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
	return coveredFields{f.Title, f.Notes, f.PrependNotes, f.AppendNotes, f.When, f.Deadline, f.Tags, f.AddTags}
}

// coveredFields are the edit flags whose effect can be predicted exactly from
// the item as read, so an edit made only of values it already has is caught
// before the read-back. Things records no change for such an edit, and
// waiting for one would end in a false "did not apply" after the full budget.
type coveredFields struct {
	title, notes, prependNotes, appendNotes, when, deadline, tags, addTags *string
}

// set reports whether any covered flag was given.
func (f coveredFields) set() bool {
	return anySet(f.title, f.notes, f.prependNotes, f.appendNotes, f.when, f.deadline, f.tags, f.addTags)
}

// unchanged reports whether each flag that is set already matches task. It is
// deliberately narrow: a value whose outcome depends on how Things reads it
// counts as a change, and the edit waits for its read-back as before. Tags
// named in dropped do not exist in Things, which ignores them, so they count
// as no change.
func (f coveredFields) unchanged(task *model.Task, dropped map[string]struct{}, hasReminder func() bool) bool {
	if f.title != nil && *f.title != task.Title {
		return false
	}
	if f.notes != nil && *f.notes != task.Notes {
		return false
	}
	// Checked in Things 3: an empty append or prepend leaves the notes as
	// they are.
	if f.prependNotes != nil && *f.prependNotes != "" || f.appendNotes != nil && *f.appendNotes != "" {
		return false
	}
	if f.when != nil && !whenUnchanged(*f.when, task, time.Now(), hasReminder) {
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

// whenUnchanged covers a --when that leaves the item where it is, as checked
// in Things 3 on to-dos and projects. anytime, and an empty value, match an
// Anytime item with no start date; someday matches a Someday item with no
// start date. today and evening match an item scheduled for today in that part
// of the day, and a date of today matches either part. Each of those clears a
// reminder, which counts as a change, so hasReminder is asked only then.
// tomorrow, or a later date, matches an item scheduled that day, reminder or
// not. A time, a phrase, or a date already past counts as a change.
func whenUnchanged(value string, task *model.Task, now time.Time, hasReminder func() bool) bool {
	v, err := things.NormalizeWhen(value)
	if err != nil {
		return false
	}
	switch v {
	case "", "anytime":
		return task.Start == model.StartAnytime && task.StartDate == nil
	case "someday":
		return task.Start == model.StartSomeday && task.StartDate == nil
	case "tomorrow":
		v = now.AddDate(0, 0, 1).Format("2006-01-02")
	}
	today := model.ThingsDateFromTime(now)
	if task.StartDate == nil {
		return false
	}
	switch v {
	case "today":
		return *task.StartDate == today && task.StartBucket == 0 && !hasReminder()
	case "evening":
		return *task.StartDate == today && task.StartBucket == 1 && !hasReminder()
	}
	date, err := time.ParseInLocation("2006-01-02", v, time.Local)
	if err != nil {
		return false
	}
	want := model.ThingsDateFromTime(date)
	switch {
	case want < today:
		return false
	case want == today:
		return *task.StartDate == today && !hasReminder()
	}
	return *task.StartDate == want
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
	same, err := checkClosed(task, want)
	if err != nil {
		return err
	}
	if same {
		fmt.Fprintf(d.errOut(), "note: %q is already %s; nothing sent\n", task.Title, want)
		return nil
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

// checkClosed reports whether task already has the closed status want, and
// refuses to switch a completed item to cancelled, or back. Listings show the
// items closed today by default, numbered like the rest, so a ref can land on
// one: closing it the same way again has nothing to do, and the switch is not
// what `complete`, `cancel` or their `edit` flags are for. Things itself
// ignores the first and applies the second.
func checkClosed(task *model.Task, want model.Status) (bool, error) {
	if task.Status == want {
		return true, nil
	}
	if task.Status != model.StatusOpen {
		return false, fmt.Errorf("%q is already %s, so it was not %s; nothing sent", task.Title, task.Status, want)
	}
	return false, nil
}
