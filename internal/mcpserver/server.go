// Package mcpserver exposes the read-only side of things-cli as a Model Context
// Protocol (MCP) server over stdio.
//
// It mirrors the CLI's read commands 1:1 and renders every result as the same
// JSON the CLI emits with --json, so MCP hosts that cannot shell out (Claude
// Desktop) — and those that can (Cursor, Claude Code) — get a typed alternative
// to driving the binary via Bash. Writes are intentionally out of scope.
package mcpserver

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ryanlewis/things-cli/internal/db"
	"github.com/ryanlewis/things-cli/internal/model"
)

// Backend is the read-only slice of the Things3 database the tools need.
// *db.DB satisfies it. Config.Open hands each tool call a fresh Backend
// (open-per-call, mirroring the CLI) which the handler closes when done.
type Backend interface {
	ListTasks(view string, opts db.TaskFilter) ([]model.Task, error)
	GetTask(ref string) (*model.Task, error)
	GetChecklistItems(taskUUID string) ([]model.ChecklistItem, error)
	SearchTasks(query string) ([]model.Task, error)
	ListProjects(area string, completed bool) ([]model.Project, error)
	ListAreas() ([]model.Area, error)
	ListTags() ([]model.Tag, error)
	Close() error
}

// Config configures the MCP server.
type Config struct {
	// Open returns a fresh Backend for a single tool call. The handler that
	// requested it is responsible for closing it. Required.
	Open func() (Backend, error)
	// Version is reported to clients as the server implementation version.
	Version string
}

// NewServer builds an *mcp.Server with the read-only tools registered. It is
// exported so tests can drive it over an in-memory transport.
func NewServer(cfg Config) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "things", Version: cfg.Version}, nil)
	registerTools(s, toolset{open: cfg.Open})
	return s
}

// Serve runs the MCP server over stdio until the client disconnects or ctx is
// cancelled.
func Serve(ctx context.Context, cfg Config) error {
	if cfg.Open == nil {
		return fmt.Errorf("mcpserver: Config.Open is required")
	}
	return NewServer(cfg).Run(ctx, &mcp.StdioTransport{})
}
