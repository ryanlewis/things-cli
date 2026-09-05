package main

import (
	"database/sql"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ryanlewis/things-cli/internal/db"
	"github.com/ryanlewis/things-cli/internal/model"
	"github.com/ryanlewis/things-cli/internal/things"
)

// importPayload writes payload to a temp file and returns the path, so a test
// can drive `import --file` without touching stdin.
func importPayload(t *testing.T, payload string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "payload.json")
	if err := os.WriteFile(path, []byte(payload), 0o644); err != nil {
		t.Fatalf("write payload: %v", err)
	}
	return path
}

// runImport runs `import --file` against database and returns what the command
// wrote to stderr alongside the error.
func runImport(t *testing.T, database *db.DB, payload string, extra ...string) (string, error) {
	t.Helper()
	args := append([]string{"import", "--file", importPayload(t, payload)}, extra...)
	return runCapturingStderr(t, database, args...)
}

func TestImportUpdatesWalksNestedItems(t *testing.T) {
	payload := `[
	  {"type":"to-do","attributes":{"title":"created"}},
	  {"type":"to-do","operation":"update","id":"a","attributes":{"when":"today"}},
	  {"type":"project","operation":"update","id":"b","attributes":{"items":[
	     {"type":"to-do","operation":"update","id":"c","attributes":{"completed":true}}
	  ]}}
	]`
	got := importUpdates([]byte(payload))
	want := []struct{ path, id string }{
		{"[1]", "a"},
		{"[2]", "b"},
		{"[2].attributes.items[0]", "c"},
	}
	if len(got) != len(want) {
		t.Fatalf("found %d updates, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i].path != w.path || got[i].id != w.id {
			t.Errorf("update %d = {%s %s}, want {%s %s}", i, got[i].path, got[i].id, w.path, w.id)
		}
	}
}

// The walk descends through maps, whose iteration order Go randomises. Repeat
// it to catch an ordering that only shows up sometimes.
func TestImportUpdatesOrderIsStable(t *testing.T) {
	payload := `[{"type":"project","operation":"update","id":"p","attributes":{
	    "title":"P","when":"today","items":[
	      {"type":"to-do","operation":"update","id":"x","attributes":{"completed":true}},
	      {"type":"to-do","operation":"update","id":"y","attributes":{"canceled":true}}
	    ]}}]`
	var first []string
	for i := 0; i < 25; i++ {
		var paths []string
		for _, u := range importUpdates([]byte(payload)) {
			paths = append(paths, u.path)
		}
		if i == 0 {
			first = paths
			continue
		}
		if strings.Join(paths, ",") != strings.Join(first, ",") {
			t.Fatalf("run %d gave %v, first run gave %v", i, paths, first)
		}
	}
}

func TestImportUpdatesIgnoresUnparseablePayload(t *testing.T) {
	if got := importUpdates([]byte(`[{"type":}]`)); got != nil {
		t.Errorf("want no updates from invalid JSON, got %+v", got)
	}
}

