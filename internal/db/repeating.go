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

// recurrenceCol returns the recurrence column reference, aliased against the
// `t` TMTask row, for callers to test with IS NULL / IS NOT NULL. On a schema
// carrying no such column it is the literal NULL, which makes "IS NOT NULL"
// false for every row and "IS NULL" true for every row — nothing repeats. The
// probe runs once per DB and the result is cached.
//
// A bare column reference rather than a CASE expression is deliberate: Things
// ships a partial index (index_TMTask_id_where_recurrenceRuleNotNull, ON
// TMTask(uuid) WHERE rt1_recurrenceRule IS NOT NULL) that SQLite only matches
// syntactically, so `t."rt1_recurrenceRule" IS NOT NULL` uses it while
// `CASE WHEN ... END = 1` falls back to scanning every task.
func (d *DB) recurrenceCol() string {
	d.probeRepeating()
	return d.repeatSQL
}

// probeRepeating resolves the recurrence column reference and the assembled
// task query once per DB.
func (d *DB) probeRepeating() {
	d.repeatOnce.Do(func() {
		d.repeatSQL = "NULL"
		defer func() {
			d.repeatQuery = strings.Replace(baseTaskQuery, repeatingPlaceholder, d.repeatSQL, 1)
		}()
		cols, err := d.tableColumns("TMTask")
		if err != nil {
			return
		}
		for _, c := range recurrenceColumns {
			if cols[c] {
				d.repeatSQL = `t."` + c + `"`
				return
			}
		}
	})
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

// taskQuery returns baseTaskQuery with the repeating placeholder filled in.
func (d *DB) taskQuery() string {
	d.probeRepeating()
	return d.repeatQuery
}
