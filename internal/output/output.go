package output

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/charmbracelet/lipgloss"

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
	var numW, statusW, titleW, tagsW, dateW int
	for i, t := range tasks {
		title := t.Title
		if t.Status == model.StatusCompleted || t.Status == model.StatusCancelled {
			title = titleDimStyle.Render(title)
		}
		if t.Start == model.StartAnytime && t.StartBucket == 0 && t.StartDate != nil {
			title = starStyle.Render("★") + " " + title
		}

		var date string
		switch {
		case t.Deadline != nil:
			date = styledDate(t.Deadline, true)
		case t.StartDate != nil:
			date = styledDate(t.StartDate, false)
		}

		r := row{
			num:    fmt.Sprintf("%d.", i+1),
			status: styledStatus(t.Status),
			title:  title,
			tags:   styledTags(t.Tags),
			date:   date,
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

		if n := lipgloss.Width(r.num); n > numW {
			numW = n
		}
		if n := lipgloss.Width(r.status); n > statusW {
			statusW = n
		}
		if n := lipgloss.Width(r.title); n > titleW {
			titleW = n
		}
		if n := lipgloss.Width(r.tags); n > tagsW {
			tagsW = n
		}
		if n := lipgloss.Width(r.date); n > dateW {
			dateW = n
		}
	}

	width := termWidth()
	gap := "  "
	dropTags := false
	dropDate := false
	rowWidth := numW + statusW + titleW + tagsW + dateW + 4*len(gap)
	if rowWidth > width {
		dropTags = true
		rowWidth = numW + statusW + titleW + dateW + 3*len(gap)
		if rowWidth > width {
			dropDate = true
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
				fmt.Fprintf(w, "    %s\n", headerStyle.Render(r.groupTitle))
			}
			*current = r.groupKey
			*other = sentinel
		}

		cols := []string{
			lipgloss.NewStyle().Width(numW).Render(r.num),
			lipgloss.NewStyle().Width(statusW).Render(r.status),
			lipgloss.NewStyle().Width(titleW).Render(r.title),
		}
		if !dropTags {
			cols = append(cols, lipgloss.NewStyle().Width(tagsW).Render(r.tags))
		}
		if !dropDate {
			cols = append(cols, r.date)
		}
		fmt.Fprintln(w, joinWithGap(cols, gap))
	}
	return nil
}

func joinWithGap(cols []string, gap string) string {
	parts := make([]string, 0, len(cols)*2-1)
	for i, c := range cols {
		if i > 0 {
			parts = append(parts, gap)
		}
		parts = append(parts, c)
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, parts...)
}

func printTaskDetail(w io.Writer, t *model.Task, items []model.ChecklistItem) error {
	const labelW = 10
	label := func(s string) string {
		return lipgloss.NewStyle().Width(labelW).Render(labelStyle.Render(s))
	}

	fmt.Fprintf(w, "%s%s\n", label("Title:"), t.Title)
	fmt.Fprintf(w, "%s%s\n", label("UUID:"), t.UUID)
	fmt.Fprintf(w, "%s%s\n", label("Status:"), statusText(t.Status))
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
	type row struct{ icon, title, area, tags string }
	rows := make([]row, len(projects))
	var iconW, titleW, areaW int
	for i, p := range projects {
		r := row{
			icon:  styledProjectIcon(p),
			title: p.Title,
			area:  areaStyle.Render(p.AreaTitle),
			tags:  styledTags(p.Tags),
		}
		rows[i] = r
		if n := lipgloss.Width(r.icon); n > iconW {
			iconW = n
		}
		if n := lipgloss.Width(r.title); n > titleW {
			titleW = n
		}
		if n := lipgloss.Width(r.area); n > areaW {
			areaW = n
		}
	}
	for _, r := range rows {
		fmt.Fprintln(w, joinWithGap([]string{
			lipgloss.NewStyle().Width(iconW).Render(r.icon),
			lipgloss.NewStyle().Width(titleW).Render(r.title),
			lipgloss.NewStyle().Width(areaW).Render(r.area),
			r.tags,
		}, "  "))
	}
	return nil
}

func printAreas(w io.Writer, areas []model.Area) error {
	type row struct{ title, vis string }
	rows := make([]row, len(areas))
	var titleW int
	for i, a := range areas {
		r := row{title: a.Title}
		if !a.Visible {
			r.vis = dateNormalStyle.Render("(hidden)")
		}
		rows[i] = r
		if n := lipgloss.Width(r.title); n > titleW {
			titleW = n
		}
	}
	for _, r := range rows {
		fmt.Fprintln(w, joinWithGap([]string{
			lipgloss.NewStyle().Width(titleW).Render(r.title),
			r.vis,
		}, "  "))
	}
	return nil
}

func printTags(w io.Writer, tags []model.Tag) error {
	type row struct{ title, shortcut string }
	rows := make([]row, len(tags))
	var titleW int
	for i, t := range tags {
		r := row{title: t.Title}
		if t.Shortcut != "" {
			r.shortcut = dateNormalStyle.Render("(" + t.Shortcut + ")")
		}
		rows[i] = r
		if n := lipgloss.Width(r.title); n > titleW {
			titleW = n
		}
	}
	for _, r := range rows {
		fmt.Fprintln(w, joinWithGap([]string{
			lipgloss.NewStyle().Width(titleW).Render(r.title),
			r.shortcut,
		}, "  "))
	}
	return nil
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
