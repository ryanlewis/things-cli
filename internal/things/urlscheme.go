package things

import (
	"fmt"
	"net/url"
	"slices"
	"strings"
)

// AddCommon holds the fields `add` and `add-project` share. AddParams and
// AddProjectParams embed it.
type AddCommon struct {
	Title    string
	Notes    string
	When     string
	Deadline string
	Tags     string
}

// Common returns the shared fields, so a caller handed either params type
// can read them.
func (c AddCommon) Common() AddCommon {
	return c
}

// normalized checks the shared fields, with own holding the caller's checks
// of its own fields, and returns c with When and Deadline canonicalised.
func (c AddCommon) normalized(own ...error) (AddCommon, error) {
	when, err := NormalizeWhen(c.When)
	if err != nil {
		return c, err
	}
	shared := []error{
		validateString("title", c.Title),
		validateNotes("notes", c.Notes),
		validateTags("tags", c.Tags),
	}
	if err := firstErr(append(shared, own...)...); err != nil {
		return c, err
	}
	deadline, err := NormalizeDeadline(c.Deadline)
	if err != nil {
		return c, err
	}
	c.When, c.Deadline = when, deadline
	return c, nil
}

// values sets the shared keys.
func (c AddCommon) values() url.Values {
	v := url.Values{}
	v.Set("title", c.Title)
	setNonEmpty(v, "notes", c.Notes)
	setNonEmpty(v, "when", c.When)
	setNonEmpty(v, "deadline", c.Deadline)
	setNonEmpty(v, "tags", c.Tags)
	return v
}

type AddParams struct {
	AddCommon

	Checklist string
	Heading   string
	List      string
	ListID    string
}

// Validate returns the error AddTask would refuse p with, so a caller can
// refuse p before doing anything else, such as creating tags.
func (p AddParams) Validate() error {
	_, err := p.normalized()
	return err
}

func (p AddParams) normalized() (AddParams, error) {
	c, err := p.AddCommon.normalized(
		validateChecklist("checklist", p.Checklist),
		validateString("list", p.List),
		validateString("heading", p.Heading),
	)
	p.AddCommon = c
	return p, err
}

type AddProjectParams struct {
	AddCommon

	Area   string
	AreaID string
	Todos  string
}

// Validate returns the error AddProject would refuse p with, so a caller can
// refuse p before doing anything else, such as creating tags.
func (p AddProjectParams) Validate() error {
	_, err := p.normalized()
	return err
}

func (p AddProjectParams) normalized() (AddProjectParams, error) {
	c, err := p.AddCommon.normalized(
		validateString("area", p.Area),
		validateItems("todos", p.Todos),
	)
	p.AddCommon = c
	return p, err
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

// SplitTags splits the comma-separated tag syntax the Things URL scheme uses
// into individual names, trimming surrounding whitespace and dropping empty
// entries. Things has no escape for a comma inside a tag name.
func SplitTags(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	var tags []string
	for _, t := range strings.Split(s, ",") {
		if t = strings.TrimSpace(t); t != "" {
			tags = append(tags, t)
		}
	}
	return tags
}

// BuiltinLists are the navigable list IDs the Things URL scheme accepts
// verbatim as `id=…`. Some (e.g. all-projects, logged-projects) have no direct
// DB equivalent — they're app-side views only. "repeating" does have one:
// `things repeating` lists the same templates.
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
	params, err := params.normalized()
	if err != nil {
		return err
	}
	v := params.values()
	setNonEmpty(v, "area", params.Area)
	setNonEmpty(v, "area-id", params.AreaID)
	setNonEmpty(v, "to-dos", params.Todos)
	return openThingsURL("add-project", v)
}

// UpdateCommon holds the fields `update` and `update-project` share.
// UpdateParams and UpdateProjectParams embed it.
type UpdateCommon struct {
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
	Completed    bool
	Canceled     bool
	Duplicate    bool
	Reveal       bool
}

// normalized returns invalid if the caller's validation failed, and
// otherwise c with When and Deadline normalised. It checks the fields only,
// not the id or auth token, so it can run before the CLI knows whether the
// edit will be sent at all.
func (c UpdateCommon) normalized(invalid error) (UpdateCommon, error) {
	if invalid != nil {
		return c, invalid
	}
	if c.When != nil {
		v, err := NormalizeWhen(*c.When)
		if err != nil {
			return c, err
		}
		c.When = &v
	}
	if c.Deadline != nil {
		v, err := NormalizeDeadline(*c.Deadline)
		if err != nil {
			return c, err
		}
		c.Deadline = &v
	}
	return c, nil
}

