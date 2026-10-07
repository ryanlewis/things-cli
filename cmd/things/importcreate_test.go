package main

import (
	"encoding/json"
	"errors"
	"io"
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

// A created item that never appears fails the import. The error names the
// missing items to search for, and keeps the confirmed ones apart, with their
// uuids.
func TestImportCreatedNotFoundFails(t *testing.T) {
	fastVerify(t)
	database, sqlDB := seedWritable(t)
	stubExecAdding(t, sqlDB, createRows[0])

	out, _, err := runImportOut(t, database, createPayload)
	if err == nil {
		t.Fatal("expected a non-zero exit")
	}
	for _, want := range []string{
		"2 of 3 created items did not appear",
		`[1]: no new project titled "Launch" appeared`,
		`[1].attributes.items[1]: no new task titled "Book venue" appeared`,
		"things search",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q:\n%v", want, err)
		}
	}
	if !strings.Contains(err.Error(), "The other created items:\n  Created and confirmed: [0] \"Buy oat milk\" (new-1)") {
		t.Errorf("the confirmed item is not reported apart:\n%v", err)
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
	if len(p.Created) != 3 || !p.Created[0].Confirmed || p.Created[0].UUID != "new-1" || p.Created[1].Reason != "not-found" {
		t.Errorf("created = %+v, want all three verdicts with new-1 confirmed", p.Created)
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
		createdRow{uuid: "new-2", title: "Buy oat milk", extra: `creationDate = creationDate + 0.001`},
		createdRow{uuid: "new-1", title: "Buy oat milk"})

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

// Two new items saved at the same instant cannot be paired with the payload's
// items by order, so both are ambiguous rather than confirmed under a uuid
// that may belong to the other.
func TestImportCreatedDuplicateTitlesSameInstant(t *testing.T) {
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
	for _, c := range decodeCreated(t, out) {
		if c.Confirmed || c.Reason != "ambiguous" || strings.Join(c.Candidates, ",") != "new-1,new-2" {
			t.Errorf("item = %+v, want ambiguous with both candidates", c)
		}
	}
}

// A dropped status change fails the import, and the created items that were
// confirmed keep their uuids in the error rather than vanishing with the
// success list.
func TestImportStatusFailureKeepsConfirmedCreates(t *testing.T) {
	fastVerify(t)
	database, sqlDB := seedWritable(t)
	stubExecAdding(t, sqlDB, createdRow{uuid: "new-1", title: "Buy oat milk"})

	payload := `[
	  {"type":"to-do","operation":"update","id":"one-1","attributes":{"completed":true}},
	  {"type":"to-do","attributes":{"title":"Buy oat milk"}}
	]`
	_, _, err := runImportOut(t, database, payload)
	if err == nil || !strings.Contains(err.Error(), "The other created items:\n  Created and confirmed: [1] \"Buy oat milk\" (new-1)") {
		t.Fatalf("err = %v, want the confirmed item named", err)
	}
	if strings.Contains(err.Error(), "created items did not appear") {
		t.Errorf("the confirmed item was reported missing:\n%v", err)
	}
	p, raw := decodePayload(t, err)
	if len(p.Items) != 1 || p.Items[0].Path != "[0]" {
		t.Errorf("items = %s, want only the status change", raw)
	}
	if len(p.Created) != 1 || !p.Created[0].Confirmed || p.Created[0].UUID != "new-1" {
		t.Errorf("created = %+v, want new-1 confirmed", p.Created)
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
	for _, want := range []string{"1 of 1 requested status changes did not apply", "1 of 1 created items did not appear"} {
		if !strings.Contains(p.Message, want) {
			t.Errorf("message missing %q: %s", want, p.Message)
		}
	}
}

// An item the payload gives a creation-date is saved with that date, so the
// read-back could never find it among the items created since the write.
// It is reported as not checked, exits 0, and the rest are still read back.
func TestImportCreatedWithCreationDateIsNotChecked(t *testing.T) {
	fastVerify(t)
	database, sqlDB := seedWritable(t)
	stubExecAdding(t, sqlDB, createdRow{uuid: "new-1", title: "Buy oat milk"})

	payload := `[
	  {"type":"to-do","attributes":{"title":"Old note","creation-date":"2020-01-01T09:00:00Z"}},
	  {"type":"to-do","attributes":{"title":"Buy oat milk"}}
	]`
	out, _, err := runImportOut(t, database, payload)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	want := `Sent to Things, not checked (creation-date set): [0] "Old note"
Created and confirmed: [1] "Buy oat milk" (new-1)
`
	if out != want {
		t.Errorf("output =\n%s\nwant\n%s", out, want)
	}

	stubExecDropping(t)
	out, _, err = runImportOut(t, database, `[{"type":"project","attributes":{"title":"Old","creation-date":"2020-01-01T09:00:00Z"}}]`, "--json")
	if err != nil {
		t.Fatalf("import --json: %v", err)
	}
	got := decodeCreated(t, out)
	if len(got) != 1 || got[0].Confirmed || got[0].Reason != "creation-date" || got[0].Kind != "project" {
		t.Errorf("got %+v, want one project not checked for its creation-date", got)
	}
}

// A dated item's row can land inside the read-back window, so an undated
// item with the same kind and title cannot be told apart from it where both
// could be filed. Here Things drops the undated to-do nested in a project
// update and saves only the dated one, at the top level: its row must not
// confirm the dropped item, and the import fails naming it as not found.
func TestImportUndatedSharingDatedTitleIsNotConfirmed(t *testing.T) {
	fastVerify(t)
	database, sqlDB := seedWritable(t)
	stubExecAdding(t, sqlDB, createdRow{uuid: "dated-1", title: "Weekly review"})

	payload := `[
	  {"type":"to-do","attributes":{"title":"Weekly review","creation-date":"` + time.Now().UTC().Format(time.RFC3339) + `"}},
	  {"type":"project","operation":"update","id":"repproj-1","attributes":{"items":[
	    {"type":"to-do","attributes":{"title":"Weekly review"}}
	  ]}}
	]`
	out, _, err := runImportOut(t, database, payload, "--json")
	if err == nil {
		t.Fatalf("expected a non-zero exit, got output %s", out)
	}
	var verr *importVerifyError
	if !errors.As(err, &verr) {
		t.Fatalf("err = %v, want an importVerifyError", err)
	}
	items := verr.jsonItems()
	if len(items) != 1 || items[0].Path != "[1].attributes.items[0]" || items[0].Reason != "not-found" {
		t.Errorf("items = %+v, want the nested to-do with reason not-found", items)
	}
	for _, c := range verr.created {
		if c.Confirmed {
			t.Errorf("%s confirmed as %s, want nothing confirmed", c.Path, c.UUID)
		}
	}
}

// A to-do sharing a dated item's title is not confirmed, but names the new
// items it could be, as an ambiguous one does, in the error and under --json.
func TestImportSharesDatedTitleListsCandidates(t *testing.T) {
	fastVerify(t)
	database, sqlDB := seedWritable(t)
	stubExecAdding(t, sqlDB, createdRow{uuid: "new-1", title: "Weekly review"})

	payload := `[
	  {"type":"to-do","attributes":{"title":"Weekly review","creation-date":"` + time.Now().UTC().Format(time.RFC3339) + `"}},
	  {"type":"to-do","attributes":{"title":"Weekly review"}}
	]`
	_, _, err := runImportOut(t, database, payload, "--json")
	var verr *importVerifyError
	if !errors.As(err, &verr) {
		t.Fatalf("err = %v, want an importVerifyError", err)
	}
	items := verr.jsonItems()
	if len(items) != 1 || items[0].Reason != "shares-dated-title" || strings.Join(items[0].Candidates, ",") != "new-1" {
		t.Errorf("items = %+v, want [1] shares-dated-title with candidates [new-1]", items)
	}
	if !strings.Contains(err.Error(), "may be either (new-1)") {
		t.Errorf("error does not name the candidate:\n%v", err)
	}
}

// Things rejects the whole payload over a creation-date or completion-date
// with no time or no UTC offset, on an item it creates or updates, so the
// import refuses it before sending anything, even under --no-verify.
func TestImportRefusesCreationDateThingsRejects(t *testing.T) {
	database := seedFullDB(t)
	captured := stubExec(t)

	payload := `[
	  {"type":"to-do","attributes":{"title":"Fine","creation-date":"2026-10-05T10:30:00Z"}},
	  {"type":"to-do","attributes":{"title":"Day only","creation-date":"2026-10-05"}},
	  {"type":"project","attributes":{"title":"P","items":[
	    {"type":"to-do","attributes":{"title":"No offset","creation-date":"2026-10-05T10:30:00"}}
	  ]}},
	  {"type":"to-do","operation":"update","id":"t1","attributes":{"completion-date":"2026-10-05"}}
	]`
	_, _, err := runImportOut(t, database, payload, "--no-verify")
	if err == nil {
		t.Fatal("expected the payload to be refused")
	}
	for _, want := range []string{`[1] creation-date: "2026-10-05"`, `[2].attributes.items[0] creation-date: "2026-10-05T10:30:00"`, `[3] (id t1) completion-date: "2026-10-05"`, "Nothing was sent"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q:\n%v", want, err)
		}
	}
	if strings.Contains(err.Error(), " [0] ") {
		t.Errorf("error names the valid creation-date:\n%v", err)
	}
	if len(*captured) != 0 {
		t.Errorf("payload was sent: %v", *captured)
	}
}

// The creation-date refusal comes before --create-tags makes any tag, so
// nothing is written, and under --json it is an import refused naming each
// item with creation-date blocked.
func TestImportCreationDateRefusalWritesNothing(t *testing.T) {
	database := seedFullDB(t)
	captured := stubExec(t)

	payload := `[{"type":"to-do","attributes":{"title":"Day only","tags":["brand-new"],"creation-date":"2026-10-05"}}]`
	err := runWith(t, database, "--json", "import", "--create-tags", "--file", importPayload(t, payload))
	if err == nil {
		t.Fatal("expected the payload to be refused")
	}
	if len(*captured) != 0 {
		t.Errorf("something was written before the refusal: %v", *captured)
	}
	p, raw := decodePayload(t, err)
	if p.Error != "import refused" {
		t.Errorf("error token = %q, want %q (%s)", p.Error, "import refused", raw)
	}
	it := findItem(t, p.Items, "[0]")
	if it.Title != "Day only" || strings.Join(it.Blocked, ",") != "creation-date" {
		t.Errorf("item = %+v, want Day only blocked [creation-date]", it)
	}
}

// The date and repeating refusals are one: an item refused for both is one
// entry naming every blocked attribute and both reasons, an update item
// carries its id and the title the database has, and the item type is read
// trimmed, as the rest of the import reads it.
func TestImportRefusesDatesAndRepeatingTogether(t *testing.T) {
	database, _ := seedWritable(t)
	calls := stubExecDropping(t)

	payload := `[
	  {"type":"to-do","operation":"update","id":"rep-1","attributes":{"when":"today","creation-date":"2026-10-05"}},
	  {"type":"to-do","operation":"update","id":"one-1","attributes":{"completion-date":"2026-10-05"}},
	  {"type":" to-do ","attributes":{"title":"Padded","creation-date":"2026-10-05"}}
	]`
	err := runWith(t, database, "--json", "import", "--file", importPayload(t, payload))
	if *calls != 0 {
		t.Errorf("payload was sent (%d calls)", *calls)
	}
	p, raw := decodePayload(t, err)
	if p.Error != "import refused" || len(p.Items) != 3 {
		t.Fatalf("got %s, want import refused with 3 items", raw)
	}
	want := []jsonErrorItem{
		{Path: "[0]", ID: "rep-1", Title: "Water plants", Blocked: []string{"when", "creation-date"}, Reason: "repeating invalid-date"},
		{Path: "[1]", ID: "one-1", Title: "Post letter", Blocked: []string{"completion-date"}, Reason: "invalid-date"},
		{Path: "[2]", Title: "Padded", Blocked: []string{"creation-date"}, Reason: "invalid-date"},
	}
	for i, w := range want {
		got := p.Items[i]
		if got.Path != w.Path || got.ID != w.ID || got.Title != w.Title || strings.Join(got.Blocked, ",") != strings.Join(w.Blocked, ",") || got.Reason != w.Reason {
			t.Errorf("item %d = %+v, want %+v", i, got, w)
		}
	}
	for _, want := range []string{
		`[0] (id rep-1): "Water plants" is a repeating task — when`,
		`[0] (id rep-1) creation-date: "2026-10-05"`,
		`[1] (id one-1) completion-date: "2026-10-05"`,
	} {
		if !strings.Contains(p.Message, want) {
			t.Errorf("message missing %q:\n%s", want, p.Message)
		}
	}
}

// With no rows to read, an undated item sharing a recent dated item's title
// still fails the import as shares-dated-title rather than passing as
// unreadable.
func TestImportSharesDatedTitleWhenUnreadable(t *testing.T) {
	database, _ := seedWritable(t)
	d := &Deps{DB: database, Stdout: io.Discard, Stderr: io.Discard}
	creates := []importCreate{
		{path: "[0]", typ: model.TypeTask, title: "Weekly review", dated: true, datedAt: time.Now()},
		{path: "[1]", typ: model.TypeTask, title: "Weekly review"},
	}
	got := readBackCreates(d, database, creates, createdSnapshot{since: time.Now()}, errors.New("database is locked"), time.Millisecond)
	if got[1].Reason != "shares-dated-title" || !failsImport(got[1].Reason) {
		t.Errorf("[1] = %+v, want shares-dated-title", got[1])
	}
}

// Things saves an item whose creation-date is null as created now, so it is
// read back like an undated one.
func TestImportNullCreationDateIsReadBack(t *testing.T) {
	fastVerify(t)
	database, sqlDB := seedWritable(t)
	stubExecAdding(t, sqlDB, createdRow{uuid: "new-1", title: "Buy oat milk"})

	out, _, err := runImportOut(t, database, `[{"type":"to-do","attributes":{"title":"Buy oat milk","creation-date":null}}]`)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if want := "Created and confirmed: [0] \"Buy oat milk\" (new-1)\n"; out != want {
		t.Errorf("output = %q, want %q", out, want)
	}
}

// A dated item whose creation-date is well before the import can never land
// in the read-back window, so an undated item with the same title is still
// read back and confirmed, and the import exits 0.
func TestImportUndatedSharingBackdatedTitleIsConfirmed(t *testing.T) {
	fastVerify(t)
	database, sqlDB := seedWritable(t)
	stubExecAdding(t, sqlDB, createdRow{uuid: "new-1", title: "Weekly review"})

	payload := `[
	  {"type":"to-do","attributes":{"title":"Weekly review","creation-date":"2020-01-01T09:00:00Z"}},
	  {"type":"to-do","attributes":{"title":"Weekly review"}}
	]`
	out, _, err := runImportOut(t, database, payload)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	want := `Sent to Things, not checked (creation-date set): [0] "Weekly review"
Created and confirmed: [1] "Weekly review" (new-1)
`
	if out != want {
		t.Errorf("output =\n%s\nwant\n%s", out, want)
	}
}

// An unreadable created item beside a failed import says why it is not
// confirmed, rather than leaving an empty line in the error.
func TestImportUnreadableCreatedItemInErrorSaysWhy(t *testing.T) {
	database, _ := seedWritable(t)
	d := &Deps{DB: database, Stdout: io.Discard, Stderr: io.Discard}
	creates := []importCreate{{path: "[1]", typ: model.TypeTask, title: "Buy oat milk"}}
	created := readBackCreates(d, database, creates, createdSnapshot{}, errors.New("database is locked"), time.Millisecond)
	err := &importVerifyError{
		items:   []importVerifyItem{{Path: "[0]", err: errors.New("status change did not apply")}},
		total:   1,
		created: created,
		search:  "things search",
	}

	want := `  Sent to Things, not confirmed (database unreadable): [1] "Buy oat milk"`
	if !strings.Contains(err.Error(), want) {
		t.Errorf("error missing %q:\n%v", want, err)
	}
	if strings.Contains(err.Error(), "[1]: \n") || strings.HasSuffix(err.Error(), "[1]: ") {
		t.Errorf("error has an empty line for the item:\n%v", err)
	}
}

// An import's created item is checked against where the payload files it,
// as an add's is: a row with its title filed elsewhere, from another command
// adding the same title at the same moment, does not confirm it. A list the
// CLI cannot resolve is not checked, and when that leaves two items able to
// claim the same row, neither is confirmed; when fewer rows appeared than
// the items that could claim them, they are not found. A recently dated
// item stops the read-back of an undated one with its title only where its
// row could be filed.
func TestImportCreatedChecksDestination(t *testing.T) {
	now := time.Now().UTC().Format(time.RFC3339)
	cases := []struct {
		name, payload string
		rows          []createdRow
		want          []string // per item: the uuid confirmed, or the reason
	}{
		{"otherProject", `[{"type":"to-do","attributes":{"title":"Buy oat milk","list":"Garden"}}]`,
			[]createdRow{{uuid: "other", title: "Buy oat milk", extra: inProj1}}, []string{"not-found"}},
		{"listID", `[{"type":"to-do","attributes":{"title":"Buy oat milk","list-id":"proj-2"}}]`,
			[]createdRow{{uuid: "other", title: "Buy oat milk", extra: inProj1}, {uuid: "mine", title: "Buy oat milk", extra: inProj2}}, []string{"mine"}},
		{"inbox", `[{"type":"to-do","attributes":{"title":"Buy oat milk"}}]`,
			[]createdRow{{uuid: "other", title: "Buy oat milk", extra: inProj1}}, []string{"not-found"}},
		{"projectArea", `[{"type":"project","attributes":{"title":"Launch","area":"errands"}}]`,
			[]createdRow{{uuid: "other", title: "Launch", typ: model.TypeProject, extra: inArea1}, {uuid: "mine", title: "Launch", typ: model.TypeProject, extra: `area = 'area-2'`}}, []string{"mine"}},
		{"nestedInUpdate", `[{"type":"project","operation":"update","id":"proj-2","attributes":{"items":[{"type":"to-do","attributes":{"title":"Buy oat milk"}}]}}]`,
			[]createdRow{{uuid: "other", title: "Buy oat milk", extra: inProj1}}, []string{"not-found"}},
		{"unknownList", `[{"type":"to-do","attributes":{"title":"Buy oat milk","list":"Nowhere"}}]`,
			[]createdRow{{uuid: "other", title: "Buy oat milk", extra: inProj1}}, []string{"other"}},
		{"sharedRow", `[{"type":"to-do","attributes":{"title":"Buy oat milk","list":"Nowhere"}},{"type":"to-do","attributes":{"title":"Buy oat milk","list":"Tools"}}]`,
			[]createdRow{{uuid: "other", title: "Buy oat milk", extra: inProj1}}, []string{"not-found", "not-found"}},
		{"sharedRowBothLand", `[{"type":"to-do","attributes":{"title":"Buy oat milk","list":"Nowhere"}},{"type":"to-do","attributes":{"title":"Buy oat milk","list":"Tools"}}]`,
			[]createdRow{{uuid: "other", title: "Buy oat milk", extra: inProj1}, {uuid: "mine", title: "Buy oat milk"}}, []string{"ambiguous", "other"}},
		{"uncheckedTakesShortRow", `[{"type":"to-do","attributes":{"title":"Buy oat milk","list":"Nowhere"}},{"type":"to-do","attributes":{"title":"Buy oat milk","list":"Tools"}},{"type":"to-do","attributes":{"title":"Buy oat milk","list":"Tools"}}]`,
			[]createdRow{{uuid: "other", title: "Buy oat milk", extra: inProj1}}, []string{"not-found", "not-found", "not-found"}},
		// A list-id and a title naming the same project are one
		// destination, so the two rows pair with the items as usual.
		{"listIDAndTitle", `[{"type":"to-do","attributes":{"title":"Buy oat milk","list-id":"proj-1"}},{"type":"to-do","attributes":{"title":"Buy oat milk","list":"Tools"}}]`,
			[]createdRow{{uuid: "first", title: "Buy oat milk", extra: inProj1}, {uuid: "second", title: "Buy oat milk", extra: inProj1 + `, creationDate = creationDate + 0.001`}}, []string{"first", "second"}},
		// The project the payload creates is matched by its trimmed title.
		{"nestedInCreatedPadded", `[{"type":"project","attributes":{"title":" Launch ","items":[{"type":"to-do","attributes":{"title":"Buy oat milk"}}]}}]`,
			[]createdRow{{uuid: "new-p", title: "Launch", typ: model.TypeProject}, {uuid: "mine", title: "Buy oat milk", extra: `project = 'new-p'`}}, []string{"new-p", "mine"}},
		{"headingIDAlone", `[{"type":"to-do","attributes":{"title":"Buy oat milk","heading-id":"head-1"}}]`,
			[]createdRow{{uuid: "mine", title: "Buy oat milk", extra: underHead1}}, []string{"mine"}},
		// Things takes list by title only, so a uuid there is not checked
		// against the project it names: the item may land in the Inbox.
		{"uuidAsList", `[{"type":"to-do","attributes":{"title":"Buy oat milk","list":"proj-1"}}]`,
			[]createdRow{{uuid: "mine", title: "Buy oat milk"}}, []string{"mine"}},
		{"datedElsewhere", `[{"type":"to-do","attributes":{"title":"Buy oat milk","list":"Tools","creation-date":"` + now + `"}},{"type":"to-do","attributes":{"title":"Buy oat milk"}}]`,
			[]createdRow{{uuid: "dated", title: "Buy oat milk", extra: inProj1}, {uuid: "mine", title: "Buy oat milk"}}, []string{"creation-date", "mine"}},
		{"datedSameList", `[{"type":"to-do","attributes":{"title":"Buy oat milk","list":"Tools","creation-date":"` + now + `"}},{"type":"to-do","attributes":{"title":"Buy oat milk","list":"Tools"}}]`,
			[]createdRow{{uuid: "dated", title: "Buy oat milk", extra: inProj1}, {uuid: "mine", title: "Buy oat milk", extra: inProj1}}, []string{"creation-date", "shares-dated-title"}},
		// A title two areas share fits either of them.
		{"sharedAreaTitle", `[{"type":"to-do","attributes":{"title":"Buy oat milk","list":"Personal"}}]`,
			[]createdRow{{uuid: "mine", title: "Buy oat milk", extra: `area = 'area-3'`}}, []string{"mine"}},
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
			fx.Project("proj-2", "Garden", 6)
			fx.Heading("head-1", "Setup", 1, dbtest.InProject("proj-1"))
			stubExecAdding(t, sqlDB, tc.rows...)

			out, _, err := runImportOut(t, database, tc.payload, "--json")
			var got []importCreated
			var verr *importVerifyError
			switch {
			case errors.As(err, &verr):
				got = verr.created
			case err != nil:
				t.Fatalf("import: %v", err)
			default:
				got = decodeCreated(t, out)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %+v, want %d items", got, len(tc.want))
			}
			for i, want := range tc.want {
				if got[i].UUID != want && got[i].Reason != want {
					t.Errorf("item %d = %+v, want %s", i, got[i], want)
				}
				if got[i].Reason != "" && got[i].UUID != "" {
					t.Errorf("item %d = %+v, want no uuid on an unconfirmed item", i, got[i])
				}
			}
		})
	}
}
