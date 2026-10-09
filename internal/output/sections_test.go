package output

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/ryanlewis/things-cli/internal/model"
)

// lineIndex returns the index of the first line of out whose trimmed text is
// want, or -1.
func lineIndex(out, want string) int {
	for i, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == want {
			return i
		}
	}
	return -1
}

// lineContaining returns the index of the first line of out that contains
// sub, or -1.
func lineContaining(out, sub string) int {
	for i, line := range strings.Split(out, "\n") {
		if strings.Contains(line, sub) {
			return i
		}
	}
	return -1
}

// Today's evening rows print under a This Evening header, after the day rows,
// and their area header prints again inside the section.
func TestPrintTodayEveningSection(t *testing.T) {
	today := mustDate(2026, 10, 9)
	tasks := []model.Task{
		{UUID: "d1", Title: "Day loose", Start: model.StartAnytime, StartDate: today},
		{UUID: "d2", Title: "Day in area", Start: model.StartAnytime, StartDate: today, AreaUUID: "a1", AreaTitle: "Work"},
		{UUID: "e1", Title: "Evening loose", Start: model.StartAnytime, StartBucket: 1, StartDate: today},
		{UUID: "e2", Title: "Evening in area", Start: model.StartAnytime, StartBucket: 1, StartDate: today, AreaUUID: "a1", AreaTitle: "Work"},
	}
	var buf bytes.Buffer
	if err := PrintViewTaskList(&buf, tasks, false, "today", ""); err != nil {
		t.Fatal(err)
	}
	out := buf.String()

	section := lineIndex(out, eveningSection)
	if section < 0 {
		t.Fatalf("no %q header:\n%s", eveningSection, out)
	}
	if n := strings.Count(out, eveningSection); n != 1 {
		t.Errorf("%q printed %d times, want 1:\n%s", eveningSection, n, out)
	}
	if d := lineContaining(out, "Day in area"); d > section {
		t.Errorf("day row after the evening header:\n%s", out)
	}
	if e := lineContaining(out, "Evening loose"); e < section {
		t.Errorf("evening row above the evening header:\n%s", out)
	}
	if n := strings.Count(out, "Work"); n != 2 {
		t.Errorf("area header printed %d times, want once per section:\n%s", n, out)
	}
}

// The evening section belongs to Today alone: a project page or an area
// listing carries evening rows among the others, and no header.
func TestPrintEveningSectionOnlyInToday(t *testing.T) {
	tasks := []model.Task{
		{UUID: "e1", Title: "Evening loose", Start: model.StartAnytime, StartBucket: 1, StartDate: mustDate(2026, 10, 9)},
	}
	for _, view := range []string{"", "project", "anytime"} {
		var buf bytes.Buffer
		if err := PrintViewTaskList(&buf, tasks, false, view, ""); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(buf.String(), eveningSection) {
			t.Errorf("view %q printed %q:\n%s", view, eveningSection, buf.String())
		}
	}
}

// JSON keeps the bare task array: the evening flag is already there as
// startBucket, so no section field is added.
func TestPrintTodayEveningJSONUnchanged(t *testing.T) {
	tasks := []model.Task{
		{UUID: "d1", Title: "Day", Start: model.StartAnytime, StartDate: mustDate(2026, 10, 9)},
		{UUID: "e1", Title: "Evening", Start: model.StartAnytime, StartBucket: 1, StartDate: mustDate(2026, 10, 9)},
	}
	var buf bytes.Buffer
	if err := PrintViewTaskList(&buf, tasks, true, "today", ""); err != nil {
		t.Fatal(err)
	}
	var got []map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal %q: %v", buf.String(), err)
	}
	if len(got) != 2 || got[1]["startBucket"] != float64(1) {
		t.Errorf("got %v, want two rows, the second with startBucket 1", got)
	}
	if strings.Contains(buf.String(), eveningSection) {
		t.Errorf("JSON carries the section header:\n%s", buf.String())
	}
}

// The Logbook prints a date header for each local day rows were closed on,
// newest first, and restarts the group headers under each.
func TestPrintLogbookDaySections(t *testing.T) {
	at := func(y, m, d, h int) *time.Time {
		v := time.Date(y, time.Month(m), d, h, 0, 0, 0, time.Local)
		return &v
	}
	tasks := []model.Task{
		{UUID: "a", Title: "Closed late", Status: model.StatusCompleted, StopDate: at(2026, 10, 9, 22), ProjectUUID: "p1", ProjectTitle: "Chores"},
		{UUID: "b", Title: "Closed early", Status: model.StatusCompleted, StopDate: at(2026, 10, 9, 1), ProjectUUID: "p1", ProjectTitle: "Chores"},
		{UUID: "c", Title: "Closed before", Status: model.StatusCancelled, StopDate: at(2026, 10, 4, 0), ProjectUUID: "p1", ProjectTitle: "Chores"},
	}
	var buf bytes.Buffer
	if err := PrintViewTaskList(&buf, tasks, false, "logbook", ""); err != nil {
		t.Fatal(err)
	}
	out := buf.String()

	first, second := lineIndex(out, "2026-10-09"), lineIndex(out, "2026-10-04")
	if first < 0 || second < 0 || first > second {
		t.Fatalf("day headers missing or out of order:\n%s", out)
	}
	if early := lineContaining(out, "Closed early"); early > second {
		t.Errorf("a row closed on 9 Oct printed under 4 Oct:\n%s", out)
	}
	if before := lineContaining(out, "Closed before"); before < second {
		t.Errorf("a row closed on 4 Oct printed above its header:\n%s", out)
	}
	if n := strings.Count(out, "Chores"); n != 2 {
		t.Errorf("project header printed %d times, want once per day:\n%s", n, out)
	}
}

// Other views print no date headers, however their rows are dated.
func TestPrintDaySectionsOnlyInLogbook(t *testing.T) {
	stop := time.Date(2026, 10, 9, 12, 0, 0, 0, time.Local)
	tasks := []model.Task{{UUID: "a", Title: "Closed", Status: model.StatusCompleted, StopDate: &stop}}
	var buf bytes.Buffer
	if err := PrintViewTaskList(&buf, tasks, false, "today", ""); err != nil {
		t.Fatal(err)
	}
	if lineIndex(buf.String(), "2026-10-09") >= 0 {
		t.Errorf("today printed a day header:\n%s", buf.String())
	}
}

// A project row closing one day does not fold away the project header of its
// to-do opening the next: the date header sits between them.
func TestPrintSectionStartKeepsProjectHeader(t *testing.T) {
	later := time.Date(2026, 10, 9, 12, 0, 0, 0, time.Local)
	earlier := time.Date(2026, 10, 8, 12, 0, 0, 0, time.Local)
	tasks := []model.Task{
		{UUID: "p1", Title: "Chores", Type: model.TypeProject, Status: model.StatusCompleted, StopDate: &later},
		{UUID: "t1", Title: "Sweep", Status: model.StatusCompleted, StopDate: &earlier, ProjectUUID: "p1", ProjectTitle: "Chores"},
	}
	var buf bytes.Buffer
	if err := PrintViewTaskList(&buf, tasks, false, "logbook", ""); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if n := strings.Count(out, "Chores"); n != 2 {
		t.Errorf("Chores printed %d times, want the row and a header under 2026-10-08:\n%s", n, out)
	}
}
