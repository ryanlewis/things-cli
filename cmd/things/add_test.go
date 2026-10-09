package main

import (
	"database/sql"
	"encoding/json"
	"io"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/ryanlewis/things-cli/internal/config"
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
		{"trashed", []createdRow{{uuid: "new-1", title: "Buy oat milk", extra: `trashed = 1`}}},
		// Titles match exactly, bar surrounding whitespace.
		{"otherCase", []createdRow{{uuid: "new-1", title: "buy oat milk"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fastVerify(t)
			sqlDB := dbtest.NewSQL(t)
			if _, err := sqlDB.Exec(`ALTER TABLE TMTask ADD COLUMN rt1_repeatingTemplate TEXT`); err != nil {
				t.Fatalf("add template column: %v", err)
			}
			stubExecAdding(t, sqlDB, tc.rows...)

			out, err := runOut(t, db.NewFromSQL(sqlDB), "add", "Buy oat milk")
			if err == nil || !strings.Contains(err.Error(), "add not confirmed") || !strings.Contains(err.Error(), "things search") {
				t.Fatalf("err = %v, want add not confirmed with a search hint", err)
			}
			if out != "" {
				t.Errorf("stdout = %q, want nothing beside the error", out)
			}
		})
	}
}

// The search hint quotes the title for the shell, so a title with $ in it can
// be pasted as it stands.
func TestAddNotFoundQuotesSearchHint(t *testing.T) {
	fastVerify(t)
	database, _ := seedWritable(t)
	stubExecDropping(t)

	_, err := runOut(t, database, "add", " Pay $5 fee ")
	if err == nil || !strings.Contains(err.Error(), "things search 'Pay $5 fee'") {
		t.Fatalf("err = %v, want a shell-quoted search hint", err)
	}
}

// Surrounding whitespace on either side does not stop a match.
func TestAddMatchesTrimmedTitle(t *testing.T) {
	fastVerify(t)
	database, sqlDB := seedWritable(t)
	stubExecAdding(t, sqlDB, createdRow{uuid: "new-1", title: "Buy oat milk "})

	out, err := runOut(t, database, "add", "  Buy oat milk")
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if !strings.Contains(out, "new-1") {
		t.Errorf("output = %q, want the new item", out)
	}
}

// A title sent with decomposed accents is confirmed by a row stored composed,
// and the other way round, but a title differing in case is not.
func TestAddMatchesTitleInEitherNormalisationForm(t *testing.T) {
	const composed, decomposed = "Caf\u00e9 run", "Cafe\u0301 run"
	for name, c := range map[string]struct{ sent, stored string }{
		"sent decomposed, stored composed": {decomposed, composed},
		"sent composed, stored decomposed": {composed, decomposed},
	} {
		t.Run(name, func(t *testing.T) {
			fastVerify(t)
			database, sqlDB := seedWritable(t)
			stubExecAdding(t, sqlDB, createdRow{uuid: "new-1", title: c.stored})

			out, err := runOut(t, database, "add", c.sent)
			if err != nil {
				t.Fatalf("add: %v", err)
			}
			if !strings.Contains(out, "new-1") {
				t.Errorf("output = %q, want the new item", out)
			}
		})
	}
}

func TestNewCreatedKeyKeepsCase(t *testing.T) {
	if newCreatedKey(model.TypeTask, "Cafe\u0301") != newCreatedKey(model.TypeTask, "Caf\u00e9 ") {
		t.Error("NFD and NFC titles are different keys")
	}
	if newCreatedKey(model.TypeTask, "Buy milk") == newCreatedKey(model.TypeTask, "buy milk") {
		t.Error("titles differing in case are the same key")
	}
}

// storedTitle turns each line feed into a space, as Things does, and leaves
// every other character alone, as Things does.
func TestStoredTitle(t *testing.T) {
	for _, c := range []struct{ name, sent, want string }{
		{"newline", "line one\nline two", "line one line two"},
		{"two newlines", "a\n\nb", "a  b"},
		{"CRLF keeps the CR", "a\r\nb", "a\r b"},
		{"lone CR", "a\rb", "a\rb"},
		{"tab", "a\tb", "a\tb"},
		{"spaces", "  a  b  ", "  a  b  "},
		{"line separator", "a b", "a b"},
	} {
		t.Run(c.name, func(t *testing.T) {
			if got := storedTitle(c.sent); got != c.want {
				t.Errorf("storedTitle(%q) = %q, want %q", c.sent, got, c.want)
			}
		})
	}
}

// A to-do added with a newline in its title is confirmed by the row Things
// saves with a space in its place.
func TestAddMatchesNewlineStoredAsSpace(t *testing.T) {
	for name, c := range map[string]struct{ sent, stored string }{
		"newline":      {"line one\nline two", "line one line two"},
		"CRLF":         {"line one\r\nline two", "line one\r line two"},
		"trailing":     {"line one\n", "line one "},
		"two newlines": {"line one\n\nline two", "line one  line two"},
	} {
		t.Run(name, func(t *testing.T) {
			fastVerify(t)
			database, sqlDB := seedWritable(t)
			stubExecAdding(t, sqlDB, createdRow{uuid: "new-1", title: c.stored})

			out, err := runOut(t, database, "add", c.sent)
			if err != nil {
				t.Fatalf("add: %v", err)
			}
			if !strings.Contains(out, "new-1") {
				t.Errorf("output = %q, want the new item", out)
			}
		})
	}
}

