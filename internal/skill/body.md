# things-cli — Things3 CLI for macOS

Use the `things` CLI whenever the user mentions Things3, tasks, todos, inbox,
today, upcoming, projects, or areas on macOS.

## Safety

- Reads (`list`, `show`, `projects`, `areas`, `tags`, `search`) are safe — use freely.
- Writes (`add`, `project add`, `edit`, `complete`, `cancel`, `log`, `open`) modify the user's real data. Confirm before destructive ones (`complete`, `cancel`, bulk `edit`).

## Output

Most commands accept `--json` / `-j`. Prefer it when parsing output.

## Core commands

```
things list [view] [--project P] [--area A] [--tag T]
    # views: today, inbox, upcoming, anytime, someday, logbook, trash, deadlines
    # shortcut: `things today`, `things inbox`, etc.

things show <task>              # task detail
things projects [--area A] [--completed]
things areas
things tags
things search <query>

things add <title> [--notes --when --deadline --tags --checklist --project --heading --list]
things project add <title> [--notes --when --deadline --tags --area --todos]
things project edit <project> [--title --notes --when --deadline --tags --add-tags --area --area-id --complete --cancel --duplicate --reveal ...]
things edit <task> [--title --notes --when --deadline --tags --add-tags --list --heading --complete --cancel --duplicate --reveal ...]
things complete <task>
things cancel <task>
things log                      # move Today → Logbook
things open <ref|list>          # reveal task/project/area/tag/built-in list in the app

things import [--file F] [--reveal] < payload.json
    # batch create/update via the Things JSON URL scheme
    # payload is the array documented at culturedcode.com/things/support/articles/2803573/
```

### Task reference forms

`<task>` accepts:

- UUID
- Numeric index from the last list (1-based) — `things list today; things complete 2`
- Title substring — interactive prompt disambiguates; non-TTY errors with the match list.

### Multi-line values

Newline-separated fields (`--checklist`, `--todos`, `--prepend-checklist`, `--append-checklist`) accept the literal two-character escape `\n` to pack multi-line values into one shell-quoted argument:

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

Reschedule and tag an existing task:

```
things edit "Ship release" --when tomorrow --add-tags "priority"
```

Pipe JSON to another tool:

```
things --json list today | jq '.[] | .title'
```

## Tips

- Prefer `--json` in scripted contexts.
- After a `list`/`search`, numeric indices stay valid until the next one.
- Use `things open` when the user wants to *see* something in the app rather than read data back.
