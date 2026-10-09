package output

import (
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/ryanlewis/things-cli/internal/model"
)

// Styled output renders full-fidelity ANSI; the printers below downsample or
// strip it on the way out, through newWriter, according to the active color
// profile. JSON carries no ANSI, so it is written to the raw writer.

// PrintProjects renders a project listing.
func PrintProjects(w io.Writer, projects []model.Project, asJSON bool) error {
	if asJSON {
		return PrintJSON(w, projects)
	}
	return printProjects(newWriter(w), projects, currentLayout())
}

// PrintAreas renders an area listing.
func PrintAreas(w io.Writer, areas []model.Area, asJSON bool) error {
	if asJSON {
		return PrintJSON(w, areas)
	}
	return printAreas(newWriter(w), areas)
}

// PrintTags renders a tag listing.
func PrintTags(w io.Writer, tags []model.Tag, asJSON bool) error {
	if asJSON {
		return PrintJSON(w, tags)
	}
	return printTags(newWriter(w), tags)
}

// PrintTaskList renders a task listing. When view is non-empty it prefixes a
// line naming the view the listing was drawn from, so a filtered slice of a
// view is not mistaken for the filter target's full contents (issue #140).
// JSON output is the plain task array either way.
func PrintTaskList(w io.Writer, tasks []model.Task, asJSON bool, view string) error {
	if asJSON {
		return PrintJSON(w, tasks)
	}
	tw := newWriter(w)
	if view != "" {
		fmt.Fprintf(tw, "%s%s\n", headerIndent, dimStyle.Render("view: "+view))
	}
	return printTasks(tw, tasks, currentLayout())
}

func PrintTaskWithChecklist(w io.Writer, t *model.Task, items []model.ChecklistItem, asJSON bool) error {
	if asJSON {
		type taskWithChecklist struct {
			*model.Task
			Checklist []model.ChecklistItem `json:"checklist,omitempty"`
		}
		return PrintJSON(w, taskWithChecklist{Task: t, Checklist: items})
	}
	return printTaskDetail(newWriter(w), t, items, currentLayout())
}

// PrintJSON writes v as indented JSON. Every --json payload goes through it,
// so the encoder settings live in one place.
func PrintJSON(w io.Writer, v any) error {
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
	colChecklist
	colTags
	colStart
	colDate
)

// headerIndent sets a task listing's group headers, and its view line, in
// from the rows.
const headerIndent = "    "

func printTasks(w io.Writer, tasks []model.Task, lay layout) error {
	tbl := &table{
		gap: columnGap,
		// Unlike every other surface, a task listing fits lay.width even when
		// piped, where it is termWidth's 120-column fallback: an over-long row
		// gives up whole columns there, though no cell is cut or shortened.
		maxWidth: lay.width,
		// When a row will not fit, checklist progress goes first, since
		// `things show` has the checklist itself. The start date beside a
		// deadline is next: the date column still carries the deadline, as it
		// does on a narrow terminal. Both go before any title is cut.
		dropFirst: []int{colChecklist, colStart},
		// Then, once titles are at their soft minimum, the tags and the date
		// switch to their short forms on a terminal, and only then does the
		// tags column go, then the date: a row still says what it is and when
		// it is due for as long as it can.
		dropOrder: []int{colTags, colDate},
	}
	// A group header is cut to fit behind its indent, and, as with titles,
	// only on a terminal: piped output keeps every header whole.
	headerWidth := 0
	if lay.tty {
		headerWidth = max(lay.width-len(headerIndent), 1)
		// Once the checklist progress and the extra start date have gone, an
		// over-long title gives up its end, down to 40 columns, before the tags
		// or the date are dropped from every row for it: 40 still reads as the
		// task it names, and at 80 columns leaves room for the tags and the
		// date. Once nothing is left to drop, it goes on down to 10 rather than
		// wrapping under the row numbers. Only on a terminal: piped output
		// keeps every title whole.
		tbl.shrink = []shrinkCol{{col: colTitle, min: 10, soft: 40}}
	}
	for i := range tasks {
		tbl.add(taskCells(i+1, &tasks[i], lay.tty)...)
	}

	headers := groupHeaders(tasks)
	for i, line := range tbl.lines() {
		h := headers[i]
		if h.gap {
			fmt.Fprintln(w)
		}
		if h.title != "" {
			fmt.Fprintf(w, "%s%s\n", headerIndent, headerStyle.Render(fitHeader(h.title, headerWidth)))
		}
		fmt.Fprintln(w, line)
	}
	return nil
}

