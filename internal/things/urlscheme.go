package things

import (
	"fmt"
	"net/url"
	"slices"
	"strings"
)

type AddParams struct {
	Title     string
	Notes     string
	When      string
	Deadline  string
	Tags      string
	Checklist string
	Heading   string
	List      string
}

type AddProjectParams struct {
	Title    string
	Notes    string
	When     string
	Deadline string
	Tags     string
	Area     string
	Todos    string
}

// openThingsURL hands a things:/// URL to `open -g` so writes don't steal
// focus. url.Values.Encode uses + for spaces, but Things expects %20.
func openThingsURL(command string, v url.Values) error {
	return runOpen("-g", buildThingsURL(command, v))
}

func buildThingsURL(command string, v url.Values) string {
	return "things:///" + command + "?" + strings.ReplaceAll(v.Encode(), "+", "%20")
}

func runOpen(args ...string) error {
	if err := execCommand("open", args...).Run(); err != nil {
		return fmt.Errorf("opening URL scheme: %w", err)
	}
	return nil
}

func setStr(v url.Values, key string, p *string) {
	if p != nil {
		v.Set(key, *p)
	}
}

func setBool(v url.Values, key string, b bool) {
	if b {
		v.Set(key, "true")
	}
}

func setNonEmpty(v url.Values, key, value string) {
	if value != "" {
		v.Set(key, value)
	}
}

// BuiltinLists are the navigable list IDs the Things URL scheme accepts
// verbatim as `id=…`. Some (e.g. repeating, all-projects) have no direct
// DB equivalent — they're app-side views only.
var BuiltinLists = []string{
	"inbox", "today", "anytime", "upcoming", "someday", "logbook",
	"tomorrow", "deadlines", "repeating", "all-projects", "logged-projects",
}

func IsBuiltinList(name string) bool {
	return slices.Contains(BuiltinLists, name)
}

type ShowParams struct {
	// ID is a UUID or built-in list name (inbox, today, upcoming, …).
	ID string
	// Query triggers app-side quick find instead of a direct show.
	Query string
	// Filter is a comma-separated tag list that scopes the shown view.
	Filter string
	// Background uses `open -g` to avoid bringing Things to the foreground.
	Background bool
}

// Show navigates Things to a task, project, area, tag, built-in list, or
// query result via `things:///show`.
func Show(params ShowParams) error {
	if params.ID == "" && params.Query == "" {
		return fmt.Errorf("show: id or query is required")
	}
	v := url.Values{}
	if params.ID != "" {
		v.Set("id", params.ID)
	}
	if params.Query != "" {
		v.Set("query", params.Query)
	}
	if params.Filter != "" {
		v.Set("filter", params.Filter)
	}
	u := buildThingsURL("show", v)
	if params.Background {
		return runOpen("-g", u)
	}
	return runOpen(u)
}

func AddProject(params AddProjectParams) error {
	if err := validateAddProject(params); err != nil {
		return err
	}
	when, err := NormalizeWhen(params.When)
	if err != nil {
		return err
	}
	params.When = when
	deadline, err := NormalizeDeadline(params.Deadline)
	if err != nil {
		return err
	}
	params.Deadline = deadline
	v := url.Values{}
	v.Set("title", params.Title)
	setNonEmpty(v, "notes", params.Notes)
	setNonEmpty(v, "when", params.When)
	setNonEmpty(v, "deadline", params.Deadline)
	setNonEmpty(v, "tags", params.Tags)
	setNonEmpty(v, "area", params.Area)
	setNonEmpty(v, "to-dos", params.Todos)
	return openThingsURL("add-project", v)
}

type UpdateParams struct {
	ID        string
	AuthToken string

	Title            *string
	Notes            *string
	PrependNotes     *string
	AppendNotes      *string
	When             *string
	Deadline         *string
	Tags             *string
	AddTags          *string
	Checklist        *string
	PrependChecklist *string
	AppendChecklist  *string
	List             *string
	ListID           *string
	Heading          *string
	HeadingID        *string
	Completed        bool
	Canceled         bool
	Duplicate        bool
	Reveal           bool
}

