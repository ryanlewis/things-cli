package mcpserver_test

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ryanlewis/things-cli/internal/db"
	"github.com/ryanlewis/things-cli/internal/mcpserver"
	"github.com/ryanlewis/things-cli/internal/model"
	"github.com/ryanlewis/things-cli/internal/things"
)

// recordingWriter is a fake mcpserver.Writer that captures the last call to each
// method instead of shelling out to open/osascript, so write handlers can be
// tested for the params they forward.
type recordingWriter struct {
	addTask         *things.AddParams
	addProject      *things.AddProjectParams
	updateTask      *things.UpdateParams
	updateProject   *things.UpdateProjectParams
	completeTask    string
	completeProject string
	cancelTask      string
	logged          bool
	importData      string
	importToken     string
	importReveal    bool
	err             error // returned by every method when set
}

func (w *recordingWriter) AddTask(p things.AddParams) error { w.addTask = &p; return w.err }
func (w *recordingWriter) AddProject(p things.AddProjectParams) error {
	w.addProject = &p
	return w.err
}
func (w *recordingWriter) UpdateTask(p things.UpdateParams) error { w.updateTask = &p; return w.err }
func (w *recordingWriter) UpdateProject(p things.UpdateProjectParams) error {
	w.updateProject = &p
	return w.err
}
func (w *recordingWriter) CompleteTask(uuid string) error    { w.completeTask = uuid; return w.err }
func (w *recordingWriter) CompleteProject(uuid string) error { w.completeProject = uuid; return w.err }
func (w *recordingWriter) CancelTask(uuid string) error      { w.cancelTask = uuid; return w.err }
func (w *recordingWriter) LogCompleted() error               { w.logged = true; return w.err }
func (w *recordingWriter) ImportJSON(data, token string, reveal bool) error {
	w.importData, w.importToken, w.importReveal = data, token, reveal
	return w.err
}

// fakeBackend serves a fixed task and auth token to the resolve+token path of
// the write handlers. The embedded nil Backend panics if any other method is
// reached, which keeps the handlers honest about what they touch.
type fakeBackend struct {
	mcpserver.Backend
	task   *model.Task
	getErr error
	token  string
}

func (fakeBackend) Close() error { return nil }
func (f fakeBackend) GetTask(string) (*model.Task, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.task, nil
}
func (f fakeBackend) GetAuthToken() (string, error) { return f.token, nil }

func openFake(b fakeBackend) func() (mcpserver.Backend, error) {
	return func() (mcpserver.Backend, error) { return b, nil }
}

// mustNotOpen fails the test if a handler opens the database — used for tools
// (add, add_project, log) that mutate without needing the DB.
func mustNotOpen(t *testing.T) func() (mcpserver.Backend, error) {
	t.Helper()
	return func() (mcpserver.Backend, error) {
		t.Error("backend opened, but this tool should not need the database")
		return fakeBackend{}, nil
	}
}

func writeSession(t *testing.T, w mcpserver.Writer, open func() (mcpserver.Backend, error)) *mcp.ClientSession {
	t.Helper()
	return sessionCfg(t, mcpserver.Config{EnableWrites: true, Writer: w, Open: open})
}

func toolNames(t *testing.T, cs *mcp.ClientSession) map[string]bool {
	t.Helper()
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	names := make(map[string]bool, len(res.Tools))
	for _, tool := range res.Tools {
		names[tool.Name] = true
	}
	return names
}

