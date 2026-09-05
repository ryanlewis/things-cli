# things-cli — Things3 CLI for macOS

Use the `things` CLI whenever the user mentions Things3, tasks, todos, inbox,
today, upcoming, projects, or areas on macOS.

## Safety

- Reads (`list`, `show`, `projects`, `areas`, `tags`, `search`) are safe — use freely.
- Writes (`add`, `project add`, `edit`, `complete`, `cancel`, `log`, `open`) modify the user's real data. Confirm before destructive ones (`complete`, `cancel`, bulk `edit`).
- `edit`, `project edit`, and `import` payloads with `operation: update` require *Things → Settings → General → Enable Things URLs*. The error to recognise: `update: auth token is required — enable Things URLs in Things → Settings → General …`.
- `--complete` and `--cancel` are mutually exclusive on `edit` and `project edit`.
- `complete` and `cancel` (and `edit --complete` / `--cancel`) read the item back afterwards and exit non-zero if the status did not change. Treat a non-zero exit as "the task is still open" — do not report it as done.

## Output

Most commands accept `--json` / `-j`. Prefer it when parsing output.

Tasks and projects carry `"repeating": true` in JSON when Things treats them as repeating; the field is omitted otherwise. `things show` prints a `Repeats:` line for them.

In JSON, `status` is a string enum — `"open"`, `"cancelled"`, or `"completed"` (not the raw Things integer) — on tasks, projects, and checklist items. Filter with e.g. `jq 'select(.status=="open")'`.

Human output is styled with colors and aligned columns. Color auto-disables when piping or when `NO_COLOR` is set. Override with `--color=always|never` (default `auto`). JSON output is unaffected.

## Core commands

```
things list [view] [--project P] [--area A] [--tag T] [--on D | --from D --to D] [--include-completed]
    # views: today, inbox, upcoming, anytime, someday, repeating, logbook, trash, deadlines
    # shortcut: `things today`, `things inbox`, etc.
    # No view: bare `things` is today, but --project/--area/--tag on their own
    # list every open task in that project/area/tag. Name a view to scope the
    # filter to it (`things today --project X`); human output then prints a
    # `view: <name>` line so a slice isn't read as the whole project.
    # Tasks under a project heading belong to that project — they match
    # --project and the project's --area, and report projectTitle.
    # --on / --from / --to take YYYY-MM-DD (or RFC3339). They filter startDate
    # on most views and `deadline` on the `deadlines` view. Not supported on
    # inbox/trash/logbook/someday/repeating (those items have no start date).
    # --on is mutually exclusive with --from/--to.
    # today shows only open tasks; --include-completed also lists completed/
    # cancelled items Things hasn't logged out of Today yet (today only).

things show <task>              # task detail
things projects [-a|--area A] [--completed]
things areas
things tags
things search <query>

things add <title> [--notes --when --deadline --tags --checklist --project --heading --list --strict-tags]
things project add <title> [--notes --when --deadline --tags --area --todos --strict-tags]
things project edit <project> [--title --notes --prepend-notes --append-notes --when --deadline --tags --add-tags --area --area-id --complete --cancel --duplicate --reveal --strict-tags]
things edit <task> [--title --notes --prepend-notes --append-notes --when --deadline --tags --add-tags --checklist --prepend-checklist --append-checklist --list --list-id --heading --heading-id --complete --cancel --duplicate --reveal --strict-tags]
things complete <task>          # task or project; project completion asks to confirm
things cancel <task>            # task or project; project cancellation asks to confirm
things log                      # move Today → Logbook
things --no-verify complete <task>   # skip the read-back (rarely needed)
things open [<ref>] [-p P | -a A | -t T | -q Q] [--filter T1,T2] [--background]
    # ref: task/project UUID, numeric list index, title, or built-in list name
    #      (today, inbox, upcoming, anytime, someday, repeating, logbook, trash,
    #      deadlines)
    # exactly one of <ref> / -p / -a / -t / -q is required
    # --filter narrows the opened list by tags; --background keeps focus elsewhere

things import [--file F] [--reveal] [--strict-tags] < payload.json
    # batch create/update via the Things JSON URL scheme
    # payload is the array documented at culturedcode.com/things/support/articles/2803573/
```

### Repeating to-dos and projects

A repeating to-do is stored as a template plus the to-dos it generates. `things repeating` lists the templates; they do not appear in `someday` or any other view except `trash` and `logbook`, which report what the database holds. The generated to-dos carry no recurrence rule of their own, so they list as ordinary tasks under `today`, `upcoming` and the rest.

Things refuses to update `when`, `deadline`, completed/canceled status, and duplication on repeating items, and drops the request silently rather than reporting an error. The CLI checks first and fails with a non-zero exit:

```
"Water plants" is a repeating to-do — Things does not allow canceled to be changed
on repeating to-dos and drops the request silently (…). Change it in the Things app instead
```

There is no CLI workaround — the user has to make the change in the Things app. Every other attribute (`--title`, `--notes`, `--tags`, `--list`, …) edits normally.

### Tags must already exist

Things applies only tags that already exist and ignores the rest without saying so. Before any write that carries tags, the CLI checks them against the database and warns on stderr about ones it cannot find:

```
warning: these tags do not exist in Things and will be ignored: cifas-auto-reject
```

The write still goes ahead. Pass `--strict-tags` to fail before writing instead. There is no way to create a tag from the CLI — create it in Things first, then re-run.

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

`things completions <bash|zsh|fish>` prints a completion script for that shell. It delegates back to the binary (which must be on `PATH`), so it stays in sync with the CLI surface. The Homebrew cask generates these on install; on other install paths the user loads it with `source <(things completions zsh)` (bash/zsh) or `things completions fish | source`. Completion is flag/subcommand-name only — it never reads the Things database.

## Tips

- Prefer `--json` in scripted contexts.
- After a `list`/`search`, numeric indices stay valid until the next one.
- Use `things open` when the user wants to *see* something in the app rather than read data back.
