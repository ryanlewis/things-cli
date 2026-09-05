#!/usr/bin/env bash
# Re-record the README demo GIF. Run from anywhere; `make demo` calls this.
#
# Requires: vhs (brew install vhs), python3, jq, and Go to build the binary.
set -euo pipefail

ROOT=$(cd -- "$(dirname -- "$0")/../.." && pwd)
cd "$ROOT"

command -v vhs >/dev/null || { echo "vhs not found: brew install vhs" >&2; exit 1; }
command -v jq  >/dev/null || { echo "jq not found: brew install jq" >&2; exit 1; }
command -v ffmpeg >/dev/null || { echo "ffmpeg not found: brew install ffmpeg" >&2; exit 1; }

make build

# The tape types a literal deadline, so it has to be regenerated per recording
# to stay a few days ahead of the seeded "today".
if DEADLINE=$(date -v+3d +%F 2>/dev/null); then :; else DEADLINE=$(date -d '+3 days' +%F); fi

# Record every tape, or just the ones named on the command line
# (`record.sh demo`, `record.sh agent`).
tapes=("$@")
[[ ${#tapes[@]} -gt 0 ]] || tapes=(demo agent)

TAPE=$(mktemp -t things-cli-demo.XXXXXX.tape)
trap 'rm -f "$TAPE"' EXIT
# VHS records in real time, and the agent tape waits on a live `claude -p`
# call. Drop frames identical to the previous one and cap any pause at 3s, so
# a 15s wait for the model becomes a 3s pause in the GIF. Reading pauses in
# the tapes are 3s or shorter and come through unchanged.
clamp_idle() {
  local gif=$1 tmp
  tmp=$(mktemp -d -t things-cli-demo.XXXXXX)/clamped.gif   # ffmpeg picks the muxer from the extension
  ffmpeg -loglevel error -y -i "$gif" \
    -vf "mpdecimate,setpts='if(eq(N,0),0,PREV_OUTPTS+min(PTS-PREV_INPTS,3/TB))',split[a][b];[a]palettegen=stats_mode=diff[p];[b][p]paletteuse=dither=none" \
    -fps_mode vfr -final_delay 300 "$tmp"   # hold the last frame 3s before the loop restarts
  mv "$tmp" "$gif"
  rmdir "$(dirname "$tmp")"
}

for name in "${tapes[@]}"; do
  sed "s/@DEADLINE@/$DEADLINE/g" "scripts/demo/$name.tape.in" > "$TAPE"
  vhs "$TAPE"
  clamp_idle "$(sed -n 's/^Output //p' "$TAPE")"
done
ls -la docs/assets/img/demo*.gif
