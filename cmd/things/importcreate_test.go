package main

import (
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"strconv"
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
// The heading is not read back; the rest are. Things keeps the padding on
// " Book venue ", and the output reports the title as saved.
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
	{uuid: "new-2", title: " Book venue ", typ: model.TypeTask, extra: `project = 'new-p'`},
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
Created and confirmed: [1].attributes.items[1] " Book venue " (new-2)
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
		{Path: "[1].attributes.items[1]", Kind: "task", Title: " Book venue ", UUID: "new-2", Confirmed: true},
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
	pinWallClock(t)
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
	  {"type":"to-do","attributes":{"title":"Weekly review","creation-date":"` + recentCreationDate(t) + `"}},
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
	if !strings.Contains(err.Error(), "cannot be confirmed (new-1)") {
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
// carries its id and the title the database has. Things does not trim an
// item type, so a padded one is refused too.
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
		{Path: "[2]", Title: "Padded", Blocked: []string{"creation-date", "type"}, Reason: "invalid-date invalid-item"},
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
	got := readBackCreates(d, database, creates, createdSnapshot{since: time.Now()}, errors.New("database is locked"), time.Now(), time.Millisecond)
	if got[1].Reason != "shares-dated-title" || !got[1].fails() {
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
	created := readBackCreates(d, database, creates, createdSnapshot{}, errors.New("database is locked"), time.Now(), time.Millisecond)
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
	pinWallClock(t)
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
		// Things takes area by title only and does not trim it, so a padded
		// uuid there is no area Things matches; it is left unchecked, as a
		// uuid given as list is.
		{"paddedUUIDAsArea", `[{"type":"project","attributes":{"title":"Launch","area":" area-1 "}}]`,
			[]createdRow{{uuid: "mine", title: "Launch", typ: model.TypeProject}}, []string{"mine"}},
		{"projectArea", `[{"type":"project","attributes":{"title":"Launch","area":"errands"}}]`,
			[]createdRow{{uuid: "other", title: "Launch", typ: model.TypeProject, extra: inArea1}, {uuid: "mine", title: "Launch", typ: model.TypeProject, extra: `area = 'area-2'`}}, []string{"mine"}},
		{"nestedInUpdate", `[{"type":"project","operation":"update","id":"proj-2","attributes":{"items":[{"type":"to-do","attributes":{"title":"Buy oat milk"}}]}}]`,
			[]createdRow{{uuid: "other", title: "Buy oat milk", extra: inProj1}}, []string{"not-found"}},
		// Things puts a to-do whose list matches nothing in the Inbox.
		{"unknownList", `[{"type":"to-do","attributes":{"title":"Buy oat milk","list":"Nowhere"}}]`,
			[]createdRow{{uuid: "other", title: "Buy oat milk", extra: inProj1}, {uuid: "mine", title: "Buy oat milk"}}, []string{"mine"}},
		// A heading-id files the to-do under the heading whatever state
		// it is in, a trashed one too.
		{"trashedHeadingID", `[{"type":"to-do","attributes":{"title":"Buy oat milk","heading-id":"head-old"}}]`,
			[]createdRow{{uuid: "other", title: "Buy oat milk", extra: inProj2}, {uuid: "mine", title: "Buy oat milk", extra: `heading = 'head-old'`}}, []string{"mine"}},
		// A to-do whose list a project created earlier in the payload
		// carries fits any list with that title, so it can claim a row
		// filed for a to-do sent to the list Things had: a row both are
		// confirmed with, or one the loose one is confirmed with that the
		// other also fits, confirms neither.
		{"sharedRow", `[{"type":"project","attributes":{"title":"tools"}},{"type":"to-do","attributes":{"title":"Buy oat milk","list":"Tools"}},{"type":"to-do","attributes":{"title":"Buy oat milk","list-id":"proj-1"}}]`,
			[]createdRow{{uuid: "new-p", title: "tools", typ: model.TypeProject}, {uuid: "other", title: "Buy oat milk", extra: inProj1}}, []string{"new-p", "not-found", "not-found"}},
		{"sharedRowBothLand", `[{"type":"project","attributes":{"title":"tools"}},{"type":"to-do","attributes":{"title":"Buy oat milk","list":"Tools"}},{"type":"to-do","attributes":{"title":"Buy oat milk","list-id":"proj-1"}}]`,
			[]createdRow{{uuid: "new-p", title: "tools", typ: model.TypeProject}, {uuid: "other", title: "Buy oat milk", extra: inProj1}, {uuid: "mine", title: "Buy oat milk", extra: `project = 'new-p'`}}, []string{"new-p", "ambiguous", "other"}},
		{"looseTakesShortRow", `[{"type":"project","attributes":{"title":"tools"}},{"type":"to-do","attributes":{"title":"Buy oat milk","list":"Tools"}},{"type":"to-do","attributes":{"title":"Buy oat milk","list-id":"proj-1"}},{"type":"to-do","attributes":{"title":"Buy oat milk","list-id":"proj-1"}}]`,
			[]createdRow{{uuid: "new-p", title: "tools", typ: model.TypeProject}, {uuid: "other", title: "Buy oat milk", extra: inProj1}}, []string{"new-p", "not-found", "not-found", "not-found"}},
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
		// An id attribute wins over its title, even an empty or unknown
		// one, and an empty or unknown one files the item nowhere.
		{"listIDOverList", `[{"type":"to-do","attributes":{"title":"Buy oat milk","list":"Garden","list-id":"proj-1"}}]`,
			[]createdRow{{uuid: "other", title: "Buy oat milk", extra: inProj2}, {uuid: "mine", title: "Buy oat milk", extra: inProj1}}, []string{"mine"}},
		{"emptyListID", `[{"type":"to-do","attributes":{"title":"Buy oat milk","list":"Tools","list-id":""}}]`,
			[]createdRow{{uuid: "other", title: "Buy oat milk", extra: inProj1}, {uuid: "mine", title: "Buy oat milk"}}, []string{"mine"}},
		{"unknownListID", `[{"type":"to-do","attributes":{"title":"Buy oat milk","list":"Tools","list-id":"nope"}}]`,
			[]createdRow{{uuid: "other", title: "Buy oat milk", extra: inProj1}, {uuid: "mine", title: "Buy oat milk"}}, []string{"mine"}},
		{"headingIDOverList", `[{"type":"to-do","attributes":{"title":"Buy oat milk","list-id":"proj-2","heading-id":"head-1"}}]`,
			[]createdRow{{uuid: "other", title: "Buy oat milk", extra: inProj2}, {uuid: "mine", title: "Buy oat milk", extra: underHead1}}, []string{"mine"}},
		{"unknownHeadingID", `[{"type":"to-do","attributes":{"title":"Buy oat milk","list-id":"proj-1","heading":"Setup","heading-id":"nope"}}]`,
			[]createdRow{{uuid: "other", title: "Buy oat milk", extra: underHead1}, {uuid: "mine", title: "Buy oat milk", extra: inProj1}}, []string{"mine"}},
		{"emptyHeadingID", `[{"type":"to-do","attributes":{"title":"Buy oat milk","list":"Tools","heading":"Setup","heading-id":""}}]`,
			[]createdRow{{uuid: "other", title: "Buy oat milk", extra: underHead1}, {uuid: "mine", title: "Buy oat milk", extra: inProj1}}, []string{"mine"}},
		{"unknownHeadingIDAlone", `[{"type":"to-do","attributes":{"title":"Buy oat milk","heading-id":"nope"}}]`,
			[]createdRow{{uuid: "mine", title: "Buy oat milk"}}, []string{"mine"}},
		{"unknownArea", `[{"type":"project","attributes":{"title":"Launch","area":"Nowhere"}}]`,
			[]createdRow{{uuid: "other", title: "Launch", typ: model.TypeProject, extra: inArea1}, {uuid: "mine", title: "Launch", typ: model.TypeProject}}, []string{"mine"}},
		{"emptyAreaID", `[{"type":"project","attributes":{"title":"Launch","area":"Errands","area-id":""}}]`,
			[]createdRow{{uuid: "other", title: "Launch", typ: model.TypeProject, extra: `area = 'area-2'`}, {uuid: "mine", title: "Launch", typ: model.TypeProject}}, []string{"mine"}},
		{"unknownAreaID", `[{"type":"project","attributes":{"title":"Launch","area":"Errands","area-id":"nope"}}]`,
			[]createdRow{{uuid: "other", title: "Launch", typ: model.TypeProject, extra: `area = 'area-2'`}, {uuid: "mine", title: "Launch", typ: model.TypeProject}}, []string{"mine"}},
		// Things does not trim a list title either.
		{"paddedList", `[{"type":"to-do","attributes":{"title":"Buy oat milk","list":"Tools "}}]`,
			[]createdRow{{uuid: "other", title: "Buy oat milk", extra: inProj1}, {uuid: "mine", title: "Buy oat milk"}}, []string{"mine"}},
		// A heading-id wins over a heading title, and a list-id naming an
		// area takes the to-do without its heading.
		{"headingIDOverHeading", `[{"type":"to-do","attributes":{"title":"Buy oat milk","list-id":"proj-1","heading-id":"head-1","heading":"Other"}}]`,
			[]createdRow{{uuid: "other", title: "Buy oat milk", extra: inProj1}, {uuid: "mine", title: "Buy oat milk", extra: underHead1}}, []string{"mine"}},
		{"areaListIDWithHeading", `[{"type":"to-do","attributes":{"title":"Buy oat milk","list-id":"area-1","heading":"Setup"}}]`,
			[]createdRow{{uuid: "other", title: "Buy oat milk", extra: underHead1}, {uuid: "mine", title: "Buy oat milk", extra: inArea1}}, []string{"mine"}},
		// Of heading twins Things takes the lowest uuid, so a row under the
		// other one does not confirm the to-do.
		{"headingTwin", `[{"type":"to-do","attributes":{"title":"Buy oat milk","list":"Tools","heading":"SETUP"}}]`,
			[]createdRow{{uuid: "other", title: "Buy oat milk", extra: `heading = 'head-twin'`}, {uuid: "mine", title: "Buy oat milk", extra: underHead1}}, []string{"mine"}},
		// A heading the payload creates earlier may take a to-do sent to
		// its title; without one, the list's lack of it is checked.
		{"headingInPayload", `[{"type":"project","operation":"update","id":"proj-1","attributes":{"items":[{"type":"heading","attributes":{"title":"Phase 2"}}]}},{"type":"to-do","attributes":{"title":"Buy oat milk","list":"Tools","heading":"phase 2"}}]`,
			[]createdRow{{uuid: "head-new", title: "Phase 2", typ: model.TypeHeading, extra: inProj1}, {uuid: "mine", title: "Buy oat milk", extra: `heading = 'head-new'`}}, []string{"mine"}},
		// A heading the payload creates may be the twin Things picks over
		// the one the list already has, so either fits.
		{"headingInPayloadAndList", `[{"type":"project","operation":"update","id":"proj-1","attributes":{"items":[{"type":"heading","attributes":{"title":"SETUP"}}]}},{"type":"to-do","attributes":{"title":"Buy oat milk","list":"Tools","heading":"Setup"}}]`,
			[]createdRow{{uuid: "head-new", title: "SETUP", typ: model.TypeHeading, extra: inProj1}, {uuid: "mine", title: "Buy oat milk", extra: `heading = 'head-new'`}}, []string{"mine"}},
		{"headingNotInPayload", `[{"type":"to-do","attributes":{"title":"Buy oat milk","list":"Tools","heading":"Phase 2"}}]`,
			[]createdRow{{uuid: "head-new", title: "Phase 2", typ: model.TypeHeading, extra: inProj1}, {uuid: "other", title: "Buy oat milk", extra: `heading = 'head-new'`}}, []string{"not-found"}},
		// To-dos in a project the payload creates go by its title, so a
		// row in the same-titled project Things had fits them too.
		{"nestedBesideListID", `[{"type":"project","attributes":{"title":"Tools","items":[{"type":"to-do","attributes":{"title":"Buy oat milk"}}]}},{"type":"to-do","attributes":{"title":"Buy oat milk","list-id":"proj-1"}}]`,
			[]createdRow{{uuid: "new-p", title: "Tools", typ: model.TypeProject}, {uuid: "mine", title: "Buy oat milk", extra: `project = 'new-p'`}, {uuid: "other", title: "Buy oat milk", extra: inProj1 + `, creationDate = creationDate + 0.001`}}, []string{"new-p", "ambiguous", "other"}},
		// A list-id files the to-do into the project whatever its state,
		// and so does a heading-id of a heading in it.
		{"loggedListID", `[{"type":"to-do","attributes":{"title":"Buy oat milk","list-id":"proj-done"}}]`,
			[]createdRow{{uuid: "other", title: "Buy oat milk"}, {uuid: "mine", title: "Buy oat milk", extra: `project = 'proj-done'`}}, []string{"mine"}},
		{"trashedListID", `[{"type":"to-do","attributes":{"title":"Buy oat milk","list-id":"proj-bin"}}]`,
			[]createdRow{{uuid: "other", title: "Buy oat milk"}, {uuid: "mine", title: "Buy oat milk", extra: `project = 'proj-bin'`}}, []string{"mine"}},
		{"trashedProjectHeadingID", `[{"type":"to-do","attributes":{"title":"Buy oat milk","heading-id":"head-bin"}}]`,
			[]createdRow{{uuid: "other", title: "Buy oat milk", extra: `project = 'proj-bin'`}, {uuid: "mine", title: "Buy oat milk", extra: `heading = 'head-bin'`}}, []string{"mine"}},
		// Things does not trim an id, so one with surrounding space matches
		// nothing, and a null one is no id at all.
		{"paddedListID", `[{"type":"to-do","attributes":{"title":"Buy oat milk","list-id":" proj-1 "}}]`,
			[]createdRow{{uuid: "other", title: "Buy oat milk", extra: inProj1}, {uuid: "mine", title: "Buy oat milk"}}, []string{"mine"}},
		{"paddedHeadingID", `[{"type":"to-do","attributes":{"title":"Buy oat milk","heading-id":" head-1 "}}]`,
			[]createdRow{{uuid: "other", title: "Buy oat milk", extra: underHead1}, {uuid: "mine", title: "Buy oat milk"}}, []string{"mine"}},
		{"paddedAreaID", `[{"type":"project","attributes":{"title":"Launch","area-id":" area-2 "}}]`,
			[]createdRow{{uuid: "other", title: "Launch", typ: model.TypeProject, extra: `area = 'area-2'`}, {uuid: "mine", title: "Launch", typ: model.TypeProject}}, []string{"mine"}},
		{"nullListID", `[{"type":"to-do","attributes":{"title":"Buy oat milk","list":"Tools","list-id":null}}]`,
			[]createdRow{{uuid: "other", title: "Buy oat milk"}, {uuid: "mine", title: "Buy oat milk", extra: inProj1}}, []string{"mine"}},
		// A list a project created earlier in the payload carries may be
		// that project or the one the database already had; a project
		// created later is not there yet, so the to-do goes to the Inbox.
		{"listCreatedEarlier", `[{"type":"project","attributes":{"title":"tools"}},{"type":"to-do","attributes":{"title":"Buy oat milk","list":"Tools"}}]`,
			[]createdRow{{uuid: "new-p", title: "tools", typ: model.TypeProject}, {uuid: "mine", title: "Buy oat milk", extra: `project = 'new-p'`}}, []string{"new-p", "mine"}},
		{"listCreatedEarlierExisting", `[{"type":"project","attributes":{"title":"tools"}},{"type":"to-do","attributes":{"title":"Buy oat milk","list":"Tools"}}]`,
			[]createdRow{{uuid: "new-p", title: "tools", typ: model.TypeProject}, {uuid: "mine", title: "Buy oat milk", extra: inProj1}}, []string{"new-p", "mine"}},
		{"listCreatedLater", `[{"type":"to-do","attributes":{"title":"Buy oat milk","list":"Shed"}},{"type":"project","attributes":{"title":"Shed"}}]`,
			[]createdRow{{uuid: "new-p", title: "Shed", typ: model.TypeProject}, {uuid: "other", title: "Buy oat milk", extra: `project = 'new-p'`}}, []string{"not-found", "new-p"}},
		// A title two areas share fits the one whose uuid sorts first,
		// which Things picks, and not the other.
		{"sharedAreaTitle", `[{"type":"to-do","attributes":{"title":"Buy oat milk","list":"Personal"}}]`,
			[]createdRow{{uuid: "mine", title: "Buy oat milk", extra: `area = 'area-1'`}}, []string{"mine"}},
		{"sharedAreaTitleOther", `[{"type":"to-do","attributes":{"title":"Buy oat milk","list":"Personal"}}]`,
			[]createdRow{{uuid: "mine", title: "Buy oat milk", extra: `area = 'area-3'`}}, []string{"not-found"}},
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
			fx.Heading("head-old", "Old", 2, dbtest.InProject("proj-1"), dbtest.Trashed())
			fx.Heading("head-twin", "SETUP", 4, dbtest.InProject("proj-1"))
			fx.Project("proj-done", "Done", 7, dbtest.Completed(model.TimeToUnix(testNow.Add(-48*time.Hour))))
			fx.Project("proj-bin", "Bin", 8, dbtest.Trashed())
			fx.Heading("head-bin", "Shelf", 3, dbtest.InProject("proj-bin"))
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

// The import warns about each item Things will not file where the payload
// asks, as add does, under --no-verify too, and not about a list a project
// created earlier in the payload carries.
func TestImportWarnsAboutDestinations(t *testing.T) {
	database, sqlDB := seedWritable(t)
	fx := dbtest.NewFixture(t, sqlDB)
	fx.Project("proj-1", "Tools", 5)
	fx.Project("proj-2", "Garden", 6)
	fx.Heading("head-1", "Plan", 1, dbtest.InProject("proj-1"))
	stubExecDropping(t)

	payload := `[
	  {"type":"to-do","attributes":{"title":"a","list":"Nowhere"}},
	  {"type":"to-do","attributes":{"title":"b","list":"Tools","list-id":""}},
	  {"type":"to-do","attributes":{"title":"c","list-id":"nope"}},
	  {"type":"to-do","attributes":{"title":"d","list":"Tools","heading":"Setup","heading-id":"nope"}},
	  {"type":"to-do","attributes":{"title":"e","list":"Tools","heading":"Setup"}},
	  {"type":"project","attributes":{"title":"Shed","area":"Nowhere"}},
	  {"type":"project","attributes":{"title":"Barn","area-id":"nope"}},
	  {"type":"to-do","attributes":{"title":"f","list":"shed"}},
	  {"type":"to-do","attributes":{"title":"g","list":"Garden","heading-id":"head-1"}},
	  {"type":"to-do","attributes":{"title":"h","list-id":"proj-2","heading-id":"head-1"}},
	  {"type":"to-do","attributes":{"title":"i","list":"proj-1"}},
	  {"type":"to-do","attributes":{"title":"j","list":" shed"}},
	  {"type":"to-do","attributes":{"title":"k","list":"Tools","heading-id":"head-1"}},
	  {"type":"to-do","attributes":{"title":"l","list-id":"","heading-id":"head-1"}},
	  {"type":"to-do","attributes":{"title":"m","list-id":" proj-2 ","heading-id":"head-1"}},
	  {"type":"to-do","attributes":{"title":"n","list":"Shed","heading-id":"head-1"}},
	  {"type":"to-do","attributes":{"title":"o","list-id":"proj-1","heading-id":"head-1","heading":"Other"}}
	]`
	_, stderr, err := runImportOut(t, database, payload, "--no-verify")
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	for _, want := range []string{
		`[0]: Things finds no project or area called "Nowhere"; it will put the to-do in the Inbox`,
		`[1]: list-id is empty; Things will put the to-do in the Inbox`,
		`[2]: Things finds no project or area with id "nope"; it will put the to-do in the Inbox`,
		`[3]: Things has no heading with id "nope"; it will ignore heading-id and heading`,
		`[4]: "Tools" has no heading "Setup"; Things will add the to-do there without a heading`,
		`[5]: Things finds no area called "Nowhere"; it will create the project in no area`,
		`[6]: Things finds no area with id "nope"; it will create the project in no area`,
		`[8]: heading-id "head-1" is in another project; Things will file the to-do there and ignore list "Garden"`,
		`[9]: heading-id "head-1" is in another project; Things will file the to-do there and ignore list-id`,
		`[10]: list "proj-1" is an id, and Things matches list by title only`,
		`[11]: Things finds no project or area called " shed"`,
		`[15]: heading-id "head-1" is in another project; Things will file the to-do there and ignore list "Shed"`,
		`[16]: Things will file the to-do under the heading heading-id "head-1" names and ignore heading "Other"`,
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr missing %q:\n%s", want, stderr)
		}
	}
	if strings.Contains(stderr, "[7]") {
		t.Errorf("warned about a list the payload creates:\n%s", stderr)
	}
	// A heading-id in the list the payload names, or with an empty or
	// padded list-id Things cannot find, files the to-do nowhere else.
	for _, quiet := range []string{"[12]", "[13]", "[14]"} {
		if strings.Contains(stderr, quiet) {
			t.Errorf("warned about %s:\n%s", quiet, stderr)
		}
	}
}

// A to-do sent to the title an earlier update item renames a project to may
// go to that project, which the database knows by its old title until the
// import lands.
func TestImportListRenamedEarlier(t *testing.T) {
	fastVerify(t)
	database, sqlDB := seedWritable(t)
	fx := dbtest.NewFixture(t, sqlDB)
	fx.Project("proj-2", "Garden", 6)
	prev := things.SetExecCommandForTest(func(string, ...string) *exec.Cmd {
		now := model.TimeToUnix(time.Now())
		if _, err := sqlDB.Exec(`UPDATE TMTask SET title = 'Shed' WHERE uuid = 'proj-2'`); err != nil {
			t.Errorf("simulating Things: %v", err)
		}
		if _, err := sqlDB.Exec(`INSERT INTO TMTask (uuid, title, type, status, trashed, start, creationDate, userModificationDate, project) VALUES ('mine', 'Buy oat milk', 0, 0, 0, 0, ?, ?, 'proj-2')`, now, now); err != nil {
			t.Errorf("simulating Things: %v", err)
		}
		return exec.Command("true")
	})
	t.Cleanup(func() { things.SetExecCommandForTest(prev) })

	payload := `[{"type":"project","operation":"update","id":"proj-2","attributes":{"title":"Shed"}},{"type":"to-do","attributes":{"title":"Buy oat milk","list":"Shed"}}]`
	out, stderr, err := runImportOut(t, database, payload, "--json")
	if err != nil {
		t.Fatalf("import: %v\n%s", err, stderr)
	}
	got := decodeCreated(t, out)
	if len(got) != 1 || got[0].UUID != "mine" {
		t.Errorf("got %+v, want the to-do confirmed as mine", got)
	}
	if strings.Contains(stderr, "Shed") {
		t.Errorf("warned about the renamed list:\n%s", stderr)
	}
}

// Things rejects the whole payload over a destination that is neither a
// string nor null, so the import refuses it before sending anything.
func TestImportRefusesNonStringDestination(t *testing.T) {
	database, _ := seedWritable(t)
	calls := stubExecDropping(t)

	payload := `[
	  {"type":"to-do","attributes":{"title":"a","list":"Tools","list-id":5}},
	  {"type":"project","attributes":{"title":"b","area-id":true,"area":null}},
	  {"type":"to-do","attributes":{"title":"c","list":null,"heading":null}}
	]`
	err := runWith(t, database, "--json", "import", "--file", importPayload(t, payload))
	if *calls != 0 {
		t.Errorf("payload was sent (%d calls)", *calls)
	}
	p, raw := decodePayload(t, err)
	if p.Error != "import refused" || len(p.Items) != 2 {
		t.Fatalf("got %s, want import refused naming [0] and [1]", raw)
	}
	for i, want := range []string{"list-id", "area-id"} {
		if it := p.Items[i]; strings.Join(it.Blocked, ",") != want || it.Reason != "invalid-type" {
			t.Errorf("item %d = %+v, want %s blocked as invalid-type", i, it, want)
		}
	}
	if !strings.Contains(p.Message, `[0] list-id: 5`) {
		t.Errorf("message does not name the value:\n%s", p.Message)
	}
}

// The import gives add's notes about where Things files an item, once per
// list or area however many items go there.
func TestImportNotesTargets(t *testing.T) {
	database, sqlDB := seedWritable(t)
	fx := dbtest.NewFixture(t, sqlDB)
	fx.Project("proj-a", "Tools", 5)
	fx.Project("proj-b", "TOOLS", 6)
	fx.Project("proj-done", "Done", 7, dbtest.Completed(model.TimeToUnix(testNow.Add(-48*time.Hour))))
	fx.Project("proj-bin", "Bin", 8, dbtest.Trashed())
	fx.Area("area-1", "Home", 1)
	fx.Area("area-2", "home", 2)
	stubExecDropping(t)

	payload := `[
	  {"type":"to-do","attributes":{"title":"a","list":"tools"}},
	  {"type":"to-do","attributes":{"title":"b","list":"tools"}},
	  {"type":"to-do","attributes":{"title":"c","list-id":"proj-done"}},
	  {"type":"to-do","attributes":{"title":"d","list-id":"proj-bin"}},
	  {"type":"project","attributes":{"title":"e","area":"HOME"}}
	]`
	_, stderr, err := runImportOut(t, database, payload, "--no-verify")
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	for _, want := range []string{
		`note: [0]: several lists are called "tools"; Things will use "Tools" (proj-a); pass a UUID to choose`,
		`note: [2]: "Done" is completed; Things will file into it and reopen it`,
		`note: [3]: "Bin" is in the Trash; Things will file into it there`,
		`note: [4]: several areas are called "HOME"; Things will use "Home" (area-1); pass a UUID to choose`,
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr missing %q:\n%s", want, stderr)
		}
	}
	if strings.Contains(stderr, "note: [1]") {
		t.Errorf("noted the same list twice:\n%s", stderr)
	}
}

// A to-do sent to the old title of a project an earlier update item renames
// is left unchecked: where Things files it was not measured, and failing it
// when it lands in the Inbox would invite a duplicate on retry.
func TestImportListOldTitleAfterRename(t *testing.T) {
	fastVerify(t)
	database, sqlDB := seedWritable(t)
	fx := dbtest.NewFixture(t, sqlDB)
	fx.Project("proj-2", "Garden", 6)
	prev := things.SetExecCommandForTest(func(string, ...string) *exec.Cmd {
		now := model.TimeToUnix(time.Now())
		if _, err := sqlDB.Exec(`UPDATE TMTask SET title = 'Shed' WHERE uuid = 'proj-2'`); err != nil {
			t.Errorf("simulating Things: %v", err)
		}
		if _, err := sqlDB.Exec(`INSERT INTO TMTask (uuid, title, type, status, trashed, start, creationDate, userModificationDate) VALUES ('mine', 'Buy oat milk', 0, 0, 0, 0, ?, ?)`, now, now); err != nil {
			t.Errorf("simulating Things: %v", err)
		}
		return exec.Command("true")
	})
	t.Cleanup(func() { things.SetExecCommandForTest(prev) })

	payload := `[{"type":"project","operation":"update","id":"proj-2","attributes":{"title":"Shed"}},{"type":"to-do","attributes":{"title":"Buy oat milk","list":"Garden"}}]`
	out, stderr, err := runImportOut(t, database, payload, "--json")
	if err != nil {
		t.Fatalf("import: %v\n%s", err, stderr)
	}
	if got := decodeCreated(t, out); len(got) != 1 || got[0].UUID != "mine" {
		t.Errorf("got %+v, want the to-do confirmed as mine", got)
	}
}

// A created item the payload completes or cancels with a completion-date is
// saved with that stopDate. One saved without it fails the import, keeping
// its uuid and not asking for it to be created again; an open item's
// completion-date, which Things ignores, is not checked.
func TestImportChecksCompletionDate(t *testing.T) {
	payload := `[
	  {"type":"to-do","attributes":{"title":"Done","completed":true,"completion-date":"2026-10-05T12:30:00+02:00"}},
	  {"type":"to-do","attributes":{"title":"Open","completion-date":"2026-10-05T10:30:00Z"}}
	]`
	for _, tc := range []struct {
		name string
		stop string
		fail bool
	}{
		{"applied", "1791196200", false},
		{"withinASecond", "1791196200.6", false},
		{"notSettled", "NULL", false},
		{"dropped", "1791200000", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fastVerify(t)
			database, sqlDB := seedWritable(t)
			stubExecAdding(t, sqlDB,
				createdRow{uuid: "done-1", title: "Done", extra: `status = 3, stopDate = ` + tc.stop},
				createdRow{uuid: "open-1", title: "Open"})
			out, _, err := runImportOut(t, database, payload, "--json")
			if !tc.fail {
				if err != nil {
					t.Fatalf("import: %v", err)
				}
				if got := decodeCreated(t, out); len(got) != 2 || !got[0].Confirmed || !got[1].Confirmed {
					t.Errorf("got %+v, want both confirmed", got)
				}
				return
			}
			var verr *importVerifyError
			if !errors.As(err, &verr) {
				t.Fatalf("err = %v, want an importVerifyError", err)
			}
			items := verr.jsonItems()
			if len(items) != 1 || items[0].Path != "[0]" || items[0].ID != "done-1" || items[0].Reason != dateDropped {
				t.Errorf("items = %+v, want [0] done-1 %s", items, dateDropped)
			}
			if msg := err.Error(); !strings.Contains(msg, "do not import them again") || strings.Contains(msg, "did not appear") {
				t.Errorf("message:\n%s", msg)
			}
		})
	}
}

