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
	tasks := []model.Task{{UUID: "u1", Title: "T1", Status: model.StatusOpen}}
	var buf bytes.Buffer
	if err := Print(&buf, tasks, true); err != nil {
		t.Fatalf("Print: %v", err)
	}
	var got []model.Task
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("parse json: %v\n%s", err, buf.String())
	}
	if len(got) != 1 || got[0].UUID != "u1" || got[0].Title != "T1" {
		t.Fatalf("unexpected json: %+v", got)
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

func TestPrintTaskDetailJSON(t *testing.T) {
	task := &model.Task{UUID: "u1", Title: "T1"}
	items := []model.ChecklistItem{{UUID: "c1", Title: "step", Status: model.StatusOpen}}
	var buf bytes.Buffer
	if err := PrintTaskWithChecklist(&buf, task, items, true); err != nil {
		t.Fatalf("PrintTaskWithChecklist: %v", err)
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
		status int
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
