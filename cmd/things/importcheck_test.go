package main

import (
	"bytes"
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
		// The docs say the status fields "cannot be updated on repeating
		// to-dos" — setting one to false is still updating it.
		{"statusFalse", `{"completed":false,"canceled":false}`, []string{"completed", "canceled"}},
		{"statusNull", `{"completed":null}`, []string{"completed"}},
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
		name  string
		attrs string
		want  model.Status
		ok    bool
	}{
		{"completed", `{"completed":true}`, model.StatusCompleted, true},
		{"canceled", `{"canceled":true}`, model.StatusCancelled, true},
		{"neither", `{"title":"x"}`, model.StatusOpen, false},
		// Both status fields are two-way: false asks for incomplete.
		{"completedFalse", `{"completed":false}`, model.StatusOpen, true},
		{"canceledFalse", `{"canceled":false}`, model.StatusOpen, true},
		// "canceled … Takes priority over completed", and completed is
		// "Ignored if canceled is also set to true".
		{"bothTrue", `{"completed":true,"canceled":true}`, model.StatusCancelled, true},
		{"bothFalse", `{"completed":false,"canceled":false}`, model.StatusOpen, true},
		{"canceledTrueCompletedFalse", `{"completed":false,"canceled":true}`, model.StatusCancelled, true},
		// The two doc entries disagree about this one; left unverified rather
		// than guessed.
		{"canceledFalseCompletedTrue", `{"completed":true,"canceled":false}`, model.StatusOpen, false},
		// A non-bool is a payload error for Things to report, not a request.
		{"nonBool", `{"completed":"true"}`, model.StatusOpen, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var attrs map[string]any
			if err := json.Unmarshal([]byte(c.attrs), &attrs); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			got, ok := wantedStatus(attrs)
			if got != c.want || ok != c.ok {
				t.Errorf("wantedStatus(%s) = (%v, %v), want (%v, %v)", c.attrs, got, ok, c.want, c.ok)
			}
		})
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
		// Setting a repeating item to incomplete is still updating a field the
		// docs say cannot be updated on repeating items.
		{"completedFalse", `{"type":"to-do","operation":"update","id":"rep-1","attributes":{"completed":false}}`, "completed"},
		{"canceledFalse", `{"type":"to-do","operation":"update","id":"rep-1","attributes":{"canceled":false}}`, "canceled"},
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

	payload := `[{"type":"to-do","operation":"update","id":"rep-1","attributes":{"title":"New title","notes":"n","tags":["urgent"]}}]`
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

