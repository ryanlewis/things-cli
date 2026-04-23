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
