package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ryanlewis/things-cli/internal/clock"
	"github.com/ryanlewis/things-cli/internal/model"
)

// shapeNow is the instant the shape tests pin the clock to, so a date
// counts as in the future or the past whenever they run.
var shapeNow = time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)

// pinShapeClock pins the clock to shapeNow for the rest of the test.
func pinShapeClock(t *testing.T) {
	t.Helper()
	t.Cleanup(clock.Pin(shapeNow))
}

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

		// A key given twice: Things keeps the first, the CLI's decoder the
		// last, so the read-back would look for a title Things never saved.
		{"title twice", `[{"type":"to-do","attributes":{"title":"first","title":"second"}}]`,
			"[0]", "title", "duplicate-key", `[0] title: given twice`},
		{"type twice", `[{"type":"to-do","type":"project","attributes":{"title":"a"}}]`,
			"[0]", "type", "duplicate-key", `[0] type: given twice`},
		{"notes twice in a project's items", `[{"type":"project","attributes":{"title":"P","items":[{"type":"to-do","attributes":{"title":"a","notes":"x","notes":"y"}}]}}]`,
			"[0].attributes.items[0]", "notes", "duplicate-key", `[0].attributes.items[0] notes: given twice`},
		{"title three times", `[{"type":"to-do","attributes":{"title":"a","title":"b","title":"c"}}]`,
			"[0]", "title", "duplicate-key", `[0] title: given twice`},

		// Longer than Things keeps: it cuts a title to 4000 characters.
		{"title over 4000", `[{"type":"to-do","attributes":{"title":"` + strings.Repeat("a", 4001) + `"}}]`,
			"[0]", "title", "too-long", `[0] title: 4001 characters, over 4000`},
		{"project title over 4000", `[{"type":"project","attributes":{"title":"` + strings.Repeat("é", 4001) + `"}}]`,
			"[0]", "title", "too-long", `[0] title: 4001 characters, over 4000`},
		{"heading title over 4000", `[{"type":"project","attributes":{"title":"P","items":[{"type":"heading","attributes":{"title":"` + strings.Repeat("a", 4001) + `"}}]}}]`,
			"[0].attributes.items[0]", "title", "too-long", `[0].attributes.items[0] title: 4001 characters, over 4000`},
		{"notes over 10000", `[{"type":"to-do","attributes":{"title":"a","notes":"` + strings.Repeat("a", 10001) + `"}}]`,
			"[0]", "notes", "too-long", `[0] notes: 10001 characters, over 10000`},

		// Attributes Things takes on another type and ignores on this one.
		{"items on a to-do", `[{"type":"to-do","attributes":{"title":"T","items":[{"type":"to-do","attributes":{"title":"child"}}]}}]`,
			"[0]", "items", "invalid-item", `[0] items: Things takes items only on a project, and ignores them on a to-do`},
		{"items a string on a to-do", `[{"type":"to-do","attributes":{"title":"T","items":"x"}}]`,
			"[0]", "items", "invalid-item", `[0] items: Things takes items only on a project, and ignores them on a to-do`},
		{"checklist-items a string on a project", `[{"type":"project","attributes":{"title":"P","checklist-items":"x"}}]`,
			"[0]", "checklist-items", "invalid-item", `[0] checklist-items: Things takes checklist-items only on a to-do, and ignores them on a project`},
		{"checklist-items on a project", `[{"type":"project","attributes":{"title":"P","checklist-items":[{"type":"checklist-item","attributes":{"title":"c"}}]}}]`,
			"[0]", "checklist-items", "invalid-item", `[0] checklist-items: Things takes checklist-items only on a to-do, and ignores them on a project`},

		// Dates in the future, which Things saves as now.
		{"future creation-date", `[{"type":"to-do","attributes":{"title":"a","creation-date":"2026-10-10T10:00:00Z"}}]`,
			"[0]", "creation-date", "future-date", `[0] creation-date: "2026-10-10T10:00:00Z" (2026-10-10T10:00:00Z)`},
		{"hour 25 today", `[{"type":"to-do","attributes":{"title":"a","creation-date":"2026-10-09T25:00:00Z"}}]`,
			"[0]", "creation-date", "future-date", `[0] creation-date: "2026-10-09T25:00:00Z" (2026-10-10T01:00:00Z)`},
		{"future completion-date", `[{"type":"to-do","attributes":{"title":"a","completed":true,"completion-date":"2027-01-01T00:00:00Z"}}]`,
			"[0]", "completion-date", "future-date", `[0] completion-date: "2027-01-01T00:00:00Z" (2027-01-01T00:00:00Z)`},
		{"future creation-date on a project", `[{"type":"project","attributes":{"title":"P","creation-date":"2026-10-09T14:00:00+01:00"}}]`,
			"[0]", "creation-date", "future-date", `[0] creation-date: "2026-10-09T14:00:00+01:00" (2026-10-09T13:00:00Z)`},

		// A when or deadline Things would read as something else.
		{"when month 13", `[{"type":"to-do","attributes":{"title":"a","when":"2026-13-01"}}]`,
			"[0]", "when", "invalid-date", `[0] when: "2026-13-01" (not a real date)`},
		{"when 30 February", `[{"type":"to-do","attributes":{"title":"a","when":"2026-02-30"}}]`,
			"[0]", "when", "invalid-date", `[0] when: "2026-02-30" (not a real date)`},
		{"when a bare time", `[{"type":"to-do","attributes":{"title":"a","when":"18:00"}}]`,
			"[0]", "when", "invalid-date", `[0] when: "18:00" (not a date as YYYY-MM-DD, or a date and time as YYYY-MM-DD@HH:MM)`},
		{"when hour 25", `[{"type":"to-do","attributes":{"title":"a","when":"2026-10-10@25:00"}}]`,
			"[0]", "when", "invalid-date", `[0] when: "2026-10-10@25:00" (not a real time of day)`},
		{"when RFC 3339", `[{"type":"to-do","attributes":{"title":"a","when":"2026-10-10T18:00:00Z"}}]`,
			"[0]", "when", "invalid-date", `[0] when: "2026-10-10T18:00:00Z" (not a date as YYYY-MM-DD`},
		{"when a typo", `[{"type":"project","attributes":{"title":"P","when":"tommorow"}}]`,
			"[0]", "when", "invalid-date", `[0] when: "tommorow" (unrecognised when value "tommorow" (did you mean "tomorrow"?`},
		{"deadline month 13", `[{"type":"to-do","attributes":{"title":"a","deadline":"2026-13-01"}}]`,
			"[0]", "deadline", "invalid-date", `[0] deadline: "2026-13-01" (not a real date)`},
		{"deadline someday", `[{"type":"to-do","attributes":{"title":"a","deadline":"someday"}}]`,
			"[0]", "deadline", "invalid-date", `[0] deadline: "someday" (deadline does not accept keywords like "someday"; pass a YYYY-MM-DD date)`},
		{"deadline with a time", `[{"type":"to-do","attributes":{"title":"a","deadline":"2026-10-10@18:00"}}]`,
			"[0]", "deadline", "invalid-date", `[0] deadline: "2026-10-10@18:00" (not a date as YYYY-MM-DD)`},
		{"when on an update", `[{"type":"to-do","operation":"update","id":"one-1","attributes":{"when":"2026-13-01"}}]`,
			"[0]", "when", "invalid-date", `[0] (id one-1) when: "2026-13-01" (not a real date)`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pinShapeClock(t)
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
		{"lowercase z", `[{"type":"to-do","attributes":{"title":"a","creation-date":"2026-10-08T10:00:00z"}}]`},
		{"hour 25 rolling into the past", `[{"type":"to-do","attributes":{"title":"a","creation-date":"2026-10-01T25:00:00Z"}}]`},
		{"creation-date a moment ahead", `[{"type":"to-do","attributes":{"title":"a","creation-date":"2026-10-09T12:00:30Z"}}]`},
		{"completion-date in the past", `[{"type":"to-do","attributes":{"title":"a","completed":true,"completion-date":"2026-10-09T11:59:00Z"}}]`},
		{"title of 4000", `[{"type":"to-do","attributes":{"title":"` + strings.Repeat("a", 4000) + `"}}]`},
		{"notes of 10000", `[{"type":"to-do","attributes":{"title":"a","notes":"` + strings.Repeat("a", 10000) + `"}}]`},
		{"checklist-items on a to-do", `[{"type":"to-do","attributes":{"title":"a","checklist-items":[{"type":"checklist-item","attributes":{"title":"c"}}]}}]`},
		{"same key in sibling items", `[{"type":"to-do","attributes":{"title":"a"}},{"type":"to-do","attributes":{"title":"b"}}]`},
		{"when keywords", `[
		  {"type":"to-do","attributes":{"title":"a","when":"today"}},
		  {"type":"to-do","attributes":{"title":"b","when":"Evening"}},
		  {"type":"to-do","attributes":{"title":"c","when":"someday"}},
		  {"type":"to-do","attributes":{"title":"d","when":"anytime"}},
		  {"type":"project","attributes":{"title":"e","when":"tomorrow"}}
		]`},
		{"when dates", `[
		  {"type":"to-do","attributes":{"title":"a","when":"2026-10-10"}},
		  {"type":"to-do","attributes":{"title":"b","when":"2026-10-10@18:00"}},
		  {"type":"to-do","attributes":{"title":"c","when":"2026-10-10@9:30"}},
		  {"type":"to-do","attributes":{"title":"d","when":"friday"}},
		  {"type":"to-do","attributes":{"title":"e","when":"next week"}},
		  {"type":"to-do","attributes":{"title":"f","when":""}}
		]`},
		{"deadlines", `[
		  {"type":"to-do","attributes":{"title":"a","deadline":"2026-10-31"}},
		  {"type":"project","attributes":{"title":"b","deadline":"friday"}},
		  {"type":"to-do","attributes":{"title":"c","deadline":"2024-02-29"}}
		]`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pinShapeClock(t)
			database, _ := seedWritable(t)
			payload := decodeImport(t, c.payload)
			plan, err := prepareImport(database, payload, importDuplicateKeys([]byte(c.payload)))
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
			_, err := prepareImport(database, decodeImport(t, c.payload), nil)
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
	  {"type":"to-do","attributes":{"title":"Weekly review","creation-date":"` + recentCreationDate() + `"}},
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
	rows, claimants := sharedRows(loose, creates, found)
	if len(rows) != 2 || claimants != 2 {
		t.Errorf("rows = %d, claimants = %d, want 2 rows for 2 undated claimants", len(rows), claimants)
	}
}

// Overlap is followed to its end: A shares a row with B and B one with C, so
// C competes for A's rows too, and its rows count only with C counted.
func TestSharedRowsFollowsChains(t *testing.T) {
	a := importCreate{path: "[0]", typ: model.TypeTask, title: "Weekly review", dest: createdDest{checked: true, list: "proj-a"}}
	b := importCreate{path: "[1]", typ: model.TypeTask, title: "Weekly review"}
	c := importCreate{path: "[2]", typ: model.TypeTask, title: "Weekly review", dest: createdDest{checked: true, list: "proj-c"}}
	other := importCreate{path: "[3]", typ: model.TypeTask, title: "Weekly review", dest: createdDest{checked: true, list: "proj-x"}}
	creates := []importCreate{a, b, c, other}
	ab, bc, x := model.Task{UUID: "ab"}, model.Task{UUID: "bc"}, model.Task{UUID: "x"}
	found := map[createdWant][]model.Task{
		a.want():     {ab},
		b.want():     {ab, bc},
		c.want():     {bc},
		other.want(): {x},
	}
	for _, start := range []importCreate{a, b, c} {
		rows, claimants := sharedRows(start, creates, found)
		if len(rows) != 2 || claimants != 3 {
			t.Errorf("from %s: rows = %d, claimants = %d, want 2 rows for 3 claimants", start.path, len(rows), claimants)
		}
	}
}

// A dated item whose creation-date is just before the read-back window can
// still mark a same-titled item shares-dated-title, but its row is never
// among the new items read back, so it does not count as a claimant: the
// undated item's row is enough, and the import exits 0.
func TestImportSharesDatedTitleDatedBeforeWindow(t *testing.T) {
	fastVerify(t)
	database, sqlDB := seedWritable(t)
	datedAt := time.Now().Add(-30 * time.Second).UTC().Truncate(time.Second)
	stubExecAdding(t, sqlDB,
		createdRow{uuid: "dated-1", title: "Weekly review", extra: fmt.Sprintf("creationDate = %f", model.TimeToUnix(datedAt))},
		createdRow{uuid: "new-1", title: "Weekly review"})
	payload := `[
	  {"type":"to-do","attributes":{"title":"Weekly review","creation-date":"` + datedAt.Format(time.RFC3339) + `"}},
	  {"type":"to-do","attributes":{"title":"Weekly review"}}
	]`
	out, _, err := runImportOut(t, database, payload, "--json")
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	got := decodeCreated(t, out)
	if len(got) != 2 || got[1].Reason != "shares-dated-title" || got[1].Present == nil || !*got[1].Present {
		t.Errorf("got %+v, want [1] shares-dated-title and present", got)
	}
}

// A failing shares-dated-title item carries present false in the error's
// items too, not only in created.
func TestImportSharesDatedTitleErrorItemCarriesPresent(t *testing.T) {
	fastVerify(t)
	database, sqlDB := seedWritable(t)
	stubExecAdding(t, sqlDB, createdRow{uuid: "new-1", title: "Weekly review"})
	payload := `[
	  {"type":"to-do","attributes":{"title":"Weekly review","creation-date":"` + recentCreationDate() + `"}},
	  {"type":"to-do","attributes":{"title":"Weekly review"}}
	]`
	_, _, err := runImportOut(t, database, payload, "--json")
	if err == nil {
		t.Fatal("expected the import to fail")
	}
	p, raw := decodePayload(t, err)
	if len(p.Items) != 1 || p.Items[0].Present == nil || *p.Items[0].Present {
		t.Errorf("items = %+v, want one item with present false (%s)", p.Items, raw)
	}
	if !strings.Contains(raw, `"present": false`) {
		t.Errorf("rendered error lacks \"present\": false: %s", raw)
	}
}

// recentCreationDate is a creation-date the read-back always counts as
// inside its window. The window starts a moment before the import is sent,
// and a date stamped with time.Now loses its fraction of a second, so once
// the clock crosses a second it can fall before the start. Five seconds
// ahead keeps it inside however the test is timed.
func recentCreationDate() string {
	return time.Now().Add(5 * time.Second).UTC().Format(time.RFC3339)
}

// Every key given twice is found by the path of the item it belongs to,
// at any depth, and a key given once in each of two objects is not.
func TestImportDuplicateKeys(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		want    map[string][]string
	}{
		{"none", `[{"type":"to-do","attributes":{"title":"a"}},{"type":"to-do","attributes":{"title":"b"}}]`, map[string][]string{}},
		{"in attributes", `[{"type":"to-do","attributes":{"title":"a","title":"b","notes":"x","notes":"y"}}]`,
			map[string][]string{"[0]": {"title", "notes"}}},
		{"on the item", `[{"type":"to-do","attributes":{"title":"a"},"attributes":{"title":"b"}}]`,
			map[string][]string{"[0]": {"attributes"}}},
		{"in a checklist item", `[{"type":"to-do","attributes":{"title":"a","checklist-items":[{"type":"checklist-item","attributes":{"title":"c","title":"d"}}]}}]`,
			map[string][]string{"[0].attributes.checklist-items[0]": {"title"}}},
		{"three times", `[{"type":"to-do","attributes":{"title":"a","title":"b","title":"c"}}]`,
			map[string][]string{"[0]": {"title"}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := importDuplicateKeys([]byte(c.payload))
			if fmt.Sprint(got) != fmt.Sprint(c.want) {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}

// The new reasons combine with the old ones on one item, in the order the
// --json reason lists them, and the plain-text refusal explains each.
func TestImportNewRefusalsCombine(t *testing.T) {
	pinShapeClock(t)
	database, _ := seedWritable(t)
	calls := stubExecDropping(t)
	payload := `[{"type":"to-do","attributes":{"title":"a","title":"b","when":"18:00","creation-date":"2027-01-01T00:00:00Z","items":[],"notes":"` + strings.Repeat("n", 10001) + `"}}]`

	err := runWith(t, database, "import", "--file", importPayload(t, payload))
	if *calls != 0 {
		t.Errorf("payload was sent to Things (%d calls)", *calls)
	}
	var refused *importRefusalError
	if !errors.As(err, &refused) || len(refused.items) != 1 {
		t.Fatalf("err = %v, want one refused item", err)
	}
	it := refused.items[0]
	if got, want := it.reason(), "invalid-date future-date invalid-item duplicate-key too-long"; got != want {
		t.Errorf("reason = %q, want %q", got, want)
	}
	if got, want := strings.Join(it.Blocked, ","), "when,creation-date,items,title,notes"; got != want {
		t.Errorf("blocked = %s, want %s", got, want)
	}
	for _, line := range []string{
		"Things saves a when or deadline it cannot read as something else",
		"Things saves a creation-date or completion-date in the future as now",
		"Things ignores these attributes on this type of item",
		"These items give an attribute twice. Things keeps the first value",
		"Things cuts a title to 4000 characters",
	} {
		if !strings.Contains(err.Error(), line) {
			t.Errorf("message missing %q:\n%v", line, err)
		}
	}
}

// An attribute refused for two reasons is named once in blocked.
func TestImportRefusalNamesAttributeOnce(t *testing.T) {
	pinShapeClock(t)
	database, _ := seedWritable(t)
	long := strings.Repeat("a", 4001)
	raw := `[{"type":"to-do","attributes":{"title":"x","title":"` + long + `"}}]`
	_, err := prepareImport(database, decodeImport(t, raw), importDuplicateKeys([]byte(raw)))
	var refusal *importRefusalError
	if !errors.As(err, &refusal) || len(refusal.items) != 1 {
		t.Fatalf("err = %v, want one refused item", err)
	}
	if got := refusal.items[0].Blocked; !slices.Equal(got, []string{"title"}) {
		t.Errorf("blocked = %q, want [title]", got)
	}
	if got, want := refusal.items[0].reason(), "duplicate-key too-long"; got != want {
		t.Errorf("reason = %q, want %q", got, want)
	}
}

// The title reported for a created item is the one Things saves: padding
// kept, and a line feed saved as a space.
func TestImportCreateShownTitleIsStored(t *testing.T) {
	creates := importCreates(decodeImport(t, `[{"type":"to-do","attributes":{"title":" a\nb "}}]`))
	if len(creates) != 1 {
		t.Fatalf("got %d creates, want 1", len(creates))
	}
	if got, want := creates[0].shownTitle(), " a b "; got != want {
		t.Errorf("shownTitle = %q, want %q", got, want)
	}
}
