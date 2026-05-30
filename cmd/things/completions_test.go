package main

import (
	"bytes"
	"strconv"
	"strings"
	"testing"

	"github.com/alecthomas/kong"
	"github.com/willabides/kongplete"

	"github.com/ryanlewis/things-cli/internal/skill"
	"github.com/ryanlewis/things-cli/internal/things"
)

// TestCompletionsEmitsScriptPerShell drives `completions <shell>` through the
// real Kong wiring and asserts each shell gets a non-empty script that names
// the binary and carries the shell-specific completion directive.
func TestCompletionsEmitsScriptPerShell(t *testing.T) {
	cases := map[string]string{
		"bash": "complete -C things things",
		"zsh":  "complete -C things things",
		"fish": "complete -f -c things",
	}
	for shell, want := range cases {
		t.Run(shell, func(t *testing.T) {
			_, ctx := parse(t, "completions", shell)
			var buf bytes.Buffer
			if err := ctx.Run(&Deps{Stdout: &buf}); err != nil {
				t.Fatalf("run completions %s: %v", shell, err)
			}
			out := buf.String()
			if out == "" {
				t.Fatalf("completions %s: empty output", shell)
			}
			if !strings.Contains(out, "things") {
				t.Errorf("completions %s: output missing binary name: %q", shell, out)
			}
			if !strings.Contains(out, want) {
				t.Errorf("completions %s: output missing %q: %q", shell, want, out)
			}
		})
	}
}

// TestCompletionsZshEnablesBashcompinit guards the zsh-specific preamble: the
// `complete -C` directive only works in zsh after bashcompinit is loaded.
func TestCompletionsZshEnablesBashcompinit(t *testing.T) {
	_, ctx := parse(t, "completions", "zsh")
	var buf bytes.Buffer
	if err := ctx.Run(&Deps{Stdout: &buf}); err != nil {
		t.Fatalf("run completions zsh: %v", err)
	}
	if !strings.Contains(buf.String(), "bashcompinit") {
		t.Errorf("zsh script must enable bashcompinit: %q", buf.String())
	}
}

// TestCompletionsRejectsUnknownShell confirms the enum tag rejects shells we
// don't ship a stub for, rather than emitting an empty or wrong script.
func TestCompletionsRejectsUnknownShell(t *testing.T) {
	var cli CLI
	parser, err := kong.New(&cli, kong.Name("things"),
		kong.Vars{
			"builtin_lists": strings.Join(things.BuiltinLists, ", "),
			"skill_agents":  skill.AgentNames(),
		},
	)
	if err != nil {
		t.Fatalf("kong.New: %v", err)
	}
	if _, err := parser.Parse([]string{"completions", "powershell"}); err == nil {
		t.Fatal("expected enum rejection for unsupported shell")
	}
}

// TestRenderCompletionSubstitutesName proves {{cmd}} actually flows from the
// command name into the output (using a non-"things" name so the assertion
// can't pass by coincidence) and that an unknown shell hits the guard rather
// than emitting an empty script.
func TestRenderCompletionSubstitutesName(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish"} {
		out, err := renderCompletion(shell, "thingz")
		if err != nil {
			t.Fatalf("renderCompletion(%q): %v", shell, err)
		}
		if strings.Contains(out, "{{cmd}}") {
			t.Errorf("renderCompletion(%q): placeholder not substituted: %q", shell, out)
		}
		if !strings.Contains(out, "thingz") || strings.Contains(out, "things ") {
			t.Errorf("renderCompletion(%q): name not propagated: %q", shell, out)
		}
	}

	if _, err := renderCompletion("powershell", "things"); err == nil {
		t.Error("renderCompletion: expected error for unsupported shell")
	}
}

// TestRuntimeCompletionAnswersCompLine locks down the load-bearing half of the
// feature: kongplete.Complete answering a COMP_LINE query from the static
// command tree. WithExitFunc intercepts the would-be os.Exit so the assertions
// run in-process; kong.Writers redirects candidates into a buffer.
func TestRuntimeCompletionAnswersCompLine(t *testing.T) {
	cases := []struct {
		line string
		want []string
	}{
		{"things ", []string{"list", "add", "completions"}},      // top-level subcommands
		{"things list --", []string{"--project", "--color"}},     // flag names
		{"things --color ", []string{"auto", "always", "never"}}, // enum values
	}
	for _, tc := range cases {
		t.Run(tc.line, func(t *testing.T) {
			t.Setenv("COMP_LINE", tc.line)
			t.Setenv("COMP_POINT", strconv.Itoa(len(tc.line)))

			var buf bytes.Buffer
			var cli CLI
			parser := kong.Must(&cli, kong.Name("things"), kong.Writers(&buf, &buf),
				kong.Vars{
					"builtin_lists": strings.Join(things.BuiltinLists, ", "),
					"skill_agents":  skill.AgentNames(),
				},
			)

			exited := -1
			kongplete.Complete(parser, kongplete.WithExitFunc(func(code int) { exited = code }))
			if exited != 0 {
				t.Fatalf("expected completion to exit 0, got %d (out=%q)", exited, buf.String())
			}
			out := buf.String()
			for _, w := range tc.want {
				if !strings.Contains(out, w) {
					t.Errorf("COMP_LINE=%q: missing %q in candidates:\n%s", tc.line, w, out)
				}
			}
		})
	}
}