// Things rejects the whole payload over a bad date on a heading or a
// checklist item too, so the import refuses it before sending anything.
func TestImportRefusesBadDateOnHeadingOrChecklistItem(t *testing.T) {
	database := seedFullDB(t)
	captured := stubExec(t)

	payload := `[{"type":"project","attributes":{"title":"P","items":[
	  {"type":"heading","attributes":{"title":"H","creation-date":"2026-10-05"}},
	  {"type":"to-do","attributes":{"title":"T","checklist-items":[
	    {"type":"checklist-item","attributes":{"title":"C","completed":true,"completion-date":"2026-10-05"}}
	  ]}}
	]}}]`
	_, _, err := runImportOut(t, database, payload, "--no-verify")
	if err == nil {
		t.Fatal("expected the payload to be refused")
	}
	for _, want := range []string{
		`[0].attributes.items[0] creation-date: "2026-10-05"`,
		`[0].attributes.items[1].attributes.checklist-items[0] completion-date: "2026-10-05"`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error missing %q:\n%v", want, err)
		}
	}
	if len(*captured) != 0 {
		t.Errorf("payload was sent: %v", *captured)
	}
}

// A to-do in the items of a project the payload creates goes in that
// project whatever list or heading it names, so it is checked against the
// project and the import warns that the rest is ignored.
func TestImportNestedIgnoresOwnDestination(t *testing.T) {
	fastVerify(t)
	database, sqlDB := seedWritable(t)
	fx := dbtest.NewFixture(t, sqlDB)
	fx.Project("proj-1", "Tools", 5)
	stubExecAdding(t, sqlDB,
		createdRow{uuid: "new-p", title: "Launch", typ: model.TypeProject},
		createdRow{uuid: "mine", title: "Buy oat milk", extra: `project = 'new-p'`})

	payload := `[{"type":"project","attributes":{"title":"Launch","items":[
	  {"type":"to-do","attributes":{"title":"Buy oat milk","list-id":"proj-1","heading":"Setup","list":null,"area":"Home"}}
	]}}]`
	out, stderr, err := runImportOut(t, database, payload, "--json")
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if got := decodeCreated(t, out); len(got) != 2 || got[1].UUID != "mine" {
		t.Errorf("got %+v, want the nested to-do confirmed in the new project", got)
	}
	if want := `[0].attributes.items[0]: Things files a to-do in a project's items in that project and ignores its list-id, heading`; !strings.Contains(stderr, want) || strings.Contains(stderr, "area") {
		t.Errorf("stderr missing %q:\n%s", want, stderr)
	}
}