// The search hint looks for the title as Things stores it, so a newline in the
// title is a space in the hint and the hint pastes and runs as one line.
func TestAddNotFoundSearchHintUsesStoredTitle(t *testing.T) {
	for name, c := range map[string]struct{ sent, want string }{
		"newline": {"line one\nline two", "things search 'line one line two'"},
		"dash":    {"-1 day\nlater", "things search -- '-1 day later'"},
	} {
		t.Run(name, func(t *testing.T) {
			fastVerify(t)
			database, _ := seedWritable(t)
			stubExecDropping(t)

			_, err := runOut(t, database, "add", "--", c.sent)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want a hint containing %s", err, c.want)
			}
			if hint := err.Error()[strings.Index(err.Error(), "Run `"):]; strings.ContainsFunc(hint, unicode.IsControl) {
				t.Errorf("hint %q holds a control character", hint)
			}
		})
	}
}

// project add --todos creates to-dos too; one titled like the project is not
// a second candidate, since only projects are looked for.
func TestProjectAddIgnoresSameTitledTodo(t *testing.T) {
	fastVerify(t)
	database, sqlDB := seedWritable(t)
	stubExecAdding(t, sqlDB,
		createdRow{uuid: "new-p", title: "Launch", typ: model.TypeProject},
		createdRow{uuid: "new-t", title: "Launch", typ: model.TypeTask, extra: `project = 'new-p'`})

	out, err := runOut(t, database, "--json", "project", "add", "Launch", "--todos", "Launch")
	if err != nil {
		t.Fatalf("project add: %v", err)
	}
	var got model.Task
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("unmarshal %q: %v", out, err)
	}
	if got.UUID != "new-p" {
		t.Errorf("printed %s, want the project new-p", got.UUID)
	}
}

// The read-back polls on after the first match, so a second item with the
// title saved one round later is seen and the add is ambiguous, not confirmed
// against whichever came first.
func TestAddSecondMatchOneRoundLaterIsAmbiguous(t *testing.T) {
	fastVerify(t)
	database, sqlDB := seedWritable(t)
	stubExecAdding(t, sqlDB, createdRow{uuid: "new-1", title: "Buy oat milk"})
	sleeps := 0
	verifySleep = func(time.Duration) {
		sleeps++
		if sleeps == 1 {
			now := model.TimeToUnix(time.Now())
			if _, err := sqlDB.Exec(`INSERT INTO TMTask (uuid, title, type, status, trashed, start, creationDate) VALUES ('new-2', 'Buy oat milk', 0, 0, 0, 0, ?)`, now); err != nil {
				t.Errorf("simulating a second add: %v", err)
			}
		}
	}

	out, err := runOut(t, database, "add", "Buy oat milk")
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if !strings.Contains(out, "not confirmed") || !strings.Contains(out, "new-1, new-2") {
		t.Errorf("output = %q, want an ambiguous line naming both", out)
	}
}

// A database that stops answering during the read-back leaves the add sent
// but unconfirmed: a warning and exit 0, not an error with no guidance.
func TestAddReadFailingDuringReadBackIsUnconfirmed(t *testing.T) {
	fastVerify(t)
	database, sqlDB := seedWritable(t)
	prev := things.SetExecCommandForTest(func(string, ...string) *exec.Cmd {
		if err := sqlDB.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
		return exec.Command("true")
	})
	t.Cleanup(func() { things.SetExecCommandForTest(prev) })

	out, stderr, err := runStreams(t, database, "--json", "add", "Buy oat milk")
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	var got unconfirmedAdd
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("unmarshal %q: %v", out, err)
	}
	if got.Confirmed || got.Reason != "unreadable" {
		t.Errorf("got %+v, want unconfirmed with reason unreadable", got)
	}
	if !strings.Contains(stderr, "warning:") {
		t.Errorf("stderr = %q, want a warning", stderr)
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

// A read that fails late in a read-back that had been reading fine does not
// make the add unreadable: the database answered and the item was not there,
// so the add is not confirmed, with the search hint.
func TestAddLateReadFailureIsNotConfirmed(t *testing.T) {
	fastVerify(t)
	database, sqlDB := seedWritable(t)
	stubExecDropping(t)
	verifySleep = func(time.Duration) { _ = sqlDB.Close() }

	_, err := runOut(t, database, "--json", "add", "Buy oat milk")
	if err == nil || !strings.Contains(err.Error(), "add not confirmed") {
		t.Fatalf("err = %v, want add not confirmed", err)
	}
}

// The search hint carries --db and --config when they were given, so it reads
// the same database the add was checked against.
func TestAddNotFoundSearchHintKeepsGlobalFlags(t *testing.T) {
	fastVerify(t)
	database, _ := seedWritable(t)
	stubExecDropping(t)
	d := &Deps{
		DB:     database,
		DBPath: "/tmp/my things.sqlite",
		Config: &config.File{Path: "/tmp/c.toml", Source: config.SourceFlag},
		Stdout: io.Discard,
		Stderr: io.Discard,
	}

	err := applyAdd(d, model.TypeTask, "Buy oat milk", createdDest{}, "", func() error { return things.AddTask(things.AddParams{Title: "Buy oat milk"}) })
	want := "things --db '/tmp/my things.sqlite' --config /tmp/c.toml search 'Buy oat milk'"
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("err = %v, want the hint %q", err, want)
	}
}

