package output

import (
	"bytes"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ryanlewis/things-cli/internal/model"
)

// The valid names are the keys the full record prints, so a field added to
// the model is selectable and one hidden from JSON is not.
func TestFieldNamesFollowJSONTags(t *testing.T) {
	for _, want := range []string{"uuid", "title", "notes", "projectTitle", "checklistProgress"} {
		if !slices.Contains(TaskFields, want) {
			t.Errorf("TaskFields lacks %q: %v", want, TaskFields)
		}
	}
	for _, name := range []string{"-", "ModificationDate", "modificationDate"} {
		if slices.Contains(TaskFields, name) {
			t.Errorf("TaskFields has %q, which the record never prints", name)
		}
	}
	if TaskFields[0] != "uuid" || TaskFields[1] != "title" {
		t.Errorf("TaskFields should follow record order, got %v", TaskFields[:2])
	}
	for _, want := range []string{"uuid", "taskCount", "openCount"} {
		if !slices.Contains(ProjectFields, want) {
			t.Errorf("ProjectFields lacks %q: %v", want, ProjectFields)
		}
	}
	if slices.Contains(ProjectFields, "notes") {
		t.Errorf("ProjectFields has notes, which a project row does not carry")
	}
}

// Every name a printed row carries must be selectable, or --fields would
// reject a key the user can see in the full output.
func TestFieldNamesCoverFullRecord(t *testing.T) {
	stop := mustTime(t, "2026-01-02T03:04:05Z")
	sd := model.ThingsDateFromTime(stop)
	rt := "09:00"
	task := model.Task{
		UUID: "u", Title: "t", Notes: "n", StartDate: &sd, ReminderTime: &rt, Deadline: &sd,
		StopDate: &stop, CreationDate: &stop, ProjectUUID: "p", ProjectTitle: "P", ProjectTrashed: true,
		AreaUUID: "a", AreaTitle: "A", HeadingUUID: "h", HeadingTitle: "H", Tags: []string{"x"},
		Repeating: true, ChecklistProgress: &model.ChecklistProgress{Total: 1},
	}
	project := model.Project{UUID: "u", Title: "t", StartDate: &sd, Deadline: &sd, AreaUUID: "a", AreaTitle: "A", Tags: []string{"x"}}
	for _, c := range []struct {
		row   any
		valid []string
	}{{task, TaskFields}, {project, ProjectFields}} {
		b, err := json.Marshal(c.row)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		if len(m) != len(c.valid) {
			t.Errorf("record has %d keys, valid list %d: %v", len(m), len(c.valid), c.valid)
		}
		for k := range m {
			if !slices.Contains(c.valid, k) {
				t.Errorf("record key %q is not a valid field", k)
			}
		}
	}
}

func TestParseFields(t *testing.T) {
	cases := []struct {
		raw  string
		want []string
	}{
		{"uuid,title", []string{"uuid", "title"}},
		{"title,uuid", []string{"title", "uuid"}},
		{" title , uuid ", []string{"title", "uuid"}},
		{"title,title,uuid,title", []string{"title", "uuid"}},
		{"uuid,,title,", []string{"uuid", "title"}},
	}
	for _, c := range cases {
		got, err := ParseFields(c.raw, "task", TaskFields)
		if err != nil {
			t.Errorf("ParseFields(%q): %v", c.raw, err)
			continue
		}
		if !slices.Equal(got, c.want) {
			t.Errorf("ParseFields(%q) = %v, want %v", c.raw, got, c.want)
		}
	}
}

func TestParseFieldsRejectsUnknown(t *testing.T) {
	_, err := ParseFields("uuid,project,bogus,project", "task", TaskFields)
	var ufe *UnknownFieldError
	if !errors.As(err, &ufe) {
		t.Fatalf("err = %v, want *UnknownFieldError", err)
	}
	if !slices.Equal(ufe.Unknown, []string{"project", "bogus"}) {
		t.Errorf("Unknown = %v", ufe.Unknown)
	}
	if ufe.Kind != "task" || !slices.Equal(ufe.Valid, TaskFields) {
		t.Errorf("Kind/Valid = %q %v", ufe.Kind, ufe.Valid)
	}
	msg := err.Error()
	for _, want := range []string{`"project"`, `"bogus"`, "task listing", "projectTitle"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q lacks %q", msg, want)
		}
	}
}