// A heading-id gives the same notes as a list-id about its project, but a
// to-do the payload closes itself leaves a closed project closed, so it gets
// no note that the project reopens.
func TestImportNotesHeadingIDAndClosedItems(t *testing.T) {
	database, sqlDB := seedWritable(t)
	fx := dbtest.NewFixture(t, sqlDB)
	fx.Project("proj-done", "Done", 7, dbtest.Completed(model.TimeToUnix(testNow.Add(-48*time.Hour))))
	fx.Project("proj-bin", "Bin", 8, dbtest.Trashed())
	fx.Heading("head-done", "Setup", 1, dbtest.InProject("proj-done"))
	fx.Heading("head-bin", "Shelf", 2, dbtest.InProject("proj-bin"))
	stubExecDropping(t)

	payload := `[
	  {"type":"to-do","attributes":{"title":"a","heading-id":"head-bin"}},
	  {"type":"to-do","attributes":{"title":"b","list-id":"proj-done","completed":true}},
	  {"type":"to-do","attributes":{"title":"c","heading-id":"head-done"}}
	]`
	_, stderr, err := runImportOut(t, database, payload, "--no-verify")
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	for _, want := range []string{
		`note: [0]: "Bin" is in the Trash; Things will file into it there`,
		`note: [2]: "Done" is completed; Things will file into it and reopen it`,
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr missing %q:\n%s", want, stderr)
		}
	}
	if strings.Contains(stderr, "note: [1]") {
		t.Errorf("noted a reopen for a to-do the payload completes:\n%s", stderr)
	}
}

