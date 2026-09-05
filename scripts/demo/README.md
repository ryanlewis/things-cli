# README demo

`docs/assets/img/demo.gif` at the top of the README is recorded with
[VHS](https://github.com/charmbracelet/vhs) from `demo.tape.in`. Re-record it
whenever the commands it shows change their output:

```sh
brew install vhs jq
make demo
```

That builds `./things`, fills the deadline placeholder in the tape with a date
a few days out, records, and writes the GIF. Commit the new GIF.

## How it stays free of real data

The demo runs the real binary, but never against the real Things database:

- `env.sh` (sourced by the tape before anything is visible) creates a
  throwaway `HOME` with a seeded SQLite database and a config file whose `db`
  points at it, so `things` needs no flags.
- `thingsdemo.py seed` fills that database from the test schema in
  `internal/db/dbtest/schema.sql` with made-up tasks. UUIDs come from a fixed
  random seed so re-recordings show the same ids.
- Writes normally go out through `open things:///...` and `osascript`.
  `bin/open` and `bin/osascript` sit ahead of the real ones on `PATH` and hand
  the URL or AppleScript to `thingsdemo.py`, which applies the change to the
  demo database. The CLI's own read-back (the check that a `complete` landed,
  the tag existence check on `add`) therefore runs for real.

Nothing in the flow reads or writes `~/Library`, the real Things database, or
`~/.config/things-cli`.

## Changing the demo

- Commands and pacing: edit `demo.tape.in`. `@DEADLINE@` is the only
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
