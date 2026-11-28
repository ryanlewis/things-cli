package main

import (
	"strings"
	"testing"

	"github.com/alecthomas/kong"

	"github.com/ryanlewis/things-cli/internal/cache"
	"github.com/ryanlewis/things-cli/internal/db"
	"github.com/ryanlewis/things-cli/internal/db/dbtest"
	"github.com/ryanlewis/things-cli/internal/model"
)

// parseRecorded parses the re-run command a listing left in the cache, the
// way the user's shell and things would. The values these tests use need no
// quoting, so splitting on spaces is the shell's word split.
func parseRecorded(t *testing.T) *CLI {
	t.Helper()
	last, err := cache.ReadLastList()
	if err != nil {
		t.Fatalf("ReadLastList: %v", err)
	}
	words := strings.Fields(last.Command)
	if len(words) == 0 || words[0] != "things" {
		t.Fatalf("recorded command = %q, want a things command", last.Command)
	}
	var cli CLI
	parser, err := kong.New(&cli, parserOptions(nil)...)
	if err != nil {
		t.Fatalf("kong.New: %v", err)
	}
	if _, err := parser.Parse(words[1:]); err != nil {
		t.Fatalf("recorded command %q does not parse: %v", last.Command, err)
	}
	return &cli
}

func dashDB(t *testing.T) *db.DB {
	t.Helper()
	sqlDB := dbtest.NewSQL(t)
	fx := dbtest.NewFixture(t, sqlDB)
	fx.Tag("tag-dash", "-urgent", 0)
	today := int64(model.ThingsDateFromTime(testNow))
	fx.Todo("task-dash", "-urgent fix", 0, dbtest.AnytimeOn(today))
	fx.Tagged("task-dash", "tag-dash")
	return db.NewFromSQL(sqlDB)
}

// A search for a query that starts with a dash has to be typed after "--", and
// the re-run the cache offers once the row numbers go stale has to be too:
// `things search -urgent` is refused as an unknown flag.
func TestSearchRerunCommandKeepsDashQueryPositional(t *testing.T) {
	database := dashDB(t)
	if err := runWith(t, database, "search", "--", "-urgent"); err != nil {
		t.Fatalf("search: %v", err)
	}
	cli := parseRecorded(t)
	if cli.Search.Query != "-urgent" {
		t.Errorf("re-run searches for %q, want %q", cli.Search.Query, "-urgent")
	}
}

// A filter value that starts with a dash has to be joined to its flag with
// "=": `--tag -urgent` is refused as a flag with no value.
func TestListRerunCommandJoinsDashFilterValue(t *testing.T) {
	database := dashDB(t)
	if err := runWith(t, database, "today", "--tag=-urgent"); err != nil {
		t.Fatalf("today: %v", err)
	}
	cli := parseRecorded(t)
	if cli.List.Tag != "-urgent" {
		t.Errorf("re-run filters by tag %q, want %q", cli.List.Tag, "-urgent")
	}
}