// Only the items of a project the payload creates were measured to ignore
// their own destination, so a to-do in an updated project's items is not
// warned about.
func TestImportNestedInUpdateNotWarned(t *testing.T) {
	database, sqlDB := seedWritable(t)
	fx := dbtest.NewFixture(t, sqlDB)
	fx.Project("proj-1", "Tools", 5)
	stubExecDropping(t)

	payload := `[{"type":"project","operation":"update","id":"proj-1","attributes":{"items":[
	  {"type":"to-do","attributes":{"title":"a","list":"Garden","area":"Home"}}
	]}}]`
	_, stderr, err := runImportOut(t, database, payload, "--no-verify")
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if strings.Contains(stderr, "ignores its") {
		t.Errorf("warned about an updated project's item:\n%s", stderr)
	}
}

// An item is confirmed only where its when files it, as an add is. A keyword
// or date that did not land fails the import as misfiled, keeping the uuid
// and saying where the item is; a free phrase Things ignored leaves the item
// unconfirmed with reason when, and the import exits 0, as add does. A
// to-do given when today and a deadline already past is the measured case:
// Things files it in the Inbox with no start date.
func TestImportChecksWhen(t *testing.T) {
	past := time.Now().AddDate(0, 0, -4).Format("2006-01-02")
	today := "start = 1, startDate = " + strconv.Itoa(int(model.ThingsDateFromTime(testNow)))
	for _, tc := range []struct {
		name    string
		attrs   string
		extra   string // the row as Things saved it; "" is the Inbox with no start date
		fail    bool
		reason  string
		landed  string
		message string
	}{
		{name: "todayPastDeadlineInInbox", attrs: `"when":"today","deadline":"` + past + `"`, fail: true, reason: "misfiled", landed: "in inbox with no start date",
			message: `[0]: task "Pay rent" (new-1) was created, but its when "today" did not file it there: it is in inbox with no start date`},
		{name: "phraseIgnored", attrs: `"when":"blorp"`, reason: "when", landed: "in inbox with no start date",
			message: `Created, but Things did not understand its when: [0] "Pay rent" (new-1), in inbox with no start date`},
		{name: "todayLanded", attrs: `"when":"today","deadline":"` + past + `"`, extra: today},
		{name: "noWhen", attrs: `"deadline":"` + past + `"`},
		{name: "closedNotChecked", attrs: `"when":"today","completed":true`, extra: "status = 3"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fastVerify(t)
			payload := `[{"type":"to-do","attributes":{"title":"Pay rent",` + tc.attrs + `}}]`
			for _, asJSON := range []bool{false, true} {
				database, sqlDB := seedWritable(t)
				stubExecAdding(t, sqlDB, createdRow{uuid: "new-1", title: "Pay rent", extra: tc.extra})
				var args []string
				if asJSON {
					args = []string{"--json"}
				}
				out, stderr, err := runImportOut(t, database, payload, args...)
				if tc.fail {
					var verr *importVerifyError
					if !errors.As(err, &verr) {
						t.Fatalf("json=%v: err = %v, want an importVerifyError", asJSON, err)
					}
					if asJSON {
						p, raw := decodePayload(t, err)
						it := findItem(t, p.Items, "[0]")
						if p.Error != "import partially applied" || it.ID != "new-1" || it.Reason != tc.reason || it.Landed != tc.landed {
							t.Errorf("payload = %s, want [0] new-1 %s landed %q", raw, tc.reason, tc.landed)
						}
						if len(p.Created) != 1 || p.Created[0].Confirmed || p.Created[0].Landed != tc.landed {
							t.Errorf("created = %+v", p.Created)
						}
						continue
					}
					msg := err.Error()
					for _, want := range []string{tc.message, "do not import them again", "things edit <uuid> --when"} {
						if !strings.Contains(msg, want) {
							t.Errorf("error missing %q:\n%s", want, msg)
						}
					}
					if strings.Contains(msg, "did not appear") {
						t.Errorf("a misfiled item is not missing:\n%s", msg)
					}
					continue
				}
				if err != nil {
					t.Fatalf("json=%v: import: %v (stderr: %s)", asJSON, err, stderr)
				}
				if !asJSON {
					if tc.reason != "" && !strings.Contains(out, tc.message) {
						t.Errorf("output = %q, want %q", out, tc.message)
					}
					continue
				}
				got := decodeCreated(t, out)
				if len(got) != 1 || got[0].UUID != "new-1" || got[0].Confirmed != (tc.reason == "") || got[0].Reason != tc.reason || got[0].Landed != tc.landed {
					t.Errorf("got %+v, want reason %q landed %q", got, tc.reason, tc.landed)
				}
			}
		})
	}
}

