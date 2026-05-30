package main

import (
	"fmt"
	"strings"

	"github.com/alecthomas/kong"
)

// CompletionsCmd prints a shell completion script for the requested shell to
// stdout. The emitted script wires the shell's native completion to call back
// into `things` at completion time — the runtime side that computes candidates
// is handled by kongplete.Complete in main — so completions stay in sync with
// the CLI surface automatically with no regeneration step.
//
// This output is what Homebrew's generate_completions_from_executable captures
// at install time, and what users on other install paths can source directly:
//
//	source <(things completions zsh)            # current shell
//	things completions zsh > ~/.things-completion.zsh   # then source it from rc
type CompletionsCmd struct {
	Shell string `arg:"" required:"" enum:"bash,zsh,fish" help:"Shell to emit completions for (bash|zsh|fish)."`
}

// Per-shell completion stubs. The {{cmd}} placeholder is replaced with the
// command name at runtime. These mirror github.com/willabides/kongplete's
// install templates: bash and zsh register `complete -C` so the shell runs
// `things` itself to compute candidates, and fish wraps the same COMP_LINE
// mechanism. We keep our own copies rather than reuse kongplete's
// InstallCompletions command because that command auto-detects the user's login
// shell and bakes in an absolute binary path; here we take an explicit shell
// argument and emit the bare command name so the script keeps working across a
// `brew upgrade` (which moves the versioned Cellar path).
const (
	bashCompletion = "complete -C {{cmd}} {{cmd}}\n"
	zshCompletion  = "autoload -U +X bashcompinit && bashcompinit\ncomplete -C {{cmd}} {{cmd}}\n"
	fishCompletion = `function __complete_{{cmd}}
    set -lx COMP_LINE (commandline -cp)
    test -z (commandline -ct)
    and set COMP_LINE "$COMP_LINE "
    {{cmd}}
end
complete -f -c {{cmd}} -a "(__complete_{{cmd}})"
`
)

var completionScripts = map[string]string{
	"bash": bashCompletion,
	"zsh":  zshCompletion,
	"fish": fishCompletion,
}

func (c *CompletionsCmd) Run(kctx *kong.Context, d *Deps) error {
	script, err := renderCompletion(c.Shell, kctx.Model.Name)
	if err != nil {
		return err
	}
	_, err = fmt.Fprint(d.Stdout, script)
	return err
}

// renderCompletion returns the completion script for shell with the command
// name substituted for {{cmd}}. It errors on a shell that has no template —
// unreachable while the enum tag and completionScripts keys agree, but the
// guard turns a silent empty script into a clear error if the two ever drift.
func renderCompletion(shell, name string) (string, error) {
	script, ok := completionScripts[shell]
	if !ok {
		return "", fmt.Errorf("unsupported shell %q (want bash, zsh, or fish)", shell)
	}
	return strings.ReplaceAll(script, "{{cmd}}", name), nil
}
