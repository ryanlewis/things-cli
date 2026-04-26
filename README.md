# things-cli

A small Go CLI for [Things3](https://culturedcode.com/things/) on macOS. Reads
tasks, projects, areas and tags straight from the Things3 SQLite database
(read-only) and writes via the `things:///` URL scheme and AppleScript — so the
app stays the source of truth and your data never leaves the machine.

**AI-friendly by design.** Every command speaks JSON (`-j` / `--json`) for
clean piping into `jq`, agents, or scripts. A bundled agent skill ships in
the binary itself — `things skill install claude` drops it into Claude
Code, and `things skill show` prints the neutral source so you can append
it to whatever your agent reads for instructions (e.g. a project
`AGENTS.md` for Codex). Install once and your agent knows when to reach
for `things` instead of guessing at AppleScript.

## CLI

By default output is plain text formatted for humans. Pass `-j` / `--json`
for structured JSON suitable for piping into `jq` or another tool. List
commands assign each result a stable index (`1`, `2`, `3`, …) you can use
in follow-up commands like `show`, `edit`, `complete`, and `cancel`.

### Global flags

| Flag | Description | Default |
| --- | --- | --- |
| `-j, --json` | Output as JSON instead of plain text | `false` |
| `--db PATH` | Override the Things3 SQLite database path | auto-detected |
| `-v, --version` | Print version, commit, and build date and exit (same as `things version`) | — |

### Listing tasks

`things <view>` prints a built-in list. With no arguments, `things` prints
`today`. View names take precedence over project names — a non-view
argument is treated as a project name (`things "Weekly Review"`), so a
project literally called `Inbox` would need `things -p Inbox`.

| View | Description |
| --- | --- |
| `today` | Tasks scheduled for today (default) |
| `inbox` | Inbox |
| `upcoming` | Scheduled tasks and deadlines |
| `anytime` | Anytime list |
| `someday` | Someday list |
| `logbook` | Completed tasks |
| `trash` | Trashed tasks |
| `deadlines` | Tasks with a deadline |

Filters (combine freely with any view):

| Flag | Description |
| --- | --- |
| `-p, --project NAME` | Filter by project name or UUID |
| `-a, --area NAME` | Filter by area name or UUID |
| `-t, --tag NAME` | Filter by tag name |

Examples:

```sh
things                            # today (default)
things inbox
things upcoming -t urgent
things "Weekly Review"            # tasks in a project by name
things -a Work                    # tasks in an area
```

### Inspecting tasks, projects, areas, tags

| Command | Description |
| --- | --- |
| `things show <task>` | Show a task's detail (with checklist) |
| `things projects [--area NAME] [--completed]` | List projects |
| `things areas` | List areas |
| `things tags` | List tags |
| `things search <query>` | Full-text search across titles and notes |

`<task>` accepts a UUID, a numeric index from the last list, or a title
substring. When a title matches multiple tasks, an interactive prompt picks
between them; non-TTY callers get the match list as an error.

```sh
things show 3                     # task #3 from the last list
things show "Pay rent"            # by title (interactive disambig)
things search migrate             # full-text search
```

### Creating tasks and projects

`things add <title>` creates a task. `things project add <title>` creates a
project.

| Flag | `add` | `project add` | Description |
| --- | --- | --- | --- |
| `--notes TEXT` | ✓ | ✓ | Free-form notes |
| `--when VALUE` | ✓ | ✓ | Schedule (see [Date values](#date-values)) |
| `--deadline DATE` | ✓ | ✓ | Deadline date |
| `--tags LIST` | ✓ | ✓ | Comma-separated tags |
| `--checklist ITEMS` | ✓ | — | Newline-separated checklist items |
| `--todos ITEMS` | — | ✓ | Newline-separated initial to-dos |
| `--project NAME` | ✓ | — | Project to add the task into |
| `--heading NAME` | ✓ | — | Heading within the project |
| `--list NAME` | ✓ | — | List (project or area) name |
| `--area NAME` | — | ✓ | Area to file the project under |

Examples:

```sh
things add "Buy milk" --when today --tags errand,shopping
things add "Ship v2" --project "Launch" --deadline 2026-04-30
things project add "Launch site" --area Work --deadline 2026-05-01
```

### Editing tasks and projects

`things edit <task>` updates a task via `things:///update`.
`things project edit <project>` updates a project via
`things:///update-project`. Only the flags you pass are sent — unset fields
stay untouched. An empty value clears the field (e.g. `--deadline ""`).
Both require the Things auth token — enable *Things → Settings → General →
Enable Things URLs*.

| Flag | `edit` | `project edit` | Description |
| --- | --- | --- | --- |
| `--title TEXT` | ✓ | ✓ | Replace title |
| `--notes TEXT` | ✓ | ✓ | Replace notes |
| `--prepend-notes TEXT` | ✓ | ✓ | Prepend to notes |
| `--append-notes TEXT` | ✓ | ✓ | Append to notes |
| `--when VALUE` | ✓ | ✓ | Reschedule (see [Date values](#date-values)) |
| `--deadline DATE` | ✓ | ✓ | Set deadline |
| `--tags LIST` | ✓ | ✓ | Replace all tags (comma-separated) |
| `--add-tags LIST` | ✓ | ✓ | Add tags without replacing existing |
| `--checklist ITEMS` | ✓ | — | Replace checklist (newline-separated) |
| `--prepend-checklist ITEMS` | ✓ | — | Prepend checklist items |
| `--append-checklist ITEMS` | ✓ | — | Append checklist items |
| `--list NAME` | ✓ | — | Move to list/project by name |
| `--list-id UUID` | ✓ | — | Move to list/project by UUID |
| `--heading NAME` | ✓ | — | Set heading within project by name |
| `--heading-id UUID` | ✓ | — | Set heading within project by UUID |
| `--area NAME` | — | ✓ | Move project to area by name |
| `--area-id UUID` | — | ✓ | Move project to area by UUID |
| `--complete` | ✓ | ✓ | Mark as completed |
| `--cancel` | ✓ | ✓ | Mark as canceled |
| `--duplicate` | ✓ | ✓ | Duplicate before applying edits |
| `--reveal` | ✓ | ✓ | Reveal in Things after editing |

Examples:

```sh
things edit 3 --title "New title" --when tomorrow
things edit "Buy milk" --add-tags urgent --deadline 2026-05-01
things edit "Old idea" --deadline ""              # clear the deadline
things project edit "Launch" --append-notes "Beta cut on Friday"
```

### Completing, cancelling, logging

| Command | Description |
| --- | --- |
| `things complete <task>` | Mark a task or project as completed (project completion is confirmed interactively) |
| `things cancel <task>` | Cancel a task |
| `things log` | Move today's done/cancelled items to the Logbook (Items → Log Completed) |

`log` is the housekeeping action; `logbook` (above) is the *view* of
already-archived tasks.

```sh
things complete 3
things cancel "Old idea"
things log
```

### Revealing items in Things3

`things open` brings Things3 forward and reveals a list, item, or quick-find
result. Pass exactly one of:

| Flag / Argument | Description |
| --- | --- |
| `<ref>` | Built-in list name (`today`, `inbox`, …), task UUID, numeric list index, or title |
| `-p, --project NAME` | Open a project by name or UUID |
| `-a, --area NAME` | Open an area by name or UUID |
| `-t, --tag NAME` | Open a tag by name or UUID |
| `-q, --query TEXT` | App-side quick find |

Additional flags:

| Flag | Description |
| --- | --- |
| `--filter TAGS` | Tag filter on the shown list (comma-separated) |
| `--background` | Don't bring Things to the foreground |

Examples:

```sh
things open today
things open "Pay rent"
things open --project "Launch"
things open --query staging
```

### Importing JSON payloads

`things import` forwards a [Things JSON URL scheme
payload](https://culturedcode.com/things/support/articles/2803573/) — a
batch of `to-do`, `project`, `heading`, and `checklist-item` items, each
with `operation` and `attributes`. The CLI validates the payload is
syntactically valid JSON, then forwards it verbatim. The auth token is
attached automatically (required for `operation: update` items, harmless
for create-only payloads).

| Flag | Description |
| --- | --- |
| `-f, --file PATH` | Read JSON payload from this file instead of stdin |
| `--reveal` | Reveal the first created/updated item in Things after import |

```sh
things import < payload.json
things import --file payload.json --reveal
```

Note: macOS `open` has a URL length limit; split very large payloads.

### Date values

`--when` accepts:

| Form | Example |
| --- | --- |
| Keyword | `today`, `tomorrow`, `evening`, `anytime`, `someday` |
| Date | `2026-05-01` |
| Time | `HH:MM` (`21:30`) or `H:MMam` / `H:MMpm` (`9:30PM`) |
| Date + time | `2026-05-01@09:30` |
| RFC3339 | `2026-05-01T09:30:00Z` (rewritten to `YYYY-MM-DD@HH:MM`; offset preserved as wall-clock, no conversion to local time) |
| Natural language | `friday`, `next monday` (English locales only; passed through verbatim) |

Inputs within edit distance 2 of a known keyword are rejected client-side
as likely typos (e.g. `tommorrow`, `evning`) with a "did you mean" hint.

`--deadline` accepts a `YYYY-MM-DD` date or an English natural-language
phrase. Keywords are not accepted.

## Claude Code skill

`things-cli` bundles an agent skill that teaches Claude Code (and other
compatible agents) how to drive the CLI. Install it once and Claude will
know when to reach for `things` instead of guessing.

| Command | Description |
| --- | --- |
| `things skill list` | Show supported agents and install status |
| `things skill install <agent>` | Install the skill (e.g. `claude` → `~/.claude/skills/things-cli`) |
| `things skill uninstall <agent>` | Remove the installed skill |
| `things skill show` | Print the neutral skill source |
| `things skill show <agent>` | Print the files that would be installed for that agent |

`install` and `uninstall` accept:

| Flag | Description |
| --- | --- |
| `--path DIR` | Install or uninstall under a custom directory (e.g. project-local `.claude/skills/`) |
| `-y, --yes` | Skip the overwrite/removal prompt |

The skill body is embedded in the binary, so a plain `things` upgrade
refreshes it — re-run `skill install` to pick up the new version.

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
