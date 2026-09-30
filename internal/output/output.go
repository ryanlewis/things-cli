package output

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/ryanlewis/things-cli/internal/model"
)

func Print(w io.Writer, v any, asJSON bool) error {
	if asJSON {
		return printJSON(w, v)
	}
	// Styled output renders full-fidelity ANSI; downsample/strip it on the way
	// out according to the active color profile. JSON carries no ANSI, so it is
	// written to the raw writer.
	switch val := v.(type) {
	case []model.Task:
		return printTasks(newWriter(w), val)
	case *model.Task:
		return printTaskDetail(newWriter(w), val, nil)
	case []model.Project:
		return printProjects(newWriter(w), val)
	case []model.Area:
		return printAreas(newWriter(w), val)
	case []model.Tag:
		return printTags(newWriter(w), val)
	default:
		return printJSON(w, v)
	}
}

// PrintTaskList renders a task listing. When view is non-empty it prefixes a
// line naming the view the listing was drawn from, so a filtered slice of a
// view is not mistaken for the filter target's full contents (issue #140).
// JSON output is the plain task array either way.
func PrintTaskList(w io.Writer, tasks []model.Task, asJSON bool, view string) error {
	if asJSON {
		return printJSON(w, tasks)
	}
	tw := newWriter(w)
	if view != "" {
		fmt.Fprintf(tw, "    %s\n", dimStyle.Render("view: "+view))
	}
	return printTasks(tw, tasks)
}

func PrintTaskWithChecklist(w io.Writer, t *model.Task, items []model.ChecklistItem, asJSON bool) error {
	if asJSON {
		type taskWithChecklist struct {
			*model.Task
			Checklist []model.ChecklistItem `json:"checklist,omitempty"`
		}
		return printJSON(w, taskWithChecklist{Task: t, Checklist: items})
	}
	return printTaskDetail(newWriter(w), t, items)
}

func printJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	// The default encoder rewrites &, < and > as \u0026, \u003c and \u003e for
	// HTML embedding. This output is read as text, so a title like "R&D" must
	// print as written.
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

// taskColumns names the columns a task row carries, so the drop order below
// and the cells added for each row cannot fall out of step.
const (
	colNum = iota
	colStatus
	colTitle
	colTags
	colStart
	colDate
)

func printTasks(w io.Writer, tasks []model.Task) error {
	// group is what a row needs beyond its cells: which project or area it
	// belongs under, and its own UUID, so the header logic below can see
	// whether a group's rows follow the row that names the group.
	type group struct {
		uuid       string
		key, title string
		isProject  bool
	}

	tbl := &table{
		gap:      columnGap,
		maxWidth: termWidth(),
		// The tags go first when a row will not fit, then the date: a row
		// still says what it is and when it is due for as long as it can.
		// The start date beside a deadline is the first to go: the date
		// column still carries the deadline, as it does on a narrow terminal.
		dropOrder: []int{colStart, colTags, colDate},
		// A list with no task carrying both dates prints as if the start
		// column were not there.
		omitEmpty: []int{colStart},
	}
	if stdoutIsTerminal() {
		// Then the title is cut short rather than wrapping under the row
		// numbers, though never so far that it stops saying what the task is.
		// Only on a terminal: piped output keeps every title whole.
		tbl.shrink = []shrinkCol{{col: colTitle, min: 10}}
	}
	groups := make([]group, len(tasks))

	for i, t := range tasks {
		title := t.Title
		if t.Status == model.StatusCompleted || t.Status == model.StatusCancelled {
			title = titleDimStyle.Render(title)
		}
		if t.Start == model.StartAnytime && t.StartBucket == 0 && t.StartDate != nil {
			title = starStyle.Render("★") + " " + title
		}
		// A task list is normally to-dos only, but `things repeating` carries
		// project templates too and `things search` can turn up a project, so
		// say which rows are projects rather than letting them read as
		// to-dos. Text, not just colour, so it survives --color never.
		if isProject(&t) {
			title += " " + dimStyle.Render("("+kindWord(&t)+")")
		}

		var date string
		switch {
		case t.Deadline != nil:
			date = styledDate(t.Deadline, true)
		case t.StartDate != nil:
			date = styledDate(t.StartDate, false)
		}
		// The date column shows the deadline over the start date, so a task
		// with both carries its start date in a column of its own, before the
		// deadline as the two fall in time.
		var start string
		if t.Deadline != nil && t.StartDate != nil {
			start = styledDate(t.StartDate, false)
		}

		tbl.row(
			fmt.Sprintf("%d.", i+1),
			styledStatus(t.Status),
			title,
			styledTags(t.Tags),
			start,
			date,
		)

		g := group{uuid: t.UUID}
		if t.ProjectUUID != "" {
			g.key, g.title, g.isProject = t.ProjectUUID, t.ProjectTitle, true
		} else {
			g.key, g.title = t.AreaUUID, t.AreaTitle
		}
		groups[i] = g
	}

	lines := tbl.lines()
	const sentinel = "\x00"
	currentProject, currentArea := sentinel, sentinel
	prevUUID := ""
	for i, g := range groups {
		current := &currentArea
		other := &currentProject
		if g.isProject {
			current, other = &currentProject, &currentArea
		}
		if g.key != *current || *other != sentinel {
			// Every view but inbox lists a project as a row of its own
			// (issues #201, #206, #212, #213). Where the view's order
			// puts its to-dos straight after that row, their project group
			// header would restate the title on the line above, so fold them
			// under the row instead of repeating it. Where the order separates
			// them the header still prints, which is what it is for. The group
			// state advances either way, so a later group breaks as usual.
			foldsIntoRowAbove := g.isProject && g.key == prevUUID
			if !foldsIntoRowAbove {
				if currentProject != sentinel || currentArea != sentinel {
					fmt.Fprintln(w)
				}
				if g.title != "" {
					fmt.Fprintf(w, "    %s\n", headerStyle.Render(g.title))
				}
			}
			*current = g.key
			*other = sentinel
		}
		prevUUID = g.uuid

		fmt.Fprintln(w, lines[i])
	}
	return nil
}

