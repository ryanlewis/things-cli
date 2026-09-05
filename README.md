# things-cli

[Website](https://things.rlew.io) ·
[Releases](https://github.com/ryanlewis/things-cli/releases/latest) ·
[Issues](https://github.com/ryanlewis/things-cli/issues)

A small Go CLI for [Things3](https://culturedcode.com/things/) on macOS. Reads
tasks, projects, areas and tags straight from the Things3 SQLite database
(read-only) and writes via the `things:///` URL scheme and AppleScript — so the
app stays the source of truth and your data never leaves the machine.

```sh
things                                 # today's tasks
things inbox -j | jq                   # JSON for piping into anything
things add "Buy milk" --when today --tags errand
things edit 3 --add-tags urgent --deadline 2026-05-01
things complete "Pay rent"             # by title, with interactive disambig
things search migrate                  # full-text across titles + notes
things open --project "Launch"         # reveal in the Things app
```

What it does:

- **List & inspect** every built-in view (`today`, `inbox`, `upcoming`,
  `anytime`, `someday`, `repeating`, `logbook`, `trash`, `deadlines`) plus
  projects, areas, tags, and full-text search
- **Create** tasks and projects with notes, schedules, deadlines, tags,
  checklists, and headings
- **Edit** anything mutable via `things:///update` — only the flags you
  pass are sent, so unset fields stay untouched
- **Complete, cancel, log**, or **reveal** items in the app
- **Import** [Things JSON URL scheme](https://culturedcode.com/things/support/articles/2803573/)
  payloads in bulk
- **JSON everywhere** — every command supports `-j` / `--json` for clean
  piping into `jq`, agents, or scripts

**Teach your agent to drive it.** A bundled skill ships in the binary —
install it once and your agent knows when to reach for `things` instead
of guessing at AppleScript:

```sh
things skill install claude            # also: codex, pi
```

For other agents, `things skill show` prints the neutral source so you can
append it to whatever your agent reads for instructions (e.g. a project
`AGENTS.md`).

## CLI

By default output is plain text formatted for humans. Pass `-j` / `--json`
for structured JSON suitable for piping into `jq` or another tool. List
commands assign each result a stable index (`1`, `2`, `3`, …) you can use
in follow-up commands like `show`, `edit`, `complete`, and `cancel`.

`--json` also implies non-interactive: the CLI never prompts, so a reference
matching several tasks returns an error listing the candidates instead of
opening the picker, and `complete`/`cancel` on a project declines instead of
asking to confirm.

### Errors under `--json`

A failing command prints a single JSON object to stdout and exits non-zero, so
a consumer parsing stdout gets a structured failure either way. `error` is a
stable token — `ambiguous task`, `not found`, or `error` for anything else —
and `message` carries the same text plain-text mode prints.

```console
$ things show milk --json; echo "exit=$?"
{
  "error": "ambiguous task",
  "message": "ambiguous task \"milk\" — matches 2 tasks: ...",
  "kind": "task",
  "query": "milk",
  "matches": [
    { "uuid": "A1B2...", "title": "Buy milk", "project": "Chores" },
    { "uuid": "C3D4...", "title": "Buy oat milk" }
  ]
}
exit=1
```

Retry with one of the `matches[].uuid` values. Argument and flag errors take
the same route, so `--json` never leaves a usage block on stdout. Without
`--json`, errors stay a plain `Error: ...` line on stderr, unchanged.

### Global flags

| Flag | Description | Default |
| --- | --- | --- |
| `-j, --json` | Output as JSON instead of plain text | `false` |
| `--color MODE` | Colour output: `auto`, `always`, or `never` | `auto` |
| `--db PATH` | Override the Things3 SQLite database path | auto-detected |
| `--config PATH` | Read defaults from this config file instead of the default location | see [Configuration](#configuration) |
| `--no-verify` | Skip the read-back that confirms a `complete`/`cancel` actually landed | `false` |
| `-v, --version` | Print version, commit, and build date and exit (same as `things version`) | — |

### Configuration

A TOML file supplies defaults for the flags below, so you can set them once
instead of typing them every time. Precedence is flag > config file >
built-in default.

The file is read from `$XDG_CONFIG_HOME/things-cli/config.toml`, falling
back to `~/.config/things-cli/config.toml`. Override the location with
`--config PATH` or the `THINGS_CLI_CONFIG` environment variable. A missing
file is not an error — `things` runs on its built-in defaults.

```sh
things config init     # write a commented template (refuses to overwrite; --force to replace)
things config path     # print the file in use and whether it exists
things config show     # print the defaults the file establishes
```

Keys are the flag name in snake_case. The hyphenated spelling
(`no-verify`) is accepted too, but `things config init` writes the
snake_case form.

| Key | Flag it sets | Type | Default |
| --- | --- | --- | --- |
| `json` | `--json` | boolean | `false` |
| `color` | `--color` | `"auto"`, `"always"`, `"never"` | `"auto"` |
| `db` | `--db` | string path (must exist) | auto-detected |
| `no_verify` | `--no-verify` | boolean | `false` |
| `strict_tags` | `--strict-tags` | boolean | `false` |

```toml
color = "always"
strict_tags = true
```

An unknown key, a value of the wrong type, or malformed TOML is an error
that names the file and the key. Anything set here still loses to a flag,
so `things --color never today` wins over `color = "always"`.

Note for scripts and agents: `json = true` changes the output of every
command. Pass `--json` (or `--json=false`) explicitly rather than relying
on whatever the config file happens to say.

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
| `repeating` | Repeating to-do templates |
| `logbook` | Completed tasks |
| `trash` | Trashed tasks |
| `deadlines` | Tasks with a deadline |

Filters:

| Flag | Description |
| --- | --- |
| `-p, --project NAME` | Filter by project name or UUID |
| `-a, --area NAME` | Filter by area name or UUID |
| `-t, --tag NAME` | Filter by tag name |
| `--on DATE` | Only tasks scheduled on `YYYY-MM-DD` (or RFC3339); on `deadlines`, filters by deadline |
| `--from DATE` | Only tasks scheduled on or after the date |
| `--to DATE` | Only tasks scheduled on or before the date |
| `--include-completed` | On `today` only: also show completed/cancelled items Things hasn't logged out of Today yet |

`-p`/`-a`/`-t` name what to list, so on their own they cover every open
task in the project, area, or tag — not just the ones scheduled for today.
Name a view as well and the filter applies within that view, and the
listing says which view it drew from:

```sh
things -p "Launch v2"                # every open task in the project
things today -p "Launch v2"          # today's slice of it, labelled "view: today"
things "Launch v2"                   # same as -p, project name as an argument
```

Tasks filed under a project heading count as part of the project, so they
show up under `-p` and under the project's `-a` area, and carry the project
name in `show` and JSON output.

A repeating to-do is stored as a template plus the to-dos it generates. Only
the template appears in `things repeating`; the generated to-dos are ordinary
tasks and show up in `today`, `upcoming` and the rest as usual. Templates are
kept out of every other view except `trash` and `logbook`, which report what
the database holds.

The date filters (`--on`, `--from`, `--to`) apply to date-filterable views —
`today`, `upcoming`, `anytime`, `deadlines`, and project listings (`someday`
items have no start date, so they can't be date-filtered, and neither can
`repeating` templates) — and `--on` can't be combined with `--from`/`--to`.
`--include-completed` applies to the `today` view only, so with a filter it
needs the view spelled out: `things today -p "Launch v2" --include-completed`.

Examples:

```sh
things                            # today (default)
things inbox
things upcoming -t urgent
things "Weekly Review"            # tasks in a project by name
things -a Work                    # tasks in an area
```

Output groups by project or area; numeric indices are stable for follow-up
commands until the next listing:

```text
$ things
    Launch v2
1.  [ ]  Draft release notes      [docs]      today
2.  [ ]  Cut RC build                          due:2026-04-30

    Errands
3.  [ ]  Buy milk                 [shopping]
4.  [x]  Pick up dry cleaning
```

### Inspecting tasks, projects, areas, tags

| Command | Description |
| --- | --- |
| `things show <task>` | Show a task's detail (with checklist) |
| `things projects [-a NAME] [--completed]` | List projects |
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

```text
$ things show 2
Title:    Cut RC build
UUID:     8K3FpQ2eRtNbHwpNiM71Eu
Status:   Open
Project:  Launch v2
Tags:     release
Deadline: 2026-04-30
Created:  2026-04-12 09:14
Notes:
  Coordinate with marketing before tagging.
Checklist:
  [x] Bump version
  [ ] Update changelog
  [ ] Tag and push
```

`things show` prints a `Repeats:` line for repeating to-dos and projects, and
JSON output carries `"repeating": true` for them (the field is omitted
otherwise). Things blocks several kinds of edit on those — see the note under
[Editing](#editing-tasks-and-projects).

`things projects` renders a one-line-per-project list; the leading glyph
shows completion progress (`○` empty, `◔ ◑ ◕` partial, `●` done, `◌`
cancelled):

```text
$ things projects
◑  Launch v2          Work
◔  Migrate API        Work
○  Garden plan        Home
●  Spring cleaning    Home
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
| `--strict-tags` | ✓ | ✓ | Fail instead of writing when a tag does not exist (see [Tags must already exist](#tags-must-already-exist)) |
| `--create-tags` | ✓ | ✓ | Create tags that do not exist before writing (see [Tags must already exist](#tags-must-already-exist)) |

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

> **Prerequisite:** `edit`, `project edit`, and `import` payloads with
> `operation: update` require the Things auth token. Enable it once via
> *Things → Settings → General → Enable Things URLs*. Without it, writes fail
> with `update: auth token is required — enable Things URLs in Things → Settings → General …`.

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
| `--complete` | ✓ | ✓ | Mark as completed (not with `--cancel`) |
| `--cancel` | ✓ | ✓ | Mark as canceled (not with `--complete`) |
| `--duplicate` | ✓ | ✓ | Duplicate before applying edits |
| `--reveal` | ✓ | ✓ | Reveal in Things after editing |
| `--strict-tags` | ✓ | ✓ | Fail instead of writing when a tag does not exist (see [Tags must already exist](#tags-must-already-exist)) |
| `--create-tags` | ✓ | ✓ | Create tags that do not exist before writing (see [Tags must already exist](#tags-must-already-exist)) |

> **Repeating items:** Things refuses `--when`, `--deadline`, `--complete`,
> `--cancel`, and `--duplicate` on repeating to-dos and projects, and drops the
> request silently instead of reporting an error
> ([docs](https://culturedcode.com/things/support/articles/2803573/)). The CLI
> checks first and exits non-zero with an explanation. Every other flag works
> normally; the restricted changes have to be made in the Things app. `import`
> makes the same check per `operation: update` item — see [Repeating items in an
> import payload](#repeating-items-in-an-import-payload).

Examples:

```sh
things edit 3 --title "New title" --when tomorrow
things edit "Buy milk" --add-tags urgent --deadline 2026-05-01
things edit "Old idea" --deadline ""              # clear the deadline
things project edit "Launch" --append-notes "Beta cut on Friday"
```

### Tags must already exist

Things applies only tags that already exist. A tag it doesn't recognise is
dropped silently — the task is still created or updated, minus the tag, and
the command exits 0. The URL scheme cannot create tags, so the CLI creates
them over AppleScript instead.

To stop the drop being invisible, every write that carries tags (`add`,
`project add`, `edit`, `project edit`, `import`) first checks them against
the Things database and warns on stderr about any it cannot find:

```
$ things add "Review the flags" --tags "Work,cifas-auto-reject"
warning: these tags do not exist in Things and will be ignored: cifas-auto-reject
warning: Things only applies tags that already exist — create them with --create-tags or `things tag add`, or use --strict-tags to fail instead of dropping them
```

The write still goes ahead. Pass `--create-tags` to create the missing tags
first, so the write applies all of them:

```
$ things add "Review the flags" --tags "Work,cifas-auto-reject" --create-tags
created in Things: cifas-auto-reject
```

Or pass `--strict-tags` to refuse the write instead:

```
$ things add "Review the flags" --tags "Work,cifas-auto-reject" --strict-tags
Error: these tags do not exist in Things: cifas-auto-reject — create them in Things first, run `things tag add cifas-auto-reject`, or drop --strict-tags to write anyway
```

Nothing is written in that case, and the exit status is non-zero. The two
flags contradict each other and are rejected together. Tag names are matched
case-insensitively, the way Things treats them. If the database can't be read,
`add` and `project add` skip the check with a warning — unless `--strict-tags`
or `--create-tags` is set, either of which then fails rather than write
unchecked. `edit`, `project edit` and `import` need the database for other
reasons and fail either way.

### Creating tags

`things tag add` creates tags without writing a task:

```
$ things tag add focus "deep work" Work
created: focus, deep work
already exists: Work
```

Names that already exist are skipped rather than duplicated, matched
case-insensitively. Creation goes through AppleScript, so Things3 must be
running. The command reads the tag list back afterwards to confirm the tags
landed; `--no-verify` skips that. `--json` reports the two lists as
`{"created": [...], "skipped": [...]}`.

### Completing, cancelling, logging

| Command | Description |
| --- | --- |
| `things complete <task>` | Mark a task or project as completed (project completion is confirmed interactively) |
| `things cancel <task>` | Cancel a task or project (project cancellation is confirmed interactively) |
| `things log` | Move today's done/cancelled items to the Logbook (Items → Log Completed) |

`log` is the housekeeping action; `logbook` (above) is the *view* of
already-archived tasks.

```sh
things complete 3
things cancel "Old idea"
things log
```

After a `complete` or `cancel` the CLI reads the item back from the database
and exits non-zero if the status did not change. Things has no callback for
writes, so without this check a request it silently dropped is
indistinguishable from one it applied. Repeating items are refused up front
(see the note under [Editing](#editing-tasks-and-projects)); the read-back
catches anything else — for example Things not running. `import` does both, per
item — see [Reading back an import's status
changes](#reading-back-an-imports-status-changes).

```text
$ things cancel W1gBDJPFpwUQrdP5Am5K7J
Error: status change did not apply: "Renew insurance" (W1gBDJPFpwUQrdP5Am5K7J) is still open after 10s. Things accepted the command and then dropped it silently — check that Things3 is running, or make the change in the app
$ echo $?
1
```

Pass `--no-verify` to skip the check when you do not want to wait for it. It
skips read-backs only; the up-front refusal of a restricted change on a
repeating item is not affected.

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
| `--strict-tags` | Fail instead of importing when a tag in the payload does not exist (see [Tags must already exist](#tags-must-already-exist)) |
| `--create-tags` | Create tags in the payload that do not exist before importing (see [Tags must already exist](#tags-must-already-exist)) |

```sh
things import < payload.json
things import --file payload.json --reveal
```

Note: macOS `open` has a URL length limit; split very large payloads.

#### Repeating items in an import payload

An `operation: update` item goes through the same repeating check as `edit`. If any item in the payload carries `when`, `deadline`, `completed` or `canceled` for a repeating to-do or project, the whole import is refused and nothing is sent. The value does not matter: the URL scheme documents both status fields as two-way ("Complete a to-do or set a to-do to incomplete") and says of each that it "cannot be updated on repeating to-dos", so `"completed": false` is refused exactly like `"completed": true`.

```text
$ things import --file reschedule.json
Error: 2 of 3 update items change attributes Things does not allow on repeating items, and drops the request silently (https://culturedcode.com/things/support/articles/2803573/). Nothing was sent to Things — fix these and run the import again, or make the changes in the Things app:
  [1] (id abc…): "Water plants" is a repeating to-do — when, deadline
  [2].attributes.items[0] (id def…): "Take the bins out" is a repeating to-do — canceled
```

The refusal is all-or-nothing because the URL scheme takes one payload and gives no per-item result: there is no way to send the rest and report what was skipped. Each offending item is named by its position in the payload (nested items included), its id, its title and the blocked attributes, so a payload can be fixed in one pass. `--no-verify` does not lift the refusal — it is a documented restriction, not a read-back.

Update items whose `id` is not in the database get a stderr warning; Things reports those itself, and the import still goes ahead.

#### Reading back an import's status changes

After the payload is sent, every update item that set `completed` or `canceled` is re-read from the database, for the same reason `complete` and `cancel` are (below). That includes setting either to `false`, which asks Things to mark the item incomplete — a reopen it drops is as invisible as a completion it drops. `canceled` takes priority over `completed` when a payload sets both, so the read-back expects what Things actually applies. Every item is checked before anything is reported, and the whole batch shares one timeout budget rather than one per item:

```text
$ things import --file finish.json
Error: 1 of 2 requested status changes did not apply. The rest of the import was still applied; re-run the import with only the failed items, or make the changes in the Things app:
  [1]: status change did not apply: "File taxes" (one-2) is still open after 10s. Things accepted the command and then dropped it silently — check that Things3 is running, or make the change in the app
$ echo $?
1
```

Unlike the refusal above, this happens after the write: the items that did land stay landed. `--no-verify` skips it.

Both the refusal and the read-back failure carry their per-item detail in the error itself rather than on a separate stream, so under `--json` the whole thing arrives in the `message` field of the [error object](#errors-under---json).

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

### Shell completions

`things completions <bash|zsh|fish>` prints a completion script for the named
shell. The script delegates back to `things` at completion time, so it never
goes stale as the command surface changes — `things <TAB>` completes
subcommands and flag names, and a flag's values complete once you've typed it
(`things list --color <TAB>` → `auto`, `always`, `never`).

The Homebrew cask generates these on install, so cask users get `things <TAB>`
with no extra steps. On every other install path, load the script yourself.
Completion shells out to `things` by name, so it works as long as `things` is on
your `PATH` (the Homebrew, `go install`, and `make install` paths all put it
there):

```sh
# bash — add to ~/.bashrc (complete -C is a bash builtin; no extra package needed)
source <(things completions bash)

# zsh — add to ~/.zshrc, after compinit runs (the stub's bashcompinit needs compdef)
source <(things completions zsh)

# fish — load now, and/or persist for new shells
things completions fish | source
mkdir -p ~/.config/fish/completions
things completions fish > ~/.config/fish/completions/things.fish
```

Completion runs entirely from the static command tree — it never reads the
Things database, so project, area, and tag *names* are not (yet) completed.

## Agent skill

`things-cli` bundles an agent skill that teaches Claude Code, OpenAI's Codex
CLI, the Pi coding agent, and other compatible agents how to drive the CLI.
Install it once and the agent will know when to reach for `things` instead
of guessing.

| Command | Description |
| --- | --- |
| `things skill list` | Show supported agents and install status |
| `things skill install <agent>` | Install the skill for an agent (`claude`, `codex`, `pi`) |
| `things skill uninstall <agent>` | Remove the installed skill |
| `things skill show` | Print the neutral skill source |
| `things skill show <agent>` | Print the files that would be installed for that agent |

Default install paths:

| Agent | Path |
| --- | --- |
| `claude` | `~/.claude/skills/things-cli/` |
| `codex` | `~/.codex/skills/things-cli/` |
| `pi` | `~/.pi/agent/skills/things-cli/` |

`install` and `uninstall` accept:

| Flag | Description |
| --- | --- |
| `--path DIR` | Install or uninstall under a custom directory (e.g. project-local `.claude/skills/` or `.agents/skills/`) |
| `-y, --yes` | Skip the overwrite/removal prompt |

The skill body is [`internal/skill/SKILL.md`](internal/skill/SKILL.md),
embedded in the binary — so a plain `things` upgrade refreshes it; re-run
`skill install` to pick up the new version.

## How it works

- **Reads** go through `modernc.org/sqlite` (pure Go, no cgo) with
  `PRAGMA query_only = ON`, so the CLI cannot mutate the Things database.
- **Writes** go through the official `things:///add` and `things:///update`
  URL schemes for creating and editing tasks, and through AppleScript for
  completing and cancelling them. This is the same interface Things exposes
  to Shortcuts and automation tools.
- **Write confirmation**: `complete` and `cancel` poll the database after
  writing until the status changes, up to 10 seconds, and fail if it never
  does. Repeating items — which Things refuses to complete, cancel,
  reschedule, or duplicate — are detected from the recurrence rule in the
  database and rejected before any write is issued.
- **Task resolution** accepts a UUID, a title (with interactive
  disambiguation when multiple tasks match) or a numeric index into the last
  listing.

## Install

With Homebrew:

```sh
brew install ryanlewis/tap/things
```

Or one-line install (downloads the latest release, verifies checksums,
installs to `/usr/local/bin`):

```sh
curl -fsSL https://raw.githubusercontent.com/ryanlewis/things-cli/main/install.sh | sh
```

Override the destination with `INSTALL_DIR` or pin a version with `VERSION`:

```sh
curl -fsSL https://raw.githubusercontent.com/ryanlewis/things-cli/main/install.sh \
  | INSTALL_DIR="$HOME/bin" VERSION=v0.1.0 sh
```

When the [GitHub CLI](https://cli.github.com) is installed and logged in, the
installer also verifies [build provenance](https://docs.github.com/en/actions/security-for-github-actions/using-artifact-attestations) —
cryptographic proof that the tarball was built by this repository's release
workflow. You can check any release artifact yourself:

```sh
gh attestation verify things_<version>_darwin_arm64.tar.gz -R ryanlewis/things-cli
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
