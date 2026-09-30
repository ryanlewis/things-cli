package db

import (
	"fmt"

	"github.com/ryanlewis/things-cli/internal/model"
)

// FindAreaUUID resolves an area reference (UUID or title) to its UUID,
// returning "" when no row matches. Titles match ignoring case, as Things
// matches them; matchRef says which row wins.
func (d *DB) FindAreaUUID(ref string) (string, error) {
	areas, err := d.ListAreas()
	if err != nil {
		return "", fmt.Errorf("finding area: %w", err)
	}
	rows := make([]uuidTitle, len(areas))
	for i, a := range areas {
		rows[i] = uuidTitle{a.UUID, a.Title}
	}
	return matchRef(rows, ref), nil
}

func (d *DB) ListAreas() ([]model.Area, error) {
	query := `
		SELECT uuid, COALESCE(title, ''), COALESCE(visible, 1)
		FROM TMArea
		-- uuid last so the order is total: two areas sharing an "index" would
		-- otherwise list in an order SQLite does not define (issue #221).
		ORDER BY "index" ASC, uuid ASC
	`
	rows, err := d.db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("querying areas: %w", err)
	}
	defer rows.Close()

	areas := []model.Area{}
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
