package things

import (
	"net/url"
	"os/exec"
	"strings"
	"testing"
)

// stubRunner records the command invoked and returns a cmd that succeeds or
// fails based on `fail`.
func stubRunner(t *testing.T, fail bool) *[]string {
	t.Helper()
	var captured []string
	orig := execCommand
	execCommand = func(name string, args ...string) *exec.Cmd {
		captured = append([]string{name}, args...)
		if fail {
			return exec.Command("false")
		}
		return exec.Command("true")
	}
	t.Cleanup(func() { execCommand = orig })
	return &captured
}

func TestAddTaskMinimal(t *testing.T) {
	captured := stubRunner(t, false)

	err := AddTask(AddParams{Title: "Hello World"})
	if err != nil {
		t.Fatalf("AddTask: %v", err)
	}
	if len(*captured) != 3 {
		t.Fatalf("expected 3 args (open -g <url>), got: %v", *captured)
	}
	if (*captured)[0] != "open" || (*captured)[1] != "-g" {
		t.Fatalf("unexpected command head: %v", *captured)
	}
	u := (*captured)[2]
	if !strings.HasPrefix(u, "things:///add?") {
		t.Fatalf("expected things URL, got %q", u)
	}
	// Spaces must be %20, not +
	if strings.Contains(u, "+") {
		t.Errorf("URL should use %%20 not +: %q", u)
	}
	if !strings.Contains(u, "title=Hello%20World") {
		t.Errorf("missing encoded title: %q", u)
	}
}

func TestAddTaskAllFields(t *testing.T) {
	captured := stubRunner(t, false)

	err := AddTask(AddParams{
		Title:     "T",
		Notes:     "n",
		When:      "today",
		Deadline:  "2026-05-01",
		Tags:      "a,b",
		Checklist: "x\ny",
		Heading:   "H",
		List:      "Inbox",
		AuthToken: "tok",
	})
	if err != nil {
		t.Fatal(err)
	}

	u := (*captured)[2]
	parsed, err := url.Parse(u)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	q := parsed.Query()

	cases := map[string]string{
		"title":           "T",
		"notes":           "n",
		"when":            "today",
		"deadline":        "2026-05-01",
		"tags":            "a,b",
		"checklist-items": "x\ny",
		"heading":         "H",
		"list":            "Inbox",
		"auth-token":      "tok",
	}
	for k, want := range cases {
		if got := q.Get(k); got != want {
			t.Errorf("query[%q] = %q, want %q", k, got, want)
		}
	}
}

func TestAddTaskOmitsEmpty(t *testing.T) {
	captured := stubRunner(t, false)

	if err := AddTask(AddParams{Title: "only"}); err != nil {
		t.Fatal(err)
	}
	u := (*captured)[2]
	for _, k := range []string{"notes=", "when=", "deadline=", "tags=", "checklist-items=", "heading=", "list=", "auth-token="} {
		if strings.Contains(u, k) {
			t.Errorf("URL should not contain %q: %s", k, u)
		}
	}
}

func TestAddProjectMinimal(t *testing.T) {
	captured := stubRunner(t, false)

	if err := AddProject(AddProjectParams{Title: "Launch site"}); err != nil {
		t.Fatalf("AddProject: %v", err)
	}
	if len(*captured) != 3 {
		t.Fatalf("expected 3 args, got: %v", *captured)
	}
	u := (*captured)[2]
	if !strings.HasPrefix(u, "things:///add-project?") {
		t.Fatalf("expected add-project URL, got %q", u)
	}
	if strings.Contains(u, "+") {
		t.Errorf("URL should use %%20 not +: %q", u)
	}
	if !strings.Contains(u, "title=Launch%20site") {
		t.Errorf("missing encoded title: %q", u)
	}
}

func TestAddProjectAllFields(t *testing.T) {
	captured := stubRunner(t, false)

	err := AddProject(AddProjectParams{
		Title:    "P",
		Notes:    "n",
		When:     "today",
		Deadline: "2026-05-01",
		Tags:     "a,b",
		Area:     "Work",
		Todos:    "one\ntwo",
	})
	if err != nil {
		t.Fatal(err)
	}

	parsed, err := url.Parse((*captured)[2])
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	q := parsed.Query()

	cases := map[string]string{
		"title":    "P",
		"notes":    "n",
		"when":     "today",
		"deadline": "2026-05-01",
		"tags":     "a,b",
		"area":     "Work",
		"to-dos":   "one\ntwo",
	}
	for k, want := range cases {
		if got := q.Get(k); got != want {
			t.Errorf("query[%q] = %q, want %q", k, got, want)
		}
	}
}

func TestAddProjectOmitsEmpty(t *testing.T) {
	captured := stubRunner(t, false)

	if err := AddProject(AddProjectParams{Title: "only"}); err != nil {
		t.Fatal(err)
	}
	u := (*captured)[2]
	for _, k := range []string{"notes=", "when=", "deadline=", "tags=", "area=", "to-dos="} {
		if strings.Contains(u, k) {
			t.Errorf("URL should not contain %q: %s", k, u)
		}
	}
}

func TestAddProjectCommandFails(t *testing.T) {
	stubRunner(t, true)

	err := AddProject(AddProjectParams{Title: "p"})
	if err == nil {
		t.Fatal("expected error from failing command")
	}
	if !strings.Contains(err.Error(), "opening URL scheme") {
		t.Errorf("error should mention URL scheme: %v", err)
	}
}

func TestAddTaskCommandFails(t *testing.T) {
	stubRunner(t, true)

	err := AddTask(AddParams{Title: "t"})
	if err == nil {
		t.Fatal("expected error from failing command")
	}
	if !strings.Contains(err.Error(), "opening URL scheme") {
		t.Errorf("error should mention URL scheme: %v", err)
	}
}
