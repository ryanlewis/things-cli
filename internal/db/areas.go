package db

import (
	"fmt"
	"strings"

	"github.com/ryanlewis/things-cli/internal/model"
)

// FindAreaUUID resolves an area reference (UUID or title) to its UUID,
// returning "" when no row matches. Titles match under FoldName, ignoring
// surrounding space; matchRef says which row wins.
func (d *DB) FindAreaUUID(ref string) (string, error) {
	areas, err := d.ListAreas()
	if err != nil {
		return "", fmt.Errorf("finding area: %w", err)
	}
	rows := make([]uuidTitle, len(areas))
	for i, a := range areas {
		rows[i] = uuidTitle{a.UUID, a.Title}
	}
	return matchRef(rows, ref, foldAreaRef), nil
}

// foldAreaRef is FoldTag's trimming with FoldName's folding: the key `open
// --area` compares area titles under.
func foldAreaRef(s string) string {
	return FoldName(strings.TrimSpace(s))
}

func (d *DB) ListAreas() ([]model.Area, error) {
	query := `
		SELECT uuid, COALESCE(title, ''), COALESCE(visible, 1)
		FROM TMArea
		-- uuid last so the order is total: two areas sharing an "index" would
		-- otherwise list in an order SQLite does not define (issue #221).
		ORDER BY "index" ASC, uuid ASC
	`
	return queryAll(d, "area", func(row rowScanner) (model.Area, error) {
		var a model.Area
		var visible int
		err := row.Scan(&a.UUID, &a.Title, &visible)
		a.Visible = visible != 0
		return a, err
	}, query)
}
