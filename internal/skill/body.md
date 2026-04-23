# things-cli — Things3 CLI for macOS

Use the `things` CLI whenever the user mentions Things3, tasks, todos, inbox,
today, upcoming, projects, or areas on macOS. The binary reads the local
Things3 SQLite database and writes via the `things:///` URL scheme /
AppleScript.

## Safety model

- **Read operations hit SQLite read-only** (`PRAGMA query_only = ON`). These
  are safe and fast: `list`, `show`, `projects`, `areas`, `tags`, `search`.
- **Write operations go through Things3**: `add`, `project add`, `edit`,
  `complete`, `cancel`, `log`, `open`. These launch URL schemes or AppleScript
  and affect the user's real data — confirm before running destructive writes
  (`complete`, `cancel`, bulk `edit`).

## Output

Most commands accept `--json` / `-j` for machine-readable output. Default
output is a human-friendly plain text table. Prefer `--json` when piping into
another tool or parsing in an agent context.

## Core commands

```
things list [view] [--project P] [--area A] [--tag T]
    # views: today, inbox, upcoming, anytime, someday, logbook, trash, deadlines
    # shortcut: `things today`, `things inbox`, etc.

things show <task>              # task detail (title/UUID/numeric index from last list)
things projects [--area A] [--completed]
things areas
things tags
things search <query>

things add <title> [--notes --when --deadline --tags --checklist --project --heading --list]
things project add <title> [--notes --when --deadline --tags --area --todos]
things edit <task> [--title --notes --when --deadline --tags --add-tags --list --heading --complete --cancel --duplicate --reveal ...]
things complete <task>
things cancel <task>
things log                      # move Today → Logbook
things open <ref|list>          # reveal task/project/area/tag/built-in list in the app
```

### Task reference forms

`<task>` accepts:

- UUID (e.g. `A1B2C3D4-...`)
- Numeric index from the last list (1-based) — `things list today; things complete 2`
- Title substring — interactive prompt disambiguates multiple matches; non-TTY context errors with the match list.

### Multi-line values

Newline-separated fields (`--checklist`, `--todos`, `--checklist`/`--prepend-checklist`/`--append-checklist` on edit) accept the literal two-character escape `\n` to pack multi-line values into a single shell-quoted argument:

```
things add "Groceries" --checklist "Milk\nBread\nEggs"
```

## Common flows

Show today and complete the 3rd item:

```
things list today
things complete 3
```

Add a task into a project with a checklist, tagged:

```
things add "Ship release" --project "things-cli" --tags "oss" \
  --checklist "Cut tag\nWait on CI\nAnnounce"
```

Edit a task: reschedule + add a tag:

```
things edit "Ship release" --when tomorrow --add-tags "priority"
```

Pipe JSON to another tool:

```
things --json list today | jq '.[] | .title'
```

## Tips for agents

- Prefer `--json` in scripted contexts; parse rather than screen-scrape.
- After listing, the task order is cached — numeric indices stay valid until the next `list`/`search`.
- `things open` is the right command when the user wants to *see* something in the Things3 app rather than read data.
- Writes are not transactional: if a URL-scheme write fails, the DB is unchanged but the agent should re-check state with `show`.
