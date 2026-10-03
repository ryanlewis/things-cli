package db

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/ryanlewis/things-cli/internal/model"
)

// AddTarget reports where things:///add would file a to-do given list and
// heading, as Things resolves them. list matches an open, untrashed project
// or an area by title, ignoring case, or by uuid; a completed or trashed
// project does not count, and Things puts the to-do in the Inbox instead.
// target is the uuid of the row list matched, "" when none did. heading
// matches an untrashed heading of that project, ignoring case; a heading
// under an area, or one the project lacks, is dropped and the to-do goes to
// the list without one. When open projects differ only in case, the one
// whose title matches list exactly is checked for the heading, as the list
// lookup does. headingFound is false whenever target is "".
func (d *DB) AddTarget(list, heading string) (target string, headingFound bool, err error) {
	list = strings.TrimSpace(list)
	var project string
	err = d.db.QueryRow(`
		SELECT uuid FROM TMTask
		WHERE type = ? AND status = ? AND COALESCE(trashed, 0) = 0 AND (uuid = ? OR fold(trim(title)) = ?)
		ORDER BY uuid = ? DESC, trim(title) = ? DESC
		LIMIT 1`, int(model.TypeProject), int(model.StatusOpen), list, FoldTag(list), list, list).Scan(&project)
	switch {
	case err == sql.ErrNoRows:
		var area string
		err := d.db.QueryRow(`
			SELECT uuid FROM TMArea WHERE uuid = ? OR fold(trim(title)) = ?
			ORDER BY uuid = ? DESC LIMIT 1`, list, FoldTag(list), list).Scan(&area)
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
		WHERE type = ? AND project = ? AND COALESCE(trashed, 0) = 0 AND fold(trim(title)) = ?`,
		int(model.TypeHeading), project, FoldTag(heading)).Scan(&n); err != nil {
		return project, false, fmt.Errorf("finding heading: %w", err)
	}
	return project, n > 0, nil
}