// Neither a checklist item nor a heading is reachable through GetTaskByUUID —
// checklist items are in their own table, and the lookups exclude the heading
// type (#149) — so looking either up would warn that Things does not know an
// id it knows perfectly well.
func TestImportDoesNotLookUpUnresolvableTypes(t *testing.T) {
	cases := []struct {
		name string
		item string
		id   string
	}{
		{"checklistItem", `{"type":"checklist-item","operation":"update","id":"chk-1","attributes":{"completed":true}}`, "chk-1"},
		{"heading", `{"type":"heading","operation":"update","id":"head-1","attributes":{"title":"Phase 2"}}`, "head-1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			database, sqlDB := seedWritable(t)
			// A real heading row: the lookup filters it out by type, not by
			// absence, so seeding it proves the skip is what keeps us quiet.
			if _, err := sqlDB.Exec(`INSERT INTO TMTask (uuid, title, type, status, trashed) VALUES ('head-1', 'Phase 2', 2, 0, 0)`); err != nil {
				t.Fatalf("seed heading: %v", err)
			}
			stubExecDropping(t)

			stderr, err := runImport(t, database, "["+c.item+"]")
			if err != nil {
				t.Fatalf("import: %v", err)
			}
			if strings.Contains(stderr, c.id) {
				t.Errorf("%s was looked up and warned about:\n%s", c.name, stderr)
			}
		})
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
	// The detail belongs in the error, not on stderr: under --json the error
	// is the only thing the consumer reads.
	if !strings.Contains(err.Error(), "1 of 2 requested status changes did not apply") {
		t.Errorf("unexpected error: %v", err)
	}
	if !strings.Contains(err.Error(), `[1]: status change did not apply: "File taxes" (one-2) is still open`) {
		t.Errorf("error missing the failing item:\n%v", err)
	}
	if strings.Contains(err.Error(), "one-1") {
		t.Errorf("the item that landed was reported as a failure:\n%v", err)
	}
	if stderr != "" {
		t.Errorf("per-item detail should be in the error, not stderr:\n%s", stderr)
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

// `"completed": false` asks Things to set an item to incomplete — the `update`
// command documents both status fields as two-way. A reopen Things drops is as
// invisible as a completion it drops, so it is read back the same way.
func TestImportReadsBackAReopen(t *testing.T) {
	fastVerify(t)
	database, sqlDB := seedWritable(t)
	if _, err := sqlDB.Exec(`INSERT INTO TMTask (uuid, title, type, status, trashed, start) VALUES ('done-1', 'File taxes', 0, 3, 0, 2)`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	payload := `[{"type":"to-do","operation":"update","id":"done-1","attributes":{"completed":false}}]`

	t.Run("dropped", func(t *testing.T) {
		stubExecApplyingAll(t, sqlDB, nil)
		_, err := runImport(t, database, payload)
		if err == nil {
			t.Fatal("expected a non-zero exit for a dropped reopen, got nil")
		}
		if !strings.Contains(err.Error(), `"File taxes" (done-1) is still completed`) {
			t.Errorf("error missing the dropped reopen:\n%v", err)
		}
	})

	t.Run("applied", func(t *testing.T) {
		stubExecApplyingAll(t, sqlDB, map[string]int{"done-1": int(model.StatusOpen)})
		stderr, err := runImport(t, database, payload)
		if err != nil {
			t.Fatalf("import: %v (stderr: %s)", err, stderr)
		}
		if stderr != "" {
			t.Errorf("unexpected stderr: %s", stderr)
		}
	})
}

// `"completed": false` on an item that is already open asks for a status it is
// already in, so the read-back is satisfied at once rather than reporting a
// failure against a payload Things had nothing to do with.
func TestImportReopenOfAnOpenItemIsNotAFailure(t *testing.T) {
	fastVerify(t)
	database, sqlDB := seedWritable(t)
	stubExecApplyingAll(t, sqlDB, nil)

	payload := `[{"type":"to-do","operation":"update","id":"one-1","attributes":{"completed":false}}]`
	stderr, err := runImport(t, database, payload)
	if err != nil {
		t.Fatalf("import: %v (stderr: %s)", err, stderr)
	}
	if stderr != "" {
		t.Errorf("unexpected stderr: %s", stderr)
	}
}

// canceled takes priority over completed, so a payload setting both is read
// back against cancelled — not completed, which Things ignores.
func TestImportReadsBackCanceledOverCompleted(t *testing.T) {
	fastVerify(t)
	database, sqlDB := seedWritable(t)
	stubExecApplyingAll(t, sqlDB, map[string]int{"one-1": int(model.StatusCancelled)})

	payload := `[{"type":"to-do","operation":"update","id":"one-1","attributes":{"completed":true,"canceled":true}}]`
	stderr, err := runImport(t, database, payload)
	if err != nil {
		t.Fatalf("import treated a cancelled item as a failed completion: %v (stderr: %s)", err, stderr)
	}
	if stderr != "" {
		t.Errorf("unexpected stderr: %s", stderr)
	}
}

// findItem returns the payload item at path, so an assertion names what it
// wants instead of depending on slice order.
func findItem(t *testing.T, items []jsonErrorItem, path string) jsonErrorItem {
	t.Helper()
	for _, it := range items {
		if it.Path == path {
			return it
		}
	}
	t.Fatalf("no item at path %q in %+v", path, items)
	return jsonErrorItem{}
}

// A refusal carries every offending item under --json, so an agent can fix the
// payload per item rather than parsing them back out of the message (#161).
func TestImportRefusalJSONItems(t *testing.T) {
	database, _ := seedWritable(t)
	calls := stubExecDropping(t)

	payload := `[
	  {"type":"to-do","operation":"update","id":"one-1","attributes":{"when":"today"}},
	  {"type":"to-do","operation":"update","id":"rep-1","attributes":{"when":"today","deadline":"2026-05-01"}},
	  {"type":"project","operation":"update","id":"repproj-1","attributes":{"canceled":true}}
	]`
	err := runWith(t, database, "--json", "import", "--file", importPayload(t, payload))
	if err == nil {
		t.Fatal("expected a refusal")
	}
	p, raw := decodePayload(t, err)

	if p.Error != "import refused" {
		t.Errorf("error token = %q, want %q (%s)", p.Error, "import refused", raw)
	}
	if len(p.Items) != 2 {
		t.Fatalf("got %d items, want 2 (%s)", len(p.Items), raw)
	}

	todo := findItem(t, p.Items, "[1]")
	if todo.ID != "rep-1" || todo.Title != "Water plants" {
		t.Errorf("item = %+v, want id rep-1 / Water plants", todo)
	}
	if strings.Join(todo.Blocked, ",") != "when,deadline" {
		t.Errorf("blocked = %v, want [when deadline]", todo.Blocked)
	}
	proj := findItem(t, p.Items, "[2]")
	if proj.ID != "repproj-1" || strings.Join(proj.Blocked, ",") != "canceled" {
		t.Errorf("item = %+v, want id repproj-1 blocked [canceled]", proj)
	}

	// The non-repeating item is not an offender and must not appear.
	for _, it := range p.Items {
		if it.ID == "one-1" {
			t.Errorf("allowed item reported as an offender: %+v", it)
		}
	}
	// The message still carries everything, for a human reading the JSON.
	if !strings.Contains(p.Message, "2 of 3 update items") {
		t.Errorf("message lost its summary: %s", p.Message)
	}
	if *calls != 0 {
		t.Errorf("payload was sent to Things anyway (%d calls)", *calls)
	}
}

// A read-back failure names the status asked for and the one the item is
// actually in, so a caller can tell a dropped write from an unexpected state.
func TestImportVerifyJSONItems(t *testing.T) {
	fastVerify(t)
	database, sqlDB := seedWritable(t)
	if _, err := sqlDB.Exec(`INSERT INTO TMTask (uuid, title, type, status, trashed, start) VALUES ('one-2', 'File taxes', 0, 0, 0, 2)`); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// Things applies the first and drops the second.
	stubExecApplyingAll(t, sqlDB, map[string]int{"one-1": int(model.StatusCompleted)})

	payload := `[
	  {"type":"to-do","operation":"update","id":"one-1","attributes":{"completed":true}},
	  {"type":"to-do","operation":"update","id":"one-2","attributes":{"canceled":true}}
	]`
	err := runWith(t, database, "--json", "import", "--file", importPayload(t, payload))
	if err == nil {
		t.Fatal("expected a read-back failure")
	}
	p, raw := decodePayload(t, err)

	if p.Error != "import partially applied" {
		t.Errorf("error token = %q, want %q (%s)", p.Error, "import partially applied", raw)
	}
	if len(p.Items) != 1 {
		t.Fatalf("got %d items, want only the dropped one (%s)", len(p.Items), raw)
	}
	it := p.Items[0]
	if it.Path != "[1]" || it.ID != "one-2" || it.Title != "File taxes" {
		t.Errorf("item = %+v, want [1] / one-2 / File taxes", it)
	}
	if it.Wanted != "cancelled" || it.Got != "open" {
		t.Errorf("wanted/got = %q/%q, want cancelled/open", it.Wanted, it.Got)
	}
	if len(it.Blocked) != 0 {
		t.Errorf("read-back item should not carry blocked attributes: %+v", it)
	}
}

// Nothing to observe means no `got`: the field is omitted rather than
// reported as the zero status, which would read as "still open".
func TestImportVerifyJSONOmitsGotWhenUnobserved(t *testing.T) {
	fastVerify(t)
	database, sqlDB := seedWritable(t)
	// Things reports success, then the row disappears before the read-back.
	prev := things.SetExecCommandForTest(func(string, ...string) *exec.Cmd {
		if _, err := sqlDB.Exec(`DELETE FROM TMTask WHERE uuid = 'one-1'`); err != nil {
			t.Errorf("simulating deletion: %v", err)
		}
		return exec.Command("true")
	})
	t.Cleanup(func() { things.SetExecCommandForTest(prev) })

	payload := `[{"type":"to-do","operation":"update","id":"one-1","attributes":{"completed":true}}]`
	err := runWith(t, database, "--json", "import", "--file", importPayload(t, payload))
	if err == nil {
		t.Fatal("expected a read-back failure")
	}
	p, raw := decodePayload(t, err)
	if len(p.Items) != 1 {
		t.Fatalf("got %d items, want 1 (%s)", len(p.Items), raw)
	}
	if p.Items[0].Got != "" {
		t.Errorf("got = %q, want it omitted when nothing was observed", p.Items[0].Got)
	}
	if p.Items[0].Wanted != "completed" {
		t.Errorf("wanted = %q, want completed", p.Items[0].Wanted)
	}
	if !strings.Contains(raw, `"wanted"`) || strings.Contains(raw, `"got"`) {
		t.Errorf("payload should carry wanted but not got: %s", raw)
	}
}

// Plain text is unchanged by the typed errors: same message, on stderr, with
// nothing on stdout.
func TestImportFailuresPlainTextUnchanged(t *testing.T) {
	database, _ := seedWritable(t)
	stubExecDropping(t)

	payload := `[{"type":"to-do","operation":"update","id":"rep-1","attributes":{"when":"today"}}]`
	err := runWith(t, database, "import", "--file", importPayload(t, payload))
	if err == nil {
		t.Fatal("expected a refusal")
	}
	var stdout, stderr bytes.Buffer
	renderError(&stdout, &stderr, false, err)
	if stdout.Len() != 0 {
		t.Errorf("plain text wrote to stdout: %q", stdout.String())
	}
	want := `Error: 1 of 1 update items change attributes Things does not allow on repeating items`
	if !strings.HasPrefix(stderr.String(), want) {
		t.Errorf("stderr = %q, want prefix %q", stderr.String(), want)
	}
	if !strings.Contains(stderr.String(), `  [0] (id rep-1): "Water plants" is a repeating to-do — when`) {
		t.Errorf("stderr lost the per-item line: %q", stderr.String())
	}
}
