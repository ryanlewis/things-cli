package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ryanlewis/things-cli/internal/model"
)

// Every payload shape Things was seen to reject with an error sheet, creating
// nothing, or to save as an untitled to-do, is refused before anything is
// sent: one --json object naming the item, the attributes and the reason.
func TestImportRefusesShapesThingsRejects(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		path    string
		blocked string
		reason  string
		message string // a line the plain-text refusal must carry
	}{
		// Dates with the shape that name no instant.
		{"month 13", `[{"type":"to-do","attributes":{"title":"a","creation-date":"2026-13-01T10:00:00Z"}}]`,
			"[0]", "creation-date", "invalid-date", `[0] creation-date: "2026-13-01T10:00:00Z"`},
		{"offset +200", `[{"type":"to-do","attributes":{"title":"a","creation-date":"2026-10-01T10:00:00+200"}}]`,
			"[0]", "creation-date", "invalid-date", `[0] creation-date: "2026-10-01T10:00:00+200"`},
		{"completion-date month 13", `[{"type":"to-do","attributes":{"title":"a","completed":true,"completion-date":"2026-13-01T10:00:00Z"}}]`,
			"[0]", "completion-date", "invalid-date", `completion-date: "2026-13-01T10:00:00Z"`},

		// Items the read-back cannot classify.
		{"title a number", `[{"type":"to-do","attributes":{"title":12345}}]`,
			"[0]", "title", "invalid-type", `[0] title: 12345`},
		{"unknown type", `[{"type":"todo","attributes":{"title":"a"}}]`,
			"[0]", "type", "invalid-item", `[0] type: "todo" is not to-do or project`},
		{"no attributes", `[{"type":"to-do"}]`,
			"[0]", "attributes", "invalid-item", `[0] attributes: missing`},
		{"null attributes", `[{"type":"to-do","attributes":null}]`,
			"[0]", "attributes", "invalid-item", `[0] attributes: missing`},
		{"attributes a string", `[{"type":"to-do","attributes":"title"}]`,
			"[0]", "attributes", "invalid-type", `[0] attributes: "title"`},
		{"heading at the top level", `[{"type":"heading","attributes":{"title":"a"}}]`,
			"[0]", "type", "invalid-item", `[0] type: "heading" is not allowed at the top level, only to-do or project`},
		{"capitalised operation", `[{"type":"to-do","operation":"Create","attributes":{"title":"a"}}]`,
			"[0]", "operation", "invalid-item", `[0] operation: "Create" is not create or update`},
		{"operation a number", `[{"type":"to-do","operation":1,"attributes":{"title":"a"}}]`,
			"[0]", "operation", "invalid-item", `[0] operation: 1 is not create or update`},
		{"bare string item", `["buy milk"]`,
			"[0]", "", "invalid-item", `[0] not an object: "buy milk"`},
		{"padded type", `[{"type":"to-do ","attributes":{"title":"a"}}]`,
			"[0]", "type", "invalid-item", `[0] type: "to-do " is not to-do or project`},
		{"type a number", `[{"type":1,"attributes":{"title":"a"}}]`,
			"[0]", "type", "invalid-item", `[0] type: 1 is not to-do or project`},

		// Blank titles, which Things saves as untitled to-dos.
		{"no title", `[{"type":"to-do","attributes":{"notes":"x"}}]`,
			"[0]", "title", "blank-title", "  [0]"},
		{"whitespace title", `[{"type":"to-do","attributes":{"title":"   "}}]`,
			"[0]", "title", "blank-title", "  [0]"},
		{"null title", `[{"type":"project","attributes":{"title":null}}]`,
			"[0]", "title", "blank-title", "  [0]"},

		// Attributes of a kind Things rejects.
		{"tags a number", `[{"type":"to-do","attributes":{"title":"a","tags":3}}]`, "[0]", "tags", "invalid-type", `[0] tags: 3`},
		{"tags a string", `[{"type":"to-do","attributes":{"title":"a","tags":"x"}}]`, "[0]", "tags", "invalid-type", `[0] tags: "x"`},
		{"tags with a number", `[{"type":"to-do","attributes":{"title":"a","tags":["x",1]}}]`, "[0]", "tags", "invalid-type", `[0] tags: ["x",1]`},
		{"notes a number", `[{"type":"to-do","attributes":{"title":"a","notes":1}}]`, "[0]", "notes", "invalid-type", `[0] notes: 1`},
		{"when a number", `[{"type":"to-do","attributes":{"title":"a","when":1}}]`, "[0]", "when", "invalid-type", `[0] when: 1`},
		{"deadline a number", `[{"type":"to-do","attributes":{"title":"a","deadline":1}}]`, "[0]", "deadline", "invalid-type", `[0] deadline: 1`},
		{"completed 1", `[{"type":"to-do","attributes":{"title":"a","completed":1}}]`, "[0]", "completed", "invalid-type", `[0] completed: 1`},
		{"completed \"true\"", `[{"type":"to-do","attributes":{"title":"a","completed":"true"}}]`, "[0]", "completed", "invalid-type", `[0] completed: "true"`},
		{"canceled 1", `[{"type":"to-do","attributes":{"title":"a","canceled":1}}]`, "[0]", "canceled", "invalid-type", `[0] canceled: 1`},
		{"canceled \"true\"", `[{"type":"project","attributes":{"title":"a","canceled":"true"}}]`, "[0]", "canceled", "invalid-type", `[0] canceled: "true"`},
		{"checklist-items a string", `[{"type":"to-do","attributes":{"title":"a","checklist-items":"x"}}]`, "[0]", "checklist-items", "invalid-type", `[0] checklist-items: "x"`},
		{"items a string", `[{"type":"project","attributes":{"title":"a","items":"x"}}]`, "[0]", "items", "invalid-type", `[0] items: "x"`},

		// Items Things does not allow where they sit.
		{"project in a project's items", `[{"type":"project","attributes":{"title":"P","items":[{"type":"project","attributes":{"title":"Q"}}]}}]`,
			"[0].attributes.items[0]", "type", "invalid-item", `[0].attributes.items[0] type: "project" is not allowed in a project's items, only to-do or heading`},
		{"to-do in checklist-items", `[{"type":"to-do","attributes":{"title":"T","checklist-items":[{"type":"to-do","attributes":{"title":"c"}}]}}]`,
			"[0].attributes.checklist-items[0]", "type", "invalid-item", `[0].attributes.checklist-items[0] type: "to-do" is not allowed in a to-do's checklist-items, only checklist-item`},
		{"bare string in items", `[{"type":"project","attributes":{"title":"P","items":["x"]}}]`,
			"[0].attributes.items[0]", "", "invalid-item", `[0].attributes.items[0] not an object: "x"`},
		{"blank to-do in items", `[{"type":"project","attributes":{"title":"P","items":[{"type":"to-do","attributes":{"title":""}}]}}]`,
			"[0].attributes.items[0]", "title", "blank-title", "  [0].attributes.items[0]"},
		{"unknown type in an update's items", `[{"type":"project","operation":"update","id":"proj-1","attributes":{"items":[{"type":"todo","attributes":{"title":"x"}}]}}]`,
			"[0].attributes.items[0]", "type", "invalid-item", `type: "todo" is not to-do or heading`},
		{"update with a padded type", `[{"type":"to-do ","operation":"update","id":"one-1","attributes":{"title":"x"}}]`,
			"[0]", "type", "invalid-item", `[0] (id one-1) type: "to-do " is not to-do or project or heading or checklist-item`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			database, _ := seedWritable(t)
			calls := stubExecDropping(t)

			err := runWith(t, database, "--json", "import", "--file", importPayload(t, c.payload))
			if *calls != 0 {
				t.Errorf("payload was sent to Things (%d calls)", *calls)
			}
			p, raw := decodePayload(t, err)
			if p.Error != "import refused" || len(p.Items) != 1 {
				t.Fatalf("got %s, want import refused with one item", raw)
			}
			got := p.Items[0]
			if got.Path != c.path || strings.Join(got.Blocked, ",") != c.blocked || got.Reason != c.reason {
				t.Errorf("item = %+v, want path %s blocked [%s] reason %s", got, c.path, c.blocked, c.reason)
			}
			if !strings.Contains(p.Message, c.message) || !strings.Contains(p.Message, "Nothing was sent to Things") {
				t.Errorf("message missing %q:\n%s", c.message, p.Message)
			}
			if p.Reason != "" {
				t.Errorf("whole-payload reason = %q, want none", p.Reason)
			}
		})
	}
}

