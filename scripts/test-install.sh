#!/bin/sh
# scripts/test-install.sh — smoke-test install.sh end-to-end.
#
# Builds a goreleaser snapshot, serves dist/ over localhost, and drives
# install.sh through a happy path plus two failure modes.
#
# Requires: go (for goreleaser), python3, curl. Optionally: shellcheck.

set -eu

ROOT=$(CDPATH='' cd -- "$(dirname -- "$0")/.." && pwd)
cd "$ROOT"

PORT=${PORT:-8765}
GORELEASER="go run github.com/goreleaser/goreleaser/v2@latest"

HTTP_PID=""
TMPDIRS=""

cleanup() {
	if [ -n "$HTTP_PID" ]; then
		kill "$HTTP_PID" 2>/dev/null || true
		wait "$HTTP_PID" 2>/dev/null || true
	fi
	for d in $TMPDIRS; do
		rm -rf "$d"
	done
	rm -rf "$ROOT/dist"
}
trap cleanup EXIT INT TERM HUP

say() {
	printf '\n\033[1;34m==> %s\033[0m\n' "$1"
}

fail() {
	printf '\033[1;31mFAIL: %s\033[0m\n' "$1" >&2
	exit 1
}

mktmp() {
	d=$(mktemp -d)
	TMPDIRS="$TMPDIRS $d"
	printf '%s' "$d"
}

serve() {
	# serve the given directory on $PORT; restarts if already running
	dir=$1
	if [ -n "$HTTP_PID" ]; then
		kill "$HTTP_PID" 2>/dev/null || true
		wait "$HTTP_PID" 2>/dev/null || true
	fi
	python3 -m http.server "$PORT" --directory "$dir" >/tmp/test-install-http.log 2>&1 &
	HTTP_PID=$!
	# wait for server to accept connections
	for _ in 1 2 3 4 5 6 7 8 9 10; do
		if curl -fsS -o /dev/null "http://localhost:$PORT/" 2>/dev/null; then
			return 0
		fi
		sleep 0.2
	done
	fail "http server did not start on port $PORT"
}

run_install() {
	# run install.sh with overrides; captures stdout+stderr, returns exit
	target=$1
	version=$2
	set +e
	out=$(INSTALL_DIR="$target" VERSION="$version" \
		RELEASE_BASE_URL="http://localhost:$PORT" \
		sh "$ROOT/install.sh" 2>&1)
	rc=$?
	set -e
	printf '%s\n' "$out"
	return $rc
}

# --- 1. shellcheck ----------------------------------------------------

say "shellcheck install.sh"
if command -v shellcheck >/dev/null 2>&1; then
	shellcheck "$ROOT/install.sh" || fail "shellcheck reported issues"
else
	printf 'shellcheck not installed — skipping (brew install shellcheck)\n'
fi

say "shellcheck scripts/test-install.sh"
if command -v shellcheck >/dev/null 2>&1; then
	shellcheck "$ROOT/scripts/test-install.sh" || fail "shellcheck reported issues on test script"
fi

# --- 2. build snapshot ------------------------------------------------

say "goreleaser release --snapshot"
# shellcheck disable=SC2086
$GORELEASER release --snapshot --clean --skip=publish >/tmp/test-install-gr.log 2>&1 \
	|| { tail -40 /tmp/test-install-gr.log; fail "goreleaser snapshot build failed"; }

# detect the snapshot version from the produced archive name
SNAP_TARBALL=$(find dist -maxdepth 1 -name 'things_*_darwin_arm64.tar.gz' -print -quit)
[ -n "$SNAP_TARBALL" ] || fail "snapshot build produced no darwin_arm64 archive"
SNAP_VERSION_RAW=$(basename "$SNAP_TARBALL" | sed -E 's/^things_(.+)_darwin_arm64\.tar\.gz$/\1/')
SNAP_VERSION="v$SNAP_VERSION_RAW"
printf 'snapshot version: %s\n' "$SNAP_VERSION"

# --- 3. happy path ----------------------------------------------------

say "happy path: install + run binary"
serve "$ROOT/dist"
TARGET=$(mktmp)
out=$(run_install "$TARGET" "$SNAP_VERSION") || fail "install.sh exited non-zero on happy path:\n$out"
printf '%s\n' "$out" | grep -q "Verifying checksum" || fail "no 'Verifying checksum' line"
printf '%s\n' "$out" | grep -q "Installed:" || fail "no 'Installed:' line"
"$TARGET/things" --version >/dev/null || fail "installed binary did not run"
printf 'OK\n'

# --- 4. version without leading v ------------------------------------

say "VERSION=<no-v> is normalized"
TARGET=$(mktmp)
out=$(run_install "$TARGET" "$SNAP_VERSION_RAW") || fail "install.sh exited non-zero with unprefixed version:\n$out"
"$TARGET/things" --version >/dev/null || fail "installed binary did not run"
printf 'OK\n'

# --- 5. missing manifest entry ---------------------------------------

say "failure: checksum manifest missing entry for our tarball"
BAD=$(mktmp)
cp "$ROOT/dist/things_${SNAP_VERSION_RAW}_darwin_arm64.tar.gz" "$BAD/"
printf 'deadbeef  something_else.tar.gz\n' > "$BAD/checksums.txt"
serve "$BAD"
TARGET=$(mktmp)
out=$(run_install "$TARGET" "$SNAP_VERSION") && fail "install.sh should have exited non-zero but succeeded:\n$out"
printf '%s\n' "$out" | grep -q "no checksum entry" || fail "expected 'no checksum entry' in output:\n$out"
printf 'OK\n'

# --- 6. checksum mismatch --------------------------------------------

say "failure: checksum mismatch"
BAD=$(mktmp)
cp "$ROOT/dist/things_${SNAP_VERSION_RAW}_darwin_arm64.tar.gz" "$BAD/"
tarball="things_${SNAP_VERSION_RAW}_darwin_arm64.tar.gz"
printf '0000000000000000000000000000000000000000000000000000000000000000  %s\n' "$tarball" > "$BAD/checksums.txt"
serve "$BAD"
TARGET=$(mktmp)
out=$(run_install "$TARGET" "$SNAP_VERSION") && fail "install.sh should have exited non-zero on checksum mismatch:\n$out"
printf '%s\n' "$out" | grep -q "checksum mismatch" || fail "expected 'checksum mismatch' in output:\n$out"
printf 'OK\n'

say "all tests passed"
