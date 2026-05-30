package mcpserver

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ryanlewis/things-cli/internal/db"
	"github.com/ryanlewis/things-cli/internal/model"
	"github.com/ryanlewis/things-cli/internal/output"
	"github.com/ryanlewis/things-cli/internal/things"
)

// toolset binds the per-call backend opener that every tool handler shares.
type toolset struct {
	open func() (Backend, error)
}

// Tool descriptions are the entire UX surface for hosts without Bash or the
// agent skill (Claude Desktop), so they spell out behavior and return shape.
const (
	listDesc = "List Things3 to-dos from a built-in list/view as JSON. " +
		"Views: today, inbox, upcoming, anytime, someday, logbook, trash, deadlines (default: today). " +
		"Optionally filter by project, area, or tag, and by scheduled date (or deadline, on the deadlines view) " +
		"with on / from / to. Read-only; returns the same JSON as `things <view> --json`."
	showDesc = "Show one Things3 to-do (or project) in full — including notes and checklist items — as JSON. " +
		"Accepts a UUID (preferred) or a title; a title matching several to-dos returns the candidates so you can retry with a UUID. " +
		"Read-only."
	searchDesc = "Search Things3 to-dos by a case-insensitive substring of their title or notes, excluding trashed items. " +
		"Returns a JSON array of matching tasks. Read-only."
	projectsDesc = "List Things3 projects as JSON, optionally filtered by area and optionally including completed projects. Read-only."
	areasDesc    = "List Things3 areas as JSON. Read-only."
	tagsDesc     = "List Things3 tags as JSON. Read-only."
)

// registerTools wires the six read-only tools onto the server. Input schemas
// are inferred from the struct types (jsonschema tags supply field
// descriptions; fields without `omitempty` are required).
func registerTools(s *mcp.Server, t toolset) {
	mcp.AddTool(s, &mcp.Tool{Name: "things_list", Description: listDesc}, t.list)
	mcp.AddTool(s, &mcp.Tool{Name: "things_show", Description: showDesc}, t.show)
	mcp.AddTool(s, &mcp.Tool{Name: "things_search", Description: searchDesc}, t.search)
	mcp.AddTool(s, &mcp.Tool{Name: "things_projects", Description: projectsDesc}, t.projects)
	mcp.AddTool(s, &mcp.Tool{Name: "things_areas", Description: areasDesc}, t.areas)
	mcp.AddTool(s, &mcp.Tool{Name: "things_tags", Description: tagsDesc}, t.tags)
}

type listInput struct {
	View    string `json:"view,omitempty" jsonschema:"Built-in list to read: today, inbox, upcoming, anytime, someday, logbook, trash, or deadlines. Defaults to today (or to the given project's tasks when project is set)."`
	Project string `json:"project,omitempty" jsonschema:"Filter by project name or UUID. When set without an explicit view, lists that project's tasks."`
	Area    string `json:"area,omitempty" jsonschema:"Filter by area name or UUID."`
	Tag     string `json:"tag,omitempty" jsonschema:"Filter by tag name."`
	On      string `json:"on,omitempty" jsonschema:"Only tasks scheduled on this date (YYYY-MM-DD or RFC3339); on the deadlines view, filters by deadline. Mutually exclusive with from/to."`
	From    string `json:"from,omitempty" jsonschema:"Only tasks scheduled on or after this date (YYYY-MM-DD or RFC3339); on the deadlines view, filters by deadline."`
	To      string `json:"to,omitempty" jsonschema:"Only tasks scheduled on or before this date (YYYY-MM-DD or RFC3339); on the deadlines view, filters by deadline."`
}

type showInput struct {
	Task string `json:"task" jsonschema:"Task title or UUID to show. Prefer a UUID; a title that matches several to-dos returns the candidates instead."`
}

type searchInput struct {
	Query string `json:"query" jsonschema:"Substring matched (case-insensitively) against task titles and notes. Trashed tasks are excluded."`
}

type projectsInput struct {
	Area      string `json:"area,omitempty" jsonschema:"Filter by area name or UUID."`
	Completed bool   `json:"completed,omitempty" jsonschema:"Include completed projects (default false)."`
}

// emptyInput is the object schema for tools that take no arguments.
type emptyInput struct{}

func (t toolset) list(_ context.Context, _ *mcp.CallToolRequest, in listInput) (*mcp.CallToolResult, any, error) {
	view, filter, err := resolveListQuery(in)
	if err != nil {
		return nil, nil, err
	}
	return t.query(func(b Backend) (any, error) { return b.ListTasks(view, filter) })
}

func (t toolset) search(_ context.Context, _ *mcp.CallToolRequest, in searchInput) (*mcp.CallToolResult, any, error) {
	return t.query(func(b Backend) (any, error) { return b.SearchTasks(in.Query) })
}

func (t toolset) projects(_ context.Context, _ *mcp.CallToolRequest, in projectsInput) (*mcp.CallToolResult, any, error) {
	return t.query(func(b Backend) (any, error) { return b.ListProjects(in.Area, in.Completed) })
}