// One run names every problem on an item and every offending item, in
// payload order, so a single fix-up covers them all.
func TestImportShapeRefusalsCombine(t *testing.T) {
	database, _ := seedWritable(t)
	calls := stubExecDropping(t)

	payload := `[
	  {"type":"to-do","attributes":{"title":"fine"}},
	  {"type":"to-do","attributes":{"title":" ","tags":"x","list":3,"creation-date":"2026-13-01T10:00:00Z"}},
	  7,
	  {"type":"todo","operation":"Create","attributes":{"title":"a"}}
	]`
	err := runWith(t, database, "--json", "import", "--file", importPayload(t, payload))
	if *calls != 0 {
		t.Errorf("payload was sent to Things (%d calls)", *calls)
	}
	p, raw := decodePayload(t, err)
	want := []jsonErrorItem{
		{Path: "[1]", Blocked: []string{"creation-date", "list", "tags", "title"}, Reason: "invalid-date invalid-type blank-title"},
		{Path: "[2]", Reason: "invalid-item"},
		{Path: "[3]", Title: "a", Blocked: []string{"operation", "type"}, Reason: "invalid-item"},
	}
	if len(p.Items) != len(want) {
		t.Fatalf("got %s, want %d items", raw, len(want))
	}
	for i, w := range want {
		got := p.Items[i]
		if got.Path != w.Path || got.Title != w.Title || strings.Join(got.Blocked, ",") != strings.Join(w.Blocked, ",") || got.Reason != w.Reason {
			t.Errorf("item %d = %+v, want %+v", i, got, w)
		}
	}
}