// An add with tags against a database that cannot be read says so once: the
// tag check reports it, and the read-back does not repeat it.
func TestAddUnreadableWithTagsWarnsOnce(t *testing.T) {
	fastVerify(t)
	closed := dbtest.NewSQL(t)
	if err := closed.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	stubExecDropping(t)

	out, stderr, err := runStreams(t, db.NewFromSQL(closed), "add", "Buy oat milk", "--tags", "Errand")
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if !strings.Contains(out, "not confirmed") {
		t.Errorf("output = %q, want the unconfirmed line", out)
	}
	if n := strings.Count(stderr, "database is closed"); n != 1 {
		t.Errorf("stderr reports the database error %d times, want once:\n%s", n, stderr)
	}
}

// Column assignments that file a stubbed add's row where Things would.
const (
	inProj1    = `project = 'proj-1'`
	inProj2    = `project = 'proj-2'`
	inArea1    = `area = 'area-1'`
	underHead1 = `heading = 'head-1'`
)

// Things files an add whose --list or --project names no open project or
// area in the Inbox, and drops a --heading it cannot find, all without a
// word. The add warns on stderr when that will happen and still sends the
// write.
func TestAddWarnsOnUnresolvedListOrHeading(t *testing.T) {
	cases := []struct {
		name  string
		args  []string
		want  string // "" for no warning
		filed string // where Things files the to-do, as a column assignment; "" for the Inbox
	}{
		{"knownProject", []string{"--list", "tools"}, "", inProj1},
		{"knownProjectFlag", []string{"--project", "Tools"}, "", inProj1},
		{"knownArea", []string{"--list", "Personal"}, "", inArea1},
		{"projectUUID", []string{"--project", "proj-1"}, "", inProj1},
		{"areaUUID", []string{"--list", "area-1"}, "", inArea1},
		{"headingInProjectUUID", []string{"--project", "proj-1", "--heading", "Setup"}, "", underHead1},
		// Measured in Things 3: list-id files into a logged project, and
		// reopens it.
		{"completedProjectUUID", []string{"--project", "proj-done"}, `"Old" is completed; Things will file into it and reopen it`, `project = 'proj-done'`},
		{"knownHeading", []string{"--list", "Tools", "--heading", "setup"}, "", underHead1},
		{"unknownList", []string{"--list", "Nowhere"}, `no project or area called "Nowhere"`, ""},
		{"unknownProjectFlag", []string{"--project", "Nowhere"}, `no project or area called "Nowhere"`, ""},
		{"completedProject", []string{"--list", "Old"}, `no project or area called "Old"`, ""},
		{"unknownHeading", []string{"--list", "Tools", "--heading", "Later"}, `"Tools" has no heading "Later"`, inProj1},
		{"headingInArea", []string{"--list", "Personal", "--heading", "Setup"}, `"Personal" has no heading "Setup"`, inArea1},
		{"headingWithoutList", []string{"--heading", "Setup"}, `--heading "Setup" needs --list or --project`, ""},
		{"paddedList", []string{"--list", " Tools "}, `no project or area called " Tools "`, ""},
		{"paddedHeading", []string{"--list", "Tools", "--heading", " Setup "}, `"Tools" has no heading " Setup "`, inProj1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fastVerify(t)
			database, sqlDB := seedWritable(t)
			fx := dbtest.NewFixture(t, sqlDB)
			fx.Area("area-1", "Personal", 1)
			fx.Project("proj-1", "Tools", 5)
			fx.Project("proj-done", "Old", 6, dbtest.Status(model.StatusCompleted))
			fx.Heading("head-1", "Setup", 1, dbtest.InProject("proj-1"))
			calls := stubExecAdding(t, sqlDB, createdRow{uuid: "new-1", title: "Buy oat milk", extra: tc.filed})

			_, stderr, err := runStreams(t, database, append([]string{"add", "Buy oat milk"}, tc.args...)...)
			if err != nil {
				t.Fatalf("add: %v", err)
			}
			if *calls != 1 {
				t.Errorf("issued %d writes, want the add sent once", *calls)
			}
			if tc.want == "" {
				if stderr != "" {
					t.Errorf("stderr = %q, want no warning", stderr)
				}
				return
			}
			if !strings.Contains(stderr, "warning: ") && !strings.Contains(stderr, "note: ") || !strings.Contains(stderr, tc.want) {
				t.Errorf("stderr = %q, want a warning or note containing %q", stderr, tc.want)
			}
		})
	}
}

// Things matches --list and --project by title only, so a uuid filed the
// to-do in the Inbox. A uuid of a project or area now goes to Things as
// list-id; a title still goes as list.
func TestAddSendsUUIDAsListID(t *testing.T) {
	cases := []struct {
		name, flag, value, want string
	}{
		{"projectUUID", "--project", "proj-1", "list-id=proj-1"},
		{"templateUUID", "--list", "repproj-1", "list-id=repproj-1"},
		{"paddedUUID", "--project", " proj-1 ", "list-id=proj-1"},
		{"areaUUID", "--list", "area-1", "list-id=area-1"},
		{"title", "--project", "Tools", "list=Tools"},
		{"unknown", "--list", "Nowhere", "list=Nowhere"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fastVerify(t)
			database, sqlDB := seedWritable(t)
			fx := dbtest.NewFixture(t, sqlDB)
			fx.Area("area-1", "Personal", 1)
			fx.Project("proj-1", "Tools", 5)
			var url string
			prev := things.SetExecCommandForTest(func(_ string, args ...string) *exec.Cmd {
				url = args[len(args)-1]
				return exec.Command("true")
			})
			t.Cleanup(func() { things.SetExecCommandForTest(prev) })

			_, _, _ = runStreams(t, database, "--no-verify", "add", "Buy oat milk", tc.flag, tc.value)
			params := url[strings.Index(url, "?")+1:]
			if !slices.Contains(strings.Split(params, "&"), tc.want) {
				t.Errorf("url = %q, want %s", url, tc.want)
			}
			if strings.HasPrefix(tc.want, "list-id=") && strings.Contains("&"+params, "&list=") {
				t.Errorf("url = %q, want no list title beside list-id", url)
			}
		})
	}
}

