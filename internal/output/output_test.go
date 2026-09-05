package output

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ryanlewis/things-cli/internal/model"
)

func mustDate(y, m, d int) *model.ThingsDate {
	td := model.ThingsDateFromTime(time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.Local))
	return &td
}

func TestPrintTasksPlain(t *testing.T) {
	tasks := []model.Task{
		{
			UUID: "u1", Title: "Buy milk", Status: model.StatusOpen,
			Tags: []string{"shop", "home"},
		},
		{
			UUID: "u2", Title: "Write report", Status: model.StatusCompleted,
			ProjectUUID: "p1", ProjectTitle: "Work",
			Deadline: mustDate(2026, 5, 1),
		},
		{
			UUID: "u3", Title: "Star task", Status: model.StatusOpen,
			ProjectUUID: "p1", ProjectTitle: "Work",
			Start: model.StartAnytime, StartBucket: 0, StartDate: mustDate(2026, 4, 15),
		},
	}
	var buf bytes.Buffer
	if err := Print(&buf, tasks, false); err != nil {
		t.Fatalf("Print: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"Buy milk", "[shop, home]", "Write report", "due:2026-05-01", "Work", "[x]", "★"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestPrintTasksJSON(t *testing.T) {
	tasks := []model.Task{{
		UUID: "u1", Title: "T1", Status: model.StatusOpen,
		StartDate: mustDate(2026, 5, 9),
		Deadline:  mustDate(2026, 5, 20),
	}}
	var buf bytes.Buffer
	if err := Print(&buf, tasks, true); err != nil {
		t.Fatalf("Print: %v", err)
	}
	out := buf.String()
	for _, want := range []string{`"startDate": "2026-05-09"`, `"deadline": "2026-05-20"`} {
		if !strings.Contains(out, want) {
			t.Errorf("json missing %q:\n%s", want, out)
		}
	}
	var got []model.Task
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("parse json: %v\n%s", err, out)
	}
	if len(got) != 1 || got[0].UUID != "u1" || got[0].Title != "T1" {
		t.Fatalf("unexpected json: %+v", got)
	}
	if got[0].StartDate == nil || got[0].StartDate.String() != "2026-05-09" {
		t.Fatalf("startDate round-trip wrong: %+v", got[0].StartDate)
	}
}

func TestPrintEmptyTasks(t *testing.T) {
	var buf bytes.Buffer
	if err := Print(&buf, []model.Task{}, false); err != nil {
		t.Fatalf("Print: %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("expected empty output, got %q", buf.String())
	}
}

// Empty lists must encode as [], not null — jq '.[]' fails on null.
func TestPrintEmptyTasksJSON(t *testing.T) {
	var buf bytes.Buffer
	if err := Print(&buf, []model.Task{}, true); err != nil {
		t.Fatalf("Print: %v", err)
	}
	if got := strings.TrimSpace(buf.String()); got != "[]" {
		t.Errorf("expected [], got %q", got)
	}
}

func TestPrintProjectsPlain(t *testing.T) {
	projects := []model.Project{
		{UUID: "p1", Title: "Empty project", TaskCount: 0},
		{UUID: "p2", Title: "Half done", TaskCount: 4, OpenCount: 2, AreaTitle: "Work", Tags: []string{"urgent"}},
		{UUID: "p3", Title: "All done", TaskCount: 3, OpenCount: 0},
		{UUID: "p4", Title: "Completed", Status: model.StatusCompleted, TaskCount: 1},
		{UUID: "p5", Title: "Cancelled", Status: model.StatusCancelled, TaskCount: 1},
	}
	var buf bytes.Buffer
	if err := Print(&buf, projects, false); err != nil {
		t.Fatalf("Print: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"Empty project", "Half done", "Work", "[urgent]", "All done", "Completed", "Cancelled"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

func TestPrintProjectsJSON(t *testing.T) {
	projects := []model.Project{{UUID: "p1", Title: "P1"}}
	var buf bytes.Buffer
	if err := Print(&buf, projects, true); err != nil {
		t.Fatalf("Print: %v", err)
	}
	var got []model.Project
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("parse json: %v", err)
	}
	if len(got) != 1 || got[0].UUID != "p1" {
		t.Fatalf("unexpected: %+v", got)
	}
}

func TestPrintAreas(t *testing.T) {
	areas := []model.Area{
		{UUID: "a1", Title: "Work", Visible: true},
		{UUID: "a2", Title: "Hidden", Visible: false},
	}
	var buf bytes.Buffer
	if err := Print(&buf, areas, false); err != nil {
		t.Fatalf("Print: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Work") || !strings.Contains(out, "Hidden") || !strings.Contains(out, "(hidden)") {
		t.Errorf("areas output wrong:\n%s", out)
	}
}

func TestPrintTags(t *testing.T) {
	tags := []model.Tag{
		{UUID: "t1", Title: "urgent", Shortcut: "u"},
		{UUID: "t2", Title: "home"},
	}
	var buf bytes.Buffer
	if err := Print(&buf, tags, false); err != nil {
		t.Fatalf("Print: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "urgent") || !strings.Contains(out, "(u)") || !strings.Contains(out, "home") {
		t.Errorf("tags output wrong:\n%s", out)
	}
}

func TestPrintTaskDetail(t *testing.T) {
	created := time.Date(2026, 4, 10, 9, 30, 0, 0, time.UTC)
	stopped := time.Date(2026, 4, 14, 17, 0, 0, 0, time.UTC)
	task := &model.Task{
		UUID: "u1", Title: "T1", Status: model.StatusCompleted,
		ProjectTitle: "Proj", AreaTitle: "Work", HeadingTitle: "H",
		Tags:         []string{"a", "b"},
		StartDate:    mustDate(2026, 4, 12),
		Deadline:     mustDate(2026, 4, 20),
		CreationDate: &created, StopDate: &stopped,
		Notes: "line1\nline2",
	}
	items := []model.ChecklistItem{
		{UUID: "c1", Title: "step1", Status: model.StatusCompleted},
		{UUID: "c2", Title: "step2", Status: model.StatusOpen},
	}

	var buf bytes.Buffer
	if err := PrintTaskWithChecklist(&buf, task, items, false); err != nil {
		t.Fatalf("PrintTaskWithChecklist: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"Title:    T1",
		"Status:   Completed",
		"Project:  Proj",
		"Area:     Work",
		"Heading:  H",
		"Tags:     a, b",
		"Start:    2026-04-12",
		"Deadline: 2026-04-20",
		"Created:  2026-04-10 09:30",
		"Stopped:  2026-04-14 17:00",
		"Notes:",
		"  line1",
		"  line2",
		"Checklist:",
		"[x] step1",
		"[ ] step2",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("detail missing %q:\n%s", want, out)
		}
	}
}

func TestPrintTaskDetail_StripsAnsiInUserContent(t *testing.T) {
	// Untrusted task content (from the Things DB) is routed through the same
	// colorprofile.Writer as styled output, so literal ANSI escapes embedded in a
	// note are stripped under --color=never / non-TTY instead of being injected
	// into the terminal. TestMain pins "never".
	task := &model.Task{
		UUID:  "u1",
		Title: "T1",
		Notes: "before\x1b[31mRED\x1b[mafter",
	}
	var buf bytes.Buffer
	if err := PrintTaskWithChecklist(&buf, task, nil, false); err != nil {
		t.Fatalf("PrintTaskWithChecklist: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "\x1b[") {
		t.Errorf("expected ANSI stripped from note content, got %q", out)
	}
	if !strings.Contains(out, "beforeREDafter") {
		t.Errorf("expected note text preserved, got %q", out)
	}
}

func TestPrintTaskDetailJSON(t *testing.T) {
	task := &model.Task{UUID: "u1", Title: "T1", Deadline: mustDate(2026, 5, 20)}
	items := []model.ChecklistItem{{UUID: "c1", Title: "step", Status: model.StatusOpen}}
	var buf bytes.Buffer
	if err := PrintTaskWithChecklist(&buf, task, items, true); err != nil {
		t.Fatalf("PrintTaskWithChecklist: %v", err)
	}
	if want := `"deadline": "2026-05-20"`; !strings.Contains(buf.String(), want) {
		t.Errorf("json missing %q:\n%s", want, buf.String())
	}
	var got struct {
		UUID      string                `json:"uuid"`
		Title     string                `json:"title"`
		Checklist []model.ChecklistItem `json:"checklist"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("parse: %v\n%s", err, buf.String())
	}
	if got.UUID != "u1" || got.Title != "T1" || len(got.Checklist) != 1 || got.Checklist[0].Title != "step" {
		t.Fatalf("unexpected: %+v", got)
	}
}

func TestPrintFallbackJSON(t *testing.T) {
	// unknown type falls through to printJSON
	type foo struct {
		X int `json:"x"`
	}
	var buf bytes.Buffer
	if err := Print(&buf, foo{X: 42}, false); err != nil {
		t.Fatalf("Print: %v", err)
	}
	if !strings.Contains(buf.String(), `"x": 42`) {
		t.Errorf("expected json fallback, got: %s", buf.String())
	}
}

func TestStatusHelpers(t *testing.T) {
	cases := []struct {
		status model.Status
		icon   string
		text   string
	}{
		{model.StatusOpen, "[ ]", "Open"},
		{model.StatusCancelled, "[~]", "Cancelled"},
		{model.StatusCompleted, "[x]", "Completed"},
		{99, "[ ]", "Unknown"},
	}
	for _, tc := range cases {
		if got := statusIcon(tc.status); got != tc.icon {
			t.Errorf("statusIcon(%d) = %q, want %q", tc.status, got, tc.icon)
		}
		if got := statusText(tc.status); got != tc.text {
			t.Errorf("statusText(%d) = %q, want %q", tc.status, got, tc.text)
		}
	}
}

func TestProjectIconBuckets(t *testing.T) {
	cases := []struct {
		p    model.Project
		want string
	}{
		{model.Project{TaskCount: 0}, "○"},
		{model.Project{TaskCount: 10, OpenCount: 10}, "○"},                // 0%
		{model.Project{TaskCount: 10, OpenCount: 8}, "◔"},                 // 20%
		{model.Project{TaskCount: 10, OpenCount: 5}, "◑"},                 // 50%
		{model.Project{TaskCount: 10, OpenCount: 2}, "◕"},                 // 80%
		{model.Project{TaskCount: 10, OpenCount: 0}, "●"},                 // 100%
		{model.Project{Status: model.StatusCompleted, TaskCount: 5}, "●"}, // explicit completed
		{model.Project{Status: model.StatusCancelled, TaskCount: 5}, "◌"}, // explicit cancelled
	}
	for _, tc := range cases {
		if got := projectIcon(tc.p); got != tc.want {
			t.Errorf("projectIcon(%+v) = %q, want %q", tc.p, got, tc.want)
		}
	}
}

func TestPrintTaskListViewLabel(t *testing.T) {
	tasks := []model.Task{{
		UUID: "u1", Title: "Buy milk", Status: model.StatusOpen,
		ProjectUUID: "p1", ProjectTitle: "Chores",
	}}

	var labelled bytes.Buffer
	if err := PrintTaskList(&labelled, tasks, false, "today"); err != nil {
		t.Fatalf("PrintTaskList: %v", err)
	}
	out := labelled.String()
	if !strings.Contains(out, "view: today") {
		t.Errorf("missing view label:\n%s", out)
	}
	if !strings.Contains(out, "Chores") || !strings.Contains(out, "Buy milk") {
		t.Errorf("missing task rows:\n%s", out)
	}

	var plain bytes.Buffer
	if err := PrintTaskList(&plain, tasks, false, ""); err != nil {
		t.Fatalf("PrintTaskList: %v", err)
	}
	if strings.Contains(plain.String(), "view:") {
		t.Errorf("unexpected view label:\n%s", plain.String())
	}

	// An empty listing still says which view it came from.
	var empty bytes.Buffer
	if err := PrintTaskList(&empty, nil, false, "today"); err != nil {
		t.Fatalf("PrintTaskList: %v", err)
	}
	if !strings.Contains(empty.String(), "view: today") {
		t.Errorf("empty listing lost its label:\n%s", empty.String())
	}
}

// The view label is human output only — JSON stays a bare task array so
// existing consumers keep parsing it.
func TestPrintTaskListJSONIgnoresViewLabel(t *testing.T) {
	tasks := []model.Task{{UUID: "u1", Title: "Buy milk", Status: model.StatusOpen}}
	var buf bytes.Buffer
	if err := PrintTaskList(&buf, tasks, true, "today"); err != nil {
		t.Fatalf("PrintTaskList: %v", err)
	}
	var got []model.Task
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal %q: %v", buf.String(), err)
	}
	if len(got) != 1 || got[0].UUID != "u1" {
		t.Errorf("got %+v, want one task u1", got)
	}
}

// `things repeating` lists project templates alongside to-do templates, and
// `things search` can turn up a project, so a project row has to say so in
// plain output rather than reading as a to-do (issue #165). The marker is
// text, not colour, so it survives --color never and a pipe.
func TestPrintTasksMarksProjects(t *testing.T) {
	tasks := []model.Task{
		{UUID: "t1", Title: "Water plants", Type: model.TypeTask, Status: model.StatusOpen},
		{UUID: "p1", Title: "Weekly review", Type: model.TypeProject, Status: model.StatusOpen},
	}
	var buf bytes.Buffer
	if err := Print(&buf, tasks, false); err != nil {
		t.Fatalf("Print: %v", err)
	}
	out := buf.String()

	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.Contains(line, "Weekly review"):
			if !strings.Contains(line, "(project)") {
				t.Errorf("project row not marked:\n%s", line)
			}
		case strings.Contains(line, "Water plants"):
			if strings.Contains(line, "(project)") {
				t.Errorf("to-do row marked as a project:\n%s", line)
			}
		}
	}
}

// `things show` on a project template must not print a block indistinguishable
// from a to-do's (issue #165). To-do detail output stays as it was.
func TestPrintTaskDetailMarksProjects(t *testing.T) {
	var buf bytes.Buffer
	project := &model.Task{UUID: "p1", Title: "Weekly review", Type: model.TypeProject, Status: model.StatusOpen}
	if err := PrintTaskWithChecklist(&buf, project, nil, false); err != nil {
		t.Fatalf("PrintTaskWithChecklist: %v", err)
	}
	if out := buf.String(); !strings.Contains(out, "project") {
		t.Errorf("project detail does not say it is a project:\n%s", out)
	}

	buf.Reset()
	todo := &model.Task{UUID: "t1", Title: "Water plants", Type: model.TypeTask, Status: model.StatusOpen}
	if err := PrintTaskWithChecklist(&buf, todo, nil, false); err != nil {
		t.Fatalf("PrintTaskWithChecklist: %v", err)
	}
	if out := buf.String(); strings.Contains(out, "Type:") {
		t.Errorf("to-do detail gained a Type line:\n%s", out)
	}
}
