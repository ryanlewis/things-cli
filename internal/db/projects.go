package db

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/ryanlewis/things-cli/internal/model"
)

func (d *DB) ListProjects(areaFilter string, includeCompleted bool) ([]model.Project, error) {
	query := `
		SELECT
			t.uuid,
			COALESCE(t.title, ''),
			COALESCE(t.status, 0),
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

	if !includeCompleted {
		query += " AND t.status = 0"
	}
	if areaFilter != "" {
		query += " AND (a.uuid = ? OR a.title LIKE ?)"
		args = append(args, areaFilter, areaFilter)
	}

	query += ` GROUP BY t.uuid ORDER BY CASE WHEN a.uuid IS NULL THEN 1 ELSE 0 END, a."index", t."index" ASC`

	rows, err := d.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("querying projects: %w", err)
	}
	defer rows.Close()

	var projects []model.Project
	for rows.Next() {
		var p model.Project
		var tagsStr string
		var status sql.NullInt64
		if err := rows.Scan(&p.UUID, &p.Title, &status, &p.AreaUUID, &p.AreaTitle, &tagsStr, &p.TaskCount, &p.OpenCount); err != nil {
			return nil, fmt.Errorf("scanning project: %w", err)
		}
		if status.Valid {
			p.Status = int(status.Int64)
		}
		if tagsStr != "" {
			p.Tags = strings.Split(tagsStr, "\x1f")
		}
		projects = append(projects, p)
	}
	return projects, rows.Err()
}