func UpdateTask(params UpdateParams) error {
	if params.ID == "" {
		return fmt.Errorf("update: task id is required")
	}
	if params.AuthToken == "" {
		return fmt.Errorf("update: auth token is required — enable Things URLs in Things → Settings → General and ensure the app has been launched at least once")
	}
	if err := validateUpdate(params); err != nil {
		return err
	}
	if params.When != nil {
		v, err := NormalizeWhen(*params.When)
		if err != nil {
			return err
		}
		params.When = &v
	}
	if params.Deadline != nil {
		v, err := NormalizeDeadline(*params.Deadline)
		if err != nil {
			return err
		}
		params.Deadline = &v
	}

	v := url.Values{}
	v.Set("id", params.ID)
	v.Set("auth-token", params.AuthToken)

	setStr(v, "title", params.Title)
	setStr(v, "notes", params.Notes)
	setStr(v, "prepend-notes", params.PrependNotes)
	setStr(v, "append-notes", params.AppendNotes)
	setStr(v, "when", params.When)
	setStr(v, "deadline", params.Deadline)
	setStr(v, "tags", params.Tags)
	setStr(v, "add-tags", params.AddTags)
	setStr(v, "checklist-items", params.Checklist)
	setStr(v, "prepend-checklist-items", params.PrependChecklist)
	setStr(v, "append-checklist-items", params.AppendChecklist)
	setStr(v, "list", params.List)
	setStr(v, "list-id", params.ListID)
	setStr(v, "heading", params.Heading)
	setStr(v, "heading-id", params.HeadingID)
	setBool(v, "completed", params.Completed)
	setBool(v, "canceled", params.Canceled)
	setBool(v, "duplicate", params.Duplicate)
	setBool(v, "reveal", params.Reveal)

	return openThingsURL("update", v)
}

// ImportJSON dispatches a Things JSON payload via `things:///json`. The
// auth token is always sent when present: it's required for any item with
// `operation: update`, and harmless on create-only payloads.
func ImportJSON(data, authToken string, reveal bool) error {
	v := url.Values{}
	v.Set("data", data)
	if authToken != "" {
		v.Set("auth-token", authToken)
	}
	if reveal {
		v.Set("reveal", "true")
	}
	if err := openThingsURL("json", v); err != nil {
		// Things reports payload-level errors via an in-app notification, not
		// via the URL handler exit code, so callers see only `exit status 1`
		// from `open`. Point them at the right place to look.
		return fmt.Errorf("%w (check Things for an error notification)", err)
	}
	return nil
}

type UpdateProjectParams struct {
	ID        string
	AuthToken string

	Title        *string
	Notes        *string
	PrependNotes *string
	AppendNotes  *string
	When         *string
	Deadline     *string
	Tags         *string
	AddTags      *string
	Area         *string
	AreaID       *string
	Completed    bool
	Canceled     bool
	Duplicate    bool
	Reveal       bool
}

func UpdateProject(params UpdateProjectParams) error {
	if params.ID == "" {
		return fmt.Errorf("update-project: project id is required")
	}
	if params.AuthToken == "" {
		return fmt.Errorf("update-project: auth token is required — enable Things URLs in Things → Settings → General and ensure the app has been launched at least once")
	}
	if err := validateUpdateProject(params); err != nil {
		return err
	}
	if params.When != nil {
		v, err := NormalizeWhen(*params.When)
		if err != nil {
			return err
		}
		params.When = &v
	}
	if params.Deadline != nil {
		v, err := NormalizeDeadline(*params.Deadline)
		if err != nil {
			return err
		}
		params.Deadline = &v
	}

	v := url.Values{}
	v.Set("id", params.ID)
	v.Set("auth-token", params.AuthToken)

	setStr(v, "title", params.Title)
	setStr(v, "notes", params.Notes)
	setStr(v, "prepend-notes", params.PrependNotes)
	setStr(v, "append-notes", params.AppendNotes)
	setStr(v, "when", params.When)
	setStr(v, "deadline", params.Deadline)
	setStr(v, "tags", params.Tags)
	setStr(v, "add-tags", params.AddTags)
	setStr(v, "area", params.Area)
	setStr(v, "area-id", params.AreaID)
	setBool(v, "completed", params.Completed)
	setBool(v, "canceled", params.Canceled)
	setBool(v, "duplicate", params.Duplicate)
	setBool(v, "reveal", params.Reveal)

	return openThingsURL("update-project", v)
}

func AddTask(params AddParams) error {
	if err := validateAdd(params); err != nil {
		return err
	}
	when, err := NormalizeWhen(params.When)
	if err != nil {
		return err
	}
	params.When = when
	deadline, err := NormalizeDeadline(params.Deadline)
	if err != nil {
		return err
	}
	params.Deadline = deadline
	v := url.Values{}
	v.Set("title", params.Title)
	setNonEmpty(v, "notes", params.Notes)
	setNonEmpty(v, "when", params.When)
	setNonEmpty(v, "deadline", params.Deadline)
	setNonEmpty(v, "tags", params.Tags)
	setNonEmpty(v, "checklist-items", params.Checklist)
	setNonEmpty(v, "list", params.List)
	setNonEmpty(v, "heading", params.Heading)
	return openThingsURL("add", v)
}
