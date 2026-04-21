# things-cli

A small Go CLI for [Things3](https://culturedcode.com/things/) on macOS. Reads
tasks, projects, areas and tags straight from the Things3 SQLite database
(read-only) and writes via the `things:///` URL scheme and AppleScript — so the
app stays the source of truth and your data never leaves the machine.

## CLI

```sh
things today                      # tasks scheduled for today
things inbox                      # inbox
things upcoming                   # scheduled + deadlines
things anytime                    # anytime list
things someday                    # someday list
things logbook                    # completed tasks
things deadlines                  # tasks with deadlines

things "Weekly Review"            # tasks in a project by name
things -p "Weekly Review"         # same, explicit
things -a Work                    # filter by area
things -t urgent                  # filter by tag

things projects                   # list projects
things areas                      # list areas
things tags                       # list tags

things show 3                     # show task #3 from the last list
things show "Pay rent"            # show by title (interactive disambig)

things add "Buy milk" --when today --tags errand,shopping
things add "Ship v2" --project "Launch" --deadline 2026-04-30
things complete 3
things cancel "Old idea"
things search migrate
```

By default output is plain text formatted for humans. Pass `-j` / `--json` for
structured JSON suitable for piping into `jq` or another tool. List commands
cache the resulting UUIDs so you can refer to tasks by their index (`1`, `2`,
`3`, …) in follow-up commands like `show`, `complete` and `cancel`.

### Flags

```
  -j, --json          output JSON instead of plain text
      --db PATH       override the Things3 database path
  -p, --project NAME  filter list by project name or UUID
  -a, --area NAME     filter list by area name or UUID
  -t, --tag NAME      filter list by tag name
```

`add` accepts `--notes`, `--when`, `--deadline`, `--tags`, `--checklist`,
`--project`, `--heading` and `--list`. `--when` takes the same values Things
itself accepts (`today`, `tomorrow`, `evening`, `anytime`, `someday`, or a
date).

## How it works

- **Reads** go through `modernc.org/sqlite` (pure Go, no cgo) with
  `PRAGMA query_only = ON`, so the CLI cannot mutate the Things database.
- **Writes** go through the official `things:///add` URL scheme for creating
  tasks and through AppleScript for completing and cancelling them. This is
  the same interface Things exposes to Shortcuts and automation tools.
- **Task resolution** accepts a UUID, a title (with interactive
  disambiguation when multiple tasks match) or a numeric index into the last
  listing.

## Install

Install the latest release with `go install`:

```sh
go install github.com/ryanlewis/things-cli/cmd/things@latest
```

Or build from source:

```sh
make build          # produces ./things
# or
go build -o things ./cmd/things
```

Requires macOS with Things3 installed. Go 1.26 or later.

## Project structure

```
cmd/things/             CLI entry point (alecthomas/kong)
internal/model/         Shared types + date codecs (ThingsDate, Core Data time)
internal/db/            SQLite queries, read-only
internal/things/        URL scheme + AppleScript writers
internal/output/        JSON and plain-text rendering
internal/cache/         Last-list UUID cache for numeric references
```

## License

MIT
