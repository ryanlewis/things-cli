package mcpserver_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ryanlewis/things-cli/internal/db"
	"github.com/ryanlewis/things-cli/internal/db/dbtest"
	"github.com/ryanlewis/things-cli/internal/mcpserver"
	"github.com/ryanlewis/things-cli/internal/model"
)

// nopCloseBackend lets every tool call share one in-memory database: the
// open-per-call contract still runs (and Close is exercised) but it doesn't
// tear down the test fixture.
type nopCloseBackend struct{ mcpserver.Backend }

func (nopCloseBackend) Close() error { return nil }

// connect seeds a database, starts the MCP server over an in-memory transport,
// and returns a connected client session.
func connect(t *testing.T) *mcp.ClientSession {
	t.Helper()
	database := seedDB(t)
	return session(t, func() (mcpserver.Backend, error) {
		return nopCloseBackend{database}, nil
	})
}

// session starts the MCP server with the given per-call opener over an
// in-memory transport and returns a connected client session.
func session(t *testing.T, open func() (mcpserver.Backend, error)) *mcp.ClientSession {
	t.Helper()
	return sessionCfg(t, mcpserver.Config{Open: open})
}

// sessionCfg is session with a caller-supplied Config (for toolset/write
// coverage). Version defaults to "test" when unset.
func sessionCfg(t *testing.T, cfg mcpserver.Config) *mcp.ClientSession {
	t.Helper()
	if cfg.Version == "" {
		cfg.Version = "test"
	}
	srv := mcpserver.NewServer(cfg)

	ctx := context.Background()
	serverT, clientT := mcp.NewInMemoryTransports()
	ss, err := srv.Connect(ctx, serverT, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	t.Cleanup(func() { _ = ss.Close() })

	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "test"}, nil)
	cs, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

// call invokes a tool and returns its text content plus the IsError flag. A
// non-nil Go error means a protocol-level failure (not a tool error).
func call(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) (string, bool) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool %s: %v", name, err)
	}
	var sb strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	return sb.String(), res.IsError
}

func TestListTools(t *testing.T) {
	cs := connect(t)
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	got := make(map[string]bool)
	for _, tool := range res.Tools {
		got[tool.Name] = true
		if tool.Description == "" {
			t.Errorf("tool %s has no description", tool.Name)
		}
	}
	for _, want := range []string{
		"things_list", "things_show", "things_search",
		"things_projects", "things_areas", "things_tags",
	} {
		if !got[want] {
			t.Errorf("missing tool %q (got %v)", want, got)
		}
	}
	if len(res.Tools) != 6 {
		t.Errorf("got %d tools, want 6", len(res.Tools))
	}
}

func TestListTool(t *testing.T) {
	cs := connect(t)

	t.Run("today default", func(t *testing.T) {
		text, isErr := call(t, cs, "things_list", nil)
		if isErr {
			t.Fatalf("unexpected tool error: %s", text)
		}
		// The result must be the CLI's --json output: pure JSON, no ANSI.
		if strings.Contains(text, "\x1b[") {
			t.Errorf("result contains ANSI escape sequences: %q", text)
		}
		tasks := decodeTasks(t, text)
		if !containsTitle(tasks, "Buy milk") {
			t.Errorf("today view missing 'Buy milk': %s", text)
		}
	})

	t.Run("inbox view", func(t *testing.T) {
		text, isErr := call(t, cs, "things_list", map[string]any{"view": "inbox"})
		if isErr {
			t.Fatalf("unexpected tool error: %s", text)
		}
		tasks := decodeTasks(t, text)
		if !containsTitle(tasks, "Think") {
			t.Errorf("inbox view missing 'Think': %s", text)
		}
		if containsTitle(tasks, "Buy milk") {
			t.Errorf("inbox view should not contain today task: %s", text)
		}
	})

	t.Run("tag filter", func(t *testing.T) {
		text, isErr := call(t, cs, "things_list", map[string]any{"view": "anytime", "tag": "urgent"})
		if isErr {
			t.Fatalf("unexpected tool error: %s", text)
		}
		tasks := decodeTasks(t, text)
		if !containsTitle(tasks, "Buy milk") {
			t.Errorf("tag filter missing 'Buy milk': %s", text)
		}
	})

	t.Run("unknown view is a tool error", func(t *testing.T) {
		text, isErr := call(t, cs, "things_list", map[string]any{"view": "bogus"})
		if !isErr {
			t.Fatalf("expected tool error for bogus view, got: %s", text)
		}
		if !strings.Contains(text, "unknown view") {
			t.Errorf("error should mention unknown view: %s", text)
		}
	})

	t.Run("date filters rejected on inbox", func(t *testing.T) {
		text, isErr := call(t, cs, "things_list", map[string]any{"view": "inbox", "on": "2026-05-30"})
		if !isErr {
			t.Fatalf("expected tool error for date filter on inbox, got: %s", text)
		}
	})

	t.Run("on cannot combine with from", func(t *testing.T) {
		text, isErr := call(t, cs, "things_list", map[string]any{"view": "anytime", "on": "2026-05-30", "from": "2026-05-01"})
		if !isErr {
			t.Fatalf("expected tool error combining on/from, got: %s", text)
		}
	})
}