// Names are exact keys: case matters, as it does to jq.
func TestParseFieldsIsCaseSensitive(t *testing.T) {
	if _, err := ParseFields("UUID", "task", TaskFields); err == nil {
		t.Error("UUID accepted; want only the key as printed")
	}
}

// An empty list is refused with the valid names, as an unknown one is, so a
// caller can read them off the error either way.
func TestParseFieldsRejectsEmpty(t *testing.T) {
	for _, raw := range []string{"", " ", ",", " , "} {
		_, err := ParseFields(raw, "task", TaskFields)
		var ufe *UnknownFieldError
		if !errors.As(err, &ufe) {
			t.Errorf("ParseFields(%q) err = %v, want *UnknownFieldError", raw, err)
			continue
		}
		if len(ufe.Unknown) != 0 || !slices.Equal(ufe.Valid, TaskFields) {
			t.Errorf("ParseFields(%q) Unknown/Valid = %v/%v", raw, ufe.Unknown, ufe.Valid)
		}
		if msg := err.Error(); !strings.Contains(msg, "names no fields") || !strings.Contains(msg, "projectTitle") {
			t.Errorf("ParseFields(%q) message = %q", raw, msg)
		}
	}
}

// all on its own is the full record, which a nil list means.
func TestParseFieldsAll(t *testing.T) {
	for _, raw := range []string{"all", " all ", "all,all", "all,"} {
		got, err := ParseFields(raw, "task", TaskFields)
		if err != nil || got != nil {
			t.Errorf("ParseFields(%q) = %v, %v; want nil, nil", raw, got, err)
		}
	}
	for _, raw := range []string{"all,uuid", "title,all"} {
		if _, err := ParseFields(raw, "task", TaskFields); err == nil || !strings.Contains(err.Error(), "full record") {
			t.Errorf("ParseFields(%q) err = %v, want all-with-names refusal", raw, err)
		}
	}
}

// Each default is a key the full record prints, given once and in record
// order, so the default output reads as a cut of the full one. all must not
// be a key, or --fields all would be ambiguous.
func TestDefaultFieldsAreValidKeys(t *testing.T) {
	for _, c := range []struct {
		kind            string
		defaults, valid []string
	}{{"task", TaskDefaultFields, TaskFields}, {"project", ProjectDefaultFields, ProjectFields}} {
		last := -1
		for _, name := range c.defaults {
			i := slices.Index(c.valid, name)
			if i < 0 {
				t.Errorf("%s default %q is not a valid key", c.kind, name)
				continue
			}
			if i <= last {
				t.Errorf("%s default %q is repeated or out of record order", c.kind, name)
			}
			last = i
		}
		if slices.Contains(c.valid, AllFields) {
			t.Errorf("%s rows have a key named %q", c.kind, AllFields)
		}
	}
}

func projectedRows(t *testing.T, out string) []map[string]json.RawMessage {
	t.Helper()
	var rows []map[string]json.RawMessage
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("output is not a JSON array of objects: %v\n%s", err, out)
	}
	return rows
}

func TestPrintTaskFieldsKeepsRequestedOrder(t *testing.T) {
	var buf bytes.Buffer
	tasks := []model.Task{{UUID: "u1", Title: "One", Notes: "long notes"}}
	if err := PrintTaskFields(&buf, tasks, []string{"title", "uuid"}); err != nil {
		t.Fatal(err)
	}
	want := "[\n  {\n    \"title\": \"One\",\n    \"uuid\": \"u1\"\n  }\n]\n"
	if buf.String() != want {
		t.Errorf("got\n%s\nwant\n%s", buf.String(), want)
	}
}