// Shapes Things takes are not refused, and every to-do and project the
// payload creates gets a read-back record: none is dropped silently.
func TestImportAcceptedShapesAreAllReadBack(t *testing.T) {
	cases := []struct {
		name    string
		payload string
	}{
		{"trailing space after a date", `[{"type":"to-do","attributes":{"title":"a","creation-date":"2026-10-08T10:00:00Z "}}]`},
		{"offset with seconds", `[{"type":"to-do","attributes":{"title":"a","creation-date":"2026-10-08T10:00:00+01:00:00"}}]`},
		{"hour 25", `[{"type":"to-do","attributes":{"title":"a","creation-date":"2026-10-05T25:30:00Z"}}]`},
		{"null attributes", `[{"type":"to-do","attributes":{"title":"a","notes":null,"tags":null,"when":null,"completed":null,"checklist-items":null,"operation":null}}]`},
		{"explicit create", `[{"type":"project","operation":"create","attributes":{"title":"P","tags":["x"],"completed":false}}]`},
		{"nested", createPayload},
		{"update with nested creates", `[
		  {"type":"project","operation":"update","id":"proj-1","attributes":{"items":[
		    {"type":"heading","attributes":{"title":"H"}},
		    {"type":"to-do","attributes":{"title":"a","checklist-items":[{"type":"checklist-item","attributes":{"title":"c","completed":true}}]}}
		  ]}},
		  {"type":"checklist-item","operation":"update","id":"chk-1","attributes":{"completed":true}},
		  {"type":"to-do","operation":"update","id":"one-1"},
		  {"type":"to-do","attributes":{"title":"b"}}
		]`},
		{"many", manyTodos(maxImportItems)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			database, _ := seedWritable(t)
			payload := decodeImport(t, c.payload)
			plan, err := prepareImport(database, payload)
			if err != nil {
				t.Fatalf("prepareImport: %v", err)
			}
			if got, want := len(plan.creates), countCreates(payload); got != want {
				t.Errorf("%d read-back records, want one for each of the %d created to-dos and projects", got, want)
			}
		})
	}
}

// countCreates counts the to-dos and projects payload creates, at the top
// level and in a project's items, walking the payload apart from the code
// under test.
func countCreates(payload []any) int {
	n := 0
	for _, raw := range payload {
		item := raw.(map[string]any)
		attrs, _ := item["attributes"].(map[string]any)
		if item["operation"] != "update" {
			n++
		}
		if item["type"] == "project" {
			sub, _ := attrs["items"].([]any)
			for _, s := range sub {
				if s.(map[string]any)["type"] == "to-do" {
					n++
				}
			}
		}
	}
	return n
}

// manyTodos is a payload of n top-level to-dos.
func manyTodos(n int) string {
	items := make([]string, n)
	for i := range items {
		items[i] = fmt.Sprintf(`{"type":"to-do","attributes":{"title":"t%d"}}`, i)
	}
	return "[" + strings.Join(items, ",") + "]"
}