// Things matches project add's area by title only, so a uuid filed the new
// project under no area. A uuid of an area now goes to Things as area-id; a
// title still goes as area.
func TestProjectAddSendsUUIDAsAreaID(t *testing.T) {
	cases := []struct {
		name, value, want string
	}{
		{"uuid", "area-1", "area-id=area-1"},
		{"paddedUUID", " area-1 ", "area-id=area-1"},
		{"title", "Personal", "area=Personal"},
		{"unknown", "Nowhere", "area=Nowhere"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fastVerify(t)
			database, sqlDB := seedWritable(t)
			dbtest.NewFixture(t, sqlDB).Area("area-1", "Personal", 1)
			var url string
			prev := things.SetExecCommandForTest(func(_ string, args ...string) *exec.Cmd {
				url = args[len(args)-1]
				return exec.Command("true")
			})
			t.Cleanup(func() { things.SetExecCommandForTest(prev) })

			_, _, _ = runStreams(t, database, "--no-verify", "project", "add", "Launch", "--area", tc.value)
			params := strings.Split(url[strings.Index(url, "?")+1:], "&")
			if !slices.Contains(params, tc.want) {
				t.Errorf("url = %q, want %s", url, tc.want)
			}
			if strings.HasPrefix(tc.want, "area-id=") && slices.ContainsFunc(params, func(p string) bool { return strings.HasPrefix(p, "area=") }) {
				t.Errorf("url = %q, want no area title beside area-id", url)
			}
		})
	}
}

// Things creates a project with no area when --area names none, without a
// word. Checked against Things 3: it matches the title ignoring case but not
// surrounding space. project add warns on stderr and still sends the write.
func TestProjectAddWarnsOnUnknownArea(t *testing.T) {
	cases := []struct {
		name, area string
		want       string // "" for no warning
		filed      string // the area Things files the project in, as a column assignment
	}{
		{"title", "Personal", "", inArea1},
		{"otherCase", "personal", "", inArea1},
		{"uuid", "area-1", "", inArea1},
		{"paddedUUID", " area-1 ", "", inArea1},
		{"unknown", "Nowhere", `no area called "Nowhere"`, ""},
		{"padded", " Personal ", `no area called " Personal "`, ""},
		{"paddedTitle", "Errands ", "", `area = 'area-2'`},
		{"paddedTitleUnpadded", "Errands", `no area called "Errands"`, ""},
		{"fullwidth", "Ｐersonal", "", inArea1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fastVerify(t)
			database, sqlDB := seedWritable(t)
			fx := dbtest.NewFixture(t, sqlDB)
			fx.Area("area-1", "Personal", 1)
			fx.Area("area-2", "Errands ", 2)
			calls := stubExecAdding(t, sqlDB, createdRow{uuid: "new-p", title: "Launch", typ: model.TypeProject, extra: tc.filed})

			_, stderr, err := runStreams(t, database, "project", "add", "Launch", "--area", tc.area)
			if err != nil {
				t.Fatalf("project add: %v", err)
			}
			if *calls != 1 {
				t.Errorf("issued %d writes, want the add sent once", *calls)
			}
			if tc.want == "" {
				if stderr != "" {
					t.Errorf("stderr = %q, want no warning", stderr)
				}
				return
			}
			if !strings.Contains(stderr, "warning: ") || !strings.Contains(stderr, tc.want) {
				t.Errorf("stderr = %q, want a warning containing %q", stderr, tc.want)
			}
		})
	}
}

// Two areas share a title: Things files the project in the one whose uuid
// sorts first, whichever title --area matches exactly, so only a project
// filed there confirms the add.
func TestProjectAddSharedAreaTitleChecksThingsPick(t *testing.T) {
	for _, tc := range []struct {
		filed string
		ok    bool
	}{{inArea1, true}, {`area = 'area-3'`, false}} {
		t.Run(tc.filed, func(t *testing.T) {
			fastVerify(t)
			database, sqlDB := seedWritable(t)
			fx := dbtest.NewFixture(t, sqlDB)
			fx.Area("area-1", "PERSONAL", 1)
			fx.Area("area-3", "Personal", 3)
			stubExecAdding(t, sqlDB, createdRow{uuid: "new-p", title: "Launch", typ: model.TypeProject, extra: tc.filed})

			_, _, err := runStreams(t, database, "project", "add", "Launch", "--area", "Personal")
			if tc.ok != (err == nil) {
				t.Errorf("project add filed %s: err = %v, want ok %v", tc.filed, err, tc.ok)
			}
		})
	}
}

