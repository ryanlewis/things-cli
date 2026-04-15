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

	// url.Values.Encode uses + for spaces, but Things expects percent-encoding.
	u := "things:///add?" + strings.ReplaceAll(v.Encode(), "+", "%20")
	cmd := execCommand("open", "-g", u)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("opening URL scheme: %w", err)
	}
	return nil
}
