package output

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ryanlewis/things-cli/internal/model"
)

// -update-golden rewrites testdata/render_golden.txt from the current code.
// Regenerating is only ever right when the rendering was *meant* to change:
// the point of the file is that a refactor which is supposed to preserve the
// output has to leave it untouched.
var updateGolden = flag.Bool("update-golden", false, "rewrite the golden rendered output")

const goldenRenderPath = "testdata/render_golden.txt"

// goldenNow is the clock the golden document is rendered against. styledDate
// buckets a date by its distance from today, so the fixtures below carry dates
// either side of this one and the file would otherwise change every day.
var goldenNow = time.Date(2026, 9, 10, 11, 30, 0, 0, time.Local)

// goldenCase is one rendered surface: a name, the width to render it at, and
// the call that writes it.
type goldenCase struct {
	name  string
	width int // terminal width; 0 renders at the 120-column default
	write func(w io.Writer) error
}

// goldenTasks is the task listing every list case renders. It is one slice
// rather than several so a single document pins the interaction between the
// pieces: group headers breaking and folding, the project marker, the star,
// the status icons and every date bucket in one column-width measurement.
func goldenTasks(t *testing.T) []model.Task {
	t.Helper()
	return []model.Task{
		// A project listed as a row of its own, under an area header. Its own
		// to-dos follow, so their group header folds into this row (#205).
		{
			UUID: "p1", Title: "Runbook audit", Type: model.TypeProject, Status: model.StatusOpen,
			AreaUUID: "a1", AreaTitle: "Work", Tags: []string{"ops"},
		},
		{
			UUID: "t1", Title: "Read logs", Type: model.TypeTask, Status: model.StatusOpen,
			ProjectUUID: "p1", ProjectTitle: "Runbook audit",
			Start: model.StartAnytime, StartBucket: 0, StartDate: thingsDate(t, "2026-09-10"),
		},
		{
			UUID: "t2", Title: "Write the postmortem for last week's outage",
			Type: model.TypeTask, Status: model.StatusCompleted,
			ProjectUUID: "p1", ProjectTitle: "Runbook audit",
			Deadline: thingsDate(t, "2026-09-01"), Tags: []string{"write", "ops"},
		},
		// A different project, not carried as a row, so its header prints.
		{
			UUID: "t3", Title: "Draft memo", Type: model.TypeTask, Status: model.StatusCancelled,
			ProjectUUID: "p2", ProjectTitle: "Q4 planning",
		},
		{
			UUID: "t4", Title: "Buy milk", Type: model.TypeTask, Status: model.StatusOpen,
			AreaUUID: "a2", AreaTitle: "Home", Tags: []string{"shop"},
			Start: model.StartSomeday, StartDate: thingsDate(t, "2026-09-13"),
		},
		// No project and no area: a group break with nothing to head it.
		{
			UUID: "t5", Title: "No group at all", Type: model.TypeTask, Status: model.StatusOpen,
			Deadline: thingsDate(t, "2026-12-25"),
		},
		{
			UUID: "t6", Title: "Unrecognised status", Type: model.TypeTask, Status: model.Status(99),
		},
	}
}

func goldenProjects() []model.Project {
	return []model.Project{
		{UUID: "p1", Title: "Empty project", TaskCount: 0},
		{UUID: "p2", Title: "Just started", TaskCount: 10, OpenCount: 8, AreaTitle: "Work", Tags: []string{"urgent"}},
		{UUID: "p3", Title: "Half done", TaskCount: 10, OpenCount: 5, AreaTitle: "Work"},
		{UUID: "p4", Title: "Nearly there", TaskCount: 10, OpenCount: 2},
		{UUID: "p5", Title: "All done", TaskCount: 10, OpenCount: 0, AreaTitle: "Home"},
		{UUID: "p6", Title: "Completed", Status: model.StatusCompleted, TaskCount: 1},
		{UUID: "p7", Title: "Cancelled", Status: model.StatusCancelled, TaskCount: 1},
	}
}

