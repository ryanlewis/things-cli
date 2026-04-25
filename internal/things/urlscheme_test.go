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
	}
	for k, want := range cases {
		if got := q.Get(k); got != want {
			t.Errorf("query[%q] = %q, want %q", k, got, want)
		}
	}
	if q.Has("auth-token") {
		t.Error("auth-token must not be sent on add (endpoint does not require it)")
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

func TestImportJSON(t *testing.T) {
	payload := `[{"type":"to-do","attributes":{"title":"Hi"}}]`

	t.Run("minimal", func(t *testing.T) {
		captured := stubRunner(t, false)
		if err := ImportJSON(payload, "", false); err != nil {
			t.Fatal(err)
		}
		u := (*captured)[2]
		if !strings.HasPrefix(u, "things:///json?") {
			t.Fatalf("expected json URL, got %q", u)
		}
		if strings.Contains(u, "+") {
			t.Errorf("URL should use %%20 not +: %q", u)
		}
		parsed, _ := url.Parse(u)
		q := parsed.Query()
		if q.Get("data") != payload {
			t.Errorf("data = %q, want %q", q.Get("data"), payload)
		}
		if q.Has("auth-token") {
			t.Error("auth-token should be omitted when empty")
		}
		if q.Has("reveal") {
			t.Error("reveal should be omitted when false")
		}
	})

	t.Run("withTokenAndReveal", func(t *testing.T) {
		captured := stubRunner(t, false)
		if err := ImportJSON(payload, "tok", true); err != nil {
			t.Fatal(err)
		}
		parsed, _ := url.Parse((*captured)[2])
		q := parsed.Query()
		if q.Get("auth-token") != "tok" {
			t.Errorf("auth-token = %q", q.Get("auth-token"))
		}
		if q.Get("reveal") != "true" {
			t.Errorf("reveal = %q", q.Get("reveal"))
		}
	})

	t.Run("commandFails", func(t *testing.T) {
		stubRunner(t, true)
		if err := ImportJSON(payload, "", false); err == nil {
			t.Fatal("expected error")
		}
	})
}

func strPtr(s string) *string { return &s }

func TestUpdateTaskEmptyWhenClearsField(t *testing.T) {
	captured := stubRunner(t, false)
	empty := ""
	if err := UpdateTask(UpdateParams{
		ID:        "id-1",
		AuthToken: "tok",
		When:      &empty,
		Deadline:  &empty,
	}); err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}
	parsed, err := url.Parse((*captured)[2])
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	q := parsed.Query()
	if _, ok := q["when"]; !ok {
		t.Error("expected `when` parameter present (empty value clears the field)")
	}
	if _, ok := q["deadline"]; !ok {
		t.Error("expected `deadline` parameter present (empty value clears the field)")
	}
	if q.Get("when") != "" || q.Get("deadline") != "" {
		t.Errorf("expected empty values, got when=%q deadline=%q", q.Get("when"), q.Get("deadline"))
	}
}

func TestUpdateTaskMinimal(t *testing.T) {
	captured := stubRunner(t, false)

	err := UpdateTask(UpdateParams{
		ID:        "abc-123",
		AuthToken: "tok",
		Title:     strPtr("New Title"),
	})
	if err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}
	u := (*captured)[2]
	if !strings.HasPrefix(u, "things:///update?") {
		t.Fatalf("expected update URL, got %q", u)
	}
	if strings.Contains(u, "+") {
		t.Errorf("URL should use %%20 not +: %q", u)
	}
	parsed, err := url.Parse(u)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	q := parsed.Query()
	if q.Get("id") != "abc-123" {
		t.Errorf("id = %q", q.Get("id"))
	}
	if q.Get("auth-token") != "tok" {
		t.Errorf("auth-token = %q", q.Get("auth-token"))
	}
	if q.Get("title") != "New Title" {
		t.Errorf("title = %q", q.Get("title"))
	}
}