// Two commands that add the same title to different places can both
// snapshot before either saves, so each sees the other's row among the new
// ones. A row filed somewhere other than where this add sent its item is not
// this add's: when its own save is dropped, or lands after the read-back
// settles, the add is not confirmed rather than handed the other row's uuid.
// A row filed where the add asked still confirms.
func TestAddIgnoresSameTitleFiledElsewhere(t *testing.T) {
	cases := []struct {
		name string
		args []string
		rows []createdRow // what lands during the read-back
		want string       // the uuid confirmed; "" for not confirmed
	}{
		{"otherProjectOwnDropped", []string{"add", "Buy oat milk", "--project", "Garden"},
			[]createdRow{{uuid: "other", title: "Buy oat milk", extra: inProj1}}, ""},
		{"otherProjectOwnLands", []string{"add", "Buy oat milk", "--project", "Garden"},
			[]createdRow{{uuid: "other", title: "Buy oat milk", extra: inProj1}, {uuid: "mine", title: "Buy oat milk", extra: inProj2}}, "mine"},
		{"projectUUID", []string{"add", "Buy oat milk", "--list", "proj-2"},
			[]createdRow{{uuid: "other", title: "Buy oat milk", extra: inProj1}, {uuid: "mine", title: "Buy oat milk", extra: inProj2}}, "mine"},
		{"inboxOwnDropped", []string{"add", "Buy oat milk"},
			[]createdRow{{uuid: "other", title: "Buy oat milk", extra: inProj1}}, ""},
		{"inboxOwnLands", []string{"add", "Buy oat milk"},
			[]createdRow{{uuid: "other", title: "Buy oat milk", extra: inProj1}, {uuid: "mine", title: "Buy oat milk"}}, "mine"},
		{"projectOwnDroppedOtherInbox", []string{"add", "Buy oat milk", "--project", "Tools"},
			[]createdRow{{uuid: "other", title: "Buy oat milk"}}, ""},
		{"otherHeading", []string{"add", "Buy oat milk", "--project", "Tools", "--heading", "Setup"},
			[]createdRow{{uuid: "other", title: "Buy oat milk", extra: inProj1}}, ""},
		// Of heading twins, Things takes the lowest uuid: a row under the
		// other one is not this add's, whatever case the add asked for.
		{"headingTwin", []string{"add", "Buy oat milk", "--project", "Tools", "--heading", "SETUP"},
			[]createdRow{{uuid: "other", title: "Buy oat milk", extra: `project = 'proj-1', heading = 'head-2'`}}, ""},
		{"headingTwinLowest", []string{"add", "Buy oat milk", "--project", "Tools", "--heading", "SETUP"},
			[]createdRow{{uuid: "other", title: "Buy oat milk", extra: `project = 'proj-1', heading = 'head-2'`}, {uuid: "mine", title: "Buy oat milk", extra: `project = 'proj-1', heading = 'head-1'`}}, "mine"},
		{"area", []string{"add", "Buy oat milk", "--list", "Personal"},
			[]createdRow{{uuid: "other", title: "Buy oat milk", extra: inProj1}, {uuid: "mine", title: "Buy oat milk", extra: inArea1}}, "mine"},
		{"projectAddOtherArea", []string{"project", "add", "Launch", "--area", "Errands"},
			[]createdRow{{uuid: "other", title: "Launch", typ: model.TypeProject, extra: inArea1}}, ""},
		{"projectAddNoAreaOtherArea", []string{"project", "add", "Launch"},
			[]createdRow{{uuid: "other", title: "Launch", typ: model.TypeProject, extra: inArea1}}, ""},
		{"projectAddArea", []string{"project", "add", "Launch", "--area", "area-2"},
			[]createdRow{{uuid: "other", title: "Launch", typ: model.TypeProject, extra: inArea1}, {uuid: "mine", title: "Launch", typ: model.TypeProject, extra: `area = 'area-2'`}}, "mine"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fastVerify(t)
			database, sqlDB := seedWritable(t)
			fx := dbtest.NewFixture(t, sqlDB)
			fx.Area("area-1", "Personal", 1)
			fx.Area("area-2", "Errands", 2)
			fx.Project("proj-1", "Tools", 5)
			fx.Project("proj-2", "Garden", 6)
			fx.Heading("head-1", "Setup", 1, dbtest.InProject("proj-1"))
			fx.Heading("head-2", "SETUP", 2, dbtest.InProject("proj-1"))
			// The other command's row lands after this add's snapshot,
			// while it reads back, as an overlapping add's would.
			stubExecAdding(t, sqlDB, tc.rows...)

			out, err := runOut(t, database, append([]string{"--json"}, tc.args...)...)
			if tc.want == "" {
				if err == nil || !strings.Contains(err.Error(), "add not confirmed") || !strings.Contains(err.Error(), "where it was sent") {
					t.Fatalf("err = %v, out = %q; want add not confirmed", err, out)
				}
				if strings.Contains(out, "other") {
					t.Errorf("out = %q, want the other command's row not reported as this add's", out)
				}
				return
			}
			if err != nil {
				t.Fatalf("%v: %v", tc.args, err)
			}
			var got model.Task
			if err := json.Unmarshal([]byte(out), &got); err != nil {
				t.Fatalf("unmarshal %q: %v", out, err)
			}
			if got.UUID != tc.want {
				t.Errorf("printed %s, want %s", got.UUID, tc.want)
			}
		})
	}
}