// taskCells is the row a task listing prints for t, numbered n. The compact
// forms of its tags and date go in only on a terminal: a relative date goes
// stale in a saved file and defeats grep, so piped output keeps the full date
// or none.
func taskCells(n int, t *model.Task, tty bool) []cell {
	title := oneLine(t.Title)
	if t.Status == model.StatusCompleted || t.Status == model.StatusCancelled {
		title = titleDimStyle.Render(title)
	}
	// Things shows a to-do's reminder time on its row, ahead of the title. A
	// reminder is a time on the start date, so it needs one to print. A row
	// without one is printed as before.
	if t.ReminderTime != nil && t.StartDate != nil {
		title = dimStyle.Render(*t.ReminderTime) + " " + title
	}
	if t.Start == model.StartAnytime && t.StartBucket == 0 && t.StartDate != nil {
		title = starStyle.Render("★") + " " + title
	}
	// A task list is normally to-dos only, but `things repeating` carries
	// project templates too and `things search` can turn up a project, so
	// say which rows are projects rather than letting them read as to-dos.
	// Text, not just colour, so it survives --color never. It goes in as the
	// title's tail, so cutting a long title keeps it.
	var marker string
	if isProject(t) {
		marker = " " + dimStyle.Render("("+kindWord(t)+")")
	}

	// The date column shows the deadline over the start date, so a task with
	// both carries its start date in a column of its own, before the
	// deadline. The date's compact form ("due:Fri") stands in for it on a
	// terminal too narrow for the full one, before the column is dropped.
	var start, date, compactDate string
	switch {
	case t.Deadline != nil:
		date = styledDate(t.Deadline, true)
		compactDate = styledCompactDate(t.Deadline, true)
		start = styledDate(t.StartDate, false)
	case t.StartDate != nil:
		date = styledDate(t.StartDate, false)
		compactDate = styledCompactDate(t.StartDate, false)
	}
	tags := cell{text: styledTags(t.Tags)}
	dateCell := cell{text: date}
	if tty {
		tags.alt = styledCompactTags(t.Tags)
		dateCell.alt = compactDate
	}

	return []cell{
		colNum:       {text: fmt.Sprintf("%d.", n)},
		colStatus:    {text: styledStatus(t.Status)},
		colTitle:     {text: title, tail: marker},
		colChecklist: {text: styledChecklist(t.ChecklistProgress)},
		colTags:      tags,
		colStart:     {text: start},
		colDate:      dateCell,
	}
}

// header is what a task listing prints above a row: a blank line, closing the
// group before, and the title of the group the row opens. A group with no
// title still gets its blank line; one folded under the row above gets
// neither.
type header struct {
	gap   bool
	title string
}

// groupHeaders works out the header above each task's row. Rows group under
// their project, or else their area; a change of group, or a move between a
// project and an area, opens a new one.
func groupHeaders(tasks []model.Task) []header {
	headers := make([]header, len(tasks))
	const sentinel = "\x00"
	currentProject, currentArea := sentinel, sentinel
	prevUUID := ""
	for i := range tasks {
		t := &tasks[i]
		key, title, isProject := t.AreaUUID, oneLine(t.AreaTitle), false
		if t.ProjectUUID != "" {
			key, title, isProject = t.ProjectUUID, oneLine(t.ProjectTitle), true
		}
		current, other := &currentArea, &currentProject
		if isProject {
			current, other = &currentProject, &currentArea
		}
		if key != *current || *other != sentinel {
			// Every view but inbox lists a project as a row of its own
			// (issues #201, #206, #212, #213). Where the view's order puts its
			// to-dos straight after that row, their project group header
			// would restate the title on the line above, so fold them under
			// the row instead of repeating it. Where the order separates them
			// the header still prints, which is what it is for. The group
			// state advances either way, so a later group breaks as usual.
			if foldsIntoRowAbove := isProject && key == prevUUID; !foldsIntoRowAbove {
				headers[i] = header{
					gap:   currentProject != sentinel || currentArea != sentinel,
					title: title,
				}
			}
			*current = key
			*other = sentinel
		}
		prevUUID = t.UUID
	}
	return headers
}

