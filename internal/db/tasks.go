package db

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/ryanlewis/things-cli/internal/model"
)

type TaskFilter struct {
	Project string
	Area    string
	Tag     string
}

const baseTaskQuery = `
SELECT
	t.uuid,
	COALESCE(t.title, ''),
	COALESCE(t.notes, ''),
	COALESCE(t.type, 0),
	COALESCE(t.status, 0),
	COALESCE(t.start, 0),
	COALESCE(t.startBucket, 0),
	t.startDate,
	t.deadline,
	t.stopDate,
	t.creationDate,
	COALESCE(t.trashed, 0),
	COALESCE(p.uuid, ''),
	COALESCE(p.title, ''),
	COALESCE(t.heading, ''),
	COALESCE(h.title, ''),
	COALESCE(a.uuid, COALESCE(pa.uuid, '')),
	COALESCE(a.title, COALESCE(pa.title, '')),
	COALESCE(GROUP_CONCAT(tag.title, char(31)), ''),
	COALESCE(t."index", 0),
	COALESCE(t.todayIndex, 0)
FROM TMTask t
LEFT JOIN TMTask p ON t.project = p.uuid
LEFT JOIN TMTask h ON t.heading = h.uuid
LEFT JOIN TMArea a ON t.area = a.uuid
LEFT JOIN TMArea pa ON p.area = pa.uuid
LEFT JOIN TMTaskTag tt ON tt.tasks = t.uuid
LEFT JOIN TMTag tag ON tt.tags = tag.uuid
`

func scanTask(row interface{ Scan(...any) error }) (model.Task, error) {
	var t model.Task
	var startDate, deadline, stopDate, creationDate sql.NullFloat64
	var tagsStr string
	var trashed int

	err := row.Scan(
		&t.UUID, &t.Title, &t.Notes,
		&t.Type, &t.Status, &t.Start, &t.StartBucket,
		&startDate, &deadline, &stopDate, &creationDate,
		&trashed,
		&t.ProjectUUID, &t.ProjectTitle,
		&t.HeadingUUID, &t.HeadingTitle,
		&t.AreaUUID, &t.AreaTitle,
		&tagsStr,
		&t.Index, &t.TodayIndex,
	)
	if err != nil {
		return t, err
	}

	t.Trashed = trashed != 0
	if startDate.Valid {
		d := model.ThingsDate(int64(startDate.Float64))
		t.StartDate = &d
	}
	if deadline.Valid {
		d := model.ThingsDate(int64(deadline.Float64))
		t.Deadline = &d
	}
	if stopDate.Valid {
		ts := model.CoreDataToTime(stopDate.Float64)
		t.StopDate = &ts
	}
	if creationDate.Valid {
		ts := model.CoreDataToTime(creationDate.Float64)
		t.CreationDate = &ts
	}
	if tagsStr != "" {
		t.Tags = strings.Split(tagsStr, "\x1f")
	}
	return t, nil
}

var viewFilters = map[string]string{
	"today":     "t.start = 1 AND t.startBucket IN (0, 1) AND t.startDate IS NOT NULL AND (t.status = 0 OR (t.status IN (2, 3) AND t.todayIndexReferenceDate = ? AND t.stopDate > COALESCE((SELECT manualLogDate FROM TMSettings LIMIT 1), 0))) AND t.trashed = 0 AND COALESCE(p.trashed, 0) = 0 AND t.type = 0",
	"inbox":     "t.start = 0 AND t.status = 0 AND t.trashed = 0 AND t.type = 0",
	"upcoming":  "t.start = 2 AND t.startDate IS NOT NULL AND t.status = 0 AND t.trashed = 0 AND t.type = 0",
	"anytime":   "t.start = 1 AND t.status = 0 AND t.trashed = 0 AND t.type = 0",
	"someday":   "t.start = 2 AND t.startDate IS NULL AND t.status = 0 AND t.trashed = 0 AND t.type = 0",
	"logbook":   "t.status = 3 AND t.trashed = 0 AND t.type = 0",
	"trash":     "t.trashed = 1 AND t.type = 0",
	"deadlines": "t.deadline IS NOT NULL AND t.status = 0 AND t.trashed = 0 AND t.type = 0",
	"project":   "t.status = 0 AND t.trashed = 0 AND t.type = 0",
}