func (t toolset) areas(_ context.Context, _ *mcp.CallToolRequest, _ emptyInput) (*mcp.CallToolResult, any, error) {
	return t.query(func(b Backend) (any, error) { return b.ListAreas() })
}

func (t toolset) tags(_ context.Context, _ *mcp.CallToolRequest, _ emptyInput) (*mcp.CallToolResult, any, error) {
	return t.query(func(b Backend) (any, error) { return b.ListTags() })
}

func (t toolset) show(_ context.Context, _ *mcp.CallToolRequest, in showInput) (*mcp.CallToolResult, any, error) {
	b, err := t.open()
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = b.Close() }()
	task, err := b.GetTask(in.Task)
	if err != nil {
		var ambig *db.AmbiguousTaskError
		if errors.As(err, &ambig) {
			return nil, nil, ambiguousError(ambig)
		}
		return nil, nil, err
	}
	items, err := b.GetChecklistItems(task.UUID)
	if err != nil {
		return nil, nil, err
	}
	var buf bytes.Buffer
	if err := output.PrintTaskWithChecklist(&buf, task, items, true); err != nil {
		return nil, nil, fmt.Errorf("rendering result: %w", err)
	}
	return textResult(buf.String()), nil, nil
}

// query opens a fresh backend (open-per-call, mirroring the CLI), runs fetch,
// renders the result as the CLI's --json output, and closes the backend.
func (t toolset) query(fetch func(Backend) (any, error)) (*mcp.CallToolResult, any, error) {
	b, err := t.open()
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = b.Close() }()
	v, err := fetch(b)
	if err != nil {
		return nil, nil, err
	}
	return jsonResult(v)
}

// resolveListQuery mirrors ListCmd.Run: default the view (to the project view
// when a project is given, else today), validate it, and apply date filters.
func resolveListQuery(in listInput) (string, db.TaskFilter, error) {
	view := in.View
	if view == "" {
		if in.Project != "" {
			view = "project"
		} else {
			view = "today"
		}
	}
	if !db.ValidView(view) {
		return "", db.TaskFilter{}, fmt.Errorf("unknown view %q (valid: today, inbox, upcoming, anytime, someday, logbook, trash, deadlines)", view)
	}
	filter := db.TaskFilter{Project: in.Project, Area: in.Area, Tag: in.Tag}
	if err := applyDateFilters(&filter, view, in.On, in.From, in.To); err != nil {
		return "", db.TaskFilter{}, err
	}
	return view, filter, nil
}

// applyDateFilters mirrors the CLI's helper of the same name (cmd/things): it
// validates the on/from/to combination against the view, parses each into a
// ThingsDate, and rejects an inverted range. It is a deliberate copy — the
// CLI's version lives in package main and can't be imported, and hoisting it
// into internal/db would couple db to internal/things for date parsing. The
// behavior here is pinned by the date-filter tests in this package; keep the
// two in sync if either changes.
func applyDateFilters(filter *db.TaskFilter, view, on, from, to string) error {
	if on == "" && from == "" && to == "" {
		return nil
	}
	if !db.DateFilterableView(view) {
		return fmt.Errorf("on/from/to are not supported on the %q view", view)
	}
	if on != "" && (from != "" || to != "") {
		return fmt.Errorf("on cannot be combined with from/to")
	}

	parse := func(field, raw string) (*model.ThingsDate, error) {
		if raw == "" {
			return nil, nil
		}
		tm, err := things.ParseListDate(field, raw)
		if err != nil {
			return nil, err
		}
		d := model.ThingsDateFromTime(tm)
		return &d, nil
	}

	var err error
	if filter.On, err = parse("on", on); err != nil {
		return err
	}
	if filter.From, err = parse("from", from); err != nil {
		return err
	}
	if filter.To, err = parse("to", to); err != nil {
		return err
	}
	if filter.From != nil && filter.To != nil && *filter.From > *filter.To {
		return fmt.Errorf("from %s is after to %s", filter.From, filter.To)
	}
	return nil
}

// jsonResult renders v as the CLI's --json output and wraps it as tool content.
func jsonResult(v any) (*mcp.CallToolResult, any, error) {
	var buf bytes.Buffer
	if err := output.Print(&buf, v, true); err != nil {
		return nil, nil, fmt.Errorf("rendering result: %w", err)
	}
	return textResult(buf.String()), nil, nil
}

func textResult(s string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: s}}}
}

// ambiguousError turns a title that matches several to-dos into a tool error
// listing the candidates, so the caller can retry with a UUID.
func ambiguousError(e *db.AmbiguousTaskError) error {
	var b strings.Builder
	fmt.Fprintf(&b, "ambiguous task %q — matches %d tasks; retry with a UUID:", e.Query, len(e.Matches))
	for _, m := range e.Matches {
		if m.ProjectTitle != "" {
			fmt.Fprintf(&b, "\n  - %s  (%s)  [%s]", m.Title, m.UUID, m.ProjectTitle)
		} else {
			fmt.Fprintf(&b, "\n  - %s  (%s)", m.Title, m.UUID)
		}
	}
	return errors.New(b.String())
}
