package main

import (
	"fmt"

	"github.com/ryanlewis/things-cli/internal/db"
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
	Todos    string `help:"Newline-separated initial tasks."`

	TagFlags
}

func (c *ProjectAddCmd) Run(d *Deps) error {
	if _, err := verifyTagStrings(d, c.TagFlags, &c.Tags); err != nil {
		return err
	}
	// Things matches area by title only; a uuid has to go as area-id.
	area, areaID := c.Area, ""
	if id := projectAreaID(d, area); id != "" {
		area, areaID = "", id
	}
	return applyAdd(d, model.TypeProject, c.Title, func() error {
		return things.AddProject(things.AddProjectParams{
			Title:    c.Title,
			Notes:    c.Notes,
			When:     c.When,
			Deadline: c.Deadline,
			Tags:     c.Tags,
			Area:     area,
			AreaID:   areaID,
			Todos:    expandNewlines(c.Todos),
		})
	})
}

// projectAreaID returns area when it is the uuid of an area, which has to go
// to Things as area-id, and warns when Things will not file the project under
// any area. Things matches the title ignoring case but not surrounding space,
// and when nothing matches it creates the project with no area without
// reporting it. The add still goes ahead. A database that cannot be read gives
// no warning here: the read-back reports that.
func projectAreaID(d *Deps, area string) string {
	if area == "" {
		return ""
	}
	database, err := d.Database()
	if err != nil {
		return ""
	}
	areas, err := database.ListAreas()
	if err != nil {
		return ""
	}
	// Not FindAreaUUID: it ignores surrounding space, and Things does not.
	// Checked in Things 3 with " Personal " against an area called Personal,
	// for project add's area and add's list alike: neither matched. AddTarget
	// and the FoldTag comment still assume a trim.
	found := false
	for _, a := range areas {
		if a.UUID == area {
			return a.UUID
		}
		found = found || db.FoldCase(a.Title) == db.FoldCase(area)
	}
	if !found {
		fmt.Fprintf(d.errOut(), "warning: Things has no area called %q; it will create the project with no area\n", area)
	}
	return ""
}

type ProjectEditCmd struct {
	Project string `arg:"" required:"" help:"Project title, UUID, or numeric index from last list."`

	commonEditFlags `embed:""`

	Area   *string `help:"Move to area by name."`
	AreaID *string `help:"Move to area by UUID." name:"area-id"`

	editStatusFlags `embed:"" set:"item=project"`
}

func (c *ProjectEditCmd) Run(d *Deps) error {
	return runEdit(d, c.Project, projectEdit, &c.commonEditFlags, &c.editStatusFlags, c.ownFieldsSet(), false, func(u things.UpdateCommon) error {
		return things.UpdateProject(things.UpdateProjectParams{
			UpdateCommon: u,
			Area:         c.Area,
			AreaID:       c.AreaID,
		})
	})
}

// ownFieldsSet reports whether any of this command's own field flags is set.
// None of them is in coveredFields; runEdit adds the shared ones.
func (c *ProjectEditCmd) ownFieldsSet() bool {
	return anySet(c.Area, c.AreaID)
}