var viewOrderBy = map[string]string{
	"logbook":   "ORDER BY t.stopDate DESC",
	"deadlines": "ORDER BY t.deadline ASC",
	"today":     "ORDER BY CASE WHEN t.project IS NULL THEN 0 ELSE 1 END, CASE WHEN t.project IS NULL THEN t.status ELSE 0 END DESC, CASE WHEN t.project IS NULL THEN t.todayIndexReferenceDate ELSE 0 END DESC, CASE WHEN t.project IS NOT NULL AND pa.uuid IS NULL THEN 1 ELSE 0 END, pa.\"index\", p.\"index\", t.todayIndex ASC",
	"project":   "ORDER BY t.start ASC, t.\"index\" ASC",
}

func ValidView(name string) bool {
	_, ok := viewFilters[name]
	return ok
}

func (d *DB) ListTasks(view string, opts TaskFilter) ([]model.Task, error) {
	where, ok := viewFilters[view]
	if !ok {
		return nil, fmt.Errorf("unknown view: %s", view)
	}

	var args []any
	if view == "today" {
		args = append(args, int64(model.ThingsDateFromTime(time.Now())))
	}
	if opts.Project != "" {
		where += " AND (p.uuid = ? OR p.title LIKE ?)"
		args = append(args, opts.Project, opts.Project)
	}
	if opts.Area != "" {
		where += " AND (COALESCE(a.uuid, pa.uuid) = ? OR COALESCE(a.title, pa.title) LIKE ?)"
		args = append(args, opts.Area, opts.Area)
	}
	if opts.Tag != "" {
		where += " AND t.uuid IN (SELECT tt2.tasks FROM TMTaskTag tt2 JOIN TMTag tg2 ON tt2.tags = tg2.uuid WHERE tg2.title LIKE ?)"
		args = append(args, opts.Tag)
	}

	orderBy := viewOrderBy[view]
	if orderBy == "" {
		orderBy = "ORDER BY t.\"index\" ASC"
	}

	query := baseTaskQuery + " WHERE " + where + " GROUP BY t.uuid " + orderBy
	return d.collectTasks(query, args...)
}

func (d *DB) GetTaskByUUID(uuid string) (*model.Task, error) {
	query := baseTaskQuery + " WHERE t.uuid = ? GROUP BY t.uuid"
	row := d.db.QueryRow(query, uuid)
	t, err := scanTask(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("querying task by uuid: %w", err)
	}
	return &t, nil
}

func (d *DB) GetTask(uuidOrTitle string) (*model.Task, error) {
	t, err := d.GetTaskByUUID(uuidOrTitle)
	if err != nil {
		return nil, err
	}
	if t != nil {
		return t, nil
	}

	// Try exact title match
	query := baseTaskQuery + " WHERE t.title = ? AND t.trashed = 0 AND t.status = 0 GROUP BY t.uuid LIMIT 1"
	row := d.db.QueryRow(query, uuidOrTitle)
	task, err := scanTask(row)
	if err == nil {
		return &task, nil
	}
	if err != sql.ErrNoRows {
		return nil, fmt.Errorf("querying task by title: %w", err)
	}

	// Try LIKE match — return all matches for disambiguation
	matches, err := d.FindTasksByTitle(uuidOrTitle)
	if err != nil {
		return nil, err
	}
	switch len(matches) {
	case 0:
		return nil, fmt.Errorf("task not found: %s", uuidOrTitle)
	case 1:
		return &matches[0], nil
	default:
		return nil, &AmbiguousTaskError{Query: uuidOrTitle, Matches: matches}
	}
}

func (d *DB) FindTasksByTitle(substr string) ([]model.Task, error) {
	query := baseTaskQuery + " WHERE t.title LIKE ? AND t.trashed = 0 AND t.status = 0 GROUP BY t.uuid ORDER BY t.\"index\" ASC"
	return d.collectTasks(query, "%"+substr+"%")
}

type AmbiguousTaskError struct {
	Query   string
	Matches []model.Task
}

func (e *AmbiguousTaskError) Error() string {
	return fmt.Sprintf("ambiguous task: %q matches %d tasks", e.Query, len(e.Matches))
}

func (d *DB) SearchTasks(query string) ([]model.Task, error) {
	pattern := "%" + query + "%"
	q := baseTaskQuery + " WHERE (t.title LIKE ? OR t.notes LIKE ?) AND t.trashed = 0 GROUP BY t.uuid ORDER BY t.\"index\" ASC"
	return d.collectTasks(q, pattern, pattern)
}

func (d *DB) collectTasks(query string, args ...any) ([]model.Task, error) {
	rows, err := d.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("querying tasks: %w", err)
	}
	defer rows.Close()

	var tasks []model.Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning task: %w", err)
		}
		tasks = append(tasks, t)
	}
	return tasks, rows.Err()
}
