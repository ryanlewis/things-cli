package main

import (
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

// runImportOut runs `import --file` and returns stdout, stderr and the error.
func runImportOut(t *testing.T, database *db.DB, payload string, args ...string) (string, string, error) {
	t.Helper()
	return runStreams(t, database, append(args, "import", "--file", importPayload(t, payload))...)
}

// decodeCreated unmarshals the --json output of a successful import.
func decodeCreated(t *testing.T, out string) []importCreated {
	t.Helper()
	var got []importCreated
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("unmarshal %q: %v", out, err)
	}
	return got
}

// A project with a to-do and a heading inside it, beside a top-level to-do.
// The heading is not read back; the rest are.
const createPayload = `[
  {"type":"to-do","attributes":{"title":"Buy oat milk"}},
  {"type":"project","attributes":{"title":"Launch","items":[
    {"type":"heading","attributes":{"title":"Phase 1"}},
    {"type":"to-do","attributes":{"title":" Book venue ","checklist-items":[
      {"type":"checklist-item","attributes":{"title":"Call them"}}
    ]}}
  ]}}
]`

var createRows = []createdRow{
	{uuid: "new-1", title: "Buy oat milk", typ: model.TypeTask},
	{uuid: "new-p", title: "Launch", typ: model.TypeProject},
	{uuid: "new-2", title: "Book venue", typ: model.TypeTask, extra: `project = 'new-p'`},
}

// Every created to-do and project is found and reported confirmed, nested
// ones included, in payload order.
func TestImportConfirmsCreatedItems(t *testing.T) {
	fastVerify(t)
	database, sqlDB := seedWritable(t)
	stubExecAdding(t, sqlDB, createRows...)

	out, stderr, err := runImportOut(t, database, createPayload)
	if err != nil {
		t.Fatalf("import: %v (stderr: %s)", err, stderr)
	}
	want := `Created and confirmed: [0] "Buy oat milk" (new-1)
Created and confirmed: [1] "Launch" (new-p)
Created and confirmed: [1].attributes.items[1] "Book venue" (new-2)
`
	if out != want {
		t.Errorf("output =\n%s\nwant\n%s", out, want)
	}
}

