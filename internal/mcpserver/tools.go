package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ryanlewis/things-cli/internal/db"
	"github.com/ryanlewis/things-cli/internal/model"
	"github.com/ryanlewis/things-cli/internal/output"
	"github.com/ryanlewis/things-cli/internal/things"
)

// toolset binds the per-call backend opener and the write surface that the tool
// handlers share. open is used by read tools and by write tools that must
// resolve a task reference or read the auth token; write is the mutation surface.
type toolset struct {
	open  func() (Backend, error)
	write Writer
}

// Tool descriptions are the entire UX surface for hosts without Bash or the
// agent skill (Claude Desktop), so they spell out behavior and return shape.
// Write tools shout that they MUTATE the database and whether the result is
// confirmed (synchronous AppleScript) or merely submitted (async URL scheme).
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

	addDesc = "Create a new Things3 to-do. MUTATES your Things database. " +
		"Only title is required; optionally set notes, a schedule (when), deadline, tags, checklist items, " +
		"a target list (project or area name), and a heading. Submitted via the Things URL scheme: creation is " +
		"asynchronous and NOT confirmed by this tool, and any payload error surfaces only as an in-app Things notification."
	addProjectDesc = "Create a new Things3 project. MUTATES your Things database. " +
		"Only title is required; optionally set notes, schedule, deadline, tags, an area, and initial to-dos. " +
		"Submitted asynchronously via the URL scheme and not confirmed by this tool."
	editDesc = "Edit an existing Things3 to-do. MUTATES your Things database. " +
		"Identify it by UUID (preferred) or title; an ambiguous title returns candidates so you can retry with a UUID. " +
		"Any field you set is changed, omitted fields are left as-is, and an explicit empty string clears that field. " +
		"Can also complete, cancel, or duplicate the to-do. Requires the Things URL auth token " +
		"(Things → Settings → General → Enable Things URLs). Submitted asynchronously; not confirmed by this tool."
	editProjectDesc = "Edit an existing Things3 project. MUTATES your Things database. " +
		"Identify it by UUID (preferred) or title. Set fields to change, omit to leave as-is, empty string to clear. " +
		"Requires the Things URL auth token (Things → Settings → General → Enable Things URLs). " +
		"Submitted asynchronously; not confirmed by this tool."
	completeDesc = "Mark a Things3 to-do as completed. MUTATES your Things database. " +
		"Identify it by UUID (preferred) or title. If the target is a project, the project AND all of its to-dos are " +
		"completed. Applied synchronously via AppleScript (requires Things3 to be running)."
	cancelDesc = "Cancel a Things3 to-do. MUTATES your Things database. " +
		"Identify it by UUID (preferred) or title. Applies to to-dos only — cancel a project with things_edit_project " +
		"(cancel=true). Applied synchronously via AppleScript (requires Things3 to be running)."
	logDesc = "Move completed and cancelled items from Today to the Logbook (Things → Items → Log Completed). " +
		"MUTATES your Things database; applied synchronously via AppleScript (requires Things3 to be running). Takes no arguments."
	importDesc = "Batch create or update Things3 items from a Things JSON array payload. MUTATES your Things database. " +
		"`data` is the JSON array accepted by the Things JSON URL scheme; items with operation \"update\" use the auth " +
		"token (sent automatically when configured). Submitted asynchronously and not confirmed by this tool. " +
		"Advanced — prefer things_add for a single to-do."
)