func TestRestrictedImportAttrs(t *testing.T) {
	cases := []struct {
		name  string
		attrs string
		want  []string
	}{
		{"none", `{"title":"x","tags":["a"]}`, nil},
		{"all", `{"when":"today","deadline":"2026-01-01","completed":true,"canceled":true}`,
			[]string{"when", "deadline", "completed", "canceled"}},
		{"clearedDeadline", `{"deadline":""}`, []string{"deadline"}},
		{"nullWhen", `{"when":null}`, []string{"when"}},
		{"statusFalse", `{"completed":false,"canceled":false}`, nil},
		{"statusNull", `{"completed":null}`, nil},
		{"statusNonBool", `{"completed":"true"}`, []string{"completed"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var attrs map[string]any
			if err := json.Unmarshal([]byte(c.attrs), &attrs); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			got := restrictedImportAttrs(attrs)
			if strings.Join(got, ",") != strings.Join(c.want, ",") {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}

func TestWantedStatus(t *testing.T) {
	cases := []struct {
		attrs string
		want  model.Status
		ok    bool
	}{
		{`{"completed":true}`, model.StatusCompleted, true},
		{`{"canceled":true}`, model.StatusCancelled, true},
		{`{"completed":true,"canceled":true}`, model.StatusCompleted, true},
		{`{"title":"x"}`, model.StatusOpen, false},
		// An explicit false is not read back: see the note on wantedStatus.
		{`{"completed":false}`, model.StatusOpen, false},
		{`{"canceled":false}`, model.StatusOpen, false},
		// A true still wins over a false alongside it.
		{`{"completed":false,"canceled":true}`, model.StatusCancelled, true},
	}
	for _, c := range cases {
		var attrs map[string]any
		if err := json.Unmarshal([]byte(c.attrs), &attrs); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		got, ok := wantedStatus(attrs)
		if got != c.want || ok != c.ok {
			t.Errorf("wantedStatus(%s) = (%v, %v), want (%v, %v)", c.attrs, got, ok, c.want, c.ok)
		}
	}
}

func TestImportRefusesRepeatingUpdates(t *testing.T) {
	cases := []struct {
		name string
		item string
		want string
	}{
		{"when", `{"type":"to-do","operation":"update","id":"rep-1","attributes":{"when":"today"}}`, "when"},
		{"deadline", `{"type":"to-do","operation":"update","id":"rep-1","attributes":{"deadline":"2026-05-01"}}`, "deadline"},
		{"completed", `{"type":"to-do","operation":"update","id":"rep-1","attributes":{"completed":true}}`, "completed"},
		{"canceled", `{"type":"to-do","operation":"update","id":"rep-1","attributes":{"canceled":true}}`, "canceled"},
		{"project", `{"type":"project","operation":"update","id":"repproj-1","attributes":{"canceled":true}}`, "canceled"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			database, _ := seedWritable(t)
			calls := stubExecDropping(t)

			err := runWith(t, database, "import", "--file", importPayload(t, "["+c.item+"]"))
			if err == nil {
				t.Fatal("expected a refusal, got nil")
			}
			for _, want := range []string{"[0]", c.want, "repeating", repeatingDocsURL, "Nothing was sent"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q missing %q", err, want)
				}
			}
			if *calls != 0 {
				t.Errorf("payload was sent to Things anyway (%d calls)", *calls)
			}
		})
	}
}

func TestImportRefusalListsEveryOffendingItem(t *testing.T) {
	database, _ := seedWritable(t)
	calls := stubExecDropping(t)

	payload := `[
	  {"type":"to-do","operation":"update","id":"one-1","attributes":{"when":"today"}},
	  {"type":"to-do","operation":"update","id":"rep-1","attributes":{"when":"today","deadline":"2026-05-01"}},
	  {"type":"project","operation":"update","id":"repproj-1","attributes":{"canceled":true}}
	]`
	err := runWith(t, database, "import", "--file", importPayload(t, payload))
	if err == nil {
		t.Fatal("expected a refusal, got nil")
	}
	for _, want := range []string{
		"2 of 3 update items",
		`[1] (id rep-1): "Water plants" is a repeating to-do — when, deadline`,
		`[2] (id repproj-1): "Weekly review" is a repeating project — canceled`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q:\n%s", want, err)
		}
	}
	if strings.Contains(err.Error(), "one-1") {
		t.Errorf("non-repeating item was reported as an offender:\n%s", err)
	}
	if *calls != 0 {
		t.Errorf("payload was sent to Things anyway (%d calls)", *calls)
	}
}

func TestImportRefusesRepeatingItemNestedInProject(t *testing.T) {
	database, _ := seedWritable(t)
	calls := stubExecDropping(t)

	payload := `[{"type":"project","operation":"update","id":"repproj-1","attributes":{
	    "title":"Renamed",
	    "items":[{"type":"to-do","operation":"update","id":"rep-1","attributes":{"completed":true}}]}}]`
	err := runWith(t, database, "import", "--file", importPayload(t, payload))
	if err == nil {
		t.Fatal("expected a refusal, got nil")
	}
	if !strings.Contains(err.Error(), "[0].attributes.items[0] (id rep-1)") {
		t.Errorf("nested item not reported by path:\n%s", err)
	}
	if *calls != 0 {
		t.Errorf("payload was sent to Things anyway (%d calls)", *calls)
	}
}

func TestImportAllowsUnrestrictedEditsOnRepeatingItems(t *testing.T) {
	database, _ := seedWritable(t)
	calls := stubExecDropping(t)

	payload := `[{"type":"to-do","operation":"update","id":"rep-1","attributes":{"title":"New title","completed":false}}]`
	if err := runWith(t, database, "import", "--file", importPayload(t, payload)); err != nil {
		t.Fatalf("import refused an allowed edit: %v", err)
	}
	if *calls != 1 {
		t.Errorf("expected the payload to be sent, got %d calls", *calls)
	}
}

func TestImportWarnsAboutUnknownIDs(t *testing.T) {
	database, _ := seedWritable(t)
	stubExecDropping(t)

	payload := `[{"type":"to-do","operation":"update","id":"nope-1","attributes":{"title":"x"}}]`
	stderr, err := runImport(t, database, payload)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if !strings.Contains(stderr, "[0] (id nope-1) is not in the Things database") {
		t.Errorf("stderr missing unknown-id warning:\n%s", stderr)
	}
}

// A checklist item lives outside TMTask, so looking its id up would always
// come back empty and warn about an item that is perfectly valid.
func TestImportDoesNotLookUpChecklistItems(t *testing.T) {
	database, _ := seedWritable(t)
	stubExecDropping(t)

	payload := `[{"type":"checklist-item","operation":"update","id":"chk-1","attributes":{"completed":true}}]`
	stderr, err := runImport(t, database, payload)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if strings.Contains(stderr, "chk-1") {
		t.Errorf("checklist item was looked up:\n%s", stderr)
	}
}

// stubExecApplyingAll mocks the import write and, as Things would, moves each
// listed uuid to its status. Ids not listed are left untouched, which is what a
// silent drop looks like.
func stubExecApplyingAll(t *testing.T, sqlDB *sql.DB, statuses map[string]int) {
	t.Helper()
	prev := things.SetExecCommandForTest(func(string, ...string) *exec.Cmd {
		for uuid, status := range statuses {
			if _, err := sqlDB.Exec(`UPDATE TMTask SET status = ? WHERE uuid = ?`, status, uuid); err != nil {
				t.Errorf("simulating Things write: %v", err)
			}
		}
		return exec.Command("true")
	})
	t.Cleanup(func() { things.SetExecCommandForTest(prev) })
}

func TestImportReadsBackStatusChanges(t *testing.T) {
	fastVerify(t)
	database, sqlDB := seedWritable(t)
	if _, err := sqlDB.Exec(`INSERT INTO TMTask (uuid, title, type, status, trashed, start) VALUES ('one-2', 'File taxes', 0, 0, 0, 2)`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	stubExecApplyingAll(t, sqlDB, map[string]int{
		"one-1": int(model.StatusCompleted),
		"one-2": int(model.StatusCancelled),
	})

	payload := `[
	  {"type":"to-do","operation":"update","id":"one-1","attributes":{"completed":true}},
	  {"type":"to-do","operation":"update","id":"one-2","attributes":{"canceled":true}}
	]`
	stderr, err := runImport(t, database, payload)
	if err != nil {
		t.Fatalf("import: %v (stderr: %s)", err, stderr)
	}
	if stderr != "" {
		t.Errorf("unexpected stderr: %s", stderr)
	}
}

func TestImportReportsEveryDroppedStatusChange(t *testing.T) {
	fastVerify(t)
	database, sqlDB := seedWritable(t)
	if _, err := sqlDB.Exec(`INSERT INTO TMTask (uuid, title, type, status, trashed, start) VALUES ('one-2', 'File taxes', 0, 0, 0, 2)`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// Things applies the first item and drops the second — the batch shape of
	// issue #129.
	stubExecApplyingAll(t, sqlDB, map[string]int{"one-1": int(model.StatusCompleted)})

	payload := `[
	  {"type":"to-do","operation":"update","id":"one-1","attributes":{"completed":true}},
	  {"type":"to-do","operation":"update","id":"one-2","attributes":{"canceled":true}}
	]`
	stderr, err := runImport(t, database, payload)
	if err == nil {
		t.Fatal("expected a non-zero exit, got nil")
	}
	if !strings.Contains(err.Error(), "1 of 2 requested status changes did not apply") {
		t.Errorf("unexpected error: %v", err)
	}
	if !strings.Contains(stderr, `import: [1]: status change did not apply: "File taxes" (one-2) is still open`) {
		t.Errorf("stderr missing the failing item:\n%s", stderr)
	}
	if strings.Contains(stderr, "one-1") {
		t.Errorf("the item that landed was reported as a failure:\n%s", stderr)
	}
}

func TestImportNoVerifySkipsReadBack(t *testing.T) {
	fastVerify(t)
	database, sqlDB := seedWritable(t)
	stubExecApplyingAll(t, sqlDB, nil)

	payload := `[{"type":"to-do","operation":"update","id":"one-1","attributes":{"completed":true}}]`
	stderr, err := runImport(t, database, payload, "--no-verify")
	if err != nil {
		t.Fatalf("import with --no-verify: %v", err)
	}
	if stderr != "" {
		t.Errorf("unexpected stderr: %s", stderr)
	}
}

// --no-verify turns off the read-back, not the up-front refusal: the refusal
// is not a guess about what Things did, it is what the docs say it will do.
func TestImportNoVerifyStillRefusesRepeating(t *testing.T) {
	database, _ := seedWritable(t)
	calls := stubExecDropping(t)

	payload := `[{"type":"to-do","operation":"update","id":"rep-1","attributes":{"completed":true}}]`
	if err := runWith(t, database, "import", "--file", importPayload(t, payload), "--no-verify"); err == nil {
		t.Fatal("expected a refusal, got nil")
	}
	if *calls != 0 {
		t.Errorf("payload was sent to Things anyway (%d calls)", *calls)
	}
}

// A refused import never reaches Things, so the unknown-id warning — which
// tells the user Things will report the id itself — must not be printed.
func TestImportRefusalDoesNotWarnAboutUnknownIDs(t *testing.T) {
	database, _ := seedWritable(t)
	calls := stubExecDropping(t)

	payload := `[
	  {"type":"to-do","operation":"update","id":"nope-1","attributes":{"title":"x"}},
	  {"type":"to-do","operation":"update","id":"rep-1","attributes":{"completed":true}}
	]`
	stderr, err := runImport(t, database, payload)
	if err == nil {
		t.Fatal("expected a refusal, got nil")
	}
	if strings.Contains(stderr, "nope-1") {
		t.Errorf("refused import still warned that Things would report the id:\n%s", stderr)
	}
	if *calls != 0 {
		t.Errorf("payload was sent to Things anyway (%d calls)", *calls)
	}
}