func TestListDateFilters(t *testing.T) {
	cs := connect(t)

	t.Run("from/to range includes scheduled task", func(t *testing.T) {
		text, isErr := call(t, cs, "things_list", map[string]any{
			"view": "upcoming", "from": "2026-06-01", "to": "2026-06-30",
		})
		if isErr {
			t.Fatalf("unexpected tool error: %s", text)
		}
		if !containsTitle(decodeTasks(t, text), "Upcoming review") {
			t.Errorf("range 2026-06 missing 'Upcoming review': %s", text)
		}
	})

	t.Run("from after the task excludes it", func(t *testing.T) {
		text, isErr := call(t, cs, "things_list", map[string]any{"view": "upcoming", "from": "2026-07-01"})
		if isErr {
			t.Fatalf("unexpected tool error: %s", text)
		}
		if containsTitle(decodeTasks(t, text), "Upcoming review") {
			t.Errorf("from 2026-07-01 should exclude the 2026-06-15 task: %s", text)
		}
	})

	t.Run("inverted range is a tool error", func(t *testing.T) {
		text, isErr := call(t, cs, "things_list", map[string]any{
			"view": "upcoming", "from": "2026-06-30", "to": "2026-06-01",
		})
		if !isErr {
			t.Fatalf("expected tool error for inverted range, got: %s", text)
		}
		if !strings.Contains(text, "is after") {
			t.Errorf("error should explain the inverted range: %s", text)
		}
	})

	t.Run("deadlines view filters by deadline date", func(t *testing.T) {
		text, isErr := call(t, cs, "things_list", map[string]any{"view": "deadlines", "on": "2026-06-20"})
		if isErr {
			t.Fatalf("unexpected tool error: %s", text)
		}
		if !containsTitle(decodeTasks(t, text), "Ship release") {
			t.Errorf("deadlines on 2026-06-20 missing 'Ship release': %s", text)
		}
	})
}

// TestBackendOpenError locks in the open-per-call error path: when the opener
// fails, the handler must surface a tool error rather than dereference a failed
// backend (the nil-interface trap).
func TestBackendOpenError(t *testing.T) {
	cs := session(t, func() (mcpserver.Backend, error) {
		return nil, errors.New("database unavailable")
	})
	for _, tool := range []string{"things_list", "things_search", "things_areas"} {
		args := map[string]any(nil)
		if tool == "things_search" {
			args = map[string]any{"query": "x"}
		}
		text, isErr := call(t, cs, tool, args)
		if !isErr {
			t.Errorf("%s: expected tool error when backend open fails, got: %s", tool, text)
		}
		if !strings.Contains(text, "database unavailable") {
			t.Errorf("%s: error should surface the open failure: %s", tool, text)
		}
	}
}

func TestShowTool(t *testing.T) {
	cs := connect(t)

	t.Run("by uuid with checklist", func(t *testing.T) {
		text, isErr := call(t, cs, "things_show", map[string]any{"task": "task-1"})
		if isErr {
			t.Fatalf("unexpected tool error: %s", text)
		}
		var got map[string]any
		if err := json.Unmarshal([]byte(text), &got); err != nil {
			t.Fatalf("unmarshal show: %v\n%s", err, text)
		}
		if got["title"] != "Buy milk" {
			t.Errorf("title = %v, want Buy milk", got["title"])
		}
		if got["checklist"] == nil {
			t.Errorf("expected checklist in show output: %s", text)
		}
	})

	t.Run("not found", func(t *testing.T) {
		text, isErr := call(t, cs, "things_show", map[string]any{"task": "no-such-task"})
		if !isErr {
			t.Fatalf("expected tool error for missing task, got: %s", text)
		}
		if !strings.Contains(text, "not found") {
			t.Errorf("error should say not found: %s", text)
		}
	})

	t.Run("ambiguous lists candidates", func(t *testing.T) {
		text, isErr := call(t, cs, "things_show", map[string]any{"task": "Review PR"})
		if !isErr {
			t.Fatalf("expected tool error for ambiguous task, got: %s", text)
		}
		if !strings.Contains(text, "ambiguous") || !strings.Contains(text, "task-3") || !strings.Contains(text, "task-4") {
			t.Errorf("ambiguous error should list candidate UUIDs: %s", text)
		}
	})
}

func TestSearchTool(t *testing.T) {
	cs := connect(t)
	text, isErr := call(t, cs, "things_search", map[string]any{"query": "milk"})
	if isErr {
		t.Fatalf("unexpected tool error: %s", text)
	}
	tasks := decodeTasks(t, text)
	if !containsTitle(tasks, "Buy milk") {
		t.Errorf("search 'milk' missing 'Buy milk': %s", text)
	}
}