// registerTools wires the tools for the enabled toolsets onto the server. Read
// tools are always registered for an enabled toolset; write tools are added only
// when readOnly is false. Input schemas are inferred from the struct types
// (jsonschema tags supply field descriptions; fields without `omitempty` are
// required).
func registerTools(s *mcp.Server, t toolset, sets map[string]bool, readOnly bool) {
	if sets["tasks"] {
		mcp.AddTool(s, &mcp.Tool{Name: "things_list", Description: listDesc}, t.list)
		mcp.AddTool(s, &mcp.Tool{Name: "things_show", Description: showDesc}, t.show)
		mcp.AddTool(s, &mcp.Tool{Name: "things_search", Description: searchDesc}, t.search)
		if !readOnly {
			mcp.AddTool(s, &mcp.Tool{Name: "things_add", Description: addDesc}, t.add)
			mcp.AddTool(s, &mcp.Tool{Name: "things_edit", Description: editDesc}, t.edit)
			mcp.AddTool(s, &mcp.Tool{Name: "things_complete", Description: completeDesc}, t.complete)
			mcp.AddTool(s, &mcp.Tool{Name: "things_cancel", Description: cancelDesc}, t.cancel)
		}
	}
	if sets["projects"] {
		mcp.AddTool(s, &mcp.Tool{Name: "things_projects", Description: projectsDesc}, t.projects)
		if !readOnly {
			mcp.AddTool(s, &mcp.Tool{Name: "things_add_project", Description: addProjectDesc}, t.addProject)
			mcp.AddTool(s, &mcp.Tool{Name: "things_edit_project", Description: editProjectDesc}, t.editProject)
		}
	}
	if sets["areas"] {
		mcp.AddTool(s, &mcp.Tool{Name: "things_areas", Description: areasDesc}, t.areas)
	}
	if sets["tags"] {
		mcp.AddTool(s, &mcp.Tool{Name: "things_tags", Description: tagsDesc}, t.tags)
	}
	if sets["bulk"] && !readOnly {
		mcp.AddTool(s, &mcp.Tool{Name: "things_log", Description: logDesc}, t.log)
		mcp.AddTool(s, &mcp.Tool{Name: "things_import", Description: importDesc}, t.importJSON)
	}
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

// --- write tool inputs ---------------------------------------------------
//
// add/add_project take list-like fields as typed arrays (more natural for an
// agent than the CLI's delimited strings); they are joined before handing off
// to internal/things. edit/edit_project keep pointer string fields so the
// "omit = leave unchanged, empty = clear" semantics of the CLI survive exactly.

type addInput struct {
	Title     string   `json:"title" jsonschema:"Title of the new to-do (required)."`
	Notes     string   `json:"notes,omitempty" jsonschema:"Note body for the to-do."`
	When      string   `json:"when,omitempty" jsonschema:"Schedule: today, tomorrow, evening, anytime, someday, a YYYY-MM-DD date, HH:MM, YYYY-MM-DD@HH:MM, or RFC3339."`
	Deadline  string   `json:"deadline,omitempty" jsonschema:"Deadline date (YYYY-MM-DD)."`
	Tags      []string `json:"tags,omitempty" jsonschema:"Tag names to apply (must already exist in Things)."`
	Checklist []string `json:"checklist,omitempty" jsonschema:"Checklist item titles, in order."`
	List      string   `json:"list,omitempty" jsonschema:"Target list: a project or area name (or UUID)."`
	Heading   string   `json:"heading,omitempty" jsonschema:"Heading within the target project to file the to-do under."`
}

type addProjectInput struct {
	Title    string   `json:"title" jsonschema:"Title of the new project (required)."`
	Notes    string   `json:"notes,omitempty" jsonschema:"Note body for the project."`
	When     string   `json:"when,omitempty" jsonschema:"Schedule: today, tomorrow, evening, anytime, someday, a YYYY-MM-DD date, HH:MM, YYYY-MM-DD@HH:MM, or RFC3339."`
	Deadline string   `json:"deadline,omitempty" jsonschema:"Deadline date (YYYY-MM-DD)."`
	Tags     []string `json:"tags,omitempty" jsonschema:"Tag names to apply (must already exist in Things)."`
	Area     string   `json:"area,omitempty" jsonschema:"Area name or UUID to file the project under."`
	Todos    []string `json:"todos,omitempty" jsonschema:"Initial to-do titles to create inside the project, in order."`
}

type editInput struct {
	Task             string  `json:"task" jsonschema:"To-do to edit: a UUID (preferred) or title. An ambiguous title returns candidates to retry with a UUID."`
	Title            *string `json:"title,omitempty" jsonschema:"Replace the title."`
	Notes            *string `json:"notes,omitempty" jsonschema:"Replace the notes (empty string clears them)."`
	PrependNotes     *string `json:"prepend_notes,omitempty" jsonschema:"Text to insert before the existing notes."`
	AppendNotes      *string `json:"append_notes,omitempty" jsonschema:"Text to append after the existing notes."`
	When             *string `json:"when,omitempty" jsonschema:"Reschedule (today/tomorrow/evening/anytime/someday, YYYY-MM-DD, HH:MM, YYYY-MM-DD@HH:MM, RFC3339); empty string clears the schedule."`
	Deadline         *string `json:"deadline,omitempty" jsonschema:"Set the deadline (YYYY-MM-DD); empty string clears it."`
	Tags             *string `json:"tags,omitempty" jsonschema:"Replace all tags with this comma-separated list; empty string clears tags."`
	AddTags          *string `json:"add_tags,omitempty" jsonschema:"Add these comma-separated tags to the existing tags."`
	Checklist        *string `json:"checklist,omitempty" jsonschema:"Replace checklist items (newline-separated)."`
	PrependChecklist *string `json:"prepend_checklist,omitempty" jsonschema:"Checklist items (newline-separated) to insert before the existing ones."`
	AppendChecklist  *string `json:"append_checklist,omitempty" jsonschema:"Checklist items (newline-separated) to append after the existing ones."`
	List             *string `json:"list,omitempty" jsonschema:"Move to a list/project by name."`
	ListID           *string `json:"list_id,omitempty" jsonschema:"Move to a list/project by UUID."`
	Heading          *string `json:"heading,omitempty" jsonschema:"Set the heading within the project by name."`
	HeadingID        *string `json:"heading_id,omitempty" jsonschema:"Set the heading by UUID."`
	Complete         bool    `json:"complete,omitempty" jsonschema:"Mark the to-do completed."`
	Cancel           bool    `json:"cancel,omitempty" jsonschema:"Mark the to-do canceled."`
	Duplicate        bool    `json:"duplicate,omitempty" jsonschema:"Duplicate the to-do before applying edits."`
	Reveal           bool    `json:"reveal,omitempty" jsonschema:"Reveal the to-do in Things after editing."`
}

type editProjectInput struct {
	Project      string  `json:"project" jsonschema:"Project to edit: a UUID (preferred) or title."`
	Title        *string `json:"title,omitempty" jsonschema:"Replace the title."`
	Notes        *string `json:"notes,omitempty" jsonschema:"Replace the notes (empty string clears them)."`
	PrependNotes *string `json:"prepend_notes,omitempty" jsonschema:"Text to insert before the existing notes."`
	AppendNotes  *string `json:"append_notes,omitempty" jsonschema:"Text to append after the existing notes."`
	When         *string `json:"when,omitempty" jsonschema:"Reschedule (today/tomorrow/evening/anytime/someday, YYYY-MM-DD, HH:MM, YYYY-MM-DD@HH:MM, RFC3339); empty string clears the schedule."`
	Deadline     *string `json:"deadline,omitempty" jsonschema:"Set the deadline (YYYY-MM-DD); empty string clears it."`
	Tags         *string `json:"tags,omitempty" jsonschema:"Replace all tags with this comma-separated list; empty string clears tags."`
	AddTags      *string `json:"add_tags,omitempty" jsonschema:"Add these comma-separated tags to the existing tags."`
	Area         *string `json:"area,omitempty" jsonschema:"Move to an area by name."`
	AreaID       *string `json:"area_id,omitempty" jsonschema:"Move to an area by UUID."`
	Complete     bool    `json:"complete,omitempty" jsonschema:"Mark the project completed."`
	Cancel       bool    `json:"cancel,omitempty" jsonschema:"Mark the project canceled."`
	Duplicate    bool    `json:"duplicate,omitempty" jsonschema:"Duplicate the project before applying edits."`
	Reveal       bool    `json:"reveal,omitempty" jsonschema:"Reveal the project in Things after editing."`
}

type completeInput struct {
	Task string `json:"task" jsonschema:"To-do (or project) to complete: a UUID (preferred) or title."`
}

type cancelInput struct {
	Task string `json:"task" jsonschema:"To-do to cancel: a UUID (preferred) or title."`
}

type importInput struct {
	Data   string `json:"data" jsonschema:"A Things JSON array payload (the format accepted by the things:///json URL scheme)."`
	Reveal bool   `json:"reveal,omitempty" jsonschema:"Reveal the imported items in Things afterwards."`
}

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
	task, err := resolveRef(b, in.Task)
	if err != nil {
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

func (t toolset) add(_ context.Context, _ *mcp.CallToolRequest, in addInput) (*mcp.CallToolResult, any, error) {
	if err := t.write.AddTask(things.AddParams{
		Title:     in.Title,
		Notes:     in.Notes,
		When:      in.When,
		Deadline:  in.Deadline,
		Tags:      strings.Join(in.Tags, ","),
		Checklist: strings.Join(in.Checklist, "\n"),
		Heading:   in.Heading,
		List:      in.List,
	}); err != nil {
		return nil, nil, err
	}
	return textResult(fmt.Sprintf("Submitted new to-do %q to Things. Creation is asynchronous and not confirmed by this tool; check Things to verify.", in.Title)), nil, nil
}

func (t toolset) addProject(_ context.Context, _ *mcp.CallToolRequest, in addProjectInput) (*mcp.CallToolResult, any, error) {
	if err := t.write.AddProject(things.AddProjectParams{
		Title:    in.Title,
		Notes:    in.Notes,
		When:     in.When,
		Deadline: in.Deadline,
		Tags:     strings.Join(in.Tags, ","),
		Area:     in.Area,
		Todos:    strings.Join(in.Todos, "\n"),
	}); err != nil {
		return nil, nil, err
	}
	return textResult(fmt.Sprintf("Submitted new project %q to Things. Creation is asynchronous and not confirmed by this tool; check Things to verify.", in.Title)), nil, nil
}

func (t toolset) edit(_ context.Context, _ *mcp.CallToolRequest, in editInput) (*mcp.CallToolResult, any, error) {
	b, err := t.open()
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = b.Close() }()
	task, err := resolveRef(b, in.Task)
	if err != nil {
		return nil, nil, err
	}
	token, err := b.GetAuthToken()
	if err != nil {
		return nil, nil, err
	}
	if err := t.write.UpdateTask(things.UpdateParams{
		ID:               task.UUID,
		AuthToken:        token,
		Title:            in.Title,
		Notes:            in.Notes,
		PrependNotes:     in.PrependNotes,
		AppendNotes:      in.AppendNotes,
		When:             in.When,
		Deadline:         in.Deadline,
		Tags:             in.Tags,
		AddTags:          in.AddTags,
		Checklist:        in.Checklist,
		PrependChecklist: in.PrependChecklist,
		AppendChecklist:  in.AppendChecklist,
		List:             in.List,
		ListID:           in.ListID,
		Heading:          in.Heading,
		HeadingID:        in.HeadingID,
		Completed:        in.Complete,
		Canceled:         in.Cancel,
		Duplicate:        in.Duplicate,
		Reveal:           in.Reveal,
	}); err != nil {
		return nil, nil, err
	}
	return textResult(fmt.Sprintf("Submitted edit to to-do %q (%s). Changes are applied asynchronously and not confirmed by this tool.", task.Title, task.UUID)), nil, nil
}

func (t toolset) editProject(_ context.Context, _ *mcp.CallToolRequest, in editProjectInput) (*mcp.CallToolResult, any, error) {
	b, err := t.open()
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = b.Close() }()
	project, err := resolveRef(b, in.Project)
	if err != nil {
		return nil, nil, err
	}
	if project.Type != model.TypeProject {
		return nil, nil, fmt.Errorf("not a project: %s", project.Title)
	}
	token, err := b.GetAuthToken()
	if err != nil {
		return nil, nil, err
	}
	if err := t.write.UpdateProject(things.UpdateProjectParams{
		ID:           project.UUID,
		AuthToken:    token,
		Title:        in.Title,
		Notes:        in.Notes,
		PrependNotes: in.PrependNotes,
		AppendNotes:  in.AppendNotes,
		When:         in.When,
		Deadline:     in.Deadline,
		Tags:         in.Tags,
		AddTags:      in.AddTags,
		Area:         in.Area,
		AreaID:       in.AreaID,
		Completed:    in.Complete,
		Canceled:     in.Cancel,
		Duplicate:    in.Duplicate,
		Reveal:       in.Reveal,
	}); err != nil {
		return nil, nil, err
	}
	return textResult(fmt.Sprintf("Submitted edit to project %q (%s). Changes are applied asynchronously and not confirmed by this tool.", project.Title, project.UUID)), nil, nil
}