func TestToolsetGating(t *testing.T) {
	noOpen := func() (mcpserver.Backend, error) { return fakeBackend{}, nil }
	cases := []struct {
		name string
		cfg  mcpserver.Config
		want []string
	}{
		{"default is read-only, all toolsets", mcpserver.Config{Open: noOpen}, []string{
			"things_list", "things_show", "things_search", "things_projects", "things_areas", "things_tags",
		}},
		{"tasks read-only", mcpserver.Config{Open: noOpen, Toolsets: []string{"tasks"}}, []string{
			"things_list", "things_show", "things_search",
		}},
		{"tasks with writes", mcpserver.Config{Open: noOpen, Toolsets: []string{"tasks"}, EnableWrites: true}, []string{
			"things_list", "things_show", "things_search", "things_add", "things_edit", "things_complete", "things_cancel",
		}},
		{"areas has no writes", mcpserver.Config{Open: noOpen, Toolsets: []string{"areas"}, EnableWrites: true}, []string{
			"things_areas",
		}},
		{"bulk is empty when read-only", mcpserver.Config{Open: noOpen, Toolsets: []string{"bulk"}}, nil},
		{"bulk with writes", mcpserver.Config{Open: noOpen, Toolsets: []string{"bulk"}, EnableWrites: true}, []string{
			"things_log", "things_import",
		}},
		{"all with writes is full parity", mcpserver.Config{Open: noOpen, Toolsets: []string{"all"}, EnableWrites: true}, []string{
			"things_list", "things_show", "things_search", "things_add", "things_edit", "things_complete", "things_cancel",
			"things_projects", "things_add_project", "things_edit_project",
			"things_areas", "things_tags", "things_log", "things_import",
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := toolNames(t, sessionCfg(t, tc.cfg))
			if len(got) != len(tc.want) {
				t.Errorf("got %d tools %v, want %d %v", len(got), got, len(tc.want), tc.want)
			}
			for _, w := range tc.want {
				if !got[w] {
					t.Errorf("missing tool %q (got %v)", w, got)
				}
			}
		})
	}
}

func TestAddForwarding(t *testing.T) {
	w := &recordingWriter{}
	cs := writeSession(t, w, mustNotOpen(t))
	text, isErr := call(t, cs, "things_add", map[string]any{
		"title":     "Buy milk",
		"notes":     "2%",
		"when":      "today",
		"tags":      []any{"urgent", "home"},
		"checklist": []any{"semi-skimmed", "lactose free"},
		"list":      "Chores",
		"heading":   "Errands",
	})
	if isErr {
		t.Fatalf("unexpected tool error: %s", text)
	}
	if w.addTask == nil {
		t.Fatal("AddTask was not called")
	}
	if w.addTask.Title != "Buy milk" || w.addTask.Notes != "2%" || w.addTask.When != "today" {
		t.Errorf("scalar fields not forwarded: %+v", *w.addTask)
	}
	if w.addTask.Tags != "urgent,home" {
		t.Errorf("tags = %q, want comma-joined", w.addTask.Tags)
	}
	if w.addTask.Checklist != "semi-skimmed\nlactose free" {
		t.Errorf("checklist = %q, want newline-joined", w.addTask.Checklist)
	}
	if w.addTask.List != "Chores" || w.addTask.Heading != "Errands" {
		t.Errorf("list/heading not forwarded: %+v", *w.addTask)
	}
	if !strings.Contains(text, "Submitted new to-do") {
		t.Errorf("result should note async submission: %s", text)
	}
}

func TestAddProjectForwarding(t *testing.T) {
	w := &recordingWriter{}
	cs := writeSession(t, w, mustNotOpen(t))
	text, isErr := call(t, cs, "things_add_project", map[string]any{
		"title": "House move",
		"area":  "Home",
		"tags":  []any{"big"},
		"todos": []any{"pack", "hire van"},
	})
	if isErr {
		t.Fatalf("unexpected tool error: %s", text)
	}
	if w.addProject == nil {
		t.Fatal("AddProject was not called")
	}
	if w.addProject.Title != "House move" || w.addProject.Area != "Home" {
		t.Errorf("title/area not forwarded: %+v", *w.addProject)
	}
	if w.addProject.Tags != "big" || w.addProject.Todos != "pack\nhire van" {
		t.Errorf("tags/todos not joined: %+v", *w.addProject)
	}
}

func TestEditForwarding(t *testing.T) {
	w := &recordingWriter{}
	task := &model.Task{UUID: "task-1", Title: "Buy milk", Type: model.TypeTask}
	cs := writeSession(t, w, openFake(fakeBackend{task: task, token: "secret"}))
	text, isErr := call(t, cs, "things_edit", map[string]any{
		"task":     "task-1",
		"title":    "Buy oat milk",
		"complete": true,
	})
	if isErr {
		t.Fatalf("unexpected tool error: %s", text)
	}
	if w.updateTask == nil {
		t.Fatal("UpdateTask was not called")
	}
	if w.updateTask.ID != "task-1" {
		t.Errorf("ID = %q, want resolved UUID task-1", w.updateTask.ID)
	}
	if w.updateTask.AuthToken != "secret" {
		t.Errorf("AuthToken = %q, want the backend's token", w.updateTask.AuthToken)
	}
	if w.updateTask.Title == nil || *w.updateTask.Title != "Buy oat milk" {
		t.Errorf("Title pointer not forwarded: %+v", w.updateTask.Title)
	}
	if !w.updateTask.Completed {
		t.Errorf("Completed flag not forwarded")
	}
}

