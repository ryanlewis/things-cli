package main

import (
	"strings"
	"testing"

	"github.com/ryanlewis/things-cli/internal/cache"
)

// stubStdoutTTY makes the terminal check answer yes for the rest of the test.
// Nothing in a test writes to a real terminal, so the hint would never appear
// otherwise.
func stubStdoutTTY(t *testing.T, tty bool) {
	t.Helper()
	orig := isStdoutTTY
	isStdoutTTY = func() bool { return tty }
	t.Cleanup(func() { isStdoutTTY = orig })
}

func TestShowAgentBrief(t *testing.T) {
	database := seedFullDB(t)
	out, err := runOut(t, database, "show", "task-1", "--agent")
	if err != nil {
		t.Fatalf("show --agent: %v", err)
	}
	for _, want := range []string{
		"# Buy milk",
		"- UUID: `task-1`",
		"- Project: Chores",
		"- Tags: urgent",
		"## Checklist",
		"- [ ] Lactose free",
		"## Closing out",
		"things complete task-1",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("brief does not contain %q\n%s", want, out)
		}
	}
}

func TestShowAgentBriefForProjectListsOpenTodos(t *testing.T) {
	database := seedFullDB(t)
	out, err := runOut(t, database, "show", "proj-1", "--agent")
	if err != nil {
		t.Fatalf("show proj-1 --agent: %v", err)
	}
	for _, want := range []string{
		"# Chores",
		"A Things3 project",
		"## Open to-dos",
		"- Buy milk — `task-1`",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("project brief does not contain %q\n%s", want, out)
		}
	}
}

// The numeric refs a user types come from the last listing. A brief is not a
// listing, so rendering one must leave those refs pointing where they did.
func TestShowAgentBriefLeavesLastListCacheAlone(t *testing.T) {
	database := seedFullDB(t)
	if err := runWith(t, database, "list", "inbox"); err != nil {
		t.Fatalf("list inbox: %v", err)
	}
	before, err := cache.ReadLastList()
	if err != nil {
		t.Fatalf("ReadLastList: %v", err)
	}
	if err := runWith(t, database, "show", "proj-1", "--agent"); err != nil {
		t.Fatalf("show proj-1 --agent: %v", err)
	}
	after, err := cache.ReadLastList()
	if err != nil {
		t.Fatalf("ReadLastList: %v", err)
	}
	if strings.Join(before, ",") != strings.Join(after, ",") {
		t.Errorf("last-list cache changed from %v to %v", before, after)
	}
}

func TestShowAgentRejectsJSONFlag(t *testing.T) {
	database := seedFullDB(t)
	_, err := runOut(t, database, "--json", "show", "task-1", "--agent")
	if err == nil {
		t.Fatal("--agent --json: want an error, got nil")
	}
	if !strings.Contains(err.Error(), "different output formats") {
		t.Errorf("error %q does not explain the conflict", err)
	}
}

// A config file only supplies defaults, and the documented precedence is
// flag > config file. An explicit --agent therefore overrides json = true
// rather than colliding with it.
func TestShowAgentBeatsJSONFromConfig(t *testing.T) {
	database := seedFullDB(t)
	isolateHome(t)
	path := writeConfig(t, "json = true\n")
	out, err := runOut(t, database, "--config", path, "show", "task-1", "--agent")
	if err != nil {
		t.Fatalf("show --agent with json = true in the config: %v", err)
	}
	if !strings.Contains(out, "# Buy milk") {
		t.Errorf("brief not rendered\n%s", out)
	}
}

func TestListPrintsAgentHint(t *testing.T) {
	database := seedFullDB(t)
	stubStdoutTTY(t, true)
	out, err := runOut(t, database, "list", "today")
	if err != nil {
		t.Fatalf("list today: %v", err)
	}
	if !strings.Contains(out, "hint: things show <n> --agent") {
		t.Errorf("listing carries no hint\n%s", out)
	}
}

// `search` prints the same numbered listing as `list`, off the same cache, so
// the pointer belongs there too.
func TestSearchPrintsAgentHint(t *testing.T) {
	database := seedFullDB(t)
	stubStdoutTTY(t, true)
	out, err := runOut(t, database, "search", "milk")
	if err != nil {
		t.Fatalf("search milk: %v", err)
	}
	if !strings.Contains(out, "hint: things show <n> --agent") {
		t.Errorf("search results carry no hint\n%s", out)
	}
}

func TestListSuppressesAgentHint(t *testing.T) {
	cases := []struct {
		name string
		tty  bool
		args []string
	}{
		{"not a terminal", false, []string{"list", "today"}},
		{"json", true, []string{"--json", "list", "today"}},
		{"no-hints", true, []string{"--no-hints", "list", "today"}},
		{"empty listing", true, []string{"list", "logbook"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			database := seedFullDB(t)
			stubStdoutTTY(t, tc.tty)
			out, err := runOut(t, database, tc.args...)
			if err != nil {
				t.Fatalf("run %v: %v", tc.args, err)
			}
			if strings.Contains(out, "hint:") {
				t.Errorf("hint printed anyway\n%s", out)
			}
		})
	}
}

func TestListHintOffViaConfig(t *testing.T) {
	database := seedFullDB(t)
	isolateHome(t)
	stubStdoutTTY(t, true)
	path := writeConfig(t, "hints = false\n")
	out, err := runOut(t, database, "--config", path, "list", "today")
	if err != nil {
		t.Fatalf("list today with hints = false: %v", err)
	}
	if strings.Contains(out, "hint:") {
		t.Errorf("hints = false did not turn the hint off\n%s", out)
	}
}

// The hint is discoverability, not data: it must never reach a caller reading
// the listing as JSON, whatever the terminal looks like.
func TestPrintAgentHintNeverUnderJSON(t *testing.T) {
	stubStdoutTTY(t, true)
	var d Deps
	d.JSON = true
	d.Hints = true
	d.Stdout = &strings.Builder{}
	if err := printAgentHint(&d, 3); err != nil {
		t.Fatalf("printAgentHint: %v", err)
	}
	if got := d.Stdout.(*strings.Builder).String(); got != "" {
		t.Errorf("printAgentHint wrote %q under --json", got)
	}
}
