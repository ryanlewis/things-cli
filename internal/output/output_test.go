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
	if err := PrintTaskList(&buf, tasks, false, ""); err != nil {
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
	if err := PrintTaskList(&buf, tasks, true, ""); err != nil {
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
	if err := PrintTaskList(&buf, []model.Task{}, false, ""); err != nil {
		t.Fatalf("Print: %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("expected empty output, got %q", buf.String())
	}
}

// Empty lists must encode as [], not null — jq '.[]' fails on null.
func TestPrintEmptyTasksJSON(t *testing.T) {
	var buf bytes.Buffer
	if err := PrintTaskList(&buf, []model.Task{}, true, ""); err != nil {
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
	if err := PrintProjects(&buf, projects, false); err != nil {
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
	if err := PrintProjects(&buf, projects, true); err != nil {
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
	if err := PrintAreas(&buf, areas, false); err != nil {
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
	if err := PrintTags(&buf, tags, false); err != nil {
		t.Fatalf("Print: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "urgent") || !strings.Contains(out, "(u)") || !strings.Contains(out, "home") {
		t.Errorf("tags output wrong:\n%s", out)
	}
}

func TestPrintTaskDetail(t *testing.T) {
	created := time.Date(2026, 4, 10, 9, 30, 0, 0, time.Local)
	stopped := time.Date(2026, 4, 14, 17, 0, 0, 0, time.Local)
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

// The database hands back creation and stop times in UTC. The detail block
// prints them without a zone, so they have to be local: printed as UTC, a to-do
// closed at 00:30 BST reads as stopped the day before.
func TestPrintTaskDetail_TimestampsInLocalTime(t *testing.T) {
	pinClock(t, time.Time{}, time.FixedZone("BST", 60*60))

	created := model.UnixToTime(model.TimeToUnix(time.Date(2026, 9, 29, 8, 15, 0, 0, time.UTC)))
	stopped := model.UnixToTime(model.TimeToUnix(time.Date(2026, 9, 29, 23, 30, 0, 0, time.UTC)))
	task := &model.Task{UUID: "u1", Title: "T1", CreationDate: &created, StopDate: &stopped}

	var buf bytes.Buffer
	if err := PrintTaskWithChecklist(&buf, task, nil, false); err != nil {
		t.Fatalf("PrintTaskWithChecklist: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"Created:  2026-09-29 09:15", "Stopped:  2026-09-30 00:30"} {
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

func TestPrintJSONAdHocStruct(t *testing.T) {
	type foo struct {
		X int `json:"x"`
	}
	var buf bytes.Buffer
	if err := PrintJSON(&buf, foo{X: 42}); err != nil {
		t.Fatalf("PrintJSON: %v", err)
	}
	if buf.String() != "{\n  \"x\": 42\n}\n" {
		t.Errorf("unexpected JSON: %q", buf.String())
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
	if err := PrintTaskList(&buf, tasks, false, ""); err != nil {
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

// Dates switch to their compact form only on a terminal: piped output keeps
// the full date or drops it, since "3d ago" goes stale in a saved file.
func TestPrintTasksPipedKeepsFullDates(t *testing.T) {
	pinClock(t, time.Date(2026, 5, 3, 12, 0, 0, 0, time.Local), nil)

	tasks := []model.Task{{
		UUID: "t1", Title: "Twenty column title.", Type: model.TypeTask, Status: model.StatusOpen,
		Deadline: mustDate(2026, 5, 8),
	}}
	for _, tty := range []bool{false, true} {
		// "1.  [ ]  " and a 20-column title leave 10 for the date: too few
		// for "due:2026-05-08", enough for "due:Fri".
		pinLayout(t, 41, tty)
		var buf bytes.Buffer
		if err := PrintTaskList(&buf, tasks, false, ""); err != nil {
			t.Fatalf("Print: %v", err)
		}
		if compact := strings.Contains(buf.String(), "due:Fri"); compact != tty {
			t.Errorf("tty=%v: compact date=%v:\n%s", tty, compact, buf.String())
		}
	}
}

// Titles are cut to the width only on a terminal: piped output falls back to
// 120 columns, and cutting there would hide text from grep and from agents.
func TestPrintTasksPipedKeepsTitlesWhole(t *testing.T) {
	title := "Write the postmortem for last week's outage"
	tasks := []model.Task{{UUID: "t1", Title: title, Type: model.TypeTask, Status: model.StatusOpen}}
	for _, tty := range []bool{false, true} {
		pinLayout(t, 30, tty)
		var buf bytes.Buffer
		if err := PrintTaskList(&buf, tasks, false, ""); err != nil {
			t.Fatalf("Print: %v", err)
		}
		if whole := strings.Contains(buf.String(), title); whole == tty {
			t.Errorf("tty=%v: title whole=%v:\n%s", tty, whole, buf.String())
		}
	}
}

// today and upcoming list a scheduled project as a row, and its own to-dos
// sort straight after it (issue #201) — anytime carries no project rows at all
// since issue #217. The to-dos' project group header would
// restate the title on the line above, so it is suppressed — the title must
// appear exactly once. A project group that does not follow its own row still
// gets its header.
func TestPrintTasksNoRepeatedProjectHeader(t *testing.T) {
	tasks := []model.Task{
		{UUID: "p1", Title: "Runbook audit", Type: model.TypeProject, Status: model.StatusOpen,
			AreaUUID: "a1", AreaTitle: "Work"},
		{UUID: "t1", Title: "Read logs", Type: model.TypeTask, Status: model.StatusOpen,
			ProjectUUID: "p1", ProjectTitle: "Runbook audit"},
		{UUID: "t2", Title: "Draft memo", Type: model.TypeTask, Status: model.StatusOpen,
			ProjectUUID: "p2", ProjectTitle: "Q4 planning"},
	}
	var buf bytes.Buffer
	if err := PrintTaskList(&buf, tasks, false, ""); err != nil {
		t.Fatalf("Print: %v", err)
	}
	out := buf.String()

	if n := strings.Count(out, "Runbook audit"); n != 1 {
		t.Errorf("%q appears %d times, want 1:\n%s", "Runbook audit", n, out)
	}
	if !strings.Contains(out, "Work") {
		t.Errorf("area header missing:\n%s", out)
	}
	// A project the listing does not carry as a row keeps its header.
	if !strings.Contains(out, "Q4 planning") {
		t.Errorf("unrelated project header missing:\n%s", out)
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

// A scheduled project renders its dates under the same JSON names and in the
// same YYYY-MM-DD form as a scheduled to-do, so callers read one vocabulary
// (issue #202).
func TestPrintProjectsJSONScheduling(t *testing.T) {
	start := model.ThingsDate(132813696)    // 2026-09-07
	deadline := model.ThingsDate(132814464) // 2026-09-13
	projects := []model.Project{{
		UUID: "p1", Title: "Runbook audit",
		Start: model.StartAnytime, StartBucket: 0,
		StartDate: &start, Deadline: &deadline,
	}}
	var buf bytes.Buffer
	if err := PrintProjects(&buf, projects, true); err != nil {
		t.Fatalf("Print: %v", err)
	}

	var got []map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("parse json: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("want 1 project, got %d", len(got))
	}
	for field, want := range map[string]any{
		// start is a string enum, not the raw Things code (issue #241).
		"start":       model.StartAnytime.String(),
		"startBucket": float64(0),
		"startDate":   "2026-09-07",
		"deadline":    "2026-09-13",
	} {
		if got[0][field] != want {
			t.Errorf("%s: got %v, want %v", field, got[0][field], want)
		}
	}
}

// JSON output is read by people and agents as text, not embedded in HTML, so
// `&`, `<` and `>` in a title must come out as written rather than as \u0026,
// \u003c and \u003e.
func TestPrintJSONDoesNotEscapeHTMLCharacters(t *testing.T) {
	title := "R&D <review> & plan"
	task := model.Task{UUID: "u1", Title: title, Notes: "a > b"}

	cases := map[string]func(*bytes.Buffer) error{
		"list":   func(b *bytes.Buffer) error { return PrintTaskList(b, []model.Task{task}, true, "") },
		"json":   func(b *bytes.Buffer) error { return PrintJSON(b, []model.Task{task}) },
		"detail": func(b *bytes.Buffer) error { return PrintTaskWithChecklist(b, &task, nil, true) },
	}
	for name, run := range cases {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := run(&buf); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(buf.String(), `\u00`) {
				t.Errorf("output escapes HTML characters:\n%s", buf.String())
			}
			if !strings.Contains(buf.String(), title) {
				t.Errorf("output missing literal title %q:\n%s", title, buf.String())
			}
		})
	}
}

// On a narrow terminal a long value wraps with a hanging indent: each wrapped
// line keeps to the column its text started in rather than running back to
// column 0. The user's own line breaks and blank lines survive, a URL moves to
// a line of its own rather than splitting at a hyphen, and only a token wider
// than the space left is broken.
func TestPrintTaskDetail_WrapsToTerminalWidth(t *testing.T) {
	pinLayout(t, 40, true)

	task := &model.Task{
		UUID: "u1", Title: "Book the venue for the autumn team offsite", Status: model.StatusOpen,
		Notes: "Call them before Friday and ask about the deposit.\n\n" +
			"See https://example.com/venue-offsite26 for details.\n" +
			"ref:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
	items := []model.ChecklistItem{
		{UUID: "c1", Title: "Confirm the headcount with every team lead", Status: model.StatusOpen},
	}

	var buf bytes.Buffer
	if err := PrintTaskWithChecklist(&buf, task, items, false); err != nil {
		t.Fatalf("PrintTaskWithChecklist: %v", err)
	}
	want := `Title:    Book the venue for the autumn
          team offsite
UUID:     u1
Status:   Open
Notes:
  Call them before Friday and ask about
  the deposit.

  See
  https://example.com/venue-offsite26
  for details.
  ref:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
  aaaaaaaaaaaa
Checklist:
  [ ] Confirm the headcount with every
      team lead
`
	if got := buf.String(); got != want {
		t.Errorf("detail at 40 columns:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// Output that is not going to a terminal is not wrapped: a script or agent
// reading a piped detail block gets each line of a note whole, with only the
// note's own line breaks, rather than breaks at an assumed width.
func TestPrintTaskDetail_NoWrapWhenNotATerminal(t *testing.T) {
	pinLayout(t, 120, false)

	long := strings.Repeat("word ", 40) + "end"
	task := &model.Task{UUID: "u1", Title: long, Status: model.StatusOpen, Notes: long + "\nsecond"}

	var buf bytes.Buffer
	if err := PrintTaskWithChecklist(&buf, task, nil, false); err != nil {
		t.Fatalf("PrintTaskWithChecklist: %v", err)
	}
	want := "Title:    " + long + "\nUUID:     u1\nStatus:   Open\nNotes:\n  " + long + "\n  second\n"
	if got := buf.String(); got != want {
		t.Errorf("unwrapped detail:\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// The hyphen stand-in used while wrapping never reaches the output: every '-'
// prints as '-', and a U+2011 the user typed prints as itself. A hyphenated
// URL in the same text still moves whole to the next line, so copying it out
// of `things show` gives back its original bytes.
func TestHang_HyphensComeBackAsWritten(t *testing.T) {
	got := hang("  ", "Typed non\u2011break, see https://ex-ample.com/docs-page ok", 40)
	want := "  Typed non\u2011break, see\n  https://ex-ample.com/docs-page ok\n"
	if got != want {
		t.Errorf("hang:\ngot  %q\nwant %q", got, want)
	}
}

// A note line that starts with an indent or a list marker wraps under the
// text after it rather than back at the note's indent, and a tab counts as
// the columns it moves to, so a tabbed line still fits the width. No wrapped
// line ends in whitespace.
func TestHang_ListLinesAndTabs(t *testing.T) {
	note := "- Call the venue and ask about the deposit\n" +
		"1. Book the coach for everyone going\n" +
		"    nested line that is long enough to wrap\n" +
		"\tTabbed\tline that is long enough to wrap\n" +
		"• see https://ex-ample.com/a-b now\n" +
		"-5 degrees is not a list item at all ok"
	got := hang("  ", note, 30)
	want := "" +
		"  - Call the venue and ask\n" +
		"    about the deposit\n" +
		"  1. Book the coach for\n" +
		"     everyone going\n" +
		"      nested line that is long\n" +
		"      enough to wrap\n" +
		"      Tabbed  line that is\n" +
		"      long enough to wrap\n" +
		"  • see\n" +
		"    https://ex-ample.com/a-b\n" +
		"    now\n" +
		"  -5 degrees is not a list\n" +
		"  item at all ok\n"
	if got != want {
		t.Errorf("hang:\ngot:\n%s\nwant:\n%s", got, want)
	}
	for _, line := range strings.Split(got, "\n") {
		if strings.TrimRight(line, " \t") != line {
			t.Errorf("line ends in whitespace: %q", line)
		}
	}
}

// A lead wider than half the space left would leave a sliver for the text,
// so that line wraps back to the base indent instead.
func TestHang_WideLeadFallsBack(t *testing.T) {
	got := hang("", "            - one two three four five", 20)
	want := "            - one\ntwo three four five\n"
	if got != want {
		t.Errorf("hang:\ngot  %q\nwant %q", got, want)
	}
}

// An indent wider than the space left does not open up a blank line: the
// text starts on the next line at the base indent.
func TestHang_IndentWiderThanWidth(t *testing.T) {
	got := hang("  ", "\t\t\t\t\t\t\t\tfoo bar\nnext", 32)
	want := "  foo bar\n  next\n"
	if got != want {
		t.Errorf("hang:\ngot  %q\nwant %q", got, want)
	}
}

// Unwrapped output (not a terminal) keeps a note's tabs and indents exactly
// as written.
func TestHang_UnwrappedKeepsTabs(t *testing.T) {
	note := "\tTabbed\tline\n- item\n\n  indented  "
	got := hang("  ", note, 0)
	want := "  \tTabbed\tline\n  - item\n  \n    indented  \n"
	if got != want {
		t.Errorf("hang:\ngot  %q\nwant %q", got, want)
	}
}

func TestOneLine(t *testing.T) {
	cases := map[string]string{
		"Plain title":               "Plain title",
		"Pack\tthe bags":            "Pack the bags",
		"Windows\r\nend":            "Windows end",
		"lone\rreturn":              "lone return",
		"two\nlines":                "two lines",
		"vertical\vtab":             "vertical tab",
		"form\ffeed":                "form feed",
		"next\u0085line":            "next line",
		"line separator":            "line separator",
		"paragraph separator":       "paragraph separator",
		"日本語\nのタイトル":                "日本語 のタイトル",
		"  keeps  its own  spaces ": "  keeps  its own  spaces ",
	}
	for in, want := range cases {
		if got := oneLine(in); got != want {
			t.Errorf("oneLine(%q) = %q, want %q", in, got, want)
		}
	}
}

func strPtr(s string) *string { return &s }

// A reminder prints in JSON only when the to-do has one.
func TestPrintTasksJSONReminderTime(t *testing.T) {
	tasks := []model.Task{
		{UUID: "u1", Title: "Reminded", StartDate: mustDate(2026, 10, 9), ReminderTime: strPtr("09:00")},
		{UUID: "u2", Title: "Plain", StartDate: mustDate(2026, 10, 9)},
	}
	var buf bytes.Buffer
	if err := PrintTaskList(&buf, tasks, true, ""); err != nil {
		t.Fatalf("Print: %v", err)
	}
	var got []map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("parse: %v\n%s", err, buf.String())
	}
	if r, ok := got[0]["reminderTime"]; !ok || r != "09:00" {
		t.Errorf("reminded: reminderTime = %v (present %v), want 09:00", r, ok)
	}
	if r, ok := got[1]["reminderTime"]; ok {
		t.Errorf("plain: reminderTime = %v, want absent", r)
	}
}

// The list row puts the reminder ahead of the title, as the app does, and a
// row without one prints exactly as before.
func TestPrintTasksReminderTime(t *testing.T) {
	pinClock(t, time.Date(2026, 10, 9, 8, 0, 0, 0, time.Local), nil)
	pinLayout(t, 120, false)
	tasks := []model.Task{
		{UUID: "u1", Title: "Reminded", Status: model.StatusOpen, Start: model.StartAnytime, StartDate: mustDate(2026, 10, 9), ReminderTime: strPtr("09:00")},
		{UUID: "u2", Title: "Plain", Status: model.StatusOpen},
	}
	var buf bytes.Buffer
	if err := PrintTaskList(&buf, tasks, false, ""); err != nil {
		t.Fatalf("Print: %v", err)
	}
	lines := strings.Split(buf.String(), "\n")
	if !strings.Contains(lines[0], "★ 09:00 Reminded") {
		t.Errorf("reminded row = %q, want the time before the title", lines[0])
	}
	if strings.Contains(lines[1], ":") {
		t.Errorf("plain row = %q, want no time", lines[1])
	}
}

// show puts the reminder on the Start line, or on a line of its own when
// there is no start date to put it beside.
func TestPrintTaskDetailReminderTime(t *testing.T) {
	pinLayout(t, 120, false)
	for _, tc := range []struct {
		name string
		task model.Task
		want string
	}{
		{"with start", model.Task{UUID: "u1", Title: "T", StartDate: mustDate(2026, 10, 9), ReminderTime: strPtr("09:00")}, "Start:    2026-10-09 09:00\n"},
		{"without start", model.Task{UUID: "u1", Title: "T", ReminderTime: strPtr("21:30")}, "Reminder: 21:30\n"},
		{"none", model.Task{UUID: "u1", Title: "T", StartDate: mustDate(2026, 10, 9)}, "Start:    2026-10-09\n"},
	} {
		var buf bytes.Buffer
		if err := PrintTaskWithChecklist(&buf, &tc.task, nil, false); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if !strings.Contains(buf.String(), tc.want) {
			t.Errorf("%s: detail missing %q:\n%s", tc.name, tc.want, buf.String())
		}
	}
}