// Above maxImportItems Things asks before creating anything, which the
// read-back cannot wait for, so the payload is refused with a whole-payload
// reason. Items nested in a project count; checklist items do not.
func TestImportRefusesTooManyItems(t *testing.T) {
	nested := func(todos, checklist int) string {
		items := make([]string, todos)
		chk := make([]string, checklist)
		for i := range chk {
			chk[i] = fmt.Sprintf(`{"type":"checklist-item","attributes":{"title":"c%d"}}`, i)
		}
		for i := range items {
			items[i] = fmt.Sprintf(`{"type":"to-do","attributes":{"title":"t%d","checklist-items":[%s]}}`, i, strings.Join(chk, ","))
		}
		return `[{"type":"project","attributes":{"title":"P","items":[` + strings.Join(items, ",") + `]}}]`
	}
	cases := []struct {
		name    string
		payload string
		refused bool
		size    int
	}{
		{"at the limit", manyTodos(maxImportItems), false, 0},
		{"one over", manyTodos(maxImportItems + 1), true, maxImportItems + 1},
		{"300", manyTodos(300), true, 300},
		{"nested over", nested(maxImportItems, 0), true, maxImportItems + 1},
		{"checklist items not counted", nested(maxImportItems-1, 3), false, 0},
		{"update items not counted", strings.TrimSuffix(manyTodos(maxImportItems), "]") + `,{"type":"to-do","operation":"update","id":"one-1","attributes":{"notes":"x"}}]`, false, 0},
		{"headings counted", strings.Replace(nested(maxImportItems-1, 0), `"items":[`, `"items":[{"type":"heading","attributes":{"title":"H"}},`, 1), true, maxImportItems + 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			database, _ := seedWritable(t)
			calls := stubExecDropping(t)
			_, err := prepareImport(database, decodeImport(t, c.payload))
			if !c.refused {
				if err != nil {
					t.Fatalf("prepareImport: %v", err)
				}
				return
			}
			var refused *importRefusalError
			if !errors.As(err, &refused) || refused.size != c.size || len(refused.items) != 0 {
				t.Fatalf("err = %v, want a size refusal for %d items", err, c.size)
			}
			msg := fmt.Sprintf("The payload creates %d items", c.size)
			if !strings.Contains(err.Error(), msg) || !strings.Contains(err.Error(), "Split it into imports of at most 200 items") {
				t.Errorf("message missing %q:\n%v", msg, err)
			}

			err = runWith(t, database, "--json", "import", "--file", importPayload(t, c.payload))
			if *calls != 0 {
				t.Errorf("payload was sent to Things (%d calls)", *calls)
			}
			p, raw := decodePayload(t, err)
			if p.Error != "import refused" || p.Reason != tooManyItems || len(p.Items) != 0 {
				t.Errorf("got %s, want import refused with reason %s and no items", raw, tooManyItems)
			}
		})
	}
}

// An oversized payload with bad items reports both in one object.
func TestImportTooManyItemsWithBadItems(t *testing.T) {
	database, _ := seedWritable(t)
	stubExecDropping(t)
	payload := strings.TrimSuffix(manyTodos(maxImportItems), "]") + `,{"type":"to-do","attributes":{"title":""}}]`
	err := runWith(t, database, "--json", "import", "--file", importPayload(t, payload))
	p, raw := decodePayload(t, err)
	if p.Reason != tooManyItems || len(p.Items) != 1 || p.Items[0].Reason != "blank-title" {
		t.Errorf("got %s, want reason %s and the blank item", raw, tooManyItems)
	}
}

