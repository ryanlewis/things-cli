package main

import (
	"fmt"
	"strings"

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
	target, read := projectArea(d, area, "it will create the project with no area")
	// Where Things will file the project, for the read-back: the area the
	// title or uuid leads to, or none when it leads nowhere.
	dest := createdDest{checked: true, list: target}
	switch {
	case !read:
		dest = createdDest{}
	case target != "" && target == strings.TrimSpace(area):
		area, areaID = "", target
	}
	return applyAdd(d, model.TypeProject, c.Title, dest, func() error {
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

// projectArea returns the uuid of the area Things files a project in when
// sent area, by title or uuid (see db.AreaTarget); when it is area itself,
// the uuid has to go to Things as area-id. It warns when Things will not
// file the project under any area: Things matches the title ignoring case
// but not surrounding space, and when nothing matches it goes ahead without
// the area and without reporting it: add creates the project with no area,
// and update leaves it where it is. fallback says which, for the warning. It
// notes when several areas share the title. read is false when the database
// cannot be read, which gives no warning here: the read-back reports that.
// An empty area returns "", true.
func projectArea(d *Deps, area, fallback string) (target string, read bool) {
	if area == "" {
		return "", true
	}
	database, err := d.Database()
	if err != nil {
		return "", false
	}
	// Not FindAreaUUID: it ignores surrounding space, and Things does not.
	// Checked in Things 3 with " Personal " against an area called Personal,
	// for project add's area and add's list alike: neither matched.
	t, err := database.AreaTarget(area)
	switch {
	case err != nil:
		return "", false
	case t.UUID == "":
		fmt.Fprintf(d.errOut(), "warning: Things has no area called %q; %s\n", area, fallback)
	default:
		noteTarget(d, area, "areas", t, true)
	}
	return t.UUID, true
}

type ProjectEditCmd struct {
	Project string `arg:"" required:"" help:"Project title, UUID, or numeric index from last list."`

	commonEditFlags `embed:""`

	Area   *string `help:"Move to area by name."`
	AreaID *string `help:"Move to area by UUID." name:"area-id"`

	editStatusFlags `embed:"" set:"item=project"`
}

func (c *ProjectEditCmd) Run(d *Deps) error {
	return runEdit(d, c.Project, projectEdit, &c.commonEditFlags, &c.editStatusFlags, c.checkOwn, false, func(u things.UpdateCommon) error {
		return things.UpdateProject(things.UpdateProjectParams{
			UpdateCommon: u,
			Area:         c.Area,
			AreaID:       c.AreaID,
		})
	})
}

// checkOwn reports whether any of this command's own field flags may change
// the project. None of them is in coveredFields; runEdit adds the shared ones.
// An --area or --area-id Things cannot match is warned about and counts as no
// change, since Things leaves the project where it is; so does the area the
// project is already in, since Things records no change for it. An area uuid
// goes as area-id.
func (c *ProjectEditCmd) checkOwn(d *Deps, database *db.DB, task *model.Task) bool {
	switch {
	case c.AreaID != nil && c.Area == nil:
		return c.checkAreaID(d, database, task)
	case c.Area == nil || c.AreaID != nil:
		return anySet(c.Area, c.AreaID)
	}
	target, read := projectArea(d, *c.Area, "the project will stay where it is")
	if target != "" && target == strings.TrimSpace(*c.Area) {
		c.Area, c.AreaID = nil, &target
	}
	return !read || target != "" && target != task.AreaUUID
}

// checkAreaID warns when Things has no area with the uuid --area-id gives,
// and reports whether the project may move. Checked in Things 3: an unknown
// area-id leaves the project where it is.
func (c *ProjectEditCmd) checkAreaID(d *Deps, database *db.DB, task *model.Task) bool {
	id := strings.TrimSpace(*c.AreaID)
	switch id {
	case "":
		// What Things does with an empty area-id was not checked.
		return true
	case task.AreaUUID:
		return false
	}
	areas, err := database.ListAreas()
	if err != nil {
		return true
	}
	for _, a := range areas {
		if a.UUID == id {
			return true
		}
	}
	fmt.Fprintf(d.errOut(), "warning: Things has no area with id %q; the project will stay where it is\n", *c.AreaID)
	return false
}
