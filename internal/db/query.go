package db

import (
	"database/sql"
	"fmt"
)

// query and queryRow run a query on the database with today_clock supplied.
// Every query goes through them, so a fragment that reads the day can be used
// anywhere.
func (d *DB) query(query string, args ...any) (*sql.Rows, error) {
	query, args = withToday(query, args)
	return d.db.Query(query, args...)
}

func (d *DB) queryRow(query string, args ...any) *sql.Row {
	query, args = withToday(query, args)
	return d.db.QueryRow(query, args...)
}

// rowScanner is what a scan function reads one row through: *sql.Row and
// *sql.Rows both satisfy it.
type rowScanner interface{ Scan(...any) error }

// queryAll runs query and scans every row it returns with scan. The result
// is empty rather than nil when no row matches. noun names one row in the
// error text: "querying <noun>s", "scanning <noun>".
func queryAll[T any](d *DB, noun string, scan func(rowScanner) (T, error), query string, args ...any) ([]T, error) {
	rows, err := d.query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("querying %ss: %w", noun, err)
	}
	defer rows.Close()

	out := []T{}
	for rows.Next() {
		v, err := scan(rows)
		if err != nil {
			return nil, fmt.Errorf("scanning %s: %w", noun, err)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
