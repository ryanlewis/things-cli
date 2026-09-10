package main

import (
	"github.com/ryanlewis/things-cli/internal/model"
	"github.com/ryanlewis/things-cli/internal/things"
)

type ProjectCmd struct {
	Add  ProjectAddCmd  `cmd:"" help:"Create a new project."`
	Edit ProjectEditCmd `cmd:"" help:"Edit a project via the Things URL scheme."`
}

type ProjectAddCmd struct {
	Title    string `arg:"" required:"" help:"Project title."`
	Notes    string `help:"Notes for the project."`
	When     string `help:"Schedule: today|tomorrow|evening|anytime|someday, YYYY-MM-DD, HH:MM, YYYY-MM-DD@HH:MM, or RFC3339."`
	Deadline string `help:"Deadline date (YYYY-MM-DD)."`
	Tags     string `help:"Comma-separated tags."`
	Area     string `help:"Area name or UUID."`
	Todos    string `help:"Newline-separated initial to-dos."`

	TagFlags
}

func (c *ProjectAddCmd) Run(d *Deps) error {
	if err := verifyTagStrings(d, c.TagFlags, &c.Tags); err != nil {
		return err
	}
	return things.AddProject(things.AddProjectParams{
		Title:    c.Title,
		Notes:    c.Notes,
		When:     c.When,
		Deadline: c.Deadline,
		Tags:     c.Tags,
		Area:     c.Area,
		Todos:    expandNewlines(c.Todos),
	})
}

type ProjectEditCmd struct {
	Project string `arg:"" required:"" help:"Project title, UUID, or numeric index from last list."`

	Title *string `help:"Replace title."`

	Notes        *string `help:"Replace notes."`
	PrependNotes *string `help:"Prepend text to notes." name:"prepend-notes"`
	AppendNotes  *string `help:"Append text to notes." name:"append-notes"`

	When     *string `help:"Schedule: today|tomorrow|evening|anytime|someday, YYYY-MM-DD, HH:MM, YYYY-MM-DD@HH:MM, RFC3339, or empty to clear."`
	Deadline *string `help:"Deadline date (YYYY-MM-DD) or empty to clear."`

	Tags    *string `help:"Replace all tags (comma-separated)."`
	AddTags *string `help:"Add tags (comma-separated)." name:"add-tags"`

	Area   *string `help:"Move to area by name."`
	AreaID *string `help:"Move to area by UUID." name:"area-id"`

	Complete  bool `help:"Mark the project as completed." xor:"status"`
	Cancel    bool `help:"Mark the project as canceled." xor:"status"`
	Duplicate bool `help:"Duplicate the project before applying edits."`
	Reveal    bool `help:"Reveal the project in Things after editing."`

	TagFlags
}

func (c *ProjectEditCmd) Run(d *Deps) error {
	database, err := d.Database()
	if err != nil {
		return err
	}
	project, err := resolveTask(d, c.Project, database)
	if err != nil {
		return err
	}
	// The mirror of the guard in EditCmd.Run: a to-do here would go to
	// things:///update-project, which cannot address one (issue #191). Same
	// structured error, so an agent can branch on the token in either
	// direction rather than string-matching the message.
	if project.Type != model.TypeProject {
		return &wrongKindError{
			Token: "not a project",
			Kind:  "task",
			Query: c.Project,
			UUID:  project.UUID,
			Title: project.Title,
			Retry: "things edit",
		}
	}
	if err := checkRepeating(project, restrictedEdits(c.When, c.Deadline, c.Complete, c.Cancel, c.Duplicate)); err != nil {
		return err
	}
	// After checkRepeating: no point warning about tags on an edit Things
	// is going to refuse anyway.
	if err := verifyTagStrings(d, c.TagFlags, c.Tags, c.AddTags); err != nil {
		return err
	}

	token, _ := database.GetAuthToken()
	update := func() error {
		return things.UpdateProject(things.UpdateProjectParams{
			ID:           project.UUID,
			AuthToken:    token,
			Title:        c.Title,
			Notes:        c.Notes,
			PrependNotes: c.PrependNotes,
			AppendNotes:  c.AppendNotes,
			When:         c.When,
			Deadline:     c.Deadline,
			Tags:         c.Tags,
			AddTags:      c.AddTags,
			Area:         c.Area,
			AreaID:       c.AreaID,
			Completed:    c.Complete,
			Canceled:     c.Cancel,
			Duplicate:    c.Duplicate,
			Reveal:       c.Reveal,
		})
	}
	return applyEditStatusWrite(d, database, project, c.Complete, c.Cancel, c.Duplicate, update)
}
