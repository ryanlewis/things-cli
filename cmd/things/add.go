package main

import (
	"github.com/ryanlewis/things-cli/internal/things"
)

type AddCmd struct {
	Title     string `arg:"" required:"" help:"Task title."`
	Notes     string `help:"Notes for the task."`
	When      string `help:"Schedule: today|tomorrow|evening|anytime|someday, YYYY-MM-DD, HH:MM, YYYY-MM-DD@HH:MM, or RFC3339."`
	Deadline  string `help:"Deadline date (YYYY-MM-DD)."`
	Tags      string `help:"Comma-separated tags."`
	Checklist string `help:"Newline-separated checklist items."`
	Project   string `help:"Project name or UUID."`
	Heading   string `help:"Heading within project."`
	List      string `help:"List (project or area) name."`

	TagFlags
}

func (c *AddCmd) Run(d *Deps) error {
	if err := verifyTagStrings(d, c.TagFlags, &c.Tags); err != nil {
		return err
	}
	list := c.List
	if list == "" {
		list = c.Project
	}
	return things.AddTask(things.AddParams{
		Title:     c.Title,
		Notes:     c.Notes,
		When:      c.When,
		Deadline:  c.Deadline,
		Tags:      c.Tags,
		Checklist: expandNewlines(c.Checklist),
		Heading:   c.Heading,
		List:      list,
	})
}
