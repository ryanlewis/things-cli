// Package mcpserver exposes things-cli as a Model Context Protocol (MCP) server
// over stdio.
//
// It mirrors the CLI's commands and renders read results as the same JSON the
// CLI emits with --json, so MCP hosts that cannot shell out (Claude Desktop) —
// and those that can (Cursor, Claude Code) — get a typed alternative to driving
// the binary via Bash.
//
// Tools are grouped into named toolsets (see Toolsets) that the operator mounts
// à la carte, GitHub-MCP style. Write tools are registered only when ReadOnly is
// false, so the default server is read-only.
package mcpserver

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/ryanlewis/things-cli/internal/db"
	"github.com/ryanlewis/things-cli/internal/model"
	"github.com/ryanlewis/things-cli/internal/things"
)

// Backend is the read slice of the Things3 database the tools need. *db.DB
// satisfies it. Config.Open hands each tool call a fresh Backend (open-per-call,
// mirroring the CLI) which the handler closes when done.
type Backend interface {
	ListTasks(view string, opts db.TaskFilter) ([]model.Task, error)
	GetTask(ref string) (*model.Task, error)
	GetChecklistItems(taskUUID string) ([]model.ChecklistItem, error)
	SearchTasks(query string) ([]model.Task, error)
	ListProjects(area string, completed bool) ([]model.Project, error)
	ListAreas() ([]model.Area, error)
	ListTags() ([]model.Tag, error)
	// GetAuthToken returns the Things URL-scheme auth token (empty if unset).
	// Required by the update-based write tools (edit / import).
	GetAuthToken() (string, error)
	Close() error
}

// Writer is the write surface the mutating tools need. The default
// implementation forwards to the package-level functions in internal/things
// (URL scheme for add/edit, AppleScript for complete/cancel). It is an interface
// so tests can record calls without shelling out to open/osascript.
type Writer interface {
	AddTask(things.AddParams) error
	AddProject(things.AddProjectParams) error
	UpdateTask(things.UpdateParams) error
	UpdateProject(things.UpdateProjectParams) error
	CompleteTask(uuid string) error
	CancelTask(uuid string) error
	LogCompleted() error
	ImportJSON(data, authToken string, reveal bool) error
}

// thingsWriter is the production Writer: a thin pass-through to internal/things.
type thingsWriter struct{}

func (thingsWriter) AddTask(p things.AddParams) error           { return things.AddTask(p) }
func (thingsWriter) AddProject(p things.AddProjectParams) error { return things.AddProject(p) }
func (thingsWriter) UpdateTask(p things.UpdateParams) error     { return things.UpdateTask(p) }
func (thingsWriter) UpdateProject(p things.UpdateProjectParams) error {
	return things.UpdateProject(p)
}
func (thingsWriter) CompleteTask(uuid string) error { return things.CompleteTask(uuid) }
func (thingsWriter) CancelTask(uuid string) error   { return things.CancelTask(uuid) }
func (thingsWriter) LogCompleted() error            { return things.LogCompleted() }
func (thingsWriter) ImportJSON(data, authToken string, reveal bool) error {
	return things.ImportJSON(data, authToken, reveal)
}

// AllToolsets lists every toolset name in registration order. Each is a domain
// that owns its read tools (always registered) and write tools (registered only
// when ReadOnly is false).
var AllToolsets = []string{"tasks", "projects", "areas", "tags", "bulk"}

// ValidateToolsets reports the first unrecognized name in names, treating the
// special value "all" as valid. Empty input is valid (defaults to all).
func ValidateToolsets(names []string) error {
	for _, n := range names {
		n = strings.ToLower(strings.TrimSpace(n))
		if n == "all" {
			continue
		}
		valid := false
		for _, a := range AllToolsets {
			if n == a {
				valid = true
				break
			}
		}
		if !valid {
			return fmt.Errorf("unknown toolset %q (valid: %s, all)", n, strings.Join(AllToolsets, ", "))
		}
	}
	return nil
}

// resolveToolsets turns the configured names into a lookup set, expanding "all"
// and defaulting to every toolset when none are given.
func resolveToolsets(names []string) map[string]bool {
	if len(names) == 0 {
		names = []string{"all"}
	}
	set := make(map[string]bool, len(AllToolsets))
	for _, n := range names {
		switch n = strings.ToLower(strings.TrimSpace(n)); n {
		case "":
			continue
		case "all":
			for _, a := range AllToolsets {
				set[a] = true
			}
		default:
			set[n] = true
		}
	}
	return set
}

// Config configures the MCP server.
type Config struct {
	// Open returns a fresh Backend for a single tool call. The handler that
	// requested it is responsible for closing it. Required.
	Open func() (Backend, error)
	// Version is reported to clients as the server implementation version.
	Version string
	// Toolsets selects which toolsets to mount. Empty means all; "all" is an
	// accepted alias. Unknown names are ignored here — validate up front with
	// ValidateToolsets for a clear startup error.
	Toolsets []string
	// EnableWrites registers the write tools of the enabled toolsets. The zero
	// value is read-only, so a default Config is always safe; the CLI flips this
	// on via --read-only=false.
	EnableWrites bool
	// Writer backs the write tools. Defaults to the production internal/things
	// pass-through when nil; tests inject a recording fake.
	Writer Writer
}

// NewServer builds an *mcp.Server with the configured tools registered. It is
// exported so tests can drive it over an in-memory transport.
func NewServer(cfg Config) *mcp.Server {
	w := cfg.Writer
	if w == nil {
		w = thingsWriter{}
	}
	s := mcp.NewServer(&mcp.Implementation{Name: "things", Version: cfg.Version}, nil)
	registerTools(s, toolset{open: cfg.Open, write: w}, resolveToolsets(cfg.Toolsets), !cfg.EnableWrites)
	return s
}

// Serve runs the MCP server over stdio until the client disconnects or ctx is
// cancelled.
func Serve(ctx context.Context, cfg Config) error {
	if cfg.Open == nil {
		return fmt.Errorf("mcpserver: Config.Open is required")
	}
	if err := ValidateToolsets(cfg.Toolsets); err != nil {
		return err
	}
	return NewServer(cfg).Run(ctx, &mcp.StdioTransport{})
}