func printTaskDetail(w io.Writer, t *model.Task, items []model.ChecklistItem) error {
	label := func(s string) string {
		return padCol(10, labelStyle.Render(s))
	}
	// Only a terminal has a width to fit. Piped output falls back to
	// termWidth's 120 columns, and wrapping there would add line breaks the
	// note does not have.
	width := 0
	if stdoutIsTerminal() {
		width = termWidth()
	}
	field := func(name, value string) {
		fmt.Fprint(w, hang(label(name), value, width))
	}

	field("Title:", t.Title)
	field("UUID:", t.UUID)
	field("Status:", statusText(t.Status))
	// A lookup resolves projects as well as to-dos, and `things repeating`
	// hands out indexes for project templates, so say when the thing being
	// shown is a project rather than leaving its detail block reading as a
	// to-do's. To-do output is unchanged.
	if isProject(t) {
		field("Type:", kindWord(t))
	}
	if t.ProjectTitle != "" {
		field("Project:", projectStyle.Render(t.ProjectTitle))
	}
	if t.AreaTitle != "" {
		field("Area:", areaStyle.Render(t.AreaTitle))
	}
	if t.HeadingTitle != "" {
		field("Heading:", t.HeadingTitle)
	}
	if len(t.Tags) > 0 {
		field("Tags:", tagStyle.Render(strings.Join(t.Tags, ", ")))
	}
	if t.StartDate != nil {
		field("Start:", styledDate(t.StartDate, false))
	}
	if t.Deadline != nil {
		field("Deadline:", styledDate(t.Deadline, false))
	}
	if t.Repeating {
		field("Repeats:", "yes (Things blocks status, when and deadline edits)")
	}
	// The timestamps arrive in UTC (model.UnixToTime), and the line carries no
	// zone, so render them in local time like every other date the reader sees.
	// Printed as UTC, an item closed after midnight in a zone ahead of UTC
	// reads as stopped the day before.
	if t.CreationDate != nil {
		field("Created:", t.CreationDate.Local().Format("2006-01-02 15:04"))
	}
	if t.StopDate != nil {
		field("Stopped:", t.StopDate.Local().Format("2006-01-02 15:04"))
	}
	if t.Notes != "" {
		// The wrap keeps the user's own line breaks and blank lines, so a
		// paragraph never runs into the next.
		fmt.Fprintln(w, labelStyle.Render("Notes:"))
		fmt.Fprint(w, hang("  ", t.Notes, width))
	}
	if len(items) > 0 {
		fmt.Fprintln(w, labelStyle.Render("Checklist:"))
		for _, item := range items {
			fmt.Fprint(w, hang("  "+styledStatus(item.Status)+" ", item.Title, width))
		}
	}
	return nil
}