// sendable checks the id and auth token, which values needs before it can
// send anything. command prefixes the errors; kind names the item.
func (c UpdateCommon) sendable(command, kind string) error {
	if c.ID == "" {
		return fmt.Errorf("%s: %s id is required", command, kind)
	}
	if c.AuthToken == "" {
		return fmt.Errorf("%s: auth token is required — enable Things URLs in Things → Settings → General and ensure the app has been launched at least once", command)
	}
	return nil
}

// values checks the id and auth token, then the fields (normalized), and
// sets the shared keys. command prefixes the guard errors; kind names the
// item ("task", "project").
func (c UpdateCommon) values(command, kind string, invalid error) (url.Values, error) {
	if err := c.sendable(command, kind); err != nil {
		return nil, err
	}
	c, err := c.normalized(invalid)
	if err != nil {
		return nil, err
	}

	v := url.Values{}
	v.Set("id", c.ID)
	v.Set("auth-token", c.AuthToken)

	setStr(v, "title", c.Title)
	setStr(v, "notes", c.Notes)
	setStr(v, "prepend-notes", c.PrependNotes)
	setStr(v, "append-notes", c.AppendNotes)
	setStr(v, "when", c.When)
	setStr(v, "deadline", c.Deadline)
	setStr(v, "tags", c.Tags)
	setStr(v, "add-tags", c.AddTags)
	setBool(v, "completed", c.Completed)
	setBool(v, "canceled", c.Canceled)
	setBool(v, "duplicate", c.Duplicate)
	setBool(v, "reveal", c.Reveal)
	return v, nil
}

type UpdateParams struct {
	UpdateCommon

	Checklist        *string
	PrependChecklist *string
	AppendChecklist  *string
	List             *string
	ListID           *string
	Heading          *string
	HeadingID        *string
}

// Validate returns the error UpdateTask would refuse p's fields with, so a
// caller can refuse p before doing anything else, such as creating tags. It
// leaves out the id and auth token checks: an edit the CLI ends up not
// sending needs neither.
func (p UpdateParams) Validate() error {
	_, err := p.normalized(validateUpdate(p))
	return err
}

// ValidateSend returns the error UpdateTask would refuse p with before
// its fields: a missing id or auth token. Validate leaves these out, so a
// caller checks them once it knows the edit will be sent.
func (p UpdateParams) ValidateSend() error {
	return p.sendable("update", "task")
}

func UpdateTask(params UpdateParams) error {
	v, err := params.values("update", "task", validateUpdate(params))
	if err != nil {
		return err
	}
	setStr(v, "checklist-items", params.Checklist)
	setStr(v, "prepend-checklist-items", params.PrependChecklist)
	setStr(v, "append-checklist-items", params.AppendChecklist)
	setStr(v, "list", params.List)
	setStr(v, "list-id", params.ListID)
	setStr(v, "heading", params.Heading)
	setStr(v, "heading-id", params.HeadingID)
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
	UpdateCommon

	Area   *string
	AreaID *string
}

// Validate returns the error UpdateProject would refuse p's fields with, so
// a caller can refuse p before doing anything else, such as creating tags.
// It leaves out the id and auth token checks, as UpdateParams.Validate does.
func (p UpdateProjectParams) Validate() error {
	_, err := p.normalized(validateUpdateProject(p))
	return err
}

// ValidateSend is UpdateParams.ValidateSend for UpdateProject.
func (p UpdateProjectParams) ValidateSend() error {
	return p.sendable("update-project", "project")
}

func UpdateProject(params UpdateProjectParams) error {
	v, err := params.values("update-project", "project", validateUpdateProject(params))
	if err != nil {
		return err
	}
	setStr(v, "area", params.Area)
	setStr(v, "area-id", params.AreaID)
	return openThingsURL("update-project", v)
}

func AddTask(params AddParams) error {
	params, err := params.normalized()
	if err != nil {
		return err
	}
	v := params.values()
	setNonEmpty(v, "checklist-items", params.Checklist)
	setNonEmpty(v, "list", params.List)
	setNonEmpty(v, "list-id", params.ListID)
	setNonEmpty(v, "heading", params.Heading)
	return openThingsURL("add", v)
}
