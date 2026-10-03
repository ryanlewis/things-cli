package main

import (
	"fmt"
	"strings"

	"github.com/ryanlewis/things-cli/internal/model"
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
	if _, err := verifyTagStrings(d, c.TagFlags, &c.Tags); err != nil {
		return err
	}
	list := c.List
	if list == "" {
		list = c.Project
	}
	// Things matches list by title only; a uuid has to go as list-id.
	var listID string
	if id := resolveAddTarget(d, list, c.Heading); id != "" && id == strings.TrimSpace(list) {
		list, listID = "", id
	}
	return applyAdd(d, model.TypeTask, c.Title, func() error {
		return things.AddTask(things.AddParams{
			Title:     c.Title,
			Notes:     c.Notes,
			When:      c.When,
			Deadline:  c.Deadline,
			Tags:      c.Tags,
			Checklist: expandNewlines(c.Checklist),
			Heading:   c.Heading,
			List:      list,
			ListID:    listID,
		})
	})
}

// resolveAddTarget returns the uuid of the project or area list names, and
// warns when Things will not file the to-do where list and heading say.
// Things matches them by title and, when nothing matches, puts the to-do in
// the Inbox or leaves out the heading without reporting it. The add still
// goes ahead. A database that cannot be read gives no warning here: the tag
// check or the read-back reports that.
func resolveAddTarget(d *Deps, list, heading string) string {
	if list == "" {
		if heading != "" {
			fmt.Fprintf(d.errOut(), "warning: --heading %q needs --list or --project; Things will ignore it and put the to-do in the Inbox\n", heading)
		}
		return ""
	}
	database, err := d.Database()
	if err != nil {
		return ""
	}
	target, headingFound, err := database.AddTarget(list, heading)
	switch {
	case err != nil:
	case target == "":
		fmt.Fprintf(d.errOut(), "warning: Things has no open project or area called %q; it will put the to-do in the Inbox\n", list)
	case heading != "" && !headingFound:
		fmt.Fprintf(d.errOut(), "warning: %q has no heading %q; Things will add the to-do there without a heading\n", list, heading)
	}
	return target
}
