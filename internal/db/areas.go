package db

import (
	"fmt"

	"github.com/ryanlewis/things-cli/internal/model"
)

func (d *DB) ListAreas() ([]model.Area, error) {
	query := `
		SELECT uuid, COALESCE(title, ''), COALESCE(visible, 1)
		FROM TMArea
		ORDER BY "index" ASC
	`
	rows, err := d.db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("querying areas: %w", err)
	}
	defer rows.Close()

	var areas []model.Area
	for rows.Next() {
		var a model.Area
		var visible int
		if err := rows.Scan(&a.UUID, &a.Title, &visible); err != nil {
			return nil, fmt.Errorf("scanning area: %w", err)
		}
		a.Visible = visible != 0
		areas = append(areas, a)
	}
	return areas, rows.Err()
}
