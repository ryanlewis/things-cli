# Sourced by the VHS tape before the visible part of the demo starts. Builds a
# throwaway HOME with a seeded demo database and a config file pointing at it,
# and puts the write shims (see thingsdemo.py) ahead of the real `open` and
# `osascript` on PATH. Nothing here touches the real Things database or the
# real ~/.config / ~/Library/Caches.

DEMO_ROOT=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
DEMO_HOME=$(mktemp -d -t things-cli-demo.XXXXXX)

export THINGS_DEMO_DB="$DEMO_HOME/things.sqlite"
python3 "$DEMO_ROOT/scripts/demo/thingsdemo.py" seed "$THINGS_DEMO_DB" "$DEMO_ROOT/internal/db/dbtest/schema.sql"

export HOME="$DEMO_HOME"
unset XDG_CONFIG_HOME
mkdir -p "$HOME/.config/things-cli"
printf 'db = "%s"\nhints = false\n' "$THINGS_DEMO_DB" > "$HOME/.config/things-cli/config.toml"

mkdir -p "$DEMO_HOME/bin"
ln -s "$DEMO_ROOT/things" "$DEMO_HOME/bin/things"
export PATH="$DEMO_HOME/bin:$DEMO_ROOT/scripts/demo/bin:$PATH"

export PS1='\[\e[1;32m\]❯\[\e[0m\] '
