# things-cli — Things3 CLI for macOS

Use the `things` CLI whenever the user mentions Things3, tasks, todos, inbox,
today, upcoming, projects, or areas on macOS.

## Safety

- Reads (`list`, `show`, `projects`, `areas`, `tags`, `search`) are safe — use freely.
- Writes (`add`, `project add`, `edit`, `complete`, `cancel`, `log`, `open`) modify the user's real data. Confirm before destructive ones (`complete`, `cancel`, bulk `edit`).
- `edit`, `project edit`, and `import` payloads with `operation: update` require *Things → Settings → General → Enable Things URLs*. The error to recognise: `update: auth token is required — enable Things URLs in Things → Settings → General …`.

## Output

Most commands accept `--json` / `-j`. Prefer it when parsing output.

Human output is styled with colors and aligned columns. Color auto-disables when piping or when `NO_COLOR` is set. Override with `--color=always|never` (default `auto`). JSON output is unaffected.

## Core commands

```
things list [view] [--project P] [--area A] [--tag T] [--on D | --from D --to D]
    # views: today, inbox, upcoming, anytime, someday, logbook, trash, deadlines
    # shortcut: `things today`, `things inbox`, etc.
    # --on / --from / --to take YYYY-MM-DD (or RFC3339). They filter startDate
    # on most views and `deadline` on the `deadlines` view. Not supported on
    # inbox/trash/logbook. --on is mutually exclusive with --from/--to.

things show <task>              # task detail
things projects [--area A] [--completed]
things areas
things tags
things search <query>

things add <title> [--notes --when --deadline --tags --checklist --project --heading --list]
things project add <title> [--notes --when --deadline --tags --area --todos]
things project edit <project> [--title --notes --prepend-notes --append-notes --when --deadline --tags --add-tags --area --area-id --complete --cancel --duplicate --reveal]
things edit <task> [--title --notes --prepend-notes --append-notes --when --deadline --tags --add-tags --checklist --prepend-checklist --append-checklist --list --list-id --heading --heading-id --complete --cancel --duplicate --reveal]
things complete <task>
things cancel <task>
things log                      # move Today → Logbook
things open [<ref>] [-p P | -a A | -t T | -q Q] [--filter T1,T2] [--background]
    # ref: task/project UUID, numeric list index, title, or built-in list name
    #      (today, inbox, upcoming, anytime, someday, logbook, trash, deadlines)
    # exactly one of <ref> / -p / -a / -t / -q is required
    # --filter narrows the opened list by tags; --background keeps focus elsewhere

things import [--file F] [--reveal] < payload.json
    # batch create/update via the Things JSON URL scheme
    # payload is the array documented at culturedcode.com/things/support/articles/2803573/
```

### Task reference forms

`<task>` accepts:

- UUID
- Numeric index from the last list (1-based) — `things list today; things complete 2`
- Title substring — interactive prompt disambiguates; non-TTY errors with the match list.

### `--when` / `--deadline` values

`--when` accepts a keyword (`today`, `tomorrow`, `evening`, `anytime`, `someday`), a date `YYYY-MM-DD`, a time `HH:MM`, a date+time `YYYY-MM-DD@HH:MM`, or an RFC3339 timestamp. English natural-language phrases (`friday`, `next monday`) are passed through. Likely typos of the keywords (within edit distance 2, e.g. `tommorrow`) are rejected client-side with a "did you mean" hint.

`--deadline` accepts a `YYYY-MM-DD` date or an English natural-language phrase — keywords like `today` are rejected.

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
things edit 3 --when monday              # weekday names work
things edit 4 --when "next friday"
```

Reschedule several tasks at once (not transactional — partial failures stick):

```
things upcoming --area Work -j | jq -r '.[].uuid' | \
  while read uuid; do things edit "$uuid" --when monday; done

things import <<'JSON'
[
  {"type":"to-do","operation":"update","id":"<uuid-1>","attributes":{"when":"monday"}},
  {"type":"to-do","operation":"update","id":"<uuid-2>","attributes":{"when":"tuesday"}}
]
JSON
```

Pipe JSON to another tool:

```
things --json list today | jq '.[] | .title'
```

## Shell completions

`things completions <bash|zsh|fish>` prints a completion script for that shell. It delegates back to the binary (which must be on `PATH`), so it stays in sync with the CLI surface. The Homebrew cask will wire this up automatically once that lands; for now the user loads it with `source <(things completions zsh)` (bash/zsh) or `things completions fish | source`. Completion is flag/subcommand-name only — it never reads the Things database.

## Tips

- Prefer `--json` in scripted contexts.
- After a `list`/`search`, numeric indices stay valid until the next one.
- Use `things open` when the user wants to *see* something in the app rather than read data back.
