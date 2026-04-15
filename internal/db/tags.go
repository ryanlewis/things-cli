package db

import (
	"fmt"

	"github.com/ryanlewis/things-cli/internal/model"
)

func (d *DB) ListTags() ([]model.Tag, error) {
	query := `
		SELECT uuid, COALESCE(title, ''), COALESCE(shortcut, ''), COALESCE(parent, '')
		FROM TMTag
		ORDER BY "index" ASC
	`
	rows, err := d.db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("querying tags: %w", err)
	}
	defer rows.Close()

	var tags []model.Tag
	for rows.Next() {
		var t model.Tag
		if err := rows.Scan(&t.UUID, &t.Title, &t.Shortcut, &t.ParentUUID); err != nil {
			return nil, fmt.Errorf("scanning tag: %w", err)
		}
		tags = append(tags, t)
	}
	return tags, rows.Err()
}
