package db

import (
	"database/sql"

	"github.com/ryanlewis/things-cli/internal/model"
)

func (d *DB) GetChecklistItems(taskUUID string) ([]model.ChecklistItem, error) {
	query := `
		SELECT uuid, COALESCE(title, ''), COALESCE(status, 0), stopDate, COALESCE("index", 0)
		FROM TMChecklistItem
		WHERE task = ?
		-- uuid last so the order is total: an edit's read-back compares two
		-- reads of the checklist row by row, and two items sharing an index
		-- would otherwise come back in an order SQLite does not define.
		ORDER BY "index" ASC, uuid ASC
	`
	return queryAll(d, "checklist item", scanChecklistItem, query, taskUUID)
}

func scanChecklistItem(row rowScanner) (model.ChecklistItem, error) {
	var item model.ChecklistItem
	var stopDate sql.NullFloat64
	err := row.Scan(&item.UUID, &item.Title, &item.Status, &stopDate, &item.Index)
	item.StopDate = unixTime(stopDate)
	return item, err
}
