//go:build integration

package main

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ryanlewis/things-cli/internal/db/dbtest"
	"github.com/ryanlewis/things-cli/internal/model"
)

// TestMCPStdioRoundTrip builds the binary and drives full MCP sessions over its
// stdio (spawn → initialize → tools/list → tools/call) under several flag
// combinations. It is hermetic: a seeded temp DB stands in for real Things3, and
// write tools are only listed, never called (calling them would shell out to
// open/osascript). Gated behind the `integration` build tag (run via
// `make test-integration`).
func TestMCPStdioRoundTrip(t *testing.T) {
	dbPath, sqlDB := dbtest.NewFileSQL(t)
	if _, err := sqlDB.Exec(
		`INSERT INTO TMTask (uuid, title, type, status, trashed, start, "index")
		 VALUES ('task-1', 'Buy milk', 0, 0, 0, 0, 0)`,
	); err != nil {
		t.Fatalf("seed: %v", err)
	}

	bin := filepath.Join(t.TempDir(), "things")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build binary: %v\n%s", err, out)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	t.Run("default is read-only with six tools and serves a read", func(t *testing.T) {
		cs := connectSpawned(t, ctx, bin, dbPath)
		if names := listToolNames(t, ctx, cs); len(names) != 6 {
			t.Fatalf("default tools/list = %d tools %v, want 6", len(names), names)
		}

		res, err := cs.CallTool(ctx, &mcp.CallToolParams{
			Name:      "things_search",
			Arguments: map[string]any{"query": "milk"},
		})
		if err != nil {
			t.Fatalf("tools/call: %v", err)
		}
		if res.IsError {
			t.Fatalf("things_search reported a tool error: %+v", res.Content)
		}
		var text string
		for _, c := range res.Content {
			if tc, ok := c.(*mcp.TextContent); ok {
				text += tc.Text
			}
		}
		var tasks []model.Task
		if err := json.Unmarshal([]byte(text), &tasks); err != nil {
			t.Fatalf("result is not the CLI's JSON: %v\n%s", err, text)
		}
		found := false
		for _, task := range tasks {
			if task.Title == "Buy milk" {
				found = true
			}
		}
		if !found {
			t.Errorf("round-trip did not return the seeded task: %s", text)
		}
	})

	t.Run("toolsets trims the read tool list", func(t *testing.T) {
		cs := connectSpawned(t, ctx, bin, dbPath, "--toolsets=tasks")
		names := listToolNames(t, ctx, cs)
		if len(names) != 3 {
			t.Fatalf("--toolsets=tasks = %d tools %v, want 3", len(names), names)
		}
		for _, want := range []string{"things_list", "things_show", "things_search"} {
			if !names[want] {
				t.Errorf("missing tool %q (got %v)", want, names)
			}
		}
	})

	t.Run("read-only=false exposes the write tools", func(t *testing.T) {
		cs := connectSpawned(t, ctx, bin, dbPath, "--read-only=false")
		names := listToolNames(t, ctx, cs)
		if len(names) != 14 {
			t.Fatalf("--read-only=false = %d tools %v, want 14", len(names), names)
		}
		for _, want := range []string{
			"things_add", "things_edit", "things_complete", "things_cancel",
			"things_add_project", "things_edit_project", "things_log", "things_import",
		} {
			if !names[want] {
				t.Errorf("missing write tool %q (got %v)", want, names)
			}
		}
		// Deliberately do not CALL any write tool here: that would shell out to
		// open/osascript and mutate real Things. The recordingWriter unit tests
		// cover write-handler behavior.
	})
}

func connectSpawned(t *testing.T, ctx context.Context, bin, dbPath string, extraArgs ...string) *mcp.ClientSession {
	t.Helper()
	args := append([]string{"--db", dbPath, "mcp"}, extraArgs...)
	transport := &mcp.CommandTransport{Command: exec.Command(bin, args...)}
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "test"}, nil)
	cs, err := client.Connect(ctx, transport, nil)
	if err != nil {
		t.Fatalf("connect to spawned server (%v): %v", extraArgs, err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func listToolNames(t *testing.T, ctx context.Context, cs *mcp.ClientSession) map[string]bool {
	t.Helper()
	tools, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	names := make(map[string]bool, len(tools.Tools))
	for _, tool := range tools.Tools {
		names[tool.Name] = true
	}
	return names
}
