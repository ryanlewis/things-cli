package db

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/ryanlewis/things-cli/internal/model"
)

// Target is the project or area Things files an item in when sent a list
// or area by title or uuid, as AddTarget and AreaTarget find it. UUID is ""
// when nothing matched, and the other fields are then zero.
type Target struct {
	UUID  string
	Title string
	// Area is true when the target is an area rather than a project.
	Area bool
	// Status and Trashed describe a project. Things files into a closed
	// project too, and reopens it, and into a trashed one by uuid.
	Status  model.Status
	Trashed bool
	// ByUUID is true when the reference was the row's uuid, sent trimmed,
	// rather than its title. Things takes list-id and area-id by uuid only,
	// and list and area by title only.
	ByUUID bool
	// Others counts the other rows the title matched as well, which Things
	// passed over, and OtherAreas how many of them are areas: when a project
	// and an area share a title, Things takes the project. Both are 0 for a
	// uuid.
	Others, OtherAreas int
	// Heading is the uuid of the heading Things files the to-do under
	// (HeadingTarget), "" when none was asked for or matched.
	Heading string
}

// AddTarget reports where things:///add would file a to-do given list and
// heading, as Things resolves them. A list matches a project by uuid, and
// Things files into it whatever its state, closed, logged or trashed. By
// title it matches an untrashed project Things still shows, open or closed
// but not yet logged, under FoldName but not ignoring surrounding space; a
// logged or trashed project does not count, and Things puts the to-do in the
// Inbox instead. With no project, AreaTarget matches an area the same way.
// heading matches an untrashed heading of that project under FoldCase; a
// heading under an area, or one the project lacks, is dropped and the to-do
// goes to the list without one. When several projects, or with no project
// several areas, match the title, Things takes the one whose uuid sorts first
// byte by byte, even when another one's title matches list exactly, and so
// does this.
// Measured in Things 3 with add, add-project's area, update and json, over
// pairs of projects and of areas that differ in case, normalisation and
// compatibility forms, created in either order, under the default "daily"
// choice of "Move completed items to Logbook"; the other choices follow the
// same not-yet-logged rule the lists use, unmeasured. headingFound is false
// whenever target.UUID is "".
func (d *DB) AddTarget(list, heading string) (target Target, headingFound bool, err error) {
	// A uuid goes to Things as list-id, which the CLI sends trimmed; a title
	// goes as typed, and Things does not trim it.
	id := strings.TrimSpace(list)
	// A repeating project's template is left out of the title match: it is
	// filed under Repeating, not offered as a list, and its generated
	// project carries the same title. Not measured, as the CLI cannot make a
	// repeating project. Its uuid still names it.
	var trashed int
	err = d.queryRow(`
		SELECT uuid, COALESCE(title, ''), COALESCE(status, 0), COALESCE(trashed, 0), COUNT(*) OVER () - 1
		FROM TMTask t
		WHERE type = ? AND (uuid = ? OR (fold_name(title) = ? AND COALESCE(trashed, 0) = 0
			AND `+openOrUnlogged("")+` AND `+d.recurrenceCol()+` IS NULL))
		ORDER BY uuid = ? DESC, uuid
		LIMIT 1`, int(model.TypeProject), id, FoldName(list), id).
		Scan(&target.UUID, &target.Title, &target.Status, &trashed, &target.Others)
	switch {
	case err == sql.ErrNoRows:
		target, err = d.AreaTarget(list)
		return target, false, err
	case err != nil:
		return Target{}, false, fmt.Errorf("finding project: %w", err)
	}
	target.Trashed = trashed != 0
	if target.ByUUID = target.UUID == id; target.ByUUID {
		target.Others = 0
	} else {
		// Measured in Things 3: a project wins over an area of the same
		// title, whichever uuid sorts first.
		if err := d.queryRow(`SELECT COUNT(*) FROM TMArea WHERE fold_name(title) = ?`, FoldName(list)).
			Scan(&target.OtherAreas); err != nil {
			return Target{}, false, fmt.Errorf("finding area: %w", err)
		}
		target.Others += target.OtherAreas
	}
	if heading == "" {
		return target, false, nil
	}
	if target.Heading, err = d.HeadingTarget(target.UUID, heading); err != nil {
		return target, false, err
	}
	return target, target.Heading != "", nil
}

// HeadingTarget returns the uuid of the heading things:///add and update file
// a to-do under when sent heading for project, "" when the project has none
// that matches. Things matches the title ignoring case, and among headings
// whose titles are the same or differ only in case it takes the one with the
// lowest uuid, whichever case was sent and whichever comes first in the
// project: measured with update on 7 Oct 2026 in six projects with two to
// four such headings, and with add on 8 Oct 2026. An archived heading counts
// the same, and Things reopens it when it files a to-do there: measured on 7
// Oct 2026 in three more projects.
func (d *DB) HeadingTarget(project, heading string) (string, error) {
	var id string
	err := d.queryRow(`
		SELECT uuid FROM TMTask
		WHERE type = ? AND project = ? AND COALESCE(trashed, 0) = 0 AND fold(title) = ?
		ORDER BY uuid LIMIT 1`,
		int(model.TypeHeading), project, FoldCase(heading)).Scan(&id)
	if err != nil && err != sql.ErrNoRows {
		return "", fmt.Errorf("finding heading: %w", err)
	}
	return id, nil
}

// AreaTarget reports the area Things files an item in when sent area, a
// title or a uuid, as add's list and add-project's and update-project's area
// do: by uuid, trimmed, or by title under FoldName but not ignoring
// surrounding space, the one whose uuid sorts first when several match.
func (d *DB) AreaTarget(area string) (Target, error) {
	id := strings.TrimSpace(area)
	t := Target{Area: true}
	err := d.queryRow(`
		SELECT uuid, COALESCE(title, ''), COUNT(*) OVER () - 1 FROM TMArea
		WHERE uuid = ? OR fold_name(title) = ?
		ORDER BY uuid = ? DESC, uuid LIMIT 1`, id, FoldName(area), id).Scan(&t.UUID, &t.Title, &t.Others)
	switch {
	case err == sql.ErrNoRows:
		return Target{}, nil
	case err != nil:
		return Target{}, fmt.Errorf("finding area: %w", err)
	}
	if t.ByUUID = t.UUID == id; t.ByUUID {
		t.Others = 0
	}
	t.OtherAreas = t.Others
	return t, nil
}

// HeadingProject returns the project of the heading uuid names, the heading's
// title, whether there is one, and whether it is in the trash. An import's
// heading-id files the to-do under that heading whatever list the item names,
// and whatever state the heading and its project are in: measured in Things
// 3, a heading in a logged project takes the to-do and is reopened with its
// project, and one in a trashed project takes it into the Trash. Things
// ignores a heading-id it cannot find.
func (d *DB) HeadingProject(uuid string) (project, title string, found, trashed bool, err error) {
	var proj sql.NullString
	var trash int
	err = d.queryRow(`
		SELECT project, COALESCE(title, ''), COALESCE(trashed, 0) FROM TMTask
		WHERE type = ? AND uuid = ?`,
		int(model.TypeHeading), uuid).Scan(&proj, &title, &trash)
	switch {
	case err == sql.ErrNoRows:
		return "", "", false, false, nil
	case err != nil:
		return "", "", false, false, fmt.Errorf("finding heading: %w", err)
	}
	return proj.String, title, true, trash != 0, nil
}