// A to-do in the items of a project the payload completes or cancels is not
// checked against its when, as a closed item is not: Things was not measured
// filing one. In an open project the same row is misfiled.
func TestImportSkipsWhenInClosedProject(t *testing.T) {
	for _, tc := range []struct {
		name, closed string
		misfiled     bool
	}{
		{"completed", `"completed":true,`, false},
		{"canceled", `"canceled":true,`, false},
		{"open", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fastVerify(t)
			database, sqlDB := seedWritable(t)
			project := createdRow{uuid: "new-p", title: "Launch", typ: model.TypeProject}
			if tc.closed != "" {
				project.extra = "status = 3"
			}
			stubExecAdding(t, sqlDB, project,
				createdRow{uuid: "new-1", title: "Book venue", extra: `project = 'new-p', start = 1`})
			payload := `[{"type":"project","attributes":{"title":"Launch",` + tc.closed + `"items":[
			  {"type":"to-do","attributes":{"title":"Book venue","when":"today"}}
			]}}]`
			out, _, err := runImportOut(t, database, payload, "--json")
			if tc.misfiled {
				var verr *importVerifyError
				if !errors.As(err, &verr) || verr.created[1].Reason != whenMisfiled {
					t.Fatalf("err = %v, want [0].attributes.items[0] misfiled", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("import: %v", err)
			}
			if got := decodeCreated(t, out); len(got) != 2 || !got[0].Confirmed || !got[1].Confirmed {
				t.Errorf("got %+v, want both confirmed", got)
			}
		})
	}
}
