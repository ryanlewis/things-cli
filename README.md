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
things project add "Launch site" --area Work --deadline 2026-05-01
things edit 3 --title "New title" --when tomorrow
things edit "Buy milk" --add-tags urgent --deadline 2026-05-01
things complete 3
things cancel "Old idea"
things search migrate

things open today                 # reveal a built-in list in the app
things open "Pay rent"            # reveal a task by title
things open --project "Launch"    # reveal a project
things open --query staging       # app-side quick find

things log                        # move today's done/cancelled items to Logbook
things import < payload.json      # batch create/update via the Things JSON URL scheme
things import --file payload.json --reveal
things version                    # print version
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
`--project`, `--heading` and `--list`.

`--when` accepts:

- a keyword: `today`, `tomorrow`, `evening`, `anytime`, `someday`
- a date: `YYYY-MM-DD` (e.g. `2026-05-01`)
- a time: `HH:MM` or `H:MMam`/`H:MMpm` (e.g. `21:30`, `9:30PM`)
- date + time: `YYYY-MM-DD@HH:MM` (e.g. `2026-05-01@09:30`)
- RFC3339: rewritten to `YYYY-MM-DD@HH:MM` before being sent (the offset is
  preserved as wall-clock; no conversion to local time)
- an English natural-language phrase (e.g. `friday`, `next monday`) — passed
  through verbatim; works only in English locales.

Inputs within edit distance 2 of a known keyword are rejected client-side as
likely typos (e.g. `tommorrow`, `evning`), with a "did you mean" hint.

`--deadline` accepts a `YYYY-MM-DD` date or an English natural-language
phrase. Keywords are not accepted.

`project add` accepts `--notes`, `--when`, `--deadline`, `--tags`, `--area`
and `--todos` (newline-separated initial to-dos).

`import` accepts a JSON array on stdin (or via `--file`) matching the
[Things JSON URL scheme payload](https://culturedcode.com/things/support/articles/2803573/)
— a batch of `to-do`, `project`, `heading`, and `checklist-item` items, each
with `operation` and `attributes`. The CLI validates the payload is
syntactically valid JSON, then forwards it verbatim. The auth token is
attached automatically (required for `operation: update` items, harmless for
create-only payloads). Pass `--reveal` to jump to the first created item.
Note: macOS `open` has a URL length limit; split very large payloads.

`project edit` updates an existing project via the `things:///update-project`
URL scheme. Only flags you pass are sent. Supported flags: `--title`,
`--notes`, `--prepend-notes`, `--append-notes`, `--when`, `--deadline`,
`--tags` (replace), `--add-tags`, `--area` / `--area-id`, `--complete`,
`--cancel`, `--duplicate`, `--reveal`. An empty value clears the field
(e.g. `--deadline ""`). Requires the Things auth token, same as `edit`.

`edit` updates an existing task via the `things:///update` URL scheme. Only
flags you pass are sent, so unset fields stay untouched. Supported flags:
`--title`, `--notes`, `--prepend-notes`, `--append-notes`, `--when`,
`--deadline`, `--tags` (replace), `--add-tags`, `--checklist`,
`--prepend-checklist`, `--append-checklist`, `--list` / `--list-id`,
`--heading` / `--heading-id`, `--complete`, `--cancel`, `--duplicate`,
`--reveal`. An empty value clears the field (e.g. `--deadline ""`). Requires
the Things auth token — enable *Things → Settings → General → Enable Things
URLs*.

## Agent skill

`things-cli` bundles an agent skill that teaches Claude Code, OpenAI's Codex
CLI, the Pi coding agent, and other compatible agents how to drive the CLI.
Install it once and the agent will know when to reach for `things` instead
of guessing.

```sh
things skill list                # show supported agents and install status
things skill install claude      # install for Claude Code (~/.claude/skills/things-cli)
things skill install codex       # install for Codex CLI   (~/.codex/skills/things-cli)
things skill install pi          # install for Pi          (~/.pi/agent/skills/things-cli)
things skill install <agent> -y  # overwrite without prompting
things skill show                # print the neutral skill source
things skill show <agent>        # print the files that would be installed
things skill uninstall <agent>   # remove the installed skill
```

Pass `--path DIR` to install or uninstall under a custom directory (e.g. a
project-local `.claude/skills/` or `.agents/skills/`). The skill body is
[`internal/skill/SKILL.md`](internal/skill/SKILL.md), embedded in the binary
— so a plain `things` upgrade refreshes it; re-run `skill install` to pick
up the new version.

## How it works

- **Reads** go through `modernc.org/sqlite` (pure Go, no cgo) with
  `PRAGMA query_only = ON`, so the CLI cannot mutate the Things database.
- **Writes** go through the official `things:///add` and `things:///update`
  URL schemes for creating and editing tasks, and through AppleScript for
  completing and cancelling them. This is the same interface Things exposes
  to Shortcuts and automation tools.
- **Task resolution** accepts a UUID, a title (with interactive
  disambiguation when multiple tasks match) or a numeric index into the last
  listing.

## Install

One-line install (downloads the latest release, verifies checksums,
installs to `/usr/local/bin`):

```sh
curl -fsSL https://raw.githubusercontent.com/ryanlewis/things-cli/main/install.sh | sh
```

Override the destination with `INSTALL_DIR` or pin a version with `VERSION`:

```sh
curl -fsSL https://raw.githubusercontent.com/ryanlewis/things-cli/main/install.sh \
  | INSTALL_DIR="$HOME/bin" VERSION=v0.1.0 sh
```

Or download a prebuilt binary manually from the
[latest release](https://github.com/ryanlewis/things-cli/releases/latest)
(`darwin_arm64` for Apple Silicon, `darwin_amd64` for Intel):

```sh
tar -xzf things_*_darwin_arm64.tar.gz
mv things /usr/local/bin/   # or ~/bin, etc.
things version
```

Or install with `go install`:

```sh
go install github.com/ryanlewis/things-cli/cmd/things@latest
```

Or build from source:

```sh
make build          # produces ./things
# or
go build -o things ./cmd/things
```

Requires macOS with Things3 installed. Go 1.26 or later when building from
source.

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

[MIT](LICENSE)
