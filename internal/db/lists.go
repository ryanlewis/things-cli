package db

import (
	"database/sql"
	"fmt"

	"github.com/ryanlewis/things-cli/internal/model"
)

// AddTarget reports where things:///add would file a to-do given list and
// heading titles, as Things resolves them. list matches an open, untrashed
// project or an area by title, ignoring case; a completed or trashed project
// does not count, and Things puts the to-do in the Inbox instead. heading
// matches an untrashed heading of that project, ignoring case; a heading
// under an area, or one the project lacks, is dropped and the to-do goes to
// the list without one. headingFound is false whenever listFound is.
func (d *DB) AddTarget(list, heading string) (listFound, headingFound bool, err error) {
	var project string
	err = d.db.QueryRow(`
		SELECT uuid FROM TMTask
		WHERE type = ? AND status = ? AND COALESCE(trashed, 0) = 0 AND fold(trim(title)) = ?
		LIMIT 1`, int(model.TypeProject), int(model.StatusOpen), FoldTag(list)).Scan(&project)
	switch {
	case err == sql.ErrNoRows:
		var n int
		if err := d.db.QueryRow(`SELECT COUNT(*) FROM TMArea WHERE fold(trim(title)) = ?`, FoldTag(list)).Scan(&n); err != nil {
			return false, false, fmt.Errorf("finding area: %w", err)
		}
		return n > 0, false, nil
	case err != nil:
		return false, false, fmt.Errorf("finding project: %w", err)
	case heading == "":
		return true, false, nil
	}
	var n int
	if err := d.db.QueryRow(`
		SELECT COUNT(*) FROM TMTask
		WHERE type = ? AND project = ? AND COALESCE(trashed, 0) = 0 AND fold(trim(title)) = ?`,
		int(model.TypeHeading), project, FoldTag(heading)).Scan(&n); err != nil {
		return true, false, fmt.Errorf("finding heading: %w", err)
	}
	return true, n > 0, nil
}