// hang renders prefix then s wrapped to fit width, indenting each wrapped
// line to where s began, so a long value keeps to its column on a narrow
// terminal instead of running back to column 0. Words break only when one
// alone is wider than the space left. A width that leaves no room after the
// prefix (0 for output that is not a terminal) leaves s unwrapped, though its
// own line breaks are still indented.
func hang(prefix, s string, width int) string {
	indent := lipgloss.Width(prefix)
	if limit := width - indent; limit >= 1 {
		// The wrap always breaks after a hyphen, which would split a URL or
		// a hyphenated word that fits whole on the next line. A private-use
		// rune the text does not already contain is the same width and never
		// a breakpoint, so it stands in for each hyphen while wrapping and
		// every other byte of the text comes back out as it went in.
		stand, ok := unusedRune(s)
		if ok {
			s = strings.ReplaceAll(s, "-", stand)
		}
		s = lipgloss.Wrap(s, limit, "")
		if ok {
			s = strings.ReplaceAll(s, stand, "-")
		}
	}
	lines := strings.Split(s, "\n")
	pad := strings.Repeat(" ", indent)
	var b strings.Builder
	for i, line := range lines {
		if i == 0 {
			b.WriteString(prefix)
		} else {
			b.WriteString(pad)
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

// unusedRune returns a rune from Unicode's private use area that s does not
// contain, for hang to stand in for a hyphen. ok is false only if s holds
// every one of them.
func unusedRune(s string) (string, bool) {
	for r := rune(0xE000); r <= 0xF8FF; r++ {
		if !strings.ContainsRune(s, r) {
			return string(r), true
		}
	}
	return "", false
}

func printProjects(w io.Writer, projects []model.Project) error {
	tbl := &table{gap: columnGap}
	for _, p := range projects {
		tbl.row(
			styledProjectIcon(p),
			p.Title,
			areaStyle.Render(p.AreaTitle),
			styledTags(p.Tags),
		)
	}
	return tbl.render(w)
}

func printAreas(w io.Writer, areas []model.Area) error {
	tbl := &table{gap: columnGap}
	for _, a := range areas {
		var vis string
		if !a.Visible {
			vis = dimStyle.Render("(hidden)")
		}
		tbl.row(a.Title, vis)
	}
	return tbl.render(w)
}

func printTags(w io.Writer, tags []model.Tag) error {
	tbl := &table{gap: columnGap}
	for _, t := range tags {
		var shortcut string
		if t.Shortcut != "" {
			shortcut = dimStyle.Render("(" + t.Shortcut + ")")
		}
		tbl.row(t.Title, shortcut)
	}
	return tbl.render(w)
}

// isProject reports whether an item is a project rather than a to-do. Every
// view but inbox carries a project as a row of its own, `things repeating`
// carries project templates and `things search` can turn one up, so listing
// rows, detail blocks and the agent brief all have to tell the two apart.
func isProject(t *model.Task) bool {
	return t.Type == model.TypeProject
}

// kindWord is the word for what the item is. The marker on a listing row, the
// Type line in a detail block and the agent brief all say "task" or "project",
// and they say it from here — in the same words the JSON `type` field carries,
// so an agent reading a brief beside a JSON payload sees one vocabulary
// (issue #245).
//
// It is not model.TaskType.String(): a heading or a code Things has yet to
// write reads as a task on these surfaces, which is what the CLI has always
// shown.
func kindWord(t *model.Task) string {
	if isProject(t) {
		return model.TypeProject.String()
	}
	return model.TypeTask.String()
}

func statusIcon(status model.Status) string {
	switch status {
	case model.StatusOpen:
		return "[ ]"
	case model.StatusCancelled:
		return "[~]"
	case model.StatusCompleted:
		return "[x]"
	default:
		return "[ ]"
	}
}

// statusText names a status for a detail block's Status line: the word the
// JSON and the agent brief already carry, capitalised to sit beside the other
// labels. model.Status is where the words live, here and everywhere else.
func statusText(status model.Status) string {
	name := status.String()
	if name == "" {
		return name
	}
	return strings.ToUpper(name[:1]) + name[1:]
}

func projectIcon(p model.Project) string {
	if p.Status == model.StatusCompleted {
		return "●"
	}
	if p.Status == model.StatusCancelled {
		return "◌"
	}
	if p.TaskCount == 0 {
		return "○"
	}
	done := p.TaskCount - p.OpenCount
	pct := float64(done) / float64(p.TaskCount)
	switch {
	case pct == 0:
		return "○"
	case pct <= 0.25:
		return "◔"
	case pct <= 0.50:
		return "◑"
	case pct < 1.0:
		return "◕"
	default:
		return "●"
	}
}