func goldenAreas() []model.Area {
	return []model.Area{
		{UUID: "a1", Title: "Work", Visible: true},
		{UUID: "a2", Title: "Home and garden", Visible: false},
		{UUID: "a3", Title: "Admin", Visible: true},
	}
}

func goldenTags() []model.Tag {
	return []model.Tag{
		{UUID: "g1", Title: "urgent", Shortcut: "u"},
		{UUID: "g2", Title: "home"},
		{UUID: "g3", Title: "waiting-on-someone", Shortcut: "w"},
	}
}

// goldenDetailTask carries every optional field of the detail block at once.
func goldenDetailTask(t *testing.T) *model.Task {
	t.Helper()
	created := time.Date(2026, 9, 1, 9, 30, 0, 0, time.UTC)
	stopped := time.Date(2026, 9, 8, 17, 0, 0, 0, time.UTC)
	return &model.Task{
		UUID: "detail-uuid", Title: "Cut RC build", Type: model.TypeTask, Status: model.StatusCompleted,
		ProjectTitle: "Launch v2", AreaTitle: "Work", HeadingTitle: "Release",
		Tags:      []string{"release", "priority"},
		StartDate: thingsDate(t, "2026-09-05"), Deadline: thingsDate(t, "2026-09-12"),
		CreationDate: &created, StopDate: &stopped,
		Notes: "Coordinate with marketing.\nSecond line.",
	}
}

func goldenChecklist() []model.ChecklistItem {
	return []model.ChecklistItem{
		{UUID: "c1", Title: "Bump version", Status: model.StatusCompleted},
		{UUID: "c2", Title: "Update changelog", Status: model.StatusOpen},
		{UUID: "c3", Title: "Ask legal", Status: model.StatusCancelled},
	}
}