// A list title more than one open project, or more than one area, carries:
// Things files the to-do in the one whose uuid sorts first, so an item that
// lands there is confirmed, and one filed in another list of that title is
// not.
func TestAddSharedListTitleChecksThingsPick(t *testing.T) {
	cases := []struct {
		name string
		args []string
		row  createdRow
		ok   bool
	}{
		{"firstArea", []string{"add", "Buy oat milk", "--list", "Personal"},
			createdRow{uuid: "mine", title: "Buy oat milk", extra: `area = 'area-1'`}, true},
		{"secondArea", []string{"add", "Buy oat milk", "--list", "personal"},
			createdRow{uuid: "mine", title: "Buy oat milk", extra: `area = 'area-3'`}, false},
		{"firstProject", []string{"add", "Buy oat milk", "--project", "Tools", "--heading", "Setup"},
			createdRow{uuid: "mine", title: "Buy oat milk", extra: `project = 'proj-1', heading = 'head-1'`}, true},
		{"firstProjectNoHeading", []string{"add", "Buy oat milk", "--project", "Tools", "--heading", "Setup"},
			createdRow{uuid: "mine", title: "Buy oat milk", extra: `project = 'proj-1'`}, false},
		{"secondProject", []string{"add", "Buy oat milk", "--project", "TOOLS"},
			createdRow{uuid: "mine", title: "Buy oat milk", extra: `project = 'proj-3'`}, false},
		{"elsewhere", []string{"add", "Buy oat milk", "--list", "Personal"},
			createdRow{uuid: "mine", title: "Buy oat milk", extra: `area = 'area-2'`}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fastVerify(t)
			database, sqlDB := seedWritable(t)
			fx := dbtest.NewFixture(t, sqlDB)
			fx.Area("area-1", "Personal", 1)
			fx.Area("area-2", "Errands", 2)
			fx.Area("area-3", "Personal", 3)
			fx.Project("proj-1", "Tools", 5)
			fx.Project("proj-3", "Tools", 7)
			fx.Heading("head-1", "Setup", 1, dbtest.InProject("proj-1"))
			stubExecAdding(t, sqlDB, tc.row)

			out, err := runOut(t, database, append([]string{"--json"}, tc.args...)...)
			if !tc.ok {
				if err == nil || !strings.Contains(err.Error(), "add not confirmed") {
					t.Fatalf("err = %v, out = %q; want add not confirmed", err, out)
				}
				return
			}
			if err != nil {
				t.Fatalf("%v: %v", tc.args, err)
			}
			var got model.Task
			if err := json.Unmarshal([]byte(out), &got); err != nil {
				t.Fatalf("unmarshal %q: %v", out, err)
			}
			if got.UUID != tc.row.uuid {
				t.Errorf("printed %s, want %s", got.UUID, tc.row.uuid)
			}
		})
	}
}

// Things files into a list the user may not expect when several share the
// title they gave, or when the project is closed: each write that names a
// list or area says so on stderr, and still goes ahead.
func TestListTargetNotes(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"addShared", []string{"add", "Buy oat milk", "--list", "personal"},
			`note: several lists are called "personal"; Things will use "Personal" (area-1); pass a UUID to choose`},
		{"addClosed", []string{"add", "Buy oat milk", "--project", "Shelved"},
			`note: "Shelved" is completed; Things will file into it and reopen it`},
		{"addTrashedUUID", []string{"add", "Buy oat milk", "--project", "proj-bin"},
			`note: "Binned" is in the Trash; Things will file into it there`},
		{"editShared", []string{"edit", "one-1", "--list", "PERSONAL"},
			`note: several lists are called "PERSONAL"; Things will use "Personal" (area-1)`},
		{"editClosed", []string{"edit", "one-1", "--list", "shelved"},
			`note: "Shelved" is completed; Things will file into it and reopen it`},
		{"projectAddShared", []string{"project", "add", "Launch", "--area", "personal"},
			`note: several areas are called "personal"; Things will use "Personal" (area-1)`},
		{"projectEditShared", []string{"project", "edit", "repproj-1", "--area", "personal"},
			`note: several areas are called "personal"; Things will use "Personal" (area-1)`},
		{"addProjectAndArea", []string{"add", "Buy oat milk", "--list", "ERRANDS"},
			`note: several lists are called "ERRANDS" (a project and an area); Things will use the project "Errands" (proj-errands); pass a UUID to choose`},
		// A move to where the item already is files it nowhere new, so it
		// gets no note.
		{"editSharedInPlace", []string{"edit", "in-area-1", "--list", "personal"}, ""},
		{"projectEditSharedInPlace", []string{"project", "edit", "proj-in-area-1", "--area", "personal"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			database, sqlDB := seedWritable(t)
			fx := dbtest.NewFixture(t, sqlDB)
			fx.Area("area-1", "Personal", 1)
			fx.Area("area-3", "personal", 3)
			fx.Project("proj-done", "Shelved", 5, dbtest.Completed(model.TimeToUnix(testNow)))
			fx.Project("proj-bin", "Binned", 6, dbtest.Trashed())
			fx.Project("proj-errands", "Errands", 7)
			fx.Area("0-errands", "errands", 4)
			fx.Todo("in-area-1", "Sweep", 8, dbtest.Anytime(), dbtest.InArea("area-1"))
			fx.Project("proj-in-area-1", "Garden", 9, dbtest.InArea("area-1"))
			stubExecDropping(t)

			_, stderr, _ := runStreams(t, database, append([]string{"--no-verify"}, tc.args...)...)
			if tc.want == "" {
				if strings.Contains(stderr, "note: ") {
					t.Errorf("stderr = %q, want no note", stderr)
				}
				return
			}
			if !strings.Contains(stderr, tc.want) {
				t.Errorf("stderr = %q, want %q", stderr, tc.want)
			}
		})
	}
}