func TestImportConfirmsCreatedItemsJSON(t *testing.T) {
	fastVerify(t)
	database, sqlDB := seedWritable(t)
	stubExecAdding(t, sqlDB, createRows...)

	out, _, err := runImportOut(t, database, createPayload, "--json")
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	got := decodeCreated(t, out)
	want := []importCreated{
		{Path: "[0]", Kind: "task", Title: "Buy oat milk", UUID: "new-1", Confirmed: true},
		{Path: "[1]", Kind: "project", Title: "Launch", UUID: "new-p", Confirmed: true},
		{Path: "[1].attributes.items[1]", Kind: "task", Title: "Book venue", UUID: "new-2", Confirmed: true},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d items, want %d: %s", len(got), len(want), out)
	}
	for i := range want {
		if got[i].Path != want[i].Path || got[i].Kind != want[i].Kind || got[i].Title != want[i].Title ||
			got[i].UUID != want[i].UUID || !got[i].Confirmed || got[i].Reason != "" {
			t.Errorf("item %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// A to-do inside the items of a project the payload updates is read back
// too. Things 3 drops these without a word, so the read-back is the only
// place that shows.
func TestImportChecksCreateNestedInUpdate(t *testing.T) {
	fastVerify(t)
	database, _ := seedWritable(t)
	stubExecDropping(t)

	payload := `[{"type":"project","operation":"update","id":"repproj-1","attributes":{"items":[
	  {"type":"to-do","attributes":{"title":"Clear inbox"}}
	]}}]`
	_, _, err := runImportOut(t, database, payload)
	if err == nil || !strings.Contains(err.Error(), `[0].attributes.items[0]: no new task titled "Clear inbox" appeared`) {
		t.Fatalf("err = %v, want the nested to-do reported", err)
	}
}

// A created item that never appears fails the import, naming only the items
// that did not land and leaving the confirmed ones out of the error.
func TestImportCreatedNotFoundFails(t *testing.T) {
	fastVerify(t)
	database, sqlDB := seedWritable(t)
	stubExecAdding(t, sqlDB, createRows[0])

	out, _, err := runImportOut(t, database, createPayload)
	if err == nil {
		t.Fatal("expected a non-zero exit")
	}
	for _, want := range []string{
		"2 of 3 created items were not confirmed",
		`[1]: no new project titled "Launch" appeared`,
		`[1].attributes.items[1]: no new task titled "Book venue" appeared`,
		"things search",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q:\n%v", want, err)
		}
	}
	if strings.Contains(err.Error(), "Buy oat milk") {
		t.Errorf("the confirmed item was reported:\n%v", err)
	}
	if out != "" {
		t.Errorf("stdout = %q, want nothing beside the error", out)
	}
}

func TestImportCreatedNotFoundJSONItems(t *testing.T) {
	fastVerify(t)
	database, sqlDB := seedWritable(t)
	stubExecAdding(t, sqlDB, createRows[0])

	err := runWith(t, database, "--json", "import", "--file", importPayload(t, createPayload))
	if err == nil {
		t.Fatal("expected a non-zero exit")
	}
	p, raw := decodePayload(t, err)
	if p.Error != "import partially applied" {
		t.Errorf("error token = %q (%s)", p.Error, raw)
	}
	if len(p.Items) != 2 {
		t.Fatalf("got %d items, want the two missing ones (%s)", len(p.Items), raw)
	}
	it := findItem(t, p.Items, "[1]")
	if it.Title != "Launch" || it.Reason != "not-found" || it.ID != "" {
		t.Errorf("item = %+v, want Launch, not-found, no id", it)
	}
	if !strings.Contains(raw, `"confirmed": false`) {
		t.Errorf("items should say confirmed false: %s", raw)
	}
}

// Two new items with one wanted title cannot be told apart: the import exits
// 0 and reports that item unconfirmed with both candidates, as add does.
func TestImportCreatedAmbiguous(t *testing.T) {
	fastVerify(t)
	database, sqlDB := seedWritable(t)
	stubExecAdding(t, sqlDB,
		createdRow{uuid: "new-1", title: "Buy oat milk"},
		createdRow{uuid: "new-2", title: "Buy oat milk"})

	payload := `[{"type":"to-do","attributes":{"title":"Buy oat milk"}}]`
	out, _, err := runImportOut(t, database, payload)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if out != "Sent to Things, not confirmed (more than one new item has this title): [0] \"Buy oat milk\" (new-1, new-2)\n" {
		t.Errorf("output = %q", out)
	}

	stubExecAdding(t, sqlDB,
		createdRow{uuid: "new-3", title: "Buy oat milk"},
		createdRow{uuid: "new-4", title: "Buy oat milk"})
	out, _, err = runImportOut(t, database, payload, "--json")
	if err != nil {
		t.Fatalf("import --json: %v", err)
	}
	got := decodeCreated(t, out)
	if len(got) != 1 || got[0].Confirmed || got[0].Reason != "ambiguous" || got[0].UUID != "" ||
		strings.Join(got[0].Candidates, ",") != "new-3,new-4" {
		t.Errorf("got %+v, want one ambiguous item with candidates new-3 and new-4", got)
	}
}

// Two payload items with the same title are both confirmed when two new
// items appear, paired with them in the order Things saved them.
func TestImportCreatedDuplicateTitles(t *testing.T) {
	fastVerify(t)
	database, sqlDB := seedWritable(t)
	stubExecAdding(t, sqlDB,
		createdRow{uuid: "new-1", title: "Buy oat milk"},
		createdRow{uuid: "new-2", title: "Buy oat milk"})

	payload := `[{"type":"to-do","attributes":{"title":"Buy oat milk"}},{"type":"to-do","attributes":{"title":"Buy oat milk"}}]`
	out, _, err := runImportOut(t, database, payload, "--json")
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	got := decodeCreated(t, out)
	if len(got) != 2 || !got[0].Confirmed || !got[1].Confirmed || got[0].UUID != "new-1" || got[1].UUID != "new-2" {
		t.Errorf("got %+v, want both confirmed as new-1 and new-2", got)
	}
}

// Fewer new items than the payload asked for is a failure, and names the
// ones that did appear so the caller can tell which landed.
func TestImportCreatedDuplicateTitlesShort(t *testing.T) {
	fastVerify(t)
	database, sqlDB := seedWritable(t)
	stubExecAdding(t, sqlDB, createdRow{uuid: "new-1", title: "Buy oat milk"})

	payload := `[{"type":"to-do","attributes":{"title":"Buy oat milk"}},{"type":"to-do","attributes":{"title":"Buy oat milk"}}]`
	err := runWith(t, database, "--json", "import", "--file", importPayload(t, payload))
	if err == nil {
		t.Fatal("expected a non-zero exit")
	}
	p, raw := decodePayload(t, err)
	if len(p.Items) != 2 {
		t.Fatalf("got %d items, want both (%s)", len(p.Items), raw)
	}
	for _, it := range p.Items {
		if it.Reason != "not-found" || strings.Join(it.Candidates, ",") != "new-1" {
			t.Errorf("item = %+v, want not-found with candidate new-1", it)
		}
	}
	if !strings.Contains(p.Message, `only 1 of 2 new tasks titled "Buy oat milk" appeared`) {
		t.Errorf("message = %s", p.Message)
	}
}

// An item with the same title that was already there before the write is
// not the one the import created.
func TestImportCreatedSkipsSameTitleCreatedBefore(t *testing.T) {
	fastVerify(t)
	database, sqlDB := seedWritable(t)
	dbtest.NewFixture(t, sqlDB).Todo("old-1", "Buy oat milk", 9)
	if _, err := sqlDB.Exec(`UPDATE TMTask SET creationDate = ? WHERE uuid = 'old-1'`, model.TimeToUnix(time.Now())); err != nil {
		t.Fatalf("seed: %v", err)
	}
	stubExecAdding(t, sqlDB, createdRow{uuid: "new-1", title: "Buy oat milk"})

	out, _, err := runImportOut(t, database, `[{"type":"to-do","attributes":{"title":"Buy oat milk"}}]`)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if !strings.Contains(out, "Created and confirmed") || !strings.Contains(out, "(new-1)") {
		t.Errorf("output = %q, want new-1 confirmed", out)
	}
}

// --no-verify sends the import and says each created item is unconfirmed,
// without reading anything back.
func TestImportCreatedNoVerify(t *testing.T) {
	fastVerify(t)
	database, _ := seedWritable(t)
	calls := stubExecDropping(t)

	payload := `[{"type":"to-do","attributes":{"title":"Buy oat milk"}}]`
	out, _, err := runImportOut(t, database, payload, "--no-verify")
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if *calls != 1 {
		t.Errorf("issued %d writes, want 1", *calls)
	}
	if out != "Sent to Things, not confirmed (--no-verify): [0] \"Buy oat milk\"\n" {
		t.Errorf("output = %q", out)
	}
	out, _, err = runImportOut(t, database, payload, "--no-verify", "--json")
	if err != nil {
		t.Fatalf("import --json: %v", err)
	}
	got := decodeCreated(t, out)
	if len(got) != 1 || got[0].Confirmed || got[0].Reason != "no-verify" || got[0].Kind != "task" {
		t.Errorf("got %+v, want one unconfirmed no-verify task", got)
	}
}

// A database that stops answering after the write leaves the created items
// sent but unconfirmed: a warning and exit 0, as add does.
func TestImportCreatedUnreadable(t *testing.T) {
	fastVerify(t)
	database, sqlDB := seedWritable(t)
	prev := things.SetExecCommandForTest(func(string, ...string) *exec.Cmd {
		_ = sqlDB.Close()
		return exec.Command("true")
	})
	t.Cleanup(func() { things.SetExecCommandForTest(prev) })

	out, stderr, err := runImportOut(t, database, `[{"type":"to-do","attributes":{"title":"Buy oat milk"}}]`, "--json")
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	got := decodeCreated(t, out)
	if len(got) != 1 || got[0].Confirmed || got[0].Reason != "unreadable" {
		t.Errorf("got %+v, want one unreadable item", got)
	}
	if !strings.Contains(stderr, "warning:") {
		t.Errorf("stderr = %q, want a warning", stderr)
	}
}

// A payload that both creates and completes reports a missing item and a
// dropped status change in one error.
func TestImportReportsCreatedAndStatusFailuresTogether(t *testing.T) {
	fastVerify(t)
	database, _ := seedWritable(t)
	stubExecDropping(t)

	payload := `[
	  {"type":"to-do","operation":"update","id":"one-1","attributes":{"completed":true}},
	  {"type":"to-do","attributes":{"title":"Buy oat milk"}}
	]`
	err := runWith(t, database, "--json", "import", "--file", importPayload(t, payload))
	if err == nil {
		t.Fatal("expected a non-zero exit")
	}
	p, raw := decodePayload(t, err)
	if p.Error != "import partially applied" || len(p.Items) != 2 {
		t.Fatalf("payload = %s, want both failures", raw)
	}
	if it := findItem(t, p.Items, "[0]"); it.Wanted != "completed" {
		t.Errorf("status item = %+v", it)
	}
	if it := findItem(t, p.Items, "[1]"); it.Reason != "not-found" {
		t.Errorf("created item = %+v", it)
	}
	for _, want := range []string{"1 of 1 requested status changes did not apply", "1 of 1 created items were not confirmed"} {
		if !strings.Contains(p.Message, want) {
			t.Errorf("message missing %q: %s", want, p.Message)
		}
	}
}