// goldenCases enumerates every public entry point of the package, at the
// widths that change what a task listing prints.
func goldenCases(t *testing.T) []goldenCase {
	t.Helper()
	tasks := goldenTasks(t)
	projects := goldenProjects()
	areas := goldenAreas()
	tags := goldenTags()
	detail := goldenDetailTask(t)
	items := goldenChecklist()

	project := &model.Task{
		UUID: "proj-uuid", Title: "Runbook audit", Type: model.TypeProject, Status: model.StatusOpen,
		AreaTitle: "Work", Notes: "Owned by the on-call rota.",
	}
	repeating := &model.Task{
		UUID: "repeat-uuid", Title: "Weekly review", Type: model.TypeTask, Status: model.StatusOpen,
		Repeating: true, Start: model.StartAnytime,
	}
	closedProject := &model.Task{
		UUID: "closed-uuid", Title: "Old migration", Type: model.TypeProject, Status: model.StatusCompleted,
		Start: model.StartSomeday,
	}
	briefTodos := []model.Task{
		{UUID: "child-1", Title: "Read logs", Status: model.StatusOpen},
		{UUID: "child-2", Title: "Archive the box", Status: model.StatusCancelled},
	}
	fenced := &model.Task{
		UUID: "fence-uuid", Title: "Notes with a fence", Type: model.TypeTask, Status: model.StatusOpen,
		Notes: "Run ```sh\nthings list today\n``` and see.",
	}

	cases := []goldenCase{
		{name: "task-detail/plain", write: func(w io.Writer) error {
			return PrintTaskWithChecklist(w, detail, items, false)
		}},
		{name: "task-detail/json", write: func(w io.Writer) error {
			return PrintTaskWithChecklist(w, detail, items, true)
		}},
		{name: "task-detail/no-checklist", write: func(w io.Writer) error {
			return PrintTaskWithChecklist(w, detail, nil, false)
		}},
		{name: "project-detail/plain", write: func(w io.Writer) error {
			return PrintTaskWithChecklist(w, project, nil, false)
		}},
		{name: "repeating-detail/plain", write: func(w io.Writer) error {
			return PrintTaskWithChecklist(w, repeating, nil, false)
		}},
		{name: "print/task-pointer", write: func(w io.Writer) error {
			return Print(w, detail, false)
		}},
		{name: "print/projects", write: func(w io.Writer) error {
			return Print(w, projects, false)
		}},
		{name: "print/projects-json", write: func(w io.Writer) error {
			return Print(w, projects, true)
		}},
		{name: "print/areas", write: func(w io.Writer) error {
			return Print(w, areas, false)
		}},
		{name: "print/areas-json", write: func(w io.Writer) error {
			return Print(w, areas, true)
		}},
		{name: "print/tags", write: func(w io.Writer) error {
			return Print(w, tags, false)
		}},
		{name: "print/tags-json", write: func(w io.Writer) error {
			return Print(w, tags, true)
		}},
		{name: "print/empty-tasks", write: func(w io.Writer) error {
			return Print(w, []model.Task{}, false)
		}},
		{name: "print/empty-projects", write: func(w io.Writer) error {
			return Print(w, []model.Project{}, false)
		}},
		{name: "print/empty-areas", write: func(w io.Writer) error {
			return Print(w, []model.Area{}, false)
		}},
		{name: "print/empty-tags", write: func(w io.Writer) error {
			return Print(w, []model.Tag{}, false)
		}},
		{name: "print/tasks-json", write: func(w io.Writer) error {
			return Print(w, tasks, true)
		}},
		{name: "task-list/json", write: func(w io.Writer) error {
			return PrintTaskList(w, tasks, true, "today")
		}},
		{name: "hint", write: func(w io.Writer) error {
			return PrintHint(w, "run `things show 1` for the detail")
		}},
		{name: "brief/task", write: func(w io.Writer) error {
			return PrintAgentBrief(w, AgentBrief{Task: detail, Checklist: items})
		}},
		{name: "brief/open-project", write: func(w io.Writer) error {
			return PrintAgentBrief(w, AgentBrief{Task: project, Todos: briefTodos})
		}},
		{name: "brief/empty-project", write: func(w io.Writer) error {
			return PrintAgentBrief(w, AgentBrief{Task: project})
		}},
		{name: "brief/closed-project", write: func(w io.Writer) error {
			return PrintAgentBrief(w, AgentBrief{Task: closedProject, Todos: briefTodos})
		}},
		{name: "brief/repeating-task", write: func(w io.Writer) error {
			return PrintAgentBrief(w, AgentBrief{Task: repeating})
		}},
		{name: "brief/fenced-notes", write: func(w io.Writer) error {
			return PrintAgentBrief(w, AgentBrief{Task: fenced})
		}},
	}

	// The task listing at three widths: wide enough for every column, narrow
	// enough to drop the tags column, and narrower still so the date goes too.
	for _, width := range []int{120, 80, 40} {
		cases = append(cases,
			goldenCase{
				name:  fmt.Sprintf("print/tasks@%d", width),
				width: width,
				write: func(w io.Writer) error { return Print(w, tasks, false) },
			},
			goldenCase{
				name:  fmt.Sprintf("task-list/labelled@%d", width),
				width: width,
				write: func(w io.Writer) error { return PrintTaskList(w, tasks, false, "today") },
			},
		)
	}
	cases = append(cases, goldenCase{
		name:  "task-list/unlabelled@120",
		width: 120,
		write: func(w io.Writer) error { return PrintTaskList(w, tasks, false, "") },
	})
	return cases
}

