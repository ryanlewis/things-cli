#!/usr/bin/env bash
# Re-record the README demo GIF. Run from anywhere; `make demo` calls this.
#
# Requires: vhs (brew install vhs), python3, jq, and Go to build the binary.
set -euo pipefail

ROOT=$(cd -- "$(dirname -- "$0")/../.." && pwd)
cd "$ROOT"

command -v vhs >/dev/null || { echo "vhs not found: brew install vhs" >&2; exit 1; }
command -v jq  >/dev/null || { echo "jq not found: brew install jq" >&2; exit 1; }

make build

# The tape types a literal deadline, so it has to be regenerated per recording
# to stay a few days ahead of the seeded "today".
if DEADLINE=$(date -v+3d +%F 2>/dev/null); then :; else DEADLINE=$(date -d '+3 days' +%F); fi

TAPE=$(mktemp -t things-cli-demo.XXXXXX.tape)
trap 'rm -f "$TAPE"' EXIT
sed "s/@DEADLINE@/$DEADLINE/g" scripts/demo/demo.tape.in > "$TAPE"

vhs "$TAPE"
ls -la docs/assets/img/demo.gif