func TestUpdateTaskAllFields(t *testing.T) {
	captured := stubRunner(t, false)

	err := UpdateTask(UpdateParams{
		ID:               "id-1",
		AuthToken:        "tok",
		Title:            strPtr("T"),
		Notes:            strPtr("n"),
		PrependNotes:     strPtr("pre"),
		AppendNotes:      strPtr("post"),
		When:             strPtr("today"),
		Deadline:         strPtr("2026-05-01"),
		Tags:             strPtr("a,b"),
		AddTags:          strPtr("c"),
		Checklist:        strPtr("x\ny"),
		PrependChecklist: strPtr("pc"),
		AppendChecklist:  strPtr("ac"),
		List:             strPtr("Inbox"),
		ListID:           strPtr("list-uuid"),
		Heading:          strPtr("H"),
		HeadingID:        strPtr("heading-uuid"),
		Completed:        true,
		Canceled:         true,
		Duplicate:        true,
		Reveal:           true,
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
		"id":                      "id-1",
		"auth-token":              "tok",
		"title":                   "T",
		"notes":                   "n",
		"prepend-notes":           "pre",
		"append-notes":            "post",
		"when":                    "today",
		"deadline":                "2026-05-01",
		"tags":                    "a,b",
		"add-tags":                "c",
		"checklist-items":         "x\ny",
		"prepend-checklist-items": "pc",
		"append-checklist-items":  "ac",
		"list":                    "Inbox",
		"list-id":                 "list-uuid",
		"heading":                 "H",
		"heading-id":              "heading-uuid",
		"completed":               "true",
		"canceled":                "true",
		"duplicate":               "true",
		"reveal":                  "true",
	}
	for k, want := range cases {
		if got := q.Get(k); got != want {
			t.Errorf("query[%q] = %q, want %q", k, got, want)
		}
	}
}

func TestUpdateTaskOmitsUnsetFlags(t *testing.T) {
	captured := stubRunner(t, false)

	if err := UpdateTask(UpdateParams{
		ID:        "id",
		AuthToken: "tok",
		Title:     strPtr("only"),
	}); err != nil {
		t.Fatal(err)
	}
	u := (*captured)[2]
	omitted := []string{
		"notes=", "prepend-notes=", "append-notes=",
		"when=", "deadline=", "tags=", "add-tags=",
		"checklist-items=", "prepend-checklist-items=", "append-checklist-items=",
		"list=", "list-id=", "heading=", "heading-id=",
		"completed=", "canceled=", "duplicate=", "reveal=",
	}
	for _, k := range omitted {
		if strings.Contains(u, k) {
			t.Errorf("URL should not contain %q: %s", k, u)
		}
	}
}

func TestUpdateTaskEmptyStringClearsField(t *testing.T) {
	captured := stubRunner(t, false)

	if err := UpdateTask(UpdateParams{
		ID:        "id",
		AuthToken: "tok",
		Notes:     strPtr(""),
	}); err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse((*captured)[2])
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	if _, ok := parsed.Query()["notes"]; !ok {
		t.Errorf("expected notes= param to be present (clear field)")
	}
}

func TestUpdateTaskRequiresID(t *testing.T) {
	stubRunner(t, false)
	err := UpdateTask(UpdateParams{AuthToken: "tok", Title: strPtr("x")})
	if err == nil {
		t.Fatal("expected error for missing id")
	}
}

func TestUpdateTaskRequiresAuthToken(t *testing.T) {
	stubRunner(t, false)
	err := UpdateTask(UpdateParams{ID: "id", Title: strPtr("x")})
	if err == nil {
		t.Fatal("expected error for missing auth token")
	}
	if !strings.Contains(err.Error(), "auth token") {
		t.Errorf("error should mention auth token: %v", err)
	}
}

func TestUpdateTaskCommandFails(t *testing.T) {
	stubRunner(t, true)
	err := UpdateTask(UpdateParams{ID: "id", AuthToken: "tok", Title: strPtr("x")})
	if err == nil {
		t.Fatal("expected error from failing command")
	}
	if !strings.Contains(err.Error(), "opening URL scheme") {
		t.Errorf("error should mention URL scheme: %v", err)
	}
}

func TestUpdateProjectMinimal(t *testing.T) {
	captured := stubRunner(t, false)

	err := UpdateProject(UpdateProjectParams{
		ID:        "p-123",
		AuthToken: "tok",
		Title:     strPtr("New"),
	})
	if err != nil {
		t.Fatalf("UpdateProject: %v", err)
	}
	u := (*captured)[2]
	if !strings.HasPrefix(u, "things:///update-project?") {
		t.Fatalf("expected update-project URL, got %q", u)
	}
	if strings.Contains(u, "+") {
		t.Errorf("URL should use %%20 not +: %q", u)
	}
	parsed, err := url.Parse(u)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	q := parsed.Query()
	if q.Get("id") != "p-123" {
		t.Errorf("id = %q", q.Get("id"))
	}
	if q.Get("auth-token") != "tok" {
		t.Errorf("auth-token = %q", q.Get("auth-token"))
	}
	if q.Get("title") != "New" {
		t.Errorf("title = %q", q.Get("title"))
	}
}

func TestUpdateProjectAllFields(t *testing.T) {
	captured := stubRunner(t, false)

	err := UpdateProject(UpdateProjectParams{
		ID:           "p-1",
		AuthToken:    "tok",
		Title:        strPtr("T"),
		Notes:        strPtr("n"),
		PrependNotes: strPtr("pre"),
		AppendNotes:  strPtr("post"),
		When:         strPtr("today"),
		Deadline:     strPtr("2026-05-01"),
		Tags:         strPtr("a,b"),
		AddTags:      strPtr("c"),
		Area:         strPtr("Work"),
		AreaID:       strPtr("area-uuid"),
		Completed:    true,
		Canceled:     true,
		Duplicate:    true,
		Reveal:       true,
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
		"id":            "p-1",
		"auth-token":    "tok",
		"title":         "T",
		"notes":         "n",
		"prepend-notes": "pre",
		"append-notes":  "post",
		"when":          "today",
		"deadline":      "2026-05-01",
		"tags":          "a,b",
		"add-tags":      "c",
		"area":          "Work",
		"area-id":       "area-uuid",
		"completed":     "true",
		"canceled":      "true",
		"duplicate":     "true",
		"reveal":        "true",
	}
	for k, want := range cases {
		if got := q.Get(k); got != want {
			t.Errorf("query[%q] = %q, want %q", k, got, want)
		}
	}
}

func TestUpdateProjectOmitsUnsetFlags(t *testing.T) {
	captured := stubRunner(t, false)

	if err := UpdateProject(UpdateProjectParams{
		ID:        "id",
		AuthToken: "tok",
		Title:     strPtr("only"),
	}); err != nil {
		t.Fatal(err)
	}
	u := (*captured)[2]
	omitted := []string{
		"notes=", "prepend-notes=", "append-notes=",
		"when=", "deadline=", "tags=", "add-tags=",
		"area=", "area-id=",
		"completed=", "canceled=", "duplicate=", "reveal=",
	}
	for _, k := range omitted {
		if strings.Contains(u, k) {
			t.Errorf("URL should not contain %q: %s", k, u)
		}
	}
}

func TestUpdateProjectEmptyStringClearsField(t *testing.T) {
	captured := stubRunner(t, false)

	if err := UpdateProject(UpdateProjectParams{
		ID:        "id",
		AuthToken: "tok",
		Notes:     strPtr(""),
	}); err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse((*captured)[2])
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	if _, ok := parsed.Query()["notes"]; !ok {
		t.Errorf("expected notes= param to be present (clear field)")
	}
}

func TestUpdateProjectRequiresID(t *testing.T) {
	stubRunner(t, false)
	err := UpdateProject(UpdateProjectParams{AuthToken: "tok", Title: strPtr("x")})
	if err == nil {
		t.Fatal("expected error for missing id")
	}
}

func TestUpdateProjectRequiresAuthToken(t *testing.T) {
	stubRunner(t, false)
	err := UpdateProject(UpdateProjectParams{ID: "id", Title: strPtr("x")})
	if err == nil {
		t.Fatal("expected error for missing auth token")
	}
	if !strings.Contains(err.Error(), "auth token") {
		t.Errorf("error should mention auth token: %v", err)
	}
}

func TestUpdateProjectCommandFails(t *testing.T) {
	stubRunner(t, true)
	err := UpdateProject(UpdateProjectParams{ID: "id", AuthToken: "tok", Title: strPtr("x")})
	if err == nil {
		t.Fatal("expected error from failing command")
	}
	if !strings.Contains(err.Error(), "opening URL scheme") {
		t.Errorf("error should mention URL scheme: %v", err)
	}
}

func TestShowByID(t *testing.T) {
	captured := stubRunner(t, false)

	if err := Show(ShowParams{ID: "task-uuid"}); err != nil {
		t.Fatalf("Show: %v", err)
	}
	if len(*captured) != 2 {
		t.Fatalf("expected 2 args (open <url>), got: %v", *captured)
	}
	if (*captured)[0] != "open" {
		t.Fatalf("unexpected command: %v", *captured)
	}
	u := (*captured)[1]
	if !strings.HasPrefix(u, "things:///show?") {
		t.Fatalf("expected show URL, got %q", u)
	}
	parsed, err := url.Parse(u)
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	if got := parsed.Query().Get("id"); got != "task-uuid" {
		t.Errorf("id = %q", got)
	}
}

func TestShowBuiltinList(t *testing.T) {
	captured := stubRunner(t, false)

	if err := Show(ShowParams{ID: "today"}); err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse((*captured)[1])
	if got := parsed.Query().Get("id"); got != "today" {
		t.Errorf("id = %q, want today", got)
	}
}

func TestShowQueryAndFilter(t *testing.T) {
	captured := stubRunner(t, false)

	if err := Show(ShowParams{Query: "staging", Filter: "urgent,work"}); err != nil {
		t.Fatal(err)
	}
	u := (*captured)[1]
	if strings.Contains(u, "+") {
		t.Errorf("URL should use %%20 not +: %q", u)
	}
	parsed, err := url.Parse(u)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	q := parsed.Query()
	if q.Get("query") != "staging" {
		t.Errorf("query = %q", q.Get("query"))
	}
	if q.Get("filter") != "urgent,work" {
		t.Errorf("filter = %q", q.Get("filter"))
	}
	if _, ok := q["id"]; ok {
		t.Errorf("id should be absent when only query is set")
	}
}

func TestShowBackgroundUsesDashG(t *testing.T) {
	captured := stubRunner(t, false)

	if err := Show(ShowParams{ID: "inbox", Background: true}); err != nil {
		t.Fatal(err)
	}
	if len(*captured) != 3 || (*captured)[1] != "-g" {
		t.Fatalf("expected open -g <url>, got: %v", *captured)
	}
}

func TestShowRequiresIDOrQuery(t *testing.T) {
	stubRunner(t, false)
	if err := Show(ShowParams{}); err == nil {
		t.Fatal("expected error when id and query are both empty")
	}
}

func TestShowCommandFails(t *testing.T) {
	stubRunner(t, true)
	err := Show(ShowParams{ID: "today"})
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