// A to-do already in a closed project is not moved there again, so nothing
// reopens and there is no note.
func TestEditNoClosedNoteInPlace(t *testing.T) {
	database, sqlDB := seedWritable(t)
	fx := dbtest.NewFixture(t, sqlDB)
	fx.Project("proj-done", "Shelved", 5, dbtest.Completed(model.TimeToUnix(testNow)))
	fx.Todo("in-done", "Sweep", 6, dbtest.Anytime(), dbtest.InProject("proj-done"))
	stubExecDropping(t)

	_, stderr, _ := runStreams(t, database, "--no-verify", "edit", "in-done", "--list", "Shelved")
	if strings.Contains(stderr, "note: ") {
		t.Errorf("stderr = %q, want no note", stderr)
	}
}

// A completed project reached by uuid gets the "is completed" note whichever
// flag carries the uuid, --list-id alone or beside a --list title, which
// Things passes over for the list-id.
func TestEditClosedNoteOnListID(t *testing.T) {
	cases := []struct {
		name string
		args []string
		note bool
	}{
		{"listUUID", []string{"edit", "one-1", "--list", "proj-done"}, true},
		{"listID", []string{"edit", "one-1", "--list-id", "proj-done"}, true},
		{"listAndListID", []string{"edit", "one-1", "--list", "Elsewhere", "--list-id", "proj-done"}, true},
		{"listAndListIDInPlace", []string{"edit", "in-done", "--list", "Elsewhere", "--list-id", "proj-done"}, false},
		{"listAndListIDWithHeadingID", []string{"edit", "one-1", "--list", "Elsewhere", "--list-id", "proj-done", "--heading-id", "head-1"}, false},
		{"listAndOpenListID", []string{"edit", "one-1", "--list", "Elsewhere", "--list-id", "proj-1"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			database, sqlDB := seedWritable(t)
			fx := dbtest.NewFixture(t, sqlDB)
			fx.Project("proj-done", "Shelved", 5, dbtest.Completed(model.TimeToUnix(testNow)))
			fx.Todo("in-done", "Sweep", 6, dbtest.Anytime(), dbtest.InProject("proj-done"))
			fx.Project("proj-1", "Tools", 7)
			fx.Heading("head-1", "Setup", 8, dbtest.InProject("proj-1"))
			stubExecDropping(t)

			_, stderr, _ := runStreams(t, database, append([]string{"--no-verify"}, tc.args...)...)
			const want = `note: "Shelved" is completed; Things will file into it and reopen it`
			if got := strings.Contains(stderr, want); got != tc.note {
				t.Errorf("stderr = %q, want the note %v", stderr, tc.note)
			}
		})
	}
}

// When the areas cannot be read, project edit --area cannot rule a move out,
// so it waits for the change rather than calling the edit a no-op.
func TestProjectEditAreaUnreadable(t *testing.T) {
	database, sqlDB := seedWritable(t)
	fx := dbtest.NewFixture(t, sqlDB)
	fx.Area("area-1", "Personal", 1)
	fx.Project("in-area", "Garden", 5, dbtest.InArea("area-1"))
	task, err := database.GetTaskByUUID("in-area")
	if err != nil || task == nil {
		t.Fatalf("GetTaskByUUID: %v", err)
	}
	if _, err := sqlDB.Exec(`DROP TABLE TMArea`); err != nil {
		t.Fatal(err)
	}
	var stderr strings.Builder
	d := &Deps{DB: database, Stderr: &stderr}
	area := "Personal"
	c := &ProjectEditCmd{Area: &area}
	if !c.checkOwn(d, database, task) {
		t.Error("checkOwn = false, want true: the move cannot be ruled out")
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want no warning", stderr.String())
	}
}

// A blank title is refused before anything is sent, with the token import
// gives the same title, on add and edit alike. An edit without --title is
// not affected.
func TestBlankTitleRefused(t *testing.T) {
	cases := []struct {
		name string
		args []string
		kind string
	}{
		{"addEmpty", []string{"add", ""}, "task"},
		{"addSpaces", []string{"add", "   "}, "task"},
		{"addTab", []string{"add", "\t\n"}, "task"},
		{"projectAdd", []string{"project", "add", " "}, "project"},
		{"editEmpty", []string{"edit", "one-1", "--title", ""}, "task"},
		{"editSpaces", []string{"edit", "one-1", "--title", "  "}, "task"},
		{"projectEdit", []string{"project", "edit", "repproj-1", "--title", ""}, "project"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fastVerify(t)
			database, _ := seedWritable(t)
			calls := stubExecDropping(t)

			_, _, err := runStreams(t, database, append([]string{"--json"}, tc.args...)...)
			if err == nil || !strings.Contains(err.Error(), "title is blank") {
				t.Fatalf("%v = %v, want the blank title refused", tc.args, err)
			}
			if *calls != 0 {
				t.Errorf("issued %d write(s), want none", *calls)
			}
			if p := errorPayload(err); p.Error != "blank-title" || p.Kind != tc.kind {
				t.Errorf("payload = %+v, want error blank-title on a %s", p, tc.kind)
			}
		})
	}
}

func TestEditWithoutTitleKeepsTitle(t *testing.T) {
	fastVerify(t)
	database, sqlDB := seedWritable(t)
	stubExecEditing(t, sqlDB, `UPDATE TMTask SET notes = 'x' WHERE uuid = 'one-1'`)
	if _, err := runOut(t, database, "edit", "one-1", "--notes", "x"); err != nil {
		t.Fatalf("edit --notes: %v", err)
	}
}

