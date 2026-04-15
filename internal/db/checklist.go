package db

import (
	"database/sql"
	"fmt"

	"github.com/ryanlewis/things-cli/internal/model"
)

func (d *DB) GetChecklistItems(taskUUID string) ([]model.ChecklistItem, error) {
	query := `
		SELECT uuid, COALESCE(title, ''), COALESCE(status, 0), stopDate, COALESCE("index", 0)
		FROM TMChecklistItem
		WHERE task = ?
		ORDER BY "index" ASC
	`
	rows, err := d.db.Query(query, taskUUID)
	if err != nil {
		return nil, fmt.Errorf("querying checklist items: %w", err)
	}
	defer rows.Close()

	var items []model.ChecklistItem
	for rows.Next() {
		var item model.ChecklistItem
		var stopDate sql.NullFloat64
		if err := rows.Scan(&item.UUID, &item.Title, &item.Status, &stopDate, &item.Index); err != nil {
			return nil, fmt.Errorf("scanning checklist item: %w", err)
		}
		if stopDate.Valid {
			ts := model.CoreDataToTime(stopDate.Float64)
			item.StopDate = &ts
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
