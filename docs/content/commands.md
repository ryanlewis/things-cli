---
title: Commands
url: /commands/
weight: 2
eyebrow: Commands
description: "Full command reference for things-cli: listing, searching, capturing, editing, completing and the bundled agent skill."
---

Read commands (`list`/views, `projects`, `areas`, `tags`, `show`, `search`)
accept `-j` / `--json` for structured output. Run `things --help` or
`things <subcommand> --help` for the full flag list.

In JSON, `status` and `type` are string enums rather than the raw Things
integers. `status` is `"open"`, `"completed"` or `"cancelled"`, and appears
on to-dos, projects and checklist items. `type` is `"todo"` or `"project"`,
and appears on task rows only — `projects`, `areas` and `tags` rows carry no
`type`. Headings are never returned by any command, so the third Things type
never reaches the output. In v0.7.0 and earlier `type` was the integer `0`,
`1` or `2`, so a caller matching on `.type==1` has to become
`.type=="project"`.

The `type` values a listing reports are not the ones an `import` payload
takes: that payload is Things' own JSON URL scheme, which spells a to-do
`"to-do"`. Do not copy `.type` from a listing into an import item.

## Listing

```sh
things                 # today's tasks (default view)
things list <view>     # explicit form — see views below
things <view>          # shortcut: things inbox, things today, etc.
```

Available views: `today`, `inbox`, `upcoming`, `anytime`, `someday`,
`repeating`, `logbook`, `trash`, `deadlines`.

`today`, `upcoming`, `anytime`, `someday`, `logbook` and `trash` list projects
as well as to-dos, matching what Things shows in those lists — a project is
scheduled the same way a to-do is, so a project put in Today is a row in
Today, one deferred to Someday is a row in Someday, a completed project is a
row in the Logbook under its completion date, and a trashed one is a row in
Trash. Project rows are marked `(project)` in plain output and carry `"type":
"project"` in JSON, so `jq 'select(.type=="project")'` picks them out. A
project has no parent project, so `--project` never matches one; `--area`
does, because a project carries its own area. `repeating` carries project
templates too, for the reason below. `inbox` and `deadlines` stay to-do only.

`repeating` lists repeating to-do and project templates. The items a template
generates are ordinary to-dos and projects and appear in `today`, `upcoming`,
`things projects` and the rest; the template itself appears only here — plus
`trash` or `logbook` for a template that ends up there, since those two report
what the database holds. Both carry projects, so a trashed or logged project
template shows there.
Project templates are marked `(project)` in plain output and carry
`"type": "project"` in JSON.

The to-dos inside a project template are hidden along with it, since they
would otherwise list against a project `things projects` does not report.
`trash` and `logbook` still show them once they are trashed or logged.

`things search` is a lookup rather than a view, so it returns templates too.
Results carry `"repeating": true`.

Filter any list with `-p/--project`, `-a/--area`, or `-t/--tag`. On their
own the filters cover every open task in the project, area, or tag; add a
view and the filter applies within it, with the view named in the output:

```sh
things -p "Launch v2"                # every open task in the project
things today -p "Launch v2"          # today's slice of it, labelled "view: today"
things upcoming -t urgent
things anytime --area "Side projects"
things --json list today | jq '.[] | .title'
```

Tasks filed under a project heading belong to that project, so they appear
under `-p` and under the project's area.

`things projects`, `things areas`, and `things tags` list the
collections themselves. `things projects` accepts `--area` and
`--completed`.

Projects are scheduled the same way to-dos are, and `things projects -j`
reports that with the same field names and encodings: `start`,
`startBucket`, `startDate` and `deadline`. A caller can tell a scheduled
project from an anytime one without a `things show` per project.

`things projects -j` also reports two counts per project. `taskCount` is
every untrashed to-do in the project; `openCount` is the ones still open.
The difference is the ones no longer open, which means completed or
cancelled. To-dos filed under a project heading count towards both; the
heading rows themselves never do, and neither do trashed to-dos or
checklist items. Both numbers are Things' own bookkeeping, read straight
from the database rather than recounted by the CLI.

That makes it one call to find projects whose work has landed but which
are still open:

```sh
things projects -j | jq '.[] | select(.openCount == 0 and .taskCount > 0)'
```

`taskCount > 0` keeps out empty projects, which have nothing done rather
than everything done. It does not tell done from cancelled: a project
whose to-dos were all cancelled matches the same filter. Plain output
marks the same projects with a filled `●` progress icon, and under
`--completed` that icon also marks every completed project, including
empty ones. A project holding a repeating to-do never appears while the
repeat is live: Things counts the hidden template row itself as an open
to-do, and a template never completes. `things list -p <project>` hides
that template, so it can report no open to-dos for a project whose
`openCount` is 1.

## Inspecting a task

```sh
things show 3                 # by index from the last list
things show <uuid>            # by Things3 UUID
things show "Buy milk"        # by title (interactive disambiguation)
things show 3 --agent         # Markdown brief for handing to an agent
```

After any list or `search`, numeric indices stay valid until the next
one.

## Handing a to-do to an agent

`things show <ref> --agent` prints a Markdown brief written for an agent,
with the commands that act on the item. See
[Working with agents](/agents/).

## Searching

```sh
things search "milk"
things search "release" --json
```

## Capturing