func TestEditProjectForwarding(t *testing.T) {
	w := &recordingWriter{}
	proj := &model.Task{UUID: "proj-1", Title: "Chores", Type: model.TypeProject}
	cs := writeSession(t, w, openFake(fakeBackend{task: proj, token: "tok"}))
	text, isErr := call(t, cs, "things_edit_project", map[string]any{
		"project": "proj-1",
		"title":   "Household",
	})
	if isErr {
		t.Fatalf("unexpected tool error: %s", text)
	}
	if w.updateProject == nil {
		t.Fatal("UpdateProject was not called")
	}
	if w.updateProject.ID != "proj-1" || w.updateProject.AuthToken != "tok" {
		t.Errorf("ID/token not forwarded: %+v", *w.updateProject)
	}
	if w.updateProject.Title == nil || *w.updateProject.Title != "Household" {
		t.Errorf("Title not forwarded: %+v", w.updateProject.Title)
	}
}

func TestEditProjectRejectsNonProject(t *testing.T) {
	w := &recordingWriter{}
	todo := &model.Task{UUID: "task-1", Title: "Buy milk", Type: model.TypeTask}
	cs := writeSession(t, w, openFake(fakeBackend{task: todo, token: "tok"}))
	text, isErr := call(t, cs, "things_edit_project", map[string]any{"project": "task-1", "title": "x"})
	if !isErr {
		t.Fatalf("expected error editing a non-project, got: %s", text)
	}
	if !strings.Contains(text, "not a project") {
		t.Errorf("error should say not a project: %s", text)
	}
	if w.updateProject != nil {
		t.Errorf("UpdateProject should not be called for a to-do")
	}
}

func TestCompleteForwarding(t *testing.T) {
	t.Run("to-do", func(t *testing.T) {
		w := &recordingWriter{}
		cs := writeSession(t, w, openFake(fakeBackend{task: &model.Task{UUID: "task-1", Title: "Buy milk", Type: model.TypeTask}}))
		text, isErr := call(t, cs, "things_complete", map[string]any{"task": "task-1"})
		if isErr {
			t.Fatalf("unexpected tool error: %s", text)
		}
		if w.completeTask != "task-1" {
			t.Errorf("CompleteTask = %q, want task-1", w.completeTask)
		}
		if w.completeProject != "" {
			t.Errorf("CompleteProject should not be called for a to-do")
		}
		if !strings.Contains(text, "Completed to-do") {
			t.Errorf("result text = %q", text)
		}
	})

	t.Run("project completes cascade", func(t *testing.T) {
		w := &recordingWriter{}
		cs := writeSession(t, w, openFake(fakeBackend{task: &model.Task{UUID: "proj-1", Title: "Chores", Type: model.TypeProject}}))
		text, isErr := call(t, cs, "things_complete", map[string]any{"task": "proj-1"})
		if isErr {
			t.Fatalf("unexpected tool error: %s", text)
		}
		if w.completeProject != "proj-1" {
			t.Errorf("CompleteProject = %q, want proj-1", w.completeProject)
		}
		if !strings.Contains(text, "Completed project") {
			t.Errorf("result text = %q", text)
		}
	})
}