func (t toolset) complete(_ context.Context, _ *mcp.CallToolRequest, in completeInput) (*mcp.CallToolResult, any, error) {
	b, err := t.open()
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = b.Close() }()
	task, err := resolveRef(b, in.Task)
	if err != nil {
		return nil, nil, err
	}
	if task.Type == model.TypeProject {
		if err := t.write.CompleteProject(task.UUID); err != nil {
			return nil, nil, err
		}
		return textResult(fmt.Sprintf("Completed project %q (%s) and all of its to-dos.", task.Title, task.UUID)), nil, nil
	}
	if err := t.write.CompleteTask(task.UUID); err != nil {
		return nil, nil, err
	}
	return textResult(fmt.Sprintf("Completed to-do %q (%s).", task.Title, task.UUID)), nil, nil
}

func (t toolset) cancel(_ context.Context, _ *mcp.CallToolRequest, in cancelInput) (*mcp.CallToolResult, any, error) {
	b, err := t.open()
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = b.Close() }()
	task, err := resolveRef(b, in.Task)
	if err != nil {
		return nil, nil, err
	}
	if task.Type == model.TypeProject {
		return nil, nil, fmt.Errorf("%q is a project; cancel it with things_edit_project (cancel=true)", task.Title)
	}
	if err := t.write.CancelTask(task.UUID); err != nil {
		return nil, nil, err
	}
	return textResult(fmt.Sprintf("Cancelled to-do %q (%s).", task.Title, task.UUID)), nil, nil
}

