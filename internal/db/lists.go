package db

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/ryanlewis/things-cli/internal/model"
)

// AddTarget reports where things:///add would file a to-do given list and
// heading, as Things resolves them. list matches an untrashed project Things
// still shows, open or closed but not yet logged, or an area, by title under
// FoldName but not ignoring surrounding space, or by uuid; a logged or
// trashed project does not count, and Things puts the to-do in the Inbox
// instead. target is the uuid of the row list matched, "" when none did.
// heading matches an untrashed heading of that project under FoldCase; a
// heading under an area, or one the project lacks, is dropped and the to-do
// goes to the list without one. When several projects, or with no project
// several areas, match the title, Things takes the one whose uuid sorts first
// byte by byte, even when another one's title matches list exactly, and so
// does this.
// Measured in Things 3 with add, add-project's area and json, over pairs of
// projects and of areas that differ in case, normalisation and compatibility
// forms, created in either order. headingFound is false whenever target is
// "".
func (d *DB) AddTarget(list, heading string) (target string, headingFound bool, err error) {
	// A uuid goes to Things as list-id, which the CLI sends trimmed; a title
	// goes as typed, and Things does not trim it.
	id := strings.TrimSpace(list)
	var project string
	err = d.db.QueryRow(`
		SELECT uuid FROM TMTask t
		WHERE type = ? AND (status = ? OR `+closedTodayUnlogged+`) AND COALESCE(trashed, 0) = 0
			AND (uuid = ? OR fold_name(title) = ?)
		ORDER BY uuid = ? DESC, uuid
		LIMIT 1`, int(model.TypeProject), int(model.StatusOpen), id, FoldName(list), id).Scan(&project)
	switch {
	case err == sql.ErrNoRows:
		var area string
		err := d.db.QueryRow(`
			SELECT uuid FROM TMArea WHERE uuid = ? OR fold_name(title) = ?
			ORDER BY uuid = ? DESC, uuid LIMIT 1`, id, FoldName(list), id).Scan(&area)
		if err != nil && err != sql.ErrNoRows {
			return "", false, fmt.Errorf("finding area: %w", err)
		}
		return area, false, nil
	case err != nil:
		return "", false, fmt.Errorf("finding project: %w", err)
	case heading == "":
		return project, false, nil
	}
	var n int
	if err := d.db.QueryRow(`
		SELECT COUNT(*) FROM TMTask
		WHERE type = ? AND project = ? AND COALESCE(trashed, 0) = 0 AND fold(title) = ?`,
		int(model.TypeHeading), project, FoldCase(heading)).Scan(&n); err != nil {
		return project, false, fmt.Errorf("finding heading: %w", err)
	}
	return project, n > 0, nil
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
