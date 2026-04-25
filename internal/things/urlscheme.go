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
	AuthToken string
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
	v := url.Values{}
	v.Set("title", params.Title)
	if params.Notes != "" {
		v.Set("notes", params.Notes)
	}
	if params.When != "" {
		v.Set("when", params.When)
	}
	if params.Deadline != "" {
		v.Set("deadline", params.Deadline)
	}
	if params.Tags != "" {
		v.Set("tags", params.Tags)
	}
	if params.Area != "" {
		v.Set("area", params.Area)
	}
	if params.Todos != "" {
		v.Set("to-dos", params.Todos)
	}
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

	v := url.Values{}
	v.Set("id", params.ID)
	v.Set("auth-token", params.AuthToken)

	setStr := func(key string, p *string) {
		if p != nil {
			v.Set(key, *p)
		}
	}
	setBool := func(key string, b bool) {
		if b {
			v.Set(key, "true")
		}
	}

	setStr("title", params.Title)
	setStr("notes", params.Notes)
	setStr("prepend-notes", params.PrependNotes)
	setStr("append-notes", params.AppendNotes)
	setStr("when", params.When)
	setStr("deadline", params.Deadline)
	setStr("tags", params.Tags)
	setStr("add-tags", params.AddTags)
	setStr("checklist-items", params.Checklist)
	setStr("prepend-checklist-items", params.PrependChecklist)
	setStr("append-checklist-items", params.AppendChecklist)
	setStr("list", params.List)
	setStr("list-id", params.ListID)
	setStr("heading", params.Heading)
	setStr("heading-id", params.HeadingID)
	setBool("completed", params.Completed)
	setBool("canceled", params.Canceled)
	setBool("duplicate", params.Duplicate)
	setBool("reveal", params.Reveal)

	return openThingsURL("update", v)
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

	v := url.Values{}
	v.Set("id", params.ID)
	v.Set("auth-token", params.AuthToken)

	setStr := func(key string, p *string) {
		if p != nil {
			v.Set(key, *p)
		}
	}
	setBool := func(key string, b bool) {
		if b {
			v.Set(key, "true")
		}
	}

	setStr("title", params.Title)
	setStr("notes", params.Notes)
	setStr("prepend-notes", params.PrependNotes)
	setStr("append-notes", params.AppendNotes)
	setStr("when", params.When)
	setStr("deadline", params.Deadline)
	setStr("tags", params.Tags)
	setStr("add-tags", params.AddTags)
	setStr("area", params.Area)
	setStr("area-id", params.AreaID)
	setBool("completed", params.Completed)
	setBool("canceled", params.Canceled)
	setBool("duplicate", params.Duplicate)
	setBool("reveal", params.Reveal)

	return openThingsURL("update-project", v)
}

func AddTask(params AddParams) error {
	if err := validateAdd(params); err != nil {
		return err
	}
	v := url.Values{}
	v.Set("title", params.Title)
	if params.Notes != "" {
		v.Set("notes", params.Notes)
	}
	if params.When != "" {
		v.Set("when", params.When)
	}
	if params.Deadline != "" {
		v.Set("deadline", params.Deadline)
	}
	if params.Tags != "" {
		v.Set("tags", params.Tags)
	}
	if params.Checklist != "" {
		v.Set("checklist-items", params.Checklist)
	}
	if params.List != "" {
		v.Set("list", params.List)
	}
	if params.Heading != "" {
		v.Set("heading", params.Heading)
	}
	if params.AuthToken != "" {
		v.Set("auth-token", params.AuthToken)
	}
	return openThingsURL("add", v)
}