```sh
things add "Buy milk"
things add "Ship the thing" --when today --tags work,urgent
things add "Pay invoice" --deadline 2026-06-01 --notes "Send PDF"
things add "Review PR" --project "things-cli"
things add "Plan offsite" --list "Open source"   # --list takes a project or area; it overrides --project if both are given
things add "Groceries" --checklist "Milk\nBread\nEggs"
```

`--when` accepts a keyword (`today`, `tomorrow`, `evening`, `anytime`,
`someday`), a date `YYYY-MM-DD`, a time `HH:MM`, a date+time
`YYYY-MM-DD@HH:MM`, or an RFC3339 timestamp. `--deadline` accepts a
`YYYY-MM-DD` date only.

`things project add` creates a new project with the same flag set
(`--notes`, `--when`, `--deadline`, `--tags`, `--area`, `--todos`).

## Editing

```sh
things edit 3 --title "Buy oat milk"
things edit 3 --tags shopping            # replace all tags
things edit 3 --add-tags urgent          # additive
things edit 3 --deadline 2026-05-15
things edit 3 --when tomorrow
things edit 3 --notes "From Holland & Barrett"
things edit 3 --append-checklist "Almond too"
things edit 3 --complete                 # also: --cancel, --duplicate, --reveal
```

`edit` is for to-dos only. A reference that resolves to a project is refused
before anything is written, because `things:///update` cannot address one —
use `things project edit` instead. `project edit` refuses a to-do the same
way, pointing back at `things edit`.

`things project edit` takes most of the same flags (`--title`, `--notes`,
`--prepend-notes`/`--append-notes`, `--when`, `--deadline`, `--tags`,
`--add-tags`, `--complete`, `--cancel`, `--duplicate`, `--reveal`) plus
`--area`/`--area-id` to move the project. It has no checklist or
heading flags.

## Tags must already exist

Things applies only tags that already exist and drops the rest without
saying so. Every write that carries tags (`add`, `project add`, `edit`,
`project edit`, `import`) checks them against the database first and warns:

```sh
things add "Review the flags" --tags "Work,cifas-auto-reject"
# warning: these tags do not exist in Things and will be ignored: cifas-auto-reject
```

The write still happens. Add `--create-tags` to create the missing tags
first so the write applies them all, or `--strict-tags` to fail and write
nothing instead. The two contradict each other and are rejected together.

## Creating tags

```sh
things tag add focus "deep work"
things tag add Work                 # skipped, it already exists
things tag add focus --json         # {"created": [...], "skipped": [...]}
```

Names that already exist are skipped rather than duplicated, matched
case-insensitively as Things matches them. Creation goes through
AppleScript, so Things3 must be running; the tag list is read back
afterwards to confirm it landed, which `--no-verify` skips.

## Completing and cancelling

```sh
things complete 3
things cancel 3
things complete "Launch v2" --yes    # skip the project confirmation
```

Completing or cancelling a project also completes or cancels every task
in it, so it asks first. A run that cannot prompt — piped stdin, or
`--json` — declines instead of guessing; `--yes` (`-y`) answers the
question up front, which is how project completion works from a script.
`assume_yes = true` in the config file sets it every time, and `--yes`
still decides each run.

Both go through AppleScript so Things3 records the change in its
activity log. Task creation (`add`) and edits go through the
`things:///` URL scheme; the CLI never writes to the database directly.

## Logbook and import

```sh
things log                    # move all of Today's completed items into the Logbook
things import < payload.json  # batch create/update via the Things JSON URL scheme
```

`import` payload is the array
[documented by Cultured Code](https://culturedcode.com/things/support/articles/2803573/).

Items with `"operation": "update"` go through the same repeating check as
`edit`: if any of them carries `when`, `deadline`, `completed` or `canceled`
for a repeating to-do or project, the whole import is refused before anything
is sent, and the error names every offending item. The status fields are
two-way, so `false` is refused as readily as `true`. Update items that set
`completed` or `canceled` are read back from the database afterwards, and any
that Things dropped are reported one per line with a non-zero exit.

## Opening in the app

```sh
things open today              # built-in views
things open inbox
things open <uuid>             # specific task or project
things open "Weekly Review"    # task or project by title
things open --area "Side projects"   # an area (bare titles never resolve areas)
things open --tag urgent             # a tag
```

## Agent skill

`things skill install <agent>` installs the bundled skill for Claude Code,
Codex or Pi. See [Working with agents](/agents/).

## Shell completions

`things completions <shell>` prints a completion script for `bash`, `zsh`,
or `fish`:

```sh
things completions zsh > ~/.things-completions.zsh   # then source it from ~/.zshrc
```

Homebrew-cask installs wire these up automatically.

## Version

```sh
things version       # or: things --version / things -v
```

Prints the version, commit, and build date.

## Configuration

A TOML file at `~/.config/things-cli/config.toml` supplies defaults for
the flags above, so you can set them once instead of typing them every
run. Precedence is flag > config file > built-in default.

```sh
things config init     # write a commented template
things config path     # the file in use, and whether it exists
things config show     # the defaults it establishes
```

See [Configuration](/configuration/) for the full key
table, an annotated example file, and what happens when the file is
wrong. The three `config` subcommands keep working against a file the
CLI cannot use — they are how you find out what is wrong with it.

## Caching

`things` caches the last list it printed in
`$HOME/Library/Caches/things-cli/last-list` so that numeric indices
(`things show 3`) work across invocations. Clear it by deleting that
file or by running any list command, which overwrites it.
