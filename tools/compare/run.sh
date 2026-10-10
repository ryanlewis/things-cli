#!/bin/bash
# Compare two things binaries on the same input, to check a change keeps
# behaviour. Each binary runs against its own fresh copy of a seeded test
# database, with `open` and `osascript` replaced by shims that only log
# (nothing reaches the Things app). The script then diffs stdout, stderr,
# the exit code, the shim log and the task statuses left in the database.
#
# Usage:
#   OLD=path/to/things-old NEW=path/to/things-new tools/compare/run.sh NAME STDIN ARGS...
#
#   NAME   a label for this case; outputs go to $OUT/NAME.old and $OUT/NAME.new
#   STDIN  a file to feed on stdin, or - for none
#   ARGS   the things command line, e.g. show TODO1
#
# Example:
#   git worktree add /tmp/things-main origin/main
#   (cd /tmp/things-main && go build -o /tmp/things-old ./cmd/things)
#   go build -o /tmp/things-new ./cmd/things
#   OLD=/tmp/things-old NEW=/tmp/things-new tools/compare/run.sh show-1 - show TODO1
#
# Prints "SAME NAME (exit N, shim N)", or "DIFF NAME" and the first lines of
# the diff and exits 1. Set SHIMAPPLY=1 for the osascript shim to apply
# complete and cancel to the database copy, so a read-back can see them. The database is
# built from internal/db/dbtest/schema.sql and tools/compare/seed.sql. OUT
# defaults to a fresh temporary directory.
#
# A refusal that only the new binary makes means the old one writes: compare
# only inputs both refuse, or use the test stubs.
set -u
here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/../.." && pwd)
: "${OLD:?set OLD to the old things binary}"
: "${NEW:?set NEW to the new things binary}"
OUT=${OUT:-$(mktemp -d)}
mkdir -p "$OUT"
[ $# -ge 2 ] || { echo "usage: OLD=... NEW=... $0 NAME STDIN|- ARGS..." >&2; exit 2; }
name=$1; shift; in=$1; shift

# Resolve both binaries now: under env -i PATH is the shim directory, so a
# bare name would not be found and both runs would fail alike as "SAME".
OLD=$(command -v "$OLD") || { echo "OLD not found" >&2; exit 2; }
NEW=$(command -v "$NEW") || { echo "NEW not found" >&2; exit 2; }
case $OLD in /*) ;; *) OLD=$PWD/$OLD;; esac
case $NEW in /*) ;; *) NEW=$PWD/$NEW;; esac

# Built under a temporary name and moved into place, so a failed seed never
# leaves a half-built base for the next case to reuse.
base=$OUT/base.sqlite
if [ ! -f "$base" ]; then
  rm -f "$base.tmp"
  if ! /usr/bin/sqlite3 "$base.tmp" < "$root/internal/db/dbtest/schema.sql" ||
    ! /usr/bin/sqlite3 "$base.tmp" < "$here/seed.sql"; then
    rm -f "$base.tmp"; exit 1
  fi
  mv "$base.tmp" "$base"
fi

for v in old new; do
  bin=$OLD; [ $v = new ] && bin=$NEW
  d=$OUT/$name.$v; rm -rf "$d"; mkdir -p "$d/home"
  cp "$base" "$d/db.sqlite"
  : > "$d/shim.log"
  if [ "$in" = "-" ]; then inf=/dev/null; else inf=$in; fi
  env -i HOME="$d/home" XDG_CONFIG_HOME="$d/home/.config" PATH="$here/shim" \
    SHIMLOG="$d/shim.log" SHIMDB="$d/db.sqlite" SHIMAPPLY="${SHIMAPPLY:-}" TZ=Europe/London \
    "$bin" --db "$d/db.sqlite" --color never --verify-timeout 600ms "$@" <"$inf" >"$d/stdout" 2>"$d/stderr"
  echo $? > "$d/exit"
  sed -i '' "s#$d#<D>#g" "$d/stdout" "$d/stderr" "$d/shim.log"
  /usr/bin/sqlite3 "$d/db.sqlite" "select uuid,status,stopDate from TMTask order by uuid" > "$d/dbstate"
done
if diff -r -x db.sqlite -x home "$OUT/$name.old" "$OUT/$name.new" >/dev/null; then
  echo "SAME $name (exit $(cat "$OUT/$name.new/exit"), shim $(wc -l < "$OUT/$name.new/shim.log" | tr -d ' '))"
else
  echo "DIFF $name"; diff -r -x db.sqlite -x home "$OUT/$name.old" "$OUT/$name.new" | head -30
  exit 1
fi
