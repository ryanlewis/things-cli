package main

import (
	"fmt"
	"maps"
	"strings"
	"time"

	"github.com/ryanlewis/things-cli/internal/clock"
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

	When     *string `help:"Schedule: today|tomorrow|evening|anytime|someday, YYYY-MM-DD, HH:MM or H:MMam|pm, YYYY-MM-DD@HH:MM, RFC3339, or empty to clear."`
	Deadline *string `help:"Deadline: a YYYY-MM-DD date, an English phrase such as \"next friday\", or empty to clear. Keywords such as today and tomorrow are rejected."`

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

// target is the status the flags ask for, or current when they ask for none.
func (s *editStatusFlags) target(current model.Status) model.Status {
	switch {
	case s.Complete:
		return model.StatusCompleted
	case s.Cancel:
		return model.StatusCancelled
	}
	return current
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
// find is ignored. An empty list unfiles the to-do into Anytime, and an
// empty heading alone takes it out of its heading. A move to where the to-do
// already is changes nothing, and Things does not record it. Each of those
// is reported as no change. Of
// headings whose titles differ only in case, Things picks one, and so does
// the check (db.HeadingTarget). A database that cannot be read gives no
// warning here, and the move counts as a change.
func (c *EditCmd) checkMove(d *Deps, database *db.DB, task *model.Task) bool {
	switch {
	case !anySet(c.List, c.ListID, c.Heading, c.HeadingID):
		return false
	case c.List != nil && c.ListID != nil:
		// Seen in Things 3: given both, Things files into the list-id, and
		// reopens a closed project there, so that list's notes are given.
		// The rest of what it does with the pair was not checked.
		if !emptyID(c.ListID) && !emptyID(c.HeadingID) {
			c.noteListID(d, database, task)
		}
		return true
	case emptyID(c.ListID), emptyID(c.HeadingID):
		// What Things does with an empty id was not checked.
		return true
	case c.List != nil && *c.List == "":
		// Checked in Things 3: an empty list takes the to-do out of its
		// project, heading and area, and files it in Anytime. Only a to-do
		// that is there already, unfiled in Anytime with no start date, is
		// left as it is. A heading-id sent with it may still move the
		// to-do, which was not checked, so that counts as a change.
		if c.HeadingID != nil {
			return true
		}
		return task.ProjectUUID != "" || task.AreaUUID != "" || task.HeadingUUID != "" ||
			task.Start != model.StartAnytime || task.StartDate != nil
	}
	heading := ""
	if c.Heading != nil {
		heading = *c.Heading
	}
	if c.HeadingID != nil {
		id := strings.TrimSpace(*c.HeadingID)
		_, _, found, trashed, err := database.HeadingProject(id)
		switch {
		case err != nil:
			return true
		case found && !trashed:
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
	switch {
	case c.Heading == nil || task.ProjectUUID == "":
	case heading == "":
		next = "it will take the to-do out of its heading"
	default:
		next = fmt.Sprintf("it will look for --heading %q in the to-do's own project", heading)
	}
	list, found := "", false
	var target db.Target
	switch {
	case c.List != nil:
		list = *c.List
		t, f, err := database.AddTarget(list, heading)
		switch {
		case err != nil:
			return true
		case t.UUID == "":
			fmt.Fprintf(d.errOut(), "warning: Things finds no project or area called %q; %s\n", list, next)
		case t.ByUUID:
			id := t.UUID
			c.List, c.ListID = nil, &id
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
		case !t.ByUUID:
			// AddTarget also matches a title, which list-id does not.
			fmt.Fprintf(d.errOut(), "warning: Things finds no project or area with id %q; %s\n", list, next)
		default:
			target, found = t, f
		}
	}
	// inList is whether the to-do is in the target list already, so a move
	// changes at most its heading, and Things files it nowhere new.
	inList := filedIn(task, target.UUID)
	if target.UUID != "" && !inList {
		noteTarget(d, list, "lists", target)
	}
	switch {
	case target.UUID != "" && found:
		return !inList || task.HeadingUUID != target.Heading
	case target.UUID != "":
		if heading != "" {
			fmt.Fprintf(d.errOut(), "warning: %q has no heading %q; Things will move the to-do there without a heading\n", list, heading)
		}
		return task.HeadingUUID != "" || !inList
	case c.Heading == nil:
		return false
	case heading == "":
		// Checked in Things 3: an empty heading, alone or after a list
		// Things cannot find, takes the to-do out of its heading and leaves
		// it in its project. A to-do under no heading stays where it is.
		return task.HeadingUUID != ""
	case task.ProjectUUID == "" && list != "":
		// The unknown list's warning has said the to-do stays put.
		return false
	case task.ProjectUUID == "":
		fmt.Fprintf(d.errOut(), "warning: --heading %q needs --list: the to-do is not in a project, so Things will leave it where it is\n", heading)
		return false
	}
	t, f, err := database.AddTarget(task.ProjectUUID, heading)
	switch {
	case err != nil || t.UUID == "":
		return true
	case !f:
		fmt.Fprintf(d.errOut(), "warning: %q has no heading %q; Things will leave the to-do where it is\n", task.ProjectTitle, heading)
		return false
	}
	return task.HeadingUUID != t.Heading
}

// noteListID gives the notes for a move into the list --list-id names
// (targetNotes), as checkMove does for a list it resolves, when the id is a
// project or area the to-do is not in already. A heading-id Things can find
// wins over the list, so no note is given then.
func (c *EditCmd) noteListID(d *Deps, database *db.DB, task *model.Task) {
	if c.HeadingID != nil {
		if _, _, found, trashed, err := database.HeadingProject(strings.TrimSpace(*c.HeadingID)); err != nil || found && !trashed {
			return
		}
	}
	t, _, err := database.AddTarget(*c.ListID, "")
	if err != nil || !t.ByUUID {
		return
	}
	if filedIn(task, t.UUID) {
		return
	}
	noteTarget(d, *c.ListID, "lists", t)
}

// filedIn reports whether task is filed in the project or area list already,
// so a move there changes at most its heading.
func filedIn(task *model.Task, list string) bool {
	return task.ProjectUUID == list || task.ProjectUUID == "" && task.AreaUUID == list
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

// typ is the type of item the command edits.
func (k editKind) typ() model.TaskType {
	if k.project {
		return model.TypeProject
	}
	return model.TypeTask
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
	item := kind.typ().String()
	if f.Title != nil {
		if err := refuseBlankTitle(*f.Title, item, "edited"); err != nil {
			return err
		}
	}
	// Before --create-tags can create anything.
	if f.When != nil {
		if _, err := things.NormalizeWhen(*f.When); err != nil {
			return err
		}
	}
	database, err := d.Database()
	if err != nil {
		return err
	}
	task, err := resolveTaskForWrite(d, ref, database)
	if err != nil {
		return err
	}
	if err := refuseTrashed(ref, task, "edited"); err != nil {
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
	// refused whole. --duplicate skips it: the edit goes to a copy, which
	// Things makes with the item's status, and the item stays as it is.
	want := s.target(task.Status)
	already := false
	if (s.Complete || s.Cancel) && !s.Duplicate {
		if already, err = checkClosed(task, want); err != nil {
			return err
		}
	}
	complete, cancel := s.Complete && !already, s.Cancel && !already
	if err := checkRepeating(task, restrictedEdits(f.When, f.Deadline, complete, cancel, s.Duplicate)); err != nil {
		return err
	}
	// After checkRepeating: no point warning about tags on an edit Things
	// is going to refuse anyway.
	unknown, err := verifyEditTags(d, s.TagFlags, f.Tags, f.AddTags, item)
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
	reads := readWhen(database, task.UUID)
	// One instant for the no-op check and the read-back's row check, so they
	// judge the item on the same day.
	now := clock.Now()
	changed := f.changesFields(uncovered) && !f.certainNoOp(task, uncovered, foldTags(unknown), reads, now)
	var when *whenCheck
	if f.When != nil && changed {
		when = &whenCheck{value: *f.When, phraseOnly: !uncovered && f.onlyWhen() && whenPhrase(*f.When)}
		if !s.Duplicate && !d.NoVerify {
			if h := heldIn(task, now, reads.stored); h == heldUnmoved || h == heldCarried {
				when.before = task
				when.carried = h == heldCarried
				when.whenOnly = !uncovered && f.onlyWhen()
			}
		}
	}
	if already {
		if !changed && !s.Reveal {
			noteAlreadyClosed(d, task)
			return printItem(d, database, task)
		}
		fmt.Fprintf(d.errOut(), "note: %q is already %s; the status is left out of the edit\n", task.Title, task.Status)
	}
	return applyEdit(d, database, task, changed, checklist, want, s.Duplicate, when, update)
}

// certainNoOp reports whether every field flag set on the edit provably
// leaves the item as it is, so there is no modification to wait for.
// uncovered says whether any field flag outside coveredFields is set,
// dropped holds the folded tag names Things will drop (foldTags), and
// reads reads the item's reminder and stored start (whenUnchanged). now is
// the instant --when is judged at.
func (f *commonEditFlags) certainNoOp(task *model.Task, uncovered bool, dropped map[string]struct{}, reads whenReads, now time.Time) bool {
	return !uncovered && f.covered().unchanged(task, dropped, reads, now)
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

// onlyWhen reports whether --when is the one covered flag given.
func (f *commonEditFlags) onlyWhen() bool {
	c := f.covered()
	c.when = nil
	return f.When != nil && !c.set()
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
func (f coveredFields) unchanged(task *model.Task, dropped map[string]struct{}, reads whenReads, now time.Time) bool {
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
	if f.when != nil && !whenUnchanged(*f.when, task, now, reads) {
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
// a project, then confirms the change landed and prints the item. An item in
// the Trash is refused.
func runStatusChange(d *Deps, ref string, yes bool, want model.Status) error {
	sc := statusChanges[want]
	database, err := d.Database()
	if err != nil {
		return err
	}
	task, err := resolveTaskForWrite(d, ref, database)
	if err != nil {
		return err
	}
	if err := refuseTrashed(ref, task, want.String()); err != nil {
		return err
	}
	same, err := checkClosed(task, want)
	if err != nil {
		return err
	}
	if same {
		noteAlreadyClosed(d, task)
		// --json prints the item, as `edit --complete` does on the same
		// item, so a caller always gets an object back. Plain output stays
		// empty: the note has said it all.
		if d.JSON {
			return printItem(d, database, task)
		}
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

// refuseTrashed refuses a write to an item in the Trash (trashedError). It
// runs before checkClosed, so a trashed item that is already closed is
// refused too rather than noted: the reference most likely meant some other
// item. done is what the write would have made of the item.
//
// A to-do whose project, directly or through its heading, is in the Trash is
// refused the same way. Things shows such a to-do only in the Trash, with its
// project, though its own trashed column stays 0. Things itself would accept
// the write; the CLI refuses it because the user cannot see the item.
func refuseTrashed(ref string, task *model.Task, done string) error {
	if !task.Trashed && !task.ProjectTrashed {
		return nil
	}
	e := &trashedError{Kind: task.Type.String(), Query: ref, UUID: task.UUID, Title: task.Title, Done: done}
	if !task.Trashed {
		e.Project = task.ProjectTitle
	}
	return e
}

// checkClosed reports whether task already has the closed status want, and
// refuses to switch a completed item to cancelled, or back. Listings show the
// items closed today by default, numbered like the rest, so a ref can land on
// one: closing it the same way again has nothing to do, and the switch is not
// what `complete`, `cancel` or their `edit` flags are for. Things itself
// ignores the first and applies the second. Any other status, open or one
// the CLI does not know, passes.
func checkClosed(task *model.Task, want model.Status) (bool, error) {
	switch task.Status {
	case want:
		return true, nil
	case model.StatusCompleted, model.StatusCancelled:
		return false, &closedSwitchError{task: task, want: want}
	}
	return false, nil
}

// noteAlreadyClosed says nothing was sent for an item already in the closed
// status asked for (checkClosed).
func noteAlreadyClosed(d *Deps, task *model.Task) {
	fmt.Fprintf(d.errOut(), "note: %q is already %s; nothing sent\n", task.Title, task.Status)
}
