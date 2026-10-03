package db

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/ryanlewis/things-cli/internal/model"
)

// AddTarget reports where things:///add would file a to-do given list and
// heading, as Things resolves them. list matches an open, untrashed project
// or an area by title, ignoring case but not surrounding space, or by uuid; a completed or trashed
// project does not count, and Things puts the to-do in the Inbox instead.
// target is the uuid of the row list matched, "" when none did. heading
// matches an untrashed heading of that project the same way; a heading
// under an area, or one the project lacks, is dropped and the to-do goes to
// the list without one. When open projects differ only in case, the CLI
// checks the one whose title matches list exactly; which one Things picks
// is not known. headingFound is false whenever target is "".
func (d *DB) AddTarget(list, heading string) (target string, headingFound bool, err error) {
	// A uuid goes to Things as list-id, which the CLI sends trimmed; a title
	// goes as typed, and Things does not trim it.
	id := strings.TrimSpace(list)
	var project string
	err = d.db.QueryRow(`
		SELECT uuid FROM TMTask
		WHERE type = ? AND status = ? AND COALESCE(trashed, 0) = 0 AND (uuid = ? OR fold(title) = ?)
		ORDER BY uuid = ? DESC, title = ? DESC
		LIMIT 1`, int(model.TypeProject), int(model.StatusOpen), id, FoldCase(list), id, list).Scan(&project)
	switch {
	case err == sql.ErrNoRows:
		var area string
		err := d.db.QueryRow(`
			SELECT uuid FROM TMArea WHERE uuid = ? OR fold(title) = ?
			ORDER BY uuid = ? DESC LIMIT 1`, id, FoldCase(list), id).Scan(&area)
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
