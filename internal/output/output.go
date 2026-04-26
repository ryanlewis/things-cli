package output

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/ryanlewis/things-cli/internal/model"
)

func Print(w io.Writer, v any, asJSON bool) error {
	if asJSON {
		return printJSON(w, v)
	}
	switch val := v.(type) {
	case []model.Task:
		return printTasks(w, val)
	case *model.Task:
		return printTaskDetail(w, val, nil)
	case []model.Project:
		return printProjects(w, val)
	case []model.Area:
		return printAreas(w, val)
	case []model.Tag:
		return printTags(w, val)
	default:
		return printJSON(w, v)
	}
}

func PrintTaskWithChecklist(w io.Writer, t *model.Task, items []model.ChecklistItem, asJSON bool) error {
	if asJSON {
		type taskWithChecklist struct {
			*model.Task
			Checklist []model.ChecklistItem `json:"checklist,omitempty"`
		}
		return printJSON(w, taskWithChecklist{Task: t, Checklist: items})
	}
	return printTaskDetail(w, t, items)
}

func printJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func printTasks(w io.Writer, tasks []model.Task) error {
	type row struct {
		num, status, title, tags, date string
		groupKey, groupTitle           string
		isProjectGroup                 bool
	}
	rows := make([]row, len(tasks))
	var numW, statusW, titleW, tagsW int
	for i, t := range tasks {
		r := row{
			num:    fmt.Sprintf("%d.", i+1),
			status: statusIcon(t.Status),
			title:  t.Title,
		}
		if t.Start == model.StartAnytime && t.StartBucket == 0 && t.StartDate != nil {
			r.title = "\u2605 " + r.title
		}
		if len(t.Tags) > 0 {
			r.tags = "[" + strings.Join(t.Tags, ", ") + "]"
		}
		switch {
		case t.Deadline != nil:
			r.date = "due:" + t.Deadline.String()
		case t.StartDate != nil:
			r.date = t.StartDate.String()
		}
		if t.ProjectUUID != "" {
			r.groupKey = t.ProjectUUID
			r.groupTitle = t.ProjectTitle
			r.isProjectGroup = true
		} else {
			r.groupKey = t.AreaUUID
			r.groupTitle = t.AreaTitle
		}
		rows[i] = r

		if n := len(r.num); n > numW {
			numW = n
		}
		if n := len(r.status); n > statusW {
			statusW = n
		}
		// Length is fine for ASCII; \u2605 is multibyte but rendered single-cell.
		if n := runeWidth(r.title); n > titleW {
			titleW = n
		}
		if n := len(r.tags); n > tagsW {
			tagsW = n
		}
	}

	const sentinel = "\x00"
	currentProject, currentArea := sentinel, sentinel
	for _, r := range rows {
		current := &currentArea
		other := &currentProject
		if r.isProjectGroup {
			current, other = &currentProject, &currentArea
		}
		if r.groupKey != *current || *other != sentinel {
			if currentProject != sentinel || currentArea != sentinel {
				fmt.Fprintln(w)
			}
			if r.groupTitle != "" {
				fmt.Fprintf(w, "    %s\n", r.groupTitle)
			}
			*current = r.groupKey
			*other = sentinel
		}
		fmt.Fprintf(w, "%-*s  %-*s  %-*s  %-*s  %s\n",
			numW, r.num,
			statusW, r.status,
			titleW, r.title,
			tagsW, r.tags,
			r.date,
		)
	}
	return nil
}

// runeWidth returns the visible width of s (rune count). Good enough for the
// titles we print \u2014 no combining marks or East-Asian wide chars in practice.
func runeWidth(s string) int {
	n := 0
	for range s {
		n++
	}
	return n
}

func printTaskDetail(w io.Writer, t *model.Task, items []model.ChecklistItem) error {
	fmt.Fprintf(w, "Title:    %s\n", t.Title)
	fmt.Fprintf(w, "UUID:     %s\n", t.UUID)
	fmt.Fprintf(w, "Status:   %s\n", statusText(t.Status))
	if t.ProjectTitle != "" {
		fmt.Fprintf(w, "Project:  %s\n", t.ProjectTitle)
	}
	if t.AreaTitle != "" {
		fmt.Fprintf(w, "Area:     %s\n", t.AreaTitle)
	}
	if t.HeadingTitle != "" {
		fmt.Fprintf(w, "Heading:  %s\n", t.HeadingTitle)
	}
	if len(t.Tags) > 0 {
		fmt.Fprintf(w, "Tags:     %s\n", strings.Join(t.Tags, ", "))
	}
	if t.StartDate != nil {
		fmt.Fprintf(w, "Start:    %s\n", t.StartDate.String())
	}
	if t.Deadline != nil {
		fmt.Fprintf(w, "Deadline: %s\n", t.Deadline.String())
	}
	if t.CreationDate != nil {
		fmt.Fprintf(w, "Created:  %s\n", t.CreationDate.Format("2006-01-02 15:04"))
	}
	if t.StopDate != nil {
		fmt.Fprintf(w, "Stopped:  %s\n", t.StopDate.Format("2006-01-02 15:04"))
	}
	if t.Notes != "" {
		fmt.Fprintf(w, "Notes:\n  %s\n", strings.ReplaceAll(t.Notes, "\n", "\n  "))
	}
	if len(items) > 0 {
		fmt.Fprintln(w, "Checklist:")
		for _, item := range items {
			icon := statusIcon(item.Status)
			fmt.Fprintf(w, "  %s %s\n", icon, item.Title)
		}
	}
	return nil
}

func printProjects(w io.Writer, projects []model.Project) error {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	for _, p := range projects {
		icon := projectIcon(p)
		tags := ""
		if len(p.Tags) > 0 {
			tags = "[" + strings.Join(p.Tags, ", ") + "]"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", icon, p.Title, p.AreaTitle, tags)
	}
	return tw.Flush()
}

func projectIcon(p model.Project) string {
	if p.Status == model.StatusCompleted {
		return "\u25cf" // ●
	}
	if p.Status == model.StatusCancelled {
		return "\u25cc" // ◌
	}
	if p.TaskCount == 0 {
		return "\u25cb" // ○
	}
	done := p.TaskCount - p.OpenCount
	pct := float64(done) / float64(p.TaskCount)
	switch {
	case pct == 0:
		return "\u25cb" // ○
	case pct <= 0.25:
		return "\u25d4" // ◔
	case pct <= 0.50:
		return "\u25d1" // ◑
	case pct < 1.0:
		return "\u25d5" // ◕
	default:
		return "\u25cf" // ●
	}
}

func printAreas(w io.Writer, areas []model.Area) error {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	for _, a := range areas {
		vis := ""
		if !a.Visible {
			vis = "(hidden)"
		}
		fmt.Fprintf(tw, "%s\t%s\n", a.Title, vis)
	}
	return tw.Flush()
}

func printTags(w io.Writer, tags []model.Tag) error {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	for _, t := range tags {
		shortcut := ""
		if t.Shortcut != "" {
			shortcut = "(" + t.Shortcut + ")"
		}
		fmt.Fprintf(tw, "%s\t%s\n", t.Title, shortcut)
	}
	return tw.Flush()
}

func statusIcon(status int) string {
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

func statusText(status int) string {
	switch status {
	case model.StatusOpen:
		return "Open"
	case model.StatusCancelled:
		return "Cancelled"
	case model.StatusCompleted:
		return "Completed"
	default:
		return "Unknown"
	}
}
