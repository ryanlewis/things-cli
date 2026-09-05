---
title: Commands
icon: fas fa-list
order: 2
---

Read commands (`list`/views, `projects`, `areas`, `tags`, `show`, `search`)
accept `-j` / `--json` for structured output. Run `things --help` or
`things <subcommand> --help` for the full flag list.

## Listing

```sh
things                 # today's tasks (default view)
things list <view>     # explicit form — see views below
things <view>          # shortcut: things inbox, things today, etc.
```

Available views: `today`, `inbox`, `upcoming`, `anytime`, `someday`,
`repeating`, `logbook`, `trash`, `deadlines`.

`repeating` lists repeating to-do and project templates. The items a template
generates are ordinary to-dos and projects and appear in `today`, `upcoming`,
`things projects` and the rest; the template itself appears only here — plus
`trash` or `logbook` for a to-do template that ends up there, since those two
report what the database holds. Both are to-do lists, so a project template
never shows in them. Project templates are marked `(project)` in plain output
and carry `"type": 1` in JSON.

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

`things show <ref> --agent` prints a self-contained Markdown brief rather
than the aligned detail view: title, UUID, status, project/area, tags,
schedule, notes, checklist, and a "Closing out" section with the commands
that act on the item. Those commands all name the UUID, because a title can
match several to-dos and a numeric index only holds until the next listing.

```sh
things show 3 --agent | claude -p "action this"
claude "$(things show 3 --agent)"
```

Point it at a project and the brief lists the project's open to-dos with
their UUIDs, and its closing commands carry `--yes` — a project-wide
`complete`/`cancel` asks for confirmation, and it changes every to-do under
the project. `--agent` cannot be combined with `--json`; a config file that
sets `json = true` is only a default, so the explicit flag still wins.

Notes are reproduced inside a fence wide enough that nothing in them can
close it, so a note cannot forge the closing-out section the agent acts on.

A plain listing from `things list` or `things search`, printed to a terminal,
ends with a pointer to this:

```text
hint: things show <n> --agent hands a to-do to an agent (disable with hints = false in the config file)
```

It is suppressed under `--json`, when stdout is not a terminal, for an empty
listing, and by `--no-hints` or `hints = false` in the config file.

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

The CLI ships a neutral, agent-readable description of itself.

```sh
things skill install claude    # writes to ~/.claude/skills/things-cli/
things skill install codex     # writes to ~/.codex/skills/things-cli/
things skill install pi        # writes to ~/.pi/agent/skills/things-cli/
things skill list              # show install status across agents
things skill show              # print the neutral source to stdout
things skill show claude       # print the rendered output for one agent
things skill uninstall claude  # remove the installed copy
```

The skill is bundled into the binary, so a plain `things` upgrade
refreshes every installed copy on next `install`.

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

See [Configuration]({{ site.baseurl }}/configuration/) for the full key
table, an annotated example file, and what happens when the file is
wrong. The three `config` subcommands keep working against a file the
CLI cannot use — they are how you find out what is wrong with it.

## Caching

`things` caches the last list it printed in
`$HOME/Library/Caches/things-cli/last-list` so that numeric indices
(`things show 3`) work across invocations. Clear it by deleting that
file or by running any list command, which overwrites it.
