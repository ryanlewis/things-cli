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
	// Others counts the other rows the title matched as well, which Things
	// passed over. It is 0 for a uuid.
	Others int
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
	err = d.db.QueryRow(`
		SELECT uuid, COALESCE(title, ''), COALESCE(status, 0), COALESCE(trashed, 0), COUNT(*) OVER () - 1
		FROM TMTask t
		WHERE type = ? AND (uuid = ? OR (fold_name(title) = ? AND COALESCE(trashed, 0) = 0
			AND (status = ? OR `+closedTodayUnlogged+`) AND `+d.recurrenceCol()+` IS NULL))
		ORDER BY uuid = ? DESC, uuid
		LIMIT 1`, int(model.TypeProject), id, FoldName(list), int(model.StatusOpen), id).
		Scan(&target.UUID, &target.Title, &target.Status, &trashed, &target.Others)
	switch {
	case err == sql.ErrNoRows:
		target, err = d.AreaTarget(list)
		return target, false, err
	case err != nil:
		return Target{}, false, fmt.Errorf("finding project: %w", err)
	}
	target.Trashed = trashed != 0
	if target.UUID == id {
		target.Others = 0
	}
	if heading == "" {
		return target, false, nil
	}
	var n int
	if err := d.db.QueryRow(`
		SELECT COUNT(*) FROM TMTask
		WHERE type = ? AND project = ? AND COALESCE(trashed, 0) = 0 AND fold(title) = ?`,
		int(model.TypeHeading), target.UUID, FoldCase(heading)).Scan(&n); err != nil {
		return target, false, fmt.Errorf("finding heading: %w", err)
	}
	return target, n > 0, nil
}

// AreaTarget reports the area Things files an item in when sent area, a
// title or a uuid, as add's list and add-project's and update-project's area
// do: by uuid, trimmed, or by title under FoldName but not ignoring
// surrounding space, the one whose uuid sorts first when several match.
func (d *DB) AreaTarget(area string) (Target, error) {
	id := strings.TrimSpace(area)
	t := Target{Area: true}
	err := d.db.QueryRow(`
		SELECT uuid, COALESCE(title, ''), COUNT(*) OVER () - 1 FROM TMArea
		WHERE uuid = ? OR fold_name(title) = ?
		ORDER BY uuid = ? DESC, uuid LIMIT 1`, id, FoldName(area), id).Scan(&t.UUID, &t.Title, &t.Others)
	switch {
	case err == sql.ErrNoRows:
		return Target{}, nil
	case err != nil:
		return Target{}, fmt.Errorf("finding area: %w", err)
	}
	if t.UUID == id {
		t.Others = 0
	}
	return t, nil
}

// HeadingExists reports whether uuid names an untrashed project heading,
// which things:///update needs for heading-id to move a to-do. Things ignores
// a heading-id it cannot find.
func (d *DB) HeadingExists(uuid string) (bool, error) {
	var n int
	if err := d.db.QueryRow(`
		SELECT COUNT(*) FROM TMTask
		WHERE type = ? AND uuid = ? AND COALESCE(trashed, 0) = 0`,
		int(model.TypeHeading), uuid).Scan(&n); err != nil {
		return false, fmt.Errorf("finding heading: %w", err)
	}
	return n > 0, nil
}
