package db

import (
	"database/sql"

	"github.com/ryanlewis/things-cli/internal/model"
)

// ListProjects lists the projects, in area order. includeCompleted lists the
// closed ones too, logged or not. Without it the listing is the open projects
// and, unless openOnly is set, the ones closed and not yet logged
// (closedTodayUnlogged), which the app's own project list still holds: on 9
// Oct 2026 AppleScript's `projects` returned a project completed that day
// that `things projects` left out. That is the rule the task lists follow.
func (d *DB) ListProjects(areaFilter string, includeCompleted, openOnly bool) ([]model.Project, error) {
	query := `
		SELECT
			t.uuid,
			COALESCE(t.title, ''),
			COALESCE(t.status, 0),
			` + shownStart + `,
			COALESCE(t.startBucket, 0),
			t.startDate,
			t.deadline,
			COALESCE(a.uuid, ''),
			COALESCE(a.title, ''),
			COALESCE(GROUP_CONCAT(tag.title, char(31)), ''),
			COALESCE(t.untrashedLeafActionsCount, 0),
			COALESCE(t.openUntrashedLeafActionsCount, 0)
		FROM TMTask t
		LEFT JOIN TMArea a ON t.area = a.uuid
		LEFT JOIN TMTaskTag tt ON tt.tasks = t.uuid
		LEFT JOIN TMTag tag ON tt.tags = tag.uuid
		WHERE t.type = 1 AND t.trashed = 0
	`
	var args []any

	// A repeating project is stored as a template plus the projects it
	// generates. Things files the template under Repeating, not under
	// Projects, so `things repeating` lists it and this does not (issue
	// #165). On a schema with no recurrence column the reference degrades to
	// NULL and the clause is a no-op.
	query += " AND " + d.recurrenceCol() + " IS NULL"

	switch {
	case includeCompleted:
	case openOnly:
		query += " AND " + openRows
	default:
		query += " AND " + openOrUnlogged("")
	}
	if areaFilter != "" {
		// The same --area flag as the list filters, escaped the same way so
		// the two commands cannot disagree about what a name matches
		// (issue #262).
		clause, clauseArgs := areaNameMatch("a.uuid", "a.title", areaFilter)
		query += " AND " + clause
		args = append(args, clauseArgs...)
	}

	// t.uuid last for the same reason the task views take it: two projects in
	// one area can share an "index", and without a total order the listing can
	// come back in a different sequence between two runs (issue #221).
	query += ` GROUP BY t.uuid ORDER BY CASE WHEN a.uuid IS NULL THEN 1 ELSE 0 END, a."index", t."index" ASC` + uuidTiebreak

	// shownStart asks whether the row is a template or inside one. A project
	// has no parent project, so that half is NULL.
	query = fillRepeating(query, d.recurrenceCol(), "NULL")

	return queryAll(d, "project", scanProject, query, args...)
}

func scanProject(row rowScanner) (model.Project, error) {
	var p model.Project
	var tagsStr string
	var startDate, deadline sql.NullFloat64
	if err := row.Scan(
		&p.UUID, &p.Title, &p.Status,
		&p.Start, &p.StartBucket, &startDate, &deadline,
		&p.AreaUUID, &p.AreaTitle, &tagsStr,
		&p.TaskCount, &p.OpenCount,
	); err != nil {
		return p, err
	}
	p.StartDate = thingsDate(startDate)
	p.Deadline = thingsDate(deadline)
	p.Tags = splitTags(tagsStr)
	return p, nil
}