func TestCancelForwarding(t *testing.T) {
	t.Run("to-do", func(t *testing.T) {
		w := &recordingWriter{}
		cs := writeSession(t, w, openFake(fakeBackend{task: &model.Task{UUID: "task-1", Title: "Buy milk", Type: model.TypeTask}}))
		text, isErr := call(t, cs, "things_cancel", map[string]any{"task": "task-1"})
		if isErr {
			t.Fatalf("unexpected tool error: %s", text)
		}
		if w.cancelTask != "task-1" {
			t.Errorf("CancelTask = %q, want task-1", w.cancelTask)
		}
	})

	t.Run("project rejected", func(t *testing.T) {
		w := &recordingWriter{}
		cs := writeSession(t, w, openFake(fakeBackend{task: &model.Task{UUID: "proj-1", Title: "Chores", Type: model.TypeProject}}))
		text, isErr := call(t, cs, "things_cancel", map[string]any{"task": "proj-1"})
		if !isErr {
			t.Fatalf("expected error cancelling a project, got: %s", text)
		}
		if !strings.Contains(text, "is a project") {
			t.Errorf("error should point at edit_project: %s", text)
		}
		if w.cancelTask != "" {
			t.Errorf("CancelTask should not be called for a project")
		}
	})
}

func TestLogForwarding(t *testing.T) {
	w := &recordingWriter{}
	cs := writeSession(t, w, mustNotOpen(t))
	text, isErr := call(t, cs, "things_log", nil)
	if isErr {
		t.Fatalf("unexpected tool error: %s", text)
	}
	if !w.logged {
		t.Errorf("LogCompleted was not called")
	}
}

func TestImportForwarding(t *testing.T) {
	w := &recordingWriter{}
	cs := writeSession(t, w, openFake(fakeBackend{token: "tok"}))
	payload := `[{"type":"to-do","attributes":{"title":"x"}}]`
	text, isErr := call(t, cs, "things_import", map[string]any{"data": payload, "reveal": true})
	if isErr {
		t.Fatalf("unexpected tool error: %s", text)
	}
	if w.importData != payload {
		t.Errorf("import data = %q, want passthrough", w.importData)
	}
	if w.importToken != "tok" {
		t.Errorf("import token = %q, want the backend's token", w.importToken)
	}
	if !w.importReveal {
		t.Errorf("reveal flag not forwarded")
	}
}

func TestImportValidation(t *testing.T) {
	w := &recordingWriter{}
	cs := writeSession(t, w, openFake(fakeBackend{}))
	for _, bad := range []struct{ name, data string }{
		{"empty", "   "},
		{"not json", "not json"},
		{"not an array", `{"a":1}`},
	} {
		t.Run(bad.name, func(t *testing.T) {
			text, isErr := call(t, cs, "things_import", map[string]any{"data": bad.data})
			if !isErr {
				t.Fatalf("expected validation error, got: %s", text)
			}
		})
	}
	if w.importData != "" {
		t.Errorf("ImportJSON should not be called for an invalid payload")
	}
}

func TestWriteAmbiguousRef(t *testing.T) {
	ambig := &db.AmbiguousTaskError{Query: "Review PR", Matches: []model.Task{
		{UUID: "task-3", Title: "Review PR alpha"},
		{UUID: "task-4", Title: "Review PR beta"},
	}}
	for _, tool := range []string{"things_edit", "things_complete", "things_cancel"} {
		t.Run(tool, func(t *testing.T) {
			w := &recordingWriter{}
			cs := writeSession(t, w, openFake(fakeBackend{getErr: ambig}))
			text, isErr := call(t, cs, tool, map[string]any{"task": "Review PR"})
			if !isErr {
				t.Fatalf("expected ambiguous tool error, got: %s", text)
			}
			if !strings.Contains(text, "ambiguous") || !strings.Contains(text, "task-3") || !strings.Contains(text, "task-4") {
				t.Errorf("error should list candidate UUIDs: %s", text)
			}
		})
	}
}

// TestEditMissingTokenError uses the real Writer (no fake) so the actual
// things.UpdateTask validates the empty auth token and the helpful "enable
// Things URLs" message reaches the caller — without shelling out (the token
// check precedes any URL-scheme call).
func TestEditMissingTokenError(t *testing.T) {
	cs := sessionCfg(t, mcpserver.Config{
		EnableWrites: true,
		Open:         openFake(fakeBackend{task: &model.Task{UUID: "task-1", Title: "Buy milk", Type: model.TypeTask}, token: ""}),
	})
	text, isErr := call(t, cs, "things_edit", map[string]any{"task": "task-1", "title": "x"})
	if !isErr {
		t.Fatalf("expected auth-token error, got: %s", text)
	}
	if !strings.Contains(text, "auth token is required") {
		t.Errorf("want the auth-token guidance: %s", text)
	}
}