// oneLine puts text from the database on one line, for a listing row or a
// group header: each line break (\r\n, \r, \n, a vertical tab, a form feed,
// or Unicode's NEL, line and paragraph separators) and each tab becomes a
// space. Left in, a line break splits the row (lipgloss splits on NEL as it
// does on \n, and a terminal moves down a line on a vertical tab or form
// feed), and a tab, which measures as no columns but prints as several,
// throws the padding out and wraps it. A space keeps the words apart and the
// text greppable as one line per row; the detail block and --json still carry
// the text as written.
func oneLine(s string) string {
	return oneLineReplacer.Replace(s)
}

// OneLine is oneLine for the command layer, for a list it prints itself:
// the candidates of an ambiguous task reference.
func OneLine(s string) string {
	return oneLine(s)
}

var oneLineReplacer = strings.NewReplacer(
	"\r\n", " ", "\r", " ", "\n", " ", "\t", " ", "\v", " ", "\f", " ",
	"\u0085", " ", "\u2028", " ", "\u2029", " ",
)

// fitHeader cuts a group header short with an ellipsis so that it fits width
// rather than wrapping. A width of zero keeps the header whole.
func fitHeader(title string, width int) string {
	if width <= 0 {
		return title
	}
	return ansi.Truncate(title, width, "…")
}

