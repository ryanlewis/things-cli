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

`repeating` lists repeating to-do templates. The to-dos a template generates
are ordinary tasks and appear in `today`, `upcoming` and the rest; the
template itself appears only here, and in `trash` or `logbook` if it ends up
there.

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
```

After any list or `search`, numeric indices stay valid until the next
one.

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
```

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

A TOML file supplies defaults for global and per-command flags. Precedence
is flag > config file > built-in default.

```sh
things config init     # write a commented template (--force to overwrite)
things config path     # print the file in use and whether it exists
things config show     # print the defaults the file establishes
things config show -j  # the same, as JSON
```

The file is read from `$XDG_CONFIG_HOME/things-cli/config.toml`, falling
back to `~/.config/things-cli/config.toml`. Override it with `--config
PATH` or `THINGS_CLI_CONFIG`. A missing file is not an error.

| Key | Flag it sets | Type | Default |
| --- | --- | --- | --- |
| `json` | `--json` | boolean | `false` |
| `color` | `--color` | `"auto"`, `"always"`, `"never"` | `"auto"` |
| `db` | `--db` | string path (must exist) | auto-detected |
| `no_verify` | `--no-verify` | boolean | `false` |
| `strict_tags` | `--strict-tags` | boolean | `false` |
| `create_tags` | `--create-tags` | boolean | `false` |

```toml
color = "always"
strict_tags = true
```

`strict_tags` and `create_tags` are mutually exclusive; setting both to
`true` is an error.

Unknown keys, wrong types, and malformed TOML are errors that name the
file and the key.

## Caching

`things` caches the last list it printed in
`$HOME/Library/Caches/things-cli/last-list` so that numeric indices
(`things show 3`) work across invocations. Clear it by deleting that
file or by running any list command, which overwrites it.
