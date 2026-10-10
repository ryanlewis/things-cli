package main

import (
	"cmp"
	"fmt"

	"github.com/ryanlewis/things-cli/internal/db"
	"github.com/ryanlewis/things-cli/internal/model"
	"github.com/ryanlewis/things-cli/internal/things"
)

type OpenCmd struct {
	Ref        string `arg:"" optional:"" help:"Task/project UUID, numeric list index, title, or built-in list name (${builtin_lists})."`
	Project    string `help:"Open project by title, UUID or list index; titles match projects only, and a to-do is refused." short:"p"`
	Area       string `help:"Open area by name or UUID." short:"a"`
	Tag        string `help:"Open tag by name or UUID." short:"t"`
	Query      string `help:"App-side quick find." short:"q"`
	Filter     string `help:"Tag filter on the shown list (comma-separated)."`
	Background bool   `help:"Don't bring Things to the foreground."`
}

func (c *OpenCmd) Run(d *Deps) error {
	database, err := d.Database()
	if err != nil {
		return err
	}

	flags := 0
	for _, s := range []string{c.Ref, c.Project, c.Area, c.Tag, c.Query} {
		if s != "" {
			flags++
		}
	}
	if flags == 0 {
		return fmt.Errorf("open: pass a reference, --project, --area, --tag, or --query")
	}
	if flags > 1 {
		return fmt.Errorf("open: pass only one of <ref>, --project, --area, --tag, --query")
	}

	params := things.ShowParams{Filter: c.Filter, Background: c.Background}

	resolveUUID := func(kind, name string, find func(string) (string, error)) (string, error) {
		uuid, err := find(name)
		if err != nil {
			return "", err
		}
		if uuid == "" {
			return "", &notFoundError{Kind: kind, Query: name}
		}
		return uuid, nil
	}

	switch {
	case c.Query != "":
		params.Query = c.Query
	case c.Area != "":
		uuid, err := resolveUUID("area", c.Area, database.FindAreaUUID)
		if err != nil {
			return err
		}
		params.ID = uuid
	case c.Tag != "":
		uuid, err := resolveUUID("tag", c.Tag, database.FindTagUUID)
		if err != nil {
			return err
		}
		params.ID = uuid
	case c.Project == "" && things.IsBuiltinList(c.Ref):
		params.ID = c.Ref
	default:
		// --project, else the positional ref, names an item.
		ref := cmp.Or(c.Project, c.Ref)
		task, err := c.resolve(d, ref, database)
		if err != nil {
			return err
		}
		// --project names a project; a to-do it reaches is refused the way
		// `project edit` refuses one. This runs before the Trash check: a
		// to-do is never what --project asks for, whether hidden or not.
		if c.Project != "" && task.Type != model.TypeProject {
			return &wrongKindError{
				Token: "not a project",
				Kind:  task.Type.String(),
				Query: ref,
				UUID:  task.UUID,
				Title: task.Title,
				Retry: "things open",
			}
		}
		if err := refuseHiddenInTrash(ref, task); err != nil {
			return err
		}
		params.ID = task.UUID
	}

	return things.Show(params)
}

// resolve finds the item ref names: by resolveProject for --project, else
// by resolveTask.
func (c *OpenCmd) resolve(d *Deps, ref string, database *db.DB) (*model.Task, error) {
	if c.Project == "" {
		return resolveTask(d, ref, database)
	}
	return resolveProject(d, ref, database)
}

// refuseHiddenInTrash refuses to open a to-do whose project is in the Trash.
// Things shows such a to-do nowhere but inside its trashed project, so asking
// it to reveal one has nothing to show; the refusal carries the "trashed"
// token and names the project, as the writes do. An item that is itself in
// the Trash still opens: Things shows it in the Trash.
func refuseHiddenInTrash(ref string, task *model.Task) error {
	if task.Trashed || !task.ProjectTrashed {
		return nil
	}
	return refuseTrashed(ref, task, "opened")
}