// renderGolden renders every case at both extremes of the colour profile and
// returns the lot as one text document.
func renderGolden(t *testing.T) string {
	t.Helper()

	prevNow, prevWidth := nowFn, termWidth
	t.Cleanup(func() {
		nowFn, termWidth = prevNow, prevWidth
		_ = SetColorMode("never")
	})
	nowFn = func() time.Time { return goldenNow }

	var b strings.Builder
	for _, mode := range []string{"never", "always"} {
		if err := SetColorMode(mode); err != nil {
			t.Fatalf("SetColorMode(%q): %v", mode, err)
		}
		for _, c := range goldenCases(t) {
			width := c.width
			if width == 0 {
				width = 120
			}
			termWidth = func() int { return width }

			var buf bytes.Buffer
			if err := c.write(&buf); err != nil {
				t.Fatalf("case %s (color=%s): %v", c.name, mode, err)
			}
			fmt.Fprintf(&b, "### case=%s color=%s\n", c.name, mode)
			b.WriteString(escapeGolden(buf.String()))
			b.WriteString("\n")
		}
	}
	return b.String()
}

// escapeGolden makes rendered output survive a round trip through a text file.
// Every line ends in "|" because a padded column emits trailing spaces that an
// editor or a hook would otherwise eat, and ANSI escapes are written "\e" so
// the file stays readable and greppable.
func escapeGolden(s string) string {
	s = strings.ReplaceAll(s, "\x1b", `\e`)
	var b strings.Builder
	for _, line := range strings.Split(s, "\n") {
		b.WriteString(line)
		b.WriteString("|\n")
	}
	return b.String()
}

// Plain text and JSON are the whole user-visible surface of this package, and
// both are assembled from four list printers, a detail block and a Markdown
// brief that repeat each other's measuring and padding. This pins the finished
// bytes of every public entry point — at three terminal widths and at both
// ends of the colour profile — so a refactor that is meant to preserve the
// output has to prove it did (issue #243).
//
// A deliberate rendering change regenerates the file with -update-golden, and
// the diff on that file is then the reviewable record of what moved.
func TestRenderGolden(t *testing.T) {
	got := renderGolden(t)

	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(goldenRenderPath), 0o755); err != nil {
			t.Fatalf("mkdir testdata: %v", err)
		}
		if err := os.WriteFile(goldenRenderPath, []byte(got), 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		t.Logf("wrote %s", goldenRenderPath)
		return
	}

	wantBytes, err := os.ReadFile(goldenRenderPath)
	if err != nil {
		t.Fatalf("read golden (regenerate with: go test ./internal/output -run TestRenderGolden -update-golden): %v", err)
	}
	want := string(wantBytes)
	if got == want {
		return
	}

	// Report the cases that differ rather than the whole document.
	gotCases, wantCases := splitGoldenCases(got), splitGoldenCases(want)
	var reported int
	for _, header := range goldenCaseOrder(want, got) {
		g, inGot := gotCases[header]
		w, inWant := wantCases[header]
		switch {
		case inGot && inWant && g == w:
			continue
		case !inWant:
			t.Errorf("%s: new case, not in the golden file", header)
		case !inGot:
			t.Errorf("%s: case gone from the rendered output", header)
		default:
			t.Errorf("%s: output changed\n--- golden ---\n%s\n--- got ---\n%s", header, w, g)
		}
		reported++
		if reported == 3 {
			t.Errorf("(further differences not listed)")
			return
		}
	}
}

// splitGoldenCases indexes the document by its "### case=..." headers.
func splitGoldenCases(doc string) map[string]string {
	cases := map[string]string{}
	var header string
	var body strings.Builder
	flush := func() {
		if header != "" {
			cases[header] = strings.TrimSpace(body.String())
		}
		body.Reset()
	}
	for line := range strings.SplitSeq(doc, "\n") {
		if strings.HasPrefix(line, "### ") {
			flush()
			header = line
			continue
		}
		body.WriteString(line)
		body.WriteString("\n")
	}
	flush()
	return cases
}

// goldenCaseOrder lists every header in either document, golden order first so
// the report reads in the same order as the file.
func goldenCaseOrder(want, got string) []string {
	seen := map[string]bool{}
	var order []string
	for _, doc := range []string{want, got} {
		for line := range strings.SplitSeq(doc, "\n") {
			if strings.HasPrefix(line, "### ") && !seen[line] {
				seen[line] = true
				order = append(order, line)
			}
		}
	}
	return order
}
