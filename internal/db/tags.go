package db

import (
	"database/sql"
	"fmt"

	"github.com/ryanlewis/things-cli/internal/model"
)

// FindTagUUID resolves a tag reference (UUID or title) to its UUID,
// returning "" when no row matches.
func (d *DB) FindTagUUID(ref string) (string, error) {
	var uuid string
	err := d.db.QueryRow(
		`SELECT uuid FROM TMTag WHERE uuid = ? OR title = ? LIMIT 1`,
		ref, ref,
	).Scan(&uuid)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("finding tag: %w", err)
	}
	return uuid, nil
}

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
