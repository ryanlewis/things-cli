package main

import (
	"fmt"

	"github.com/ryanlewis/things-cli/internal/db"
	"github.com/ryanlewis/things-cli/internal/model"
	"github.com/ryanlewis/things-cli/internal/output"
)

// agentHintActions are the next actions offered under a listing, most common
// first. <n> is the numeric index the reader already has in front of them;
// --agent is what they cannot discover from the listing itself. They are
// dropped from the end when the terminal is narrow.
var agentHintActions = []string{
	"complete <n>",
	"show <n>",
	"edit <n> --when tomorrow",
	"show <n> --agent",
}

const agentHintNote = "(disable with hints = false in the config file)"

// stdoutWidth is the width output on stdout has to fit, or 0 when stdout is
// not a terminal. It is the output package's own check, so one answer decides
// both whether the hint prints and how it wraps. It is a var so tests can stub
// it — nothing in a test writes to a real terminal.
var stdoutWidth = output.FitWidth

// printAgentHint writes the next-actions hint under a listing. It is suppressed
// under --json and whenever stdout is not a terminal, because then a program
// is reading the output and a hint is noise in its input; for an empty listing,
// because there is no <n> to show; and when hints are turned off.
func printAgentHint(d *Deps, listed int) error {
	if d.JSON || !d.Hints || listed == 0 {
		return nil
	}
	width := stdoutWidth()
	if width == 0 {
		return nil
	}
	return output.PrintHint(d.Stdout, agentHintActions, agentHintNote, width)
}

// showAgentBrief renders the Markdown brief `things show --agent` prints. A
// project also lists the tasks filed under it, each with the UUID an agent
// needs to act on it — the open ones while the project is open, and its whole
// contents once the project is closed or trashed, which is what the catch-all
// view answers for a named project since issue #229.
//
// The task UUIDs are deliberately not written to the last-list cache:
// the cache backs the numeric refs from the last listing, and a brief is not a
// listing.
func showAgentBrief(d *Deps, database *db.DB, task *model.Task, items []model.ChecklistItem) error {
	brief := output.AgentBrief{Task: task, Checklist: items}
	if task.Type == model.TypeProject {
		todos, err := database.ListTasks("project", db.TaskFilter{Project: task.UUID})
		if err != nil {
			return err
		}
		brief.Todos = todos
	}
	return output.PrintAgentBrief(d.Stdout, brief)
}

// checkAgentFormat rejects --agent alongside --json: they are two different
// output formats and there is no sensible way to serve both.
//
// A config file that sets `json = true` is not that conflict. The documented
// precedence is flag > config file, so an explicit --agent overrides the file
// the same way any flag does. Kong reports a resolved value as "set", so the
// flag cannot say where its value came from and the config file is the only
// thing left to ask. The cost of that is one uncaught case: with json = true
// in the file, `--json --agent` renders the brief instead of reporting the
// conflict. That is the format --agent asked for, so it is a missing error
// rather than wrong output, and catching it would mean reading --json off
// argv a second time.
func checkAgentFormat(d *Deps) error {
	if !d.JSON || d.config().JSON() {
		return nil
	}
	return fmt.Errorf("--agent and --json are different output formats; pass only one")
}