func printTaskDetail(w io.Writer, t *model.Task, items []model.ChecklistItem, lay layout) error {
	label := func(s string) string {
		return padCol(10, labelStyle.Render(s))
	}
	// Only a terminal has a width to fit; piped output keeps every value on
	// the lines it was written on.
	width := lay.fitWidth()
	field := func(name, value string) {
		fmt.Fprint(w, hang(label(name), value, width))
	}

	field("Title:", t.Title)
	field("UUID:", t.UUID)
	// JSON carries "trashed"; the plain block says it on the status line, so
	// an item in the Trash never reads as an ordinary open one.
	status := statusText(t.Status)
	if t.Trashed {
		status += " (in Trash)"
	}
	field("Status:", status)
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
		start := styledDate(t.StartDate, false)
		// The reminder is a time on the start date, so it goes on the Start
		// line in the same "2006-01-02 15:04" shape as Created and Stopped.
		if t.ReminderTime != nil {
			start += " " + dimStyle.Render(*t.ReminderTime)
		}
		field("Start:", start)
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
//
// When wrapping, each of s's own lines wraps on its own: tabs are expanded
// (see expandTabs), a line that starts with an indent or a list marker hangs
// its wrapped lines under the text after it (see listLead), and no line ends
// in whitespace. Unwrapped output keeps every byte of s, tabs included.
func hang(prefix, s string, width int) string {
	indent := lipgloss.Width(prefix)
	pad := strings.Repeat(" ", indent)
	limit := width - indent
	if limit < 1 {
		var b strings.Builder
		for i, line := range strings.Split(s, "\n") {
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
	// The wrap always breaks after a hyphen, which would split a URL or a
	// hyphenated word that fits whole on the next line. A private-use rune
	// the text does not already contain is the same width and never a
	// breakpoint, so it stands in for each hyphen while wrapping and every
	// other byte of the text comes back out as it went in.
	stand, ok := unusedRune(s)
	var b strings.Builder
	first := true
	for _, line := range strings.Split(s, "\n") {
		line = expandTabs(line)
		blank := strings.TrimSpace(line) == ""
		lead := listLead(line)
		leadWidth := ansi.StringWidth(lead)
		// A lead that leaves under half the width would squeeze the text
		// into a sliver, so such a line wraps back to the base indent.
		if leadWidth > limit/2 {
			lead, leadWidth = "", 0
		}
		body := line[len(lead):]
		if ok {
			body = strings.ReplaceAll(body, "-", stand)
		}
		body = lipgloss.Wrap(body, limit-leadWidth, "")
		if ok {
			body = strings.ReplaceAll(body, stand, "-")
		}
		hangPad := strings.Repeat(" ", leadWidth)
		for i, part := range strings.Split(body, "\n") {
			if i == 0 {
				part = lead + part
			} else {
				part = hangPad + part
			}
			part = strings.TrimRight(part, " ")
			// An indent wider than the space left wraps onto a line of its
			// own; dropping that keeps a blank line from opening up in the
			// middle of the text, which then starts at the base indent.
			if part == "" && !blank {
				continue
			}
			start := pad
			if first {
				start = prefix
				first = false
			}
			if part == "" {
				start = strings.TrimRight(start, " ")
			}
			b.WriteString(start)
			b.WriteString(part)
			b.WriteString("\n")
		}
	}
	return b.String()
}

// tabStop is the column interval expandTabs moves a tab to. A terminal's 8
// would spend a quarter of a 32-column wrap on one level of nesting.
const tabStop = 4

// expandTabs replaces each tab in line with the spaces that reach the next
// multiple of tabStop, counted from the start of line. The wrap measures a
// tab as no columns wide, so a line with tabs would otherwise run past the
// edge. Stops (rather than a fixed 4 spaces) keep text the user lined up in
// columns with tabs lined up.
func expandTabs(line string) string {
	if !strings.Contains(line, "\t") {
		return line
	}
	var b strings.Builder
	col := 0
	for i, seg := range strings.Split(line, "\t") {
		if i > 0 {
			n := tabStop - col%tabStop
			b.WriteString(strings.Repeat(" ", n))
			col += n
		}
		b.WriteString(seg)
		col += ansi.StringWidth(seg)
	}
	return b.String()
}

// listItem matches the lead of a line that reads as a list item or an
// indented line: leading spaces, then optionally a bullet ("- ", "* ", "• ")
// or a number ("1. ", "1) ") and the spaces after it. Tabs are already
// expanded by the time it runs.
var listItem = regexp.MustCompile(`^ *(?:(?:[-*•]|[0-9]{1,3}[.)]) +)?`)

// listLead returns the part of line that hang indents its wrapped lines past,
// or "" when line is blank or starts with its text.
func listLead(line string) string {
	lead := listItem.FindString(line)
	if lead == line {
		return ""
	}
	return lead
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

// projectColumns names the columns a project row carries, so the drop order
// below and the cells added for each row cannot fall out of step.
const (
	colProjectIcon = iota
	colProjectTitle
	colProjectArea
	colProjectTags
)

func printProjects(w io.Writer, projects []model.Project, lay layout) error {
	// On a terminal, when the widest row will not fit, an over-long title is
	// cut to 30 and an over-long area to 20 first; then every row gives up its
	// tags, then its area, and then titles are cut down to 10. A project
	// list has fewer columns than a task list, so its title needs less room
	// kept for them, and 20 still names an area. Piped output has no width to
	// fit (fitWidth is 0), so every column stays whole.
	tbl := &table{
		gap:       columnGap,
		maxWidth:  lay.fitWidth(),
		dropOrder: []int{colProjectTags, colProjectArea},
		shrink: []shrinkCol{
			{col: colProjectTitle, min: 10, soft: 30},
			{col: colProjectArea, min: 20, soft: 20},
		},
	}
	for _, p := range projects {
		title := oneLine(p.Title)
		if p.Status == model.StatusCompleted || p.Status == model.StatusCancelled {
			title = titleDimStyle.Render(title)
		}
		tbl.row(
			styledProjectIcon(p),
			title,
			areaStyle.Render(oneLine(p.AreaTitle)),
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
		tbl.row(oneLine(a.Title), vis)
	}
	return tbl.render(w)
}

func printTags(w io.Writer, tags []model.Tag) error {
	tbl := &table{gap: columnGap}
	for _, t := range tags {
		var shortcut string
		if t.Shortcut != "" {
			shortcut = dimStyle.Render("(" + oneLine(t.Shortcut) + ")")
		}
		tbl.row(oneLine(t.Title), shortcut)
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

// projectIcon is a project row's marker: a closed project takes the [x] or
// [~] a closed task carries, and an open one a circle filled to show how much
// of its work is done. A closed project used to take a full circle, the one an
// open project with every task done also takes, and `things projects` lists
// closed projects by default until they are logged, so the two have to read
// differently.
func projectIcon(p model.Project) string {
	if p.Status == model.StatusCompleted || p.Status == model.StatusCancelled {
		return statusIcon(p.Status)
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
