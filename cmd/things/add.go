package main

import (
	"fmt"

	"github.com/ryanlewis/things-cli/internal/clock"
	"github.com/ryanlewis/things-cli/internal/db"
	"github.com/ryanlewis/things-cli/internal/model"
	"github.com/ryanlewis/things-cli/internal/things"
)

type AddCmd struct {
	Title     string `arg:"" required:"" help:"Task title."`
	Notes     string `help:"Notes for the task."`
	When      string `help:"${add_when_help}"`
	Deadline  string `help:"${add_deadline_help}"`
	Tags      string `help:"Comma-separated tags."`
	Checklist string `help:"Newline-separated checklist items."`
	Project   string `help:"Project name or UUID."`
	Heading   string `help:"Heading within project."`
	List      string `help:"List (project or area) name."`

	TagFlags
}

func (c *AddCmd) Run(d *Deps) error {
	list := c.List
	if list == "" {
		list = c.Project
	}
	when := things.ResolveWhen(c.When, clock.Now())
	params := things.AddParams{
		AddCommon: things.AddCommon{
			Title:    c.Title,
			Notes:    c.Notes,
			When:     when,
			Deadline: c.Deadline,
			Tags:     c.Tags,
		},
		Checklist: expandNewlines(c.Checklist),
		Heading:   c.Heading,
		List:      list,
	}
	if err := preAdd(d, "task", params, c.TagFlags); err != nil {
		return err
	}
	// Things matches list by title only; a uuid has to go as list-id.
	target, dest := resolveAddTarget(d, &params.List, &params.Heading)
	if target.ByUUID {
		params.List, params.ListID = "", target.UUID
	}
	return applyAdd(d, model.TypeTask, c.Title, dest, c.When, when, func() error {
		return things.AddTask(params)
	})
}

// addParams is what preAdd needs of AddParams and AddProjectParams: their
// shared fields and their Validate.
type addParams interface {
	Common() things.AddCommon
	Validate() error
}

// preAdd refuses an add before anything is sent: a blank title, then
// whatever p.Validate finds, then the tags. All of it runs before
// --create-tags can create a tag, so an add that fails creates none.
func preAdd(d *Deps, kind string, p addParams, flags TagFlags) error {
	c := p.Common()
	if err := refuseBlankTitle(c.Title, kind, "added"); err != nil {
		return err
	}
	if err := p.Validate(); err != nil {
		return err
	}
	_, err := verifyTagStrings(d, flags, &c.Tags)
	return err
}

// resolveAddTarget returns the uuid of the project or area list names, and
// warns when Things will not file the to-do where list and heading say.
// Things matches them by title and, when nothing matches, puts the to-do in
// the Inbox or leaves out the heading without reporting it. The add still
// goes ahead. dest is where Things will file it, for the read-back; it checks
// nothing when the database cannot be read. A database that cannot be read
// gives no warning here: the tag check or the read-back reports that. It
// sets list and heading to the names to send, trimmed when only that form
// matches (addTargetName).
func resolveAddTarget(d *Deps, listp, headingp *string) (db.Target, createdDest) {
	list, heading := *listp, *headingp
	if list == "" {
		if heading != "" {
			fmt.Fprintf(d.errOut(), "warning: %s\n", headingNeedsList("--heading", heading, "--list or --project", "put the to-do in the Inbox"))
		}
		return db.Target{}, createdDest{checked: true}
	}
	database, err := d.Database()
	if err != nil {
		return db.Target{}, createdDest{}
	}
	target, headingFound, list, heading, err := addTargetName(database, list, heading)
	switch {
	case err != nil:
		return db.Target{}, createdDest{}
	case target.UUID == "":
		fmt.Fprintf(d.errOut(), "warning: %s\n", noTarget("project or area", list, false, "it will put the to-do in the Inbox"))
		return target, createdDest{checked: true}
	case heading != "" && !headingFound:
		fmt.Fprintf(d.errOut(), "warning: %s\n", noHeading(listName(list, target), heading, "add the to-do there without a heading"))
	}
	*listp, *headingp = list, heading
	noteTarget(d, list, "lists", target)
	return target, addDest(target)
}

// noteTarget says on stderr when Things will file into a list the user may
// not expect, though it is where ref leads (see targetNotes).
func noteTarget(d *Deps, ref, kind string, t db.Target) {
	for _, note := range targetNotes(ref, kind, t, true) {
		fmt.Fprintf(d.errOut(), "note: %s\n", note)
	}
}

// targetNotes are the notes for a write that files an item into t, where ref
// leads: one of several kind ("lists" or "areas") that share the title ref
// gives, or a closed or trashed project. Callers give it only for a write
// that files the item somewhere new. Measured in Things 3: add and update
// file into a closed project, logged or not, and reopen it, and into a
// trashed one, which stays in the Trash. reopens is false for an item the
// write closes itself, which leaves the project closed, so that note is left
// out.
func targetNotes(ref, kind string, t db.Target, reopens bool) []string {
	var notes []string
	switch {
	case t.Others == 0:
	case !t.Area && t.OtherAreas > 0:
		notes = append(notes, fmt.Sprintf("several lists are called %q (%s and %s); Things will use the project %q (%s); pass a UUID to choose",
			ref, count(t.Others-t.OtherAreas+1, "a project", "projects"), count(t.OtherAreas, "an area", "areas"), t.Title, t.UUID))
	default:
		notes = append(notes, fmt.Sprintf("several %s are called %q; Things will use %q (%s); pass a UUID to choose", kind, ref, t.Title, t.UUID))
	}
	switch {
	case t.Area:
	case t.Trashed:
		notes = append(notes, fmt.Sprintf("%q is in the Trash; Things will file into it there", t.Title))
	case reopens && t.Status.Closed():
		notes = append(notes, fmt.Sprintf("%q is %s; Things will file into it and reopen it", t.Title, t.Status))
	}
	return notes
}

// count is one when n is 1 and many otherwise.
func count(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// addDest is where Things files a to-do sent to target, as AddTarget found
// it: its list by uuid whether it was named by uuid or by title, so two
// writes that name the same list both ways look for their items in the same
// place, and the heading HeadingTarget picks, if any, by uuid, so a to-do
// filed under a heading's case-twin is not taken for one filed under it.
func addDest(target db.Target) createdDest {
	return createdDest{checked: true, list: target.UUID, heading: target.Heading}
}