// A requested key the full record leaves out on a row is left out here too,
// so a row reads the same whichever way it was asked for.
func TestPrintTaskFieldsOmitsWhatTheRecordOmits(t *testing.T) {
	d := model.ThingsDateFromTime(mustTime(t, "2026-10-12T00:00:00Z"))
	tasks := []model.Task{
		{UUID: "u1", Title: "Dated", Deadline: &d, Tags: []string{"a"}},
		{UUID: "u2", Title: "Bare"},
	}
	var buf bytes.Buffer
	if err := PrintTaskFields(&buf, tasks, []string{"uuid", "deadline", "tags", "trashed"}); err != nil {
		t.Fatal(err)
	}
	rows := projectedRows(t, buf.String())
	if len(rows) != 2 {
		t.Fatalf("rows = %d", len(rows))
	}
	var tags bytes.Buffer
	if err := json.Compact(&tags, rows[0]["tags"]); err != nil {
		t.Fatal(err)
	}
	if string(rows[0]["deadline"]) != `"2026-10-12"` || tags.String() != `["a"]` {
		t.Errorf("row 0 = %v", rows[0])
	}
	if _, ok := rows[1]["deadline"]; ok {
		t.Errorf("row 1 carries a deadline it does not have: %v", rows[1])
	}
	if _, ok := rows[1]["tags"]; ok {
		t.Errorf("row 1 carries tags it does not have: %v", rows[1])
	}
	// trashed is not omitempty, so false is printed as the record prints it.
	if string(rows[1]["trashed"]) != "false" {
		t.Errorf("row 1 trashed = %s, want false", rows[1]["trashed"])
	}
}

// Every value is the full record's value byte for byte: the projection cuts
// keys, it does not re-encode.
func TestPrintTaskFieldsValuesMatchFullRecord(t *testing.T) {
	d := model.ThingsDateFromTime(mustTime(t, "2026-10-12T00:00:00Z"))
	stop := mustTime(t, "2026-10-01T08:30:00Z")
	tasks := []model.Task{{
		UUID: "u1", Title: "R&D <draft>", Type: model.TypeProject, Status: model.StatusCompleted,
		Start: model.StartSomeday, StartDate: &d, StopDate: &stop, ProjectTitle: "P",
		ChecklistProgress: &model.ChecklistProgress{Total: 3, Open: 1},
	}}
	var full, cut bytes.Buffer
	if err := PrintTaskList(&full, tasks, true, ""); err != nil {
		t.Fatal(err)
	}
	if err := PrintTaskFields(&cut, tasks, TaskFields); err != nil {
		t.Fatal(err)
	}
	// All fields in record order is the full record.
	if cut.String() != full.String() {
		t.Errorf("all fields differs from the full record:\n%s\nvs\n%s", cut.String(), full.String())
	}
	if !strings.Contains(cut.String(), `"R&D <draft>"`) {
		t.Errorf("HTML characters were escaped: %s", cut.String())
	}
}

func TestPrintFieldsEmptyListIsArray(t *testing.T) {
	var buf bytes.Buffer
	if err := PrintTaskFields(&buf, nil, []string{"uuid"}); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(buf.String()); got != "[]" {
		t.Errorf("got %q, want []", got)
	}
}

func TestPrintProjectFields(t *testing.T) {
	projects := []model.Project{{UUID: "p1", Title: "Chores", AreaTitle: "Home", TaskCount: 4, OpenCount: 2}}
	var buf bytes.Buffer
	if err := PrintProjectFields(&buf, projects, []string{"title", "openCount"}); err != nil {
		t.Fatal(err)
	}
	want := "[\n  {\n    \"title\": \"Chores\",\n    \"openCount\": 2\n  }\n]\n"
	if buf.String() != want {
		t.Errorf("got\n%s\nwant\n%s", buf.String(), want)
	}
}

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// Every key a row can carry is either a default or one of the keys the docs
// list as left out, so a field added to the model is a decision about the
// default listing rather than a key that silently never appears in one.
func TestDefaultFieldsAccountForEveryKey(t *testing.T) {
	for _, c := range []struct {
		kind                     string
		defaults, omitted, valid []string
	}{
		{"task", TaskDefaultFields, []string{
			"notes", "creationDate", "trashed", "projectUUID",
			"areaUUID", "headingUUID", "index", "todayIndex",
		}, TaskFields},
		{"project", ProjectDefaultFields, []string{"startBucket", "areaUUID"}, ProjectFields},
	} {
		for _, k := range c.valid {
			if slices.Contains(c.defaults, k) == slices.Contains(c.omitted, k) {
				t.Errorf("%s key %q must be in exactly one of the defaults and the left-out keys", c.kind, k)
			}
		}
		for _, k := range c.omitted {
			if !slices.Contains(c.valid, k) {
				t.Errorf("%s left-out key %q is not a valid key", c.kind, k)
			}
		}
	}
}