// An undated to-do sharing a recent dated to-do's title passes once a new
// item has appeared for each of them: both are there, though which is which
// cannot be told, so it is reported unconfirmed and the import exits 0. With
// only one new item, one of them may be missing, and the import still fails,
// saying the item may exist.
func TestImportSharesDatedTitleWithBothPresent(t *testing.T) {
	payload := `[
	  {"type":"to-do","attributes":{"title":"Weekly review","creation-date":"` + time.Now().UTC().Format(time.RFC3339) + `"}},
	  {"type":"to-do","attributes":{"title":"Weekly review"}}
	]`

	t.Run("both appeared", func(t *testing.T) {
		fastVerify(t)
		database, sqlDB := seedWritable(t)
		stubExecAdding(t, sqlDB, createdRow{uuid: "new-1", title: "Weekly review"}, createdRow{uuid: "new-2", title: "Weekly review"})
		out, _, err := runImportOut(t, database, payload, "--json")
		if err != nil {
			t.Fatalf("import: %v", err)
		}
		got := decodeCreated(t, out)
		if len(got) != 2 || got[1].Confirmed || got[1].Reason != "shares-dated-title" || strings.Join(got[1].Candidates, ",") != "new-1,new-2" ||
			got[1].Present == nil || !*got[1].Present {
			t.Errorf("got %+v, want [1] unconfirmed shares-dated-title, present, with both candidates", got)
		}
		if got[0].Present != nil {
			t.Errorf("[0] carries present, want it only on shares-dated-title items: %+v", got[0])
		}
		if !strings.Contains(out, `"present": true`) {
			t.Errorf("--json output lacks \"present\": true:\n%s", out)
		}
	})

	t.Run("one appeared", func(t *testing.T) {
		fastVerify(t)
		database, sqlDB := seedWritable(t)
		stubExecAdding(t, sqlDB, createdRow{uuid: "new-1", title: "Weekly review"})
		_, _, err := runImportOut(t, database, payload)
		var verr *importVerifyError
		if !errors.As(err, &verr) {
			t.Fatalf("err = %v, want an importVerifyError", err)
		}
		if !strings.Contains(err.Error(), "only 1 new item with that title appeared for the 2 items that could be filed there, so this item may exist as one of them") {
			t.Errorf("error does not say the item may exist:\n%v", err)
		}
		if c := verr.created[1]; c.Present == nil || *c.Present {
			t.Errorf("[1] = %+v, want present false", c)
		}
	})
}

// When nothing the payload creates appears, the error says Things may have
// rejected the payload, not only that it may be slow.
func TestImportNothingAppearedSuggestsRejection(t *testing.T) {
	fastVerify(t)
	database, _ := seedWritable(t)
	stubExecDropping(t)
	_, _, err := runImportOut(t, database, `[{"type":"to-do","attributes":{"title":"a"}},{"type":"to-do","attributes":{"title":"b"}}]`)
	if err == nil || !strings.Contains(err.Error(), "None of them appeared: Things may have rejected the whole payload") {
		t.Errorf("err = %v, want the rejected-payload hint", err)
	}

	database, sqlDB := seedWritable(t)
	stubExecAdding(t, sqlDB, createdRow{uuid: "new-1", title: "a"})
	_, _, err = runImportOut(t, database, `[{"type":"to-do","attributes":{"title":"a"}},{"type":"to-do","attributes":{"title":"b"}}]`)
	if err == nil || strings.Contains(err.Error(), "rejected the whole payload") || !strings.Contains(err.Error(), "may be slow to save") {
		t.Errorf("err = %v, want the partial-drop wording when one item appeared", err)
	}
}

// The --json refusal is exactly one object, whatever the shape refused: the
// command prints nothing itself, and the rendered error is one JSON value.
func TestImportShapeRefusalIsOneJSONObject(t *testing.T) {
	database, _ := seedWritable(t)
	stubExecDropping(t)
	out, _, err := runImportOut(t, database, `["x",{"type":"to-do"}]`, "--json")
	if err == nil || out != "" {
		t.Fatalf("err = %v, stdout = %q, want a refusal and nothing printed", err, out)
	}
	var stdout, stderr bytes.Buffer
	renderError(&stdout, &stderr, true, err)
	dec := json.NewDecoder(&stdout)
	n := 0
	for {
		var v any
		if dec.Decode(&v) != nil {
			break
		}
		n++
	}
	if n != 1 || stderr.Len() != 0 {
		t.Errorf("rendered %d JSON values and stderr %q, want one value and no stderr", n, stderr.String())
	}
}

// An undated item with another destination whose new items overlap this
// one's competes for them too, so a dated item's row plus another item's row
// do not count as this item being there.
func TestSharedRowsCountsOtherDestinations(t *testing.T) {
	loose := importCreate{path: "[1]", typ: model.TypeTask, title: "Weekly review"}
	inList := importCreate{path: "[2]", typ: model.TypeTask, title: "Weekly review", dest: createdDest{checked: true, list: "proj-1"}}
	dated := importCreate{path: "[0]", typ: model.TypeTask, title: "Weekly review", dated: true}
	creates := []importCreate{dated, loose, inList}
	datedRow, listRow := model.Task{UUID: "dated-row"}, model.Task{UUID: "list-row"}
	found := map[createdWant][]model.Task{
		loose.want():  {datedRow, listRow},
		inList.want(): {listRow},
	}
	rows, claimants := sharedRows(loose, creates, found, found[loose.want()])
	if len(rows) != 2 || claimants != 2 {
		t.Errorf("rows = %d, claimants = %d, want 2 rows for 2 undated claimants", len(rows), claimants)
	}
}
