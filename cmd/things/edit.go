package main

import (
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

	token, _ := database.GetAuthToken()
	update := func() error {
		return things.UpdateTask(things.UpdateParams{
			ID:               task.UUID,
			AuthToken:        token,
			Title:            c.Title,
			Notes:            c.Notes,
			PrependNotes:     c.PrependNotes,
			AppendNotes:      c.AppendNotes,
			When:             c.When,
			Deadline:         c.Deadline,
			Tags:             c.Tags,
			AddTags:          c.AddTags,
			Checklist:        expandNewlinesPtr(c.Checklist),
			PrependChecklist: expandNewlinesPtr(c.PrependChecklist),
			AppendChecklist:  expandNewlinesPtr(c.AppendChecklist),
			List:             c.List,
			ListID:           c.ListID,
			Heading:          c.Heading,
			HeadingID:        c.HeadingID,
			Completed:        c.Complete,
			Canceled:         c.Cancel,
			Duplicate:        c.Duplicate,
			Reveal:           c.Reveal,
		})
	}
	return applyEditStatusWrite(d, database, task, c.Complete, c.Cancel, c.Duplicate, update)
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
