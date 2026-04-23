package things

import (
	"fmt"
	"net/url"
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

// openThingsURL builds a things:/// URL for the given command and runs it via
// `open -g`. url.Values.Encode uses + for spaces, but Things expects %20.
func openThingsURL(command string, v url.Values) error {
	u := "things:///" + command + "?" + strings.ReplaceAll(v.Encode(), "+", "%20")
	if err := execCommand("open", "-g", u).Run(); err != nil {
		return fmt.Errorf("opening URL scheme: %w", err)
	}
	return nil
}

func AddProject(params AddProjectParams) error {
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

func AddTask(params AddParams) error {
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
