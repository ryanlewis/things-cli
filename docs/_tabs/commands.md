---
title: Commands
icon: fas fa-list
order: 2
---

Every subcommand accepts `-j` / `--json` for structured output. Run
`things --help` or `things <subcommand> --help` for the full flag list.

## Listing

```sh
things                 # today's tasks (default view)
things list <view>     # explicit form — see views below
things <view>          # shortcut: things inbox, things today, etc.
```

Available views: `today`, `inbox`, `upcoming`, `anytime`, `someday`,
`logbook`, `trash`, `deadlines`.

Filter any list with `-p/--project`, `-a/--area`, or `-t/--tag`:

```sh
things upcoming -t urgent
things anytime --area "Side projects"
things --json list today | jq '.[] | .title'
```

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
things add "Review PR" --project "things-cli" --list "Open source"
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

`things project edit` mirrors the same flag set for projects.

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

## Opening in the app

```sh
things open today              # built-in views
things open inbox
things open <uuid>             # specific task or project
things open "Side projects"    # area or project by name
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

## Caching

`things` caches the last list it printed in
`$HOME/Library/Caches/things-cli/last-list` so that numeric indices
(`things show 3`) work across invocations. Clear it by deleting that
file or by running any list command, which overwrites it.
