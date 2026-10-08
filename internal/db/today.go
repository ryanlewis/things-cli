package db

import (
	"database/sql"
	"strings"

	"github.com/ryanlewis/things-cli/internal/clock"
	"github.com/ryanlewis/things-cli/internal/model"
)

// todayClock names the one-row table a query reads "today" from: day in the
// ThingsDate encoding, to compare with startDate and deadline, and start and
// finish, the Unix times today and the next day begin, to compare with
// stopDate. All three are the local day as Go reads it. withToday defines it
// from one reading of clock.Now, so every row, and every part of the date, is
// judged by the same day. SQLite's own 'now' holds only for one step of a
// statement, which is one row, so a listing run across midnight could judge
// its rows by different days.
const todayClock = "today_clock"

// withToday prepends the today_clock definition to a query that reads it, and
// its three values to the arguments. The values bind to the first three
// placeholders, which is where SQLite numbers them: a query that names the
// table but is not given it fails with "no such table" rather than reading
// the wrong day.
func withToday(query string, args []any) (string, []any) {
	if !strings.Contains(query, todayClock) {
		return query, args
	}
	now := clock.Now()
	day := int64(model.ThingsDateFromTime(now))
	start, finish := clock.DayStart(now), clock.NextDayStart(now)
	query = "WITH " + todayClock + "(day, start, finish) AS (SELECT ?, ?, ?) " + query
	return query, append([]any{day, model.TimeToUnix(start), model.TimeToUnix(finish)}, args...)
}

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