// TestEmptyResultIsJSONArray pins the array contract: a query matching nothing
// must serialize as `[]`, not `null`, so clients can iterate unconditionally.
func TestEmptyResultIsJSONArray(t *testing.T) {
	cs := connect(t)
	text, isErr := call(t, cs, "things_search", map[string]any{"query": "zzz-no-such-task-zzz"})
	if isErr {
		t.Fatalf("unexpected tool error: %s", text)
	}
	if got := strings.TrimSpace(text); got != "[]" {
		t.Errorf("empty search result = %q, want %q", got, "[]")
	}
}

func TestProjectsAreasTags(t *testing.T) {
	cs := connect(t)

	projText, isErr := call(t, cs, "things_projects", nil)
	if isErr {
		t.Fatalf("projects tool error: %s", projText)
	}
	var projects []model.Project
	if err := json.Unmarshal([]byte(projText), &projects); err != nil {
		t.Fatalf("unmarshal projects: %v\n%s", err, projText)
	}
	if len(projects) == 0 || projects[0].Title != "Chores" {
		t.Errorf("projects = %+v, want one titled Chores", projects)
	}

	areaText, isErr := call(t, cs, "things_areas", nil)
	if isErr {
		t.Fatalf("areas tool error: %s", areaText)
	}
	if !strings.Contains(areaText, "Home") {
		t.Errorf("areas missing 'Home': %s", areaText)
	}

	tagText, isErr := call(t, cs, "things_tags", nil)
	if isErr {
		t.Fatalf("tags tool error: %s", tagText)
	}
	if !strings.Contains(tagText, "urgent") {
		t.Errorf("tags missing 'urgent': %s", tagText)
	}
}

// --- helpers ---

func decodeTasks(t *testing.T, text string) []model.Task {
	t.Helper()
	var tasks []model.Task
	if err := json.Unmarshal([]byte(text), &tasks); err != nil {
		t.Fatalf("unmarshal tasks: %v\n%s", err, text)
	}
	return tasks
}

func containsTitle(tasks []model.Task, title string) bool {
	for _, task := range tasks {
		if task.Title == title {
			return true
		}
	}
	return false
}

func seedDB(t *testing.T) *db.DB {
	t.Helper()
	sqlDB := dbtest.NewSQL(t)
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := sqlDB.Exec(query, args...); err != nil {
			t.Fatalf("seed (%s): %v", query, err)
		}
	}

	exec(`INSERT INTO TMArea (uuid, title, visible, "index") VALUES ('area-1', 'Home', 1, 0)`)
	exec(`INSERT INTO TMTask (uuid, title, type, status, trashed, area, "index")
	      VALUES ('proj-1', 'Chores', 1, 0, 0, 'area-1', 0)`)
	exec(`INSERT INTO TMTag (uuid, title, "index") VALUES ('tag-1', 'urgent', 0)`)

	today := int64(model.ThingsDateFromTime(time.Now()))
	exec(`INSERT INTO TMTask (uuid, title, type, status, trashed, start, startBucket, startDate, project, "index")
	      VALUES ('task-1', 'Buy milk', 0, 0, 0, 1, 0, ?, 'proj-1', 0)`, today)
	exec(`INSERT INTO TMTaskTag (tasks, tags) VALUES ('task-1', 'tag-1')`)
	exec(`INSERT INTO TMChecklistItem (uuid, title, status, "index", task)
	      VALUES ('cl-1', 'Lactose free', 0, 0, 'task-1')`)

	exec(`INSERT INTO TMTask (uuid, title, type, status, trashed, start, "index")
	      VALUES ('task-2', 'Think', 0, 0, 0, 0, 1)`)

	// Two open tasks sharing a title prefix but no exact match: a show by the
	// shared substring "Review PR" must be reported as ambiguous.
	exec(`INSERT INTO TMTask (uuid, title, type, status, trashed, start, "index")
	      VALUES ('task-3', 'Review PR alpha', 0, 0, 0, 1, 2)`)
	exec(`INSERT INTO TMTask (uuid, title, type, status, trashed, start, "index")
	      VALUES ('task-4', 'Review PR beta', 0, 0, 0, 1, 3)`)

	// Scheduled (upcoming) task with a fixed startDate for from/to range tests.
	exec(`INSERT INTO TMTask (uuid, title, type, status, trashed, start, startDate, "index")
	      VALUES ('task-5', 'Upcoming review', 0, 0, 0, 2, ?, 4)`, dateInt(scheduledDate))
	// Task with a fixed deadline for deadlines-view date-filter tests.
	exec(`INSERT INTO TMTask (uuid, title, type, status, trashed, start, deadline, "index")
	      VALUES ('task-6', 'Ship release', 0, 0, 0, 1, ?, 5)`, dateInt(deadlineDate))

	return db.NewFromSQL(sqlDB)
}

// Fixed dates used by the date-filter tests (kept well clear of "today").
var (
	scheduledDate = time.Date(2026, 6, 15, 0, 0, 0, 0, time.Local)
	deadlineDate  = time.Date(2026, 6, 20, 0, 0, 0, 0, time.Local)
)

func dateInt(tm time.Time) int64 { return int64(model.ThingsDateFromTime(tm)) }
