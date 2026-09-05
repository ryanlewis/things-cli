# Sourced by the VHS tape before the visible part of the demo starts. Builds a
# throwaway HOME with a seeded demo database and a config file pointing at it,
# and puts the write shims (see thingsdemo.py) ahead of the real `open` and
# `osascript` on PATH. Nothing here touches the real Things database or the
# real ~/.config / ~/Library/Caches.

DEMO_ROOT=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
DEMO_HOME=$(mktemp -d -t things-cli-demo.XXXXXX)

export THINGS_DEMO_DB="$DEMO_HOME/things.sqlite"
python3 -B "$DEMO_ROOT/scripts/demo/thingsdemo.py" seed "$THINGS_DEMO_DB" "$DEMO_ROOT/internal/db/dbtest/schema.sql"

# Point `things` at the demo database through its config file. Normally the
# whole HOME moves to the throwaway directory, which also isolates the
# last-list cache. THINGS_DEMO_KEEP_HOME=1 leaves HOME alone and uses
# XDG_CONFIG_HOME instead, for a recording that also runs a tool needing the
# real HOME (the `claude` CLI reads its credentials from ~/.claude). The cache
# under ~/Library/Caches/things-cli is then the real one and gets overwritten.
if [[ -n "${THINGS_DEMO_KEEP_HOME:-}" ]]; then
  export XDG_CONFIG_HOME="$DEMO_HOME/.config"
  # The agent tape runs `claude -p` with DEMO_HOME as its working directory.
  # Project settings there keep it to running commands: no file edits, no
  # subagents, no web. Unsetting the nesting markers lets the recording be made
  # from inside a Claude Code session.
  mkdir -p "$DEMO_HOME/.claude"
  printf '{"permissions":{"deny":["Edit","Write","NotebookEdit","Agent","WebFetch","WebSearch"]}}\n' > "$DEMO_HOME/.claude/settings.json"
  unset CLAUDECODE CLAUDE_CODE_ENTRYPOINT
else
  export HOME="$DEMO_HOME"
  unset XDG_CONFIG_HOME
fi
mkdir -p "$DEMO_HOME/.config/things-cli"
printf 'db = "%s"\nhints = false\n' "$THINGS_DEMO_DB" > "$DEMO_HOME/.config/things-cli/config.toml"

mkdir -p "$DEMO_HOME/bin"
ln -s "$DEMO_ROOT/things" "$DEMO_HOME/bin/things"
export PATH="$DEMO_HOME/bin:$DEMO_ROOT/scripts/demo/bin:$PATH"

export PS1='\[\e[1;32m\]❯\[\e[0m\] '
