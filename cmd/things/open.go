package main

import (
	"fmt"

	"github.com/ryanlewis/things-cli/internal/things"
)

type OpenCmd struct {
	Ref        string `arg:"" optional:"" help:"Task/project UUID, numeric list index, title, or built-in list name (${builtin_lists})."`
	Project    string `help:"Open project by name or UUID." short:"p"`
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
	case c.Project != "":
		task, err := resolveTask(d, c.Project, database)
		if err != nil {
			return err
		}
		params.ID = task.UUID
	case things.IsBuiltinList(c.Ref):
		params.ID = c.Ref
	default:
		task, err := resolveTask(d, c.Ref, database)
		if err != nil {
			return err
		}
		params.ID = task.UUID
	}

	return things.Show(params)
}