// A --when shaped like a date or time that names none is refused before
// anything is sent, by add and edit alike, with the same error.
func TestImpossibleWhenRefusedUpFront(t *testing.T) {
	for _, args := range [][]string{
		{"add", "Post letter", "--when", "2026-13-01"},
		{"edit", "one-1", "--when", "2026-13-01"},
		{"add", "Post letter", "--when", "25:00"},
		{"edit", "one-1", "--when", "25:00"},
		{"add", "Post letter", "--when", "tomorrow@25:00"},
		{"edit", "one-1", "--when", "today@18:60"},
		// Refused before --create-tags creates the unknown tag over
		// AppleScript: no command runs at all.
		{"add", "Post letter", "--create-tags", "--tags", "Brandnew", "--when", "2026-13-01"},
		{"edit", "one-1", "--create-tags", "--tags", "Brandnew", "--when", "2026-13-01"},
		{"project", "add", "Launch", "--create-tags", "--tags", "Brandnew", "--when", "2026-13-01"},
		{"project", "edit", "repproj-1", "--create-tags", "--tags", "Brandnew", "--when", "2026-13-01"},
	} {
		fastVerify(t)
		database, _ := seedWritable(t)
		calls := stubExecDropping(t)
		_, _, err := runStreams(t, database, args...)
		if err == nil || !strings.HasPrefix(err.Error(), `invalid --when value "`+args[len(args)-1]+`"`) {
			t.Errorf("%v = %v, want the value refused", args, err)
		}
		if *calls != 0 {
			t.Errorf("%v issued %d writes, want none", args, *calls)
		}
	}
}

// A free phrase Things ignores leaves the new item with no start date. The
// add exits 0 and prints the item, since it exists and was found, and warns
// on stderr in both output modes that the --when did not take. A phrase
// Things understood gives the item a date, and nothing is said.
func TestAddWhenPhraseIgnored(t *testing.T) {
	dated := "start = 1, startDate = " + strconv.Itoa(int(model.ThingsDateFromTime(time.Now().AddDate(0, 0, 3))))
	cases := []struct {
		name  string
		extra string
		warn  bool
	}{
		{"ignored", "", true},
		{"understood", dated, false},
	}
	for _, tc := range cases {
		for _, asJSON := range []bool{false, true} {
			t.Run(tc.name+map[bool]string{true: "JSON"}[asJSON], func(t *testing.T) {
				fastVerify(t)
				database, sqlDB := seedWritable(t)
				stubExecAdding(t, sqlDB, createdRow{uuid: "new-1", title: "Buy oat milk", typ: model.TypeTask, extra: tc.extra})
				args, show := []string{"add", "Buy oat milk", "--when", "blorp"}, []string{"show", "new-1"}
				if asJSON {
					args, show = append([]string{"--json"}, args...), append([]string{"--json"}, show...)
				}

				stdout, stderr, err := runStreams(t, database, args...)
				if err != nil {
					t.Fatalf("add: %v", err)
				}
				const warning = `warning: Things did not understand --when "blorp"; the to-do was created in inbox with no start date`
				if got := strings.Contains(stderr, warning); got != tc.warn {
					t.Errorf("stderr = %q, want the warning %v", stderr, tc.warn)
				}
				if want, _ := runOut(t, database, show...); stdout != want {
					t.Errorf("stdout = %q, want the show output %q", stdout, want)
				}
			})
		}
	}
}

// An edit whose only change is a --when phrase is not read back: Things
// records nothing for a phrase it ignores, so the wait could only run out.
// It returns at once, unconfirmed with reason "when". With another change
// the edit waits as before, and when nothing is modified the error names
// the phrase as the likely cause.
func TestEditWhenPhrase(t *testing.T) {
	fastVerify(t)
	database, _ := seedWritable(t)
	calls := stubExecDropping(t)
	stdout, stderr, err := runStreams(t, database, "--json", "edit", "one-1", "--when", "blorp")
	if err != nil {
		t.Fatalf("edit --when blorp: %v", err)
	}
	if *calls != 1 {
		t.Errorf("issued %d writes, want 1", *calls)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		t.Fatalf("decode %s: %v", stdout, err)
	}
	if out["confirmed"] != false || out["reason"] != "when" || out["uuid"] != "one-1" {
		t.Errorf("stdout = %s, want confirmed false, reason when, uuid one-1", stdout)
	}
	if !strings.Contains(stderr, `Things may not understand --when "blorp"`) {
		t.Errorf("stderr = %q, want a warning naming the phrase", stderr)
	}

	// A weekday name is a word Things knows, not a free phrase: the edit
	// waits for the modification date as before.
	_, stderr, err = runStreams(t, database, "--json", "edit", "one-1", "--when", "friday")
	if err == nil || !strings.Contains(err.Error(), "was not modified") || strings.Contains(stderr, "may not understand") {
		t.Errorf("edit --when friday = %v, stderr %q; want it read back like any edit", err, stderr)
	}

	_, _, err = runStreams(t, database, "edit", "one-1", "--when", "blorp", "--notes", "x")
	if err == nil || !strings.Contains(err.Error(), `Things most likely did not understand --when "blorp"`) {
		t.Fatalf("edit with notes = %v, want an error naming the phrase", err)
	}
}
