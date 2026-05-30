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

// TestMCPStdioRoundTrip builds the binary, runs `things --db <seeded> mcp`, and
// drives a full MCP session over its stdio (spawn → initialize → tools/list →
// tools/call). It is hermetic: no real Things3 database is required. Gated
// behind the `integration` build tag (run via `make test-integration`).
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

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	transport := &mcp.CommandTransport{Command: exec.Command(bin, "--db", dbPath, "mcp")}
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "test"}, nil)
	cs, err := client.Connect(ctx, transport, nil)
	if err != nil {
		t.Fatalf("connect to spawned server: %v", err)
	}
	defer func() { _ = cs.Close() }()

	tools, err := cs.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	if len(tools.Tools) != 6 {
		t.Fatalf("tools/list returned %d tools, want 6", len(tools.Tools))
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
}