func (t toolset) log(_ context.Context, _ *mcp.CallToolRequest, _ emptyInput) (*mcp.CallToolResult, any, error) {
	if err := t.write.LogCompleted(); err != nil {
		return nil, nil, err
	}
	return textResult("Logged completed and cancelled items to the Logbook."), nil, nil
}

func (t toolset) importJSON(_ context.Context, _ *mcp.CallToolRequest, in importInput) (*mcp.CallToolResult, any, error) {
	if err := validateImportPayload(in.Data); err != nil {
		return nil, nil, err
	}
	b, err := t.open()
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = b.Close() }()
	token, err := b.GetAuthToken()
	if err != nil {
		return nil, nil, err
	}
	if err := t.write.ImportJSON(in.Data, token, in.Reveal); err != nil {
		return nil, nil, err
	}
	return textResult("Submitted import payload to Things. Processing is asynchronous and not confirmed by this tool; check Things for any error notification."), nil, nil
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

// resolveRef resolves a UUID-or-title reference to a task, mapping an ambiguous
// title to a candidate-listing tool error. It is the non-interactive subset of
// the CLI's resolveTask (no last-list cache index, no stdin prompt).
func resolveRef(b Backend, ref string) (*model.Task, error) {
	task, err := b.GetTask(ref)
	if err != nil {
		var ambig *db.AmbiguousTaskError
		if errors.As(err, &ambig) {
			return nil, ambiguousError(ambig)
		}
		return nil, err
	}
	return task, nil
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
		return fmt.Errorf("--on/--from/--to are not supported on the %q view", view)
	}
	if on != "" && (from != "" || to != "") {
		return fmt.Errorf("--on cannot be combined with --from/--to")
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
		return fmt.Errorf("--from %s is after --to %s", filter.From, filter.To)
	}
	return nil
}

// validateImportPayload rejects obvious garbage before handing the payload to
// the URL scheme (which reports its own errors only as an in-app notification).
// It is a lighter cousin of the CLI's validateImportJSON: non-empty, valid JSON,
// shaped as an array.
func validateImportPayload(data string) error {
	trimmed := strings.TrimSpace(data)
	if trimmed == "" {
		return fmt.Errorf("import: data is empty")
	}
	if !json.Valid([]byte(data)) {
		return fmt.Errorf("import: data is not valid JSON")
	}
	if trimmed[0] != '[' {
		return fmt.Errorf("import: data must be a JSON array of items")
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
