package output

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

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
	return enc.Encode(v)
}

// taskColumns names the columns a task row carries, so the drop order below
// and the cells added for each row cannot fall out of step.
const (
	colNum = iota
	colStatus
	colTitle
	colTags
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
		dropOrder: []int{colTags, colDate},
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

		tbl.row(
			fmt.Sprintf("%d.", i+1),
			styledStatus(t.Status),
			title,
			styledTags(t.Tags),
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

	fmt.Fprintf(w, "%s%s\n", label("Title:"), t.Title)
	fmt.Fprintf(w, "%s%s\n", label("UUID:"), t.UUID)
	fmt.Fprintf(w, "%s%s\n", label("Status:"), statusText(t.Status))
	// A lookup resolves projects as well as to-dos, and `things repeating`
	// hands out indexes for project templates, so say when the thing being
	// shown is a project rather than leaving its detail block reading as a
	// to-do's. To-do output is unchanged.
	if isProject(t) {
		fmt.Fprintf(w, "%s%s\n", label("Type:"), kindWord(t))
	}
	if t.ProjectTitle != "" {
		fmt.Fprintf(w, "%s%s\n", label("Project:"), projectStyle.Render(t.ProjectTitle))
	}
	if t.AreaTitle != "" {
		fmt.Fprintf(w, "%s%s\n", label("Area:"), areaStyle.Render(t.AreaTitle))
	}
	if t.HeadingTitle != "" {
		fmt.Fprintf(w, "%s%s\n", label("Heading:"), t.HeadingTitle)
	}
	if len(t.Tags) > 0 {
		fmt.Fprintf(w, "%s%s\n", label("Tags:"), tagStyle.Render(strings.Join(t.Tags, ", ")))
	}
	if t.StartDate != nil {
		fmt.Fprintf(w, "%s%s\n", label("Start:"), styledDate(t.StartDate, false))
	}
	if t.Deadline != nil {
		fmt.Fprintf(w, "%s%s\n", label("Deadline:"), styledDate(t.Deadline, false))
	}
	if t.Repeating {
		fmt.Fprintf(w, "%s%s\n", label("Repeats:"), "yes (Things blocks status, when and deadline edits)")
	}
	if t.CreationDate != nil {
		fmt.Fprintf(w, "%s%s\n", label("Created:"), t.CreationDate.Format("2006-01-02 15:04"))
	}
	if t.StopDate != nil {
		fmt.Fprintf(w, "%s%s\n", label("Stopped:"), t.StopDate.Format("2006-01-02 15:04"))
	}
	if t.Notes != "" {
		fmt.Fprintf(w, "%s\n  %s\n", labelStyle.Render("Notes:"), strings.ReplaceAll(t.Notes, "\n", "\n  "))
	}
	if len(items) > 0 {
		fmt.Fprintln(w, labelStyle.Render("Checklist:"))
		for _, item := range items {
			fmt.Fprintf(w, "  %s %s\n", styledStatus(item.Status), item.Title)
		}
	}
	return nil
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
