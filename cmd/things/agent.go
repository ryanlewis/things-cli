package main

import (
	"fmt"
	"os"

	"github.com/mattn/go-isatty"

	"github.com/ryanlewis/things-cli/internal/db"
	"github.com/ryanlewis/things-cli/internal/model"
	"github.com/ryanlewis/things-cli/internal/output"
)

// agentHint is the pointer printed under a plain task listing. The numeric
// index is the handle the reader already has in front of them; --agent is what
// they cannot discover from the listing itself.
const agentHint = "things show <n> --agent hands a to-do to an agent (disable with hints = false in the config file)"

// isStdoutTTY reports whether stdout is a terminal, using the same package and
// the same Cygwin allowance as isInteractive does for stdin. It is a var so
// tests can stub it — nothing in a test writes to a real terminal.
var isStdoutTTY = func() bool {
	fd := os.Stdout.Fd()
	return isatty.IsTerminal(fd) || isatty.IsCygwinTerminal(fd)
}

// printAgentHint writes the --agent pointer under a listing. It is suppressed
// under --json and whenever stdout is not a terminal, because then a program
// is reading the output and a hint is noise in its input; for an empty listing,
// because there is no <n> to show; and when hints are turned off.
func printAgentHint(d *Deps, listed int) error {
	if d.JSON || !d.Hints || listed == 0 || !isStdoutTTY() {
		return nil
	}
	return output.PrintHint(d.Stdout, agentHint)
}

// showAgentBrief renders the Markdown brief `things show --agent` prints. A
// project also lists its open to-dos, each with the UUID an agent needs to act
// on it. The to-do UUIDs are deliberately not written to the last-list cache:
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
