package main

import (
	"database/sql"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/ryanlewis/things-cli/internal/db"
	"github.com/ryanlewis/things-cli/internal/db/dbtest"
	"github.com/ryanlewis/things-cli/internal/model"
	"github.com/ryanlewis/things-cli/internal/things"
)

// createdRow is a TMTask row a stubbed add inserts, as Things would.
type createdRow struct {
	uuid, title string
	typ         model.TaskType
	extra       string // extra column assignment for an UPDATE, e.g. a recurrence rule
}

// stubExecAdding mocks the write command and, as Things would, inserts rows
// created now, so the read-back finds them. It returns the number of calls.
func stubExecAdding(t *testing.T, sqlDB *sql.DB, rows ...createdRow) *int {
	t.Helper()
	calls := 0
	prev := things.SetExecCommandForTest(func(string, ...string) *exec.Cmd {
		calls++
		now := model.TimeToUnix(time.Now())
		for _, r := range rows {
			if _, err := sqlDB.Exec(`INSERT INTO TMTask (uuid, title, type, status, trashed, start, creationDate, userModificationDate) VALUES (?, ?, ?, 0, 0, 0, ?, ?)`,
				r.uuid, r.title, int(r.typ), now, now); err != nil {
				t.Errorf("simulating Things add: %v", err)
			}
			if r.extra != "" {
				if _, err := sqlDB.Exec(`UPDATE TMTask SET `+r.extra+` WHERE uuid = ?`, r.uuid); err != nil {
					t.Errorf("simulating Things add: %v", err)
				}
			}
		}
		return exec.Command("true")
	})
	t.Cleanup(func() { things.SetExecCommandForTest(prev) })
	return &calls
}

// A confirmed add prints the new item exactly as `things show` does, in both
// output modes.
func TestAddPrintsCreatedItem(t *testing.T) {
	cases := []struct {
		name string
		args []string
		row  createdRow
	}{
		{"add", []string{"add", "Buy oat milk"}, createdRow{uuid: "new-1", title: "Buy oat milk", typ: model.TypeTask}},
		{"projectAdd", []string{"project", "add", "Launch"}, createdRow{uuid: "new-p", title: "Launch", typ: model.TypeProject}},
	}
	for _, tc := range cases {
		for _, asJSON := range []bool{false, true} {
			name := tc.name
			args := tc.args
			show := []string{"show", tc.row.uuid}
			if asJSON {
				name += "JSON"
				args = append([]string{"--json"}, args...)
				show = append([]string{"--json"}, show...)
			}
			t.Run(name, func(t *testing.T) {
				fastVerify(t)
				database, sqlDB := seedWritable(t)
				calls := stubExecAdding(t, sqlDB, tc.row)

				got, err := runOut(t, database, args...)
				if err != nil {
					t.Fatalf("%v: %v", args, err)
				}
				if *calls != 1 {
					t.Errorf("issued %d writes, want 1", *calls)
				}
				want, err := runOut(t, database, show...)
				if err != nil {
					t.Fatalf("show: %v", err)
				}
				if got != want {
					t.Errorf("output = %q, want the show output %q", got, want)
				}
			})
		}
	}
}

// An item with the same title that was already there before the write is not
// the one the add created, even when it was created within the window.
func TestAddSkipsSameTitleCreatedBefore(t *testing.T) {
	fastVerify(t)
	database, sqlDB := seedWritable(t)
	now := model.TimeToUnix(time.Now())
	if _, err := sqlDB.Exec(`INSERT INTO TMTask (uuid, title, type, status, trashed, start, creationDate) VALUES ('old-1', 'Buy oat milk', 0, 0, 0, 0, ?)`, now); err != nil {
		t.Fatalf("seed: %v", err)
	}
	stubExecAdding(t, sqlDB, createdRow{uuid: "new-1", title: "Buy oat milk"})

	out, err := runOut(t, database, "--json", "add", "Buy oat milk")
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	var got model.Task
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("unmarshal %q: %v", out, err)
	}
	if got.UUID != "new-1" {
		t.Errorf("printed %s, want the new item new-1", got.UUID)
	}
}

