package db

import (
	"fmt"
	"strings"
)

// recurrenceColumns are the TMTask columns that have carried a to-do's
// recurrence rule across Things 3 schema versions, newest first. The column is
// probed at runtime rather than hard-coded: a schema carrying none of them
// degrades to "nothing repeats" instead of breaking every task query.
var recurrenceColumns = []string{"rt1_recurrenceRule", "recurrenceRule"}

// repeatingExpr returns the SQL expression that yields 1 for a repeating item
// and 0 otherwise, aliased against the `t` TMTask row. The probe runs once per
// DB and the result is cached.
func (d *DB) repeatingExpr() string {
	d.repeatOnce.Do(func() {
		d.repeatSQL = "0"
		cols, err := d.tableColumns("TMTask")
		if err != nil {
			return
		}
		for _, c := range recurrenceColumns {
			if cols[c] {
				d.repeatSQL = `CASE WHEN t."` + c + `" IS NOT NULL THEN 1 ELSE 0 END`
				return
			}
		}
	})
	return d.repeatSQL
}

// tableColumns returns the column names of table. The name is interpolated
// because PRAGMA does not accept bound parameters; every caller passes a
// compile-time constant.
func (d *DB) tableColumns(table string) (map[string]bool, error) {
	rows, err := d.db.Query(fmt.Sprintf("PRAGMA table_info(%q)", table))
	if err != nil {
		return nil, fmt.Errorf("reading %s columns: %w", table, err)
	}
	defer rows.Close()

	cols := map[string]bool{}
	for rows.Next() {
		var (
			cid        int
			name, typ  string
			notNull    int
			dflt       any
			primaryKey int
		)
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &primaryKey); err != nil {
			return nil, fmt.Errorf("scanning %s columns: %w", table, err)
		}
		cols[name] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading %s columns: %w", table, err)
	}
	return cols, nil
}

// taskQuery fills the repeating placeholder in baseTaskQuery.
func (d *DB) taskQuery() string {
	return strings.Replace(baseTaskQuery, repeatingPlaceholder, d.repeatingExpr(), 1)
}
