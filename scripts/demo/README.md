# README demo

Two GIFs are recorded with [VHS](https://github.com/charmbracelet/vhs):

- `docs/assets/img/demo.gif`, at the top of the README, from `demo.tape.in`.
- `docs/assets/img/demo-agent.gif`, on the site's "Working with agents" page,
  from `agent.tape.in`.

Re-record whenever the commands they show change their output:

```sh
brew install vhs jq ffmpeg
make demo                      # both
scripts/demo/record.sh demo    # or one at a time
scripts/demo/record.sh agent
```

That builds `./things`, fills the deadline placeholder in the tape with a date
a few days out, records, and then drops idle frames so no pause runs longer
than 3s. Commit the new GIFs.

The agent tape pipes a brief into a real `claude -p` call, so it needs a
logged-in `claude` CLI and spends one API call per take. Claude's reply is
not scripted: check the result reads well before committing it. Claude runs
with the sandbox directory as its working directory and a project settings
file there that denies file edits, subagents and web access, and it may only
run `things` commands. The `things` commands still hit the demo database,
because the shims are on its `PATH`.

## How it stays free of real data

The demo runs the real binary, but never against the real Things database:

- `env.sh` (sourced by the tape before anything is visible) creates a
  throwaway `HOME` with a seeded SQLite database and a config file whose `db`
  points at it, so `things` needs no flags. With `THINGS_DEMO_KEEP_HOME=1`
  (the agent tape) the real `HOME` stays, because `claude` reads its login
  from `~/.claude`, and the config is wired through `XDG_CONFIG_HOME` instead.
  In that mode the last-list cache under `~/Library/Caches/things-cli` is the
  real one and gets overwritten.
- `thingsdemo.py seed` fills that database from the test schema in
  `internal/db/dbtest/schema.sql` with made-up tasks. UUIDs come from a fixed
  random seed so re-recordings show the same ids.
- Writes normally go out through `open things:///...` and `osascript`.
  `bin/open` and `bin/osascript` sit ahead of the real ones on `PATH` and hand
  the URL or AppleScript to `thingsdemo.py`, which applies the change to the
  demo database. The CLI's own read-back (the check that a `complete` landed,
  the tag existence check on `add`) therefore runs for real.

Nothing in the flow reads or writes the real Things database or
`~/.config/things-cli`.

## Changing the demo

- Commands and pacing: edit the `.tape.in` files. `@DEADLINE@` is the only
  placeholder; `record.sh` fills it. Numeric indices (`edit 3`, `show 3`)
  refer to the previous listing, so re-check them after changing the seed.
- Seed data: edit `seed()` in `thingsdemo.py`. Dates are relative to the day
  of recording.
- New write commands: extend `handle_open` / `handle_osascript`. An unhandled
  URL or script exits non-zero, which shows up as an error in the recording
  rather than silently doing nothing.

To try the flow without recording:

```sh
make build
bash -c 'source scripts/demo/env.sh; things; things add "Try it" --when today; things'
```