// An add Things dropped is an error that says to search before retrying, since
// a blind retry could create a duplicate. Repeating templates and the instances
// generated from them never count as the new item.
func TestAddNotFoundFails(t *testing.T) {
	cases := []struct {
		name string
		rows []createdRow
	}{
		{"dropped", nil},
		{"otherTitle", []createdRow{{uuid: "new-1", title: "Buy milk"}}},
		{"otherType", []createdRow{{uuid: "new-1", title: "Buy oat milk", typ: model.TypeProject}}},
		{"template", []createdRow{{uuid: "tpl-1", title: "Buy oat milk", extra: `rt1_recurrenceRule = x'00'`}}},
		{"instance", []createdRow{{uuid: "inst-1", title: "Buy oat milk", extra: `rt1_repeatingTemplate = 'tpl-0'`}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fastVerify(t)
			sqlDB := dbtest.NewSQL(t)
			if _, err := sqlDB.Exec(`ALTER TABLE TMTask ADD COLUMN rt1_repeatingTemplate TEXT`); err != nil {
				t.Fatalf("add template column: %v", err)
			}
			stubExecAdding(t, sqlDB, tc.rows...)

			_, err := runOut(t, db.NewFromSQL(sqlDB), "add", "Buy oat milk")
			if err == nil || !strings.Contains(err.Error(), "add not confirmed") || !strings.Contains(err.Error(), "things search") {
				t.Fatalf("err = %v, want add not confirmed with a search hint", err)
			}
		})
	}
}

// Two new items with the title cannot be told apart, so the add exits 0 as
// unconfirmed and names both rather than guessing.
func TestAddAmbiguousIsUnconfirmed(t *testing.T) {
	for _, asJSON := range []bool{false, true} {
		fastVerify(t)
		database, sqlDB := seedWritable(t)
		stubExecAdding(t, sqlDB, createdRow{uuid: "new-1", title: "Buy oat milk"}, createdRow{uuid: "new-2", title: "Buy oat milk"})

		args := []string{"add", "Buy oat milk"}
		if asJSON {
			args = append([]string{"--json"}, args...)
		}
		out, err := runOut(t, database, args...)
		if err != nil {
			t.Fatalf("%v: %v", args, err)
		}
		if !asJSON {
			if !strings.Contains(out, "not confirmed") || !strings.Contains(out, "new-1, new-2") {
				t.Errorf("output = %q, want an unconfirmed line naming both", out)
			}
			continue
		}
		var got unconfirmedAdd
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatalf("unmarshal %q: %v", out, err)
		}
		if got.Confirmed || got.Reason != "ambiguous" || strings.Join(got.Candidates, ",") != "new-1,new-2" {
			t.Errorf("got %+v, want unconfirmed, ambiguous, candidates new-1 and new-2", got)
		}
	}
}

// --no-verify sends the add and says it is unconfirmed, without reading back.
// A database that cannot be read does the same with a warning: capture must
// still work.
func TestAddUnconfirmed(t *testing.T) {
	closed := dbtest.NewSQL(t)
	if err := closed.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	cases := []struct {
		name     string
		database func(t *testing.T) *db.DB
		args     []string
		reason   string
		warning  bool
	}{
		{"noVerify", func(t *testing.T) *db.DB { d, _ := seedWritable(t); return d }, []string{"--no-verify"}, "no-verify", false},
		{"unreadable", func(*testing.T) *db.DB { return db.NewFromSQL(closed) }, nil, "unreadable", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fastVerify(t)
			calls := stubExecDropping(t)
			database := tc.database(t)

			args := append(append([]string{}, tc.args...), "add", "Buy oat milk")
			out, stderr, err := runStreams(t, database, args...)
			if err != nil {
				t.Fatalf("%v: %v", args, err)
			}
			if *calls != 1 {
				t.Errorf("issued %d writes, want the add sent once", *calls)
			}
			if !strings.Contains(out, `not confirmed`) || !strings.Contains(out, `"Buy oat milk"`) {
				t.Errorf("output = %q, want the unconfirmed line", out)
			}
			if got := strings.Contains(stderr, "warning:"); got != tc.warning {
				t.Errorf("stderr = %q, want a warning: %v", stderr, tc.warning)
			}

			out, _, err = runStreams(t, database, append([]string{"--json"}, args...)...)
			if err != nil {
				t.Fatalf("--json %v: %v", args, err)
			}
			var got map[string]any
			if err := json.Unmarshal([]byte(out), &got); err != nil {
				t.Fatalf("unmarshal %q: %v", out, err)
			}
			if got["confirmed"] != false || got["reason"] != tc.reason || got["title"] != "Buy oat milk" {
				t.Errorf("got %v, want unconfirmed with reason %q", got, tc.reason)
			}
			if _, ok := got["uuid"]; ok {
				t.Errorf("got %v, want no uuid", got)
			}
		})
	}
}
