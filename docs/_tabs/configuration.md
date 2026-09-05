---
title: Configuration
icon: fas fa-cog
order: 3
---

Every flag `things` accepts has a built-in default. A TOML config file lets
you change those defaults once instead of typing the same flag on every
run — colour always on, say, or a database somewhere other than where the
CLI would look.

The file only supplies defaults. A flag on the command line still wins, so
nothing you put here can take an option away from you.

## Where the file lives

`things` uses the first of these it finds:

| Order | Source | Notes |
| --- | --- | --- |
| 1 | `--config PATH` | Wins over everything else, for one run |
| 2 | `$THINGS_CLI_CONFIG` | Same, for a whole shell session |
| 3 | `$XDG_CONFIG_HOME/things-cli/config.toml` | Only when `XDG_CONFIG_HOME` is set |
| 4 | `~/.config/things-cli/config.toml` | The default |

> **A missing file is not an error.** With no config file at all, `things`
> runs on its built-in defaults. That is also true of a path you named
> yourself — run `things config path` to see which file is in use and
> whether it exists.
{: .prompt-info }

`~` and relative paths are expanded, so `--config ./project.toml` works.

## Precedence

```text
flag on the command line  >  config file  >  built-in default
```

There is no second code path: the file is a resolver the CLI hands to its
argument parser, which consults it only for flags you did not pass. That
is what makes the rule hold everywhere without exception.

```sh
# config file says color = "always"
things today                  # colour on   — from the file
things --color never today    # colour off  — the flag wins
```

For a boolean, spell the override out to turn it back off:

```sh
things --json=false today     # even with json = true in the file
```

## Keys

Keys are named after the flag they set, in `snake_case` — except
`assume_yes`, which is deliberately narrower than the `--yes` it seeds
(see [below](#assume_yes-and-the-skill-commands)). The flag's own
spelling is accepted too, so `no-verify` works as well as `no_verify` —
but pick one: setting the same option under both spellings in one file is
an error rather than a coin toss.

| Key | Also accepted | Type | Default | Applies to | What it does |
| --- | --- | --- | --- | --- | --- |
| `json` | — | boolean | `false` | every command | Print JSON instead of the plain text listing |
| `color` | — | `"auto"` \| `"always"` \| `"never"` | `"auto"` | every command | When to colour output; `auto` means only on a terminal |
| `hints` | — | boolean | `true` | every command | Print the hint line under a plain task listing |
| `db` | — | path | auto-detected | every command | Where the Things3 SQLite database is; the file must exist |
| `no_verify` | `no-verify` | boolean | `false` | `complete`, `cancel`, `edit`, `project edit`, `import`, `tag add` (and any write that creates tags) | Skip the read-back that confirms a status change or a tag creation landed |
| `strict_tags` | `strict-tags` | boolean | `false` | `add`, `edit`, `project add`, `project edit`, `import` | Fail instead of writing when a tag does not exist |
| `create_tags` | `create-tags` | boolean | `false` | `add`, `edit`, `project add`, `project edit`, `import` | Create missing tags before writing |
| `assume_yes` | `yes` | boolean | `false` | `complete`, `cancel` | Answer the confirmation before a project-wide status change |

Two constraints the CLI enforces:

- `strict_tags` and `create_tags` are mutually exclusive. Setting both to
  `true` is an error, reported as soon as the file is read — they are two
  different answers to the same question.
- `db` must point at a file that exists. The check runs when a command
  opens the database, so a stale path is reported against the config file
  rather than as a puzzling flag error — and the commands that never read
  the database keep working, which is how you find out the path is stale.

`strict_tags` and `create_tags` also override each other from the command
line: `--create-tags` on a run whose file says `strict_tags = true` is
the override it looks like, not a "can't be used together" error.

A short file setting two things:

```toml
color = "always"
assume_yes = true
```

## An example file

`things config init` writes this template, with every key present and
commented out. Uncomment a line to change that default.

```toml
# things-cli configuration
#
# Defaults for the flags below. A flag passed on the command line always
# wins: flag > this file > built-in default.
#
# Read from $XDG_CONFIG_HOME/things-cli/config.toml, or
# ~/.config/things-cli/config.toml. Override with --config PATH or
# $THINGS_CLI_CONFIG. A missing file is not an error.
#
# Uncomment a line to change the default.

# Print JSON instead of the plain text listing. Same as --json / -j.
# Turning this on changes the output of every command, including for
# agents and scripts that expect plain text.
# json = false

# Color mode: "auto", "always" or "never". Same as --color.
# "auto" colours only when stdout is a terminal.
# color = "auto"

# Print the hint line under a plain task listing. Same as --hints /
# --no-hints. The hint only ever appears when stdout is a terminal and
# the output is not JSON, so turning it off is for terminal use.
# hints = true

# Path to the Things3 SQLite database. Same as --db.
# Leave unset to let things-cli find it. The file must exist.
# db = "~/Library/Group Containers/JLMPQHK86H.com.culturedcode.ThingsMac/Things Database.thingsdatabase/main.sqlite"

# Skip the read-back that confirms a complete/cancel actually landed.
# Same as --no-verify. Faster, but a write Things silently drops is
# then reported as a success.
# no_verify = false

# Fail instead of writing when a tag does not exist in Things.
# Same as --strict-tags. Off by default, which warns and writes anyway.
# Mutually exclusive with create_tags.
# strict_tags = false

# Create tags that do not exist in Things before writing.
# Same as --create-tags. Mutually exclusive with strict_tags.
# create_tags = false

# Answer the confirmation asked before a project-wide complete or
# cancel, instead of prompting. Same as --yes / -y on those two
# commands; the --yes on `skill install` and `skill uninstall` is
# deliberately not covered, so this cannot delete an installed skill.
# Turning this on removes the last check before a project and every
# task in it changes status.
# assume_yes = false
```

> This block is checked against the real template by a test in the
> repository, so it cannot drift from what `things config init` writes.
{: .prompt-tip }

## The `config` commands

### `things config path`

Prints the file in use and whether it is there.

```console
$ things config path
/Users/me/.config/things-cli/config.toml (exists)
```

`--json` adds where the path came from — `flag`, `env`, or `default`:

```console
$ things --json config path
{
  "path": "/Users/me/.config/things-cli/config.toml",
  "exists": true,
  "source": "default"
}
```

### `things config show`

Prints the default each key resolves to and whether that came from the
file or the CLI. These are the values that apply when you pass no flag —
flags you pass to `config show` itself do not appear here.

```console
$ things config show
config: /Users/me/.config/things-cli/config.toml (exists)
These apply when no flag overrides them.

  json         false    default
  color        always   config
  hints        true     default
  db           (unset)  default
  no_verify    false    default
  strict_tags  false    default
  create_tags  false    default
  assume_yes   true     config
```

`--json` gives the same thing as a `settings` array — one entry per key,
in the order above, abridged here:

```console
$ things --json config show
{
  "path": "/Users/me/.config/things-cli/config.toml",
  "exists": true,
  "source": "default",
  "settings": [
    {
      "key": "json",
      "value": false,
      "source": "default"
    },
    {
      "key": "color",
      "value": "always",
      "source": "config"
    }
  ]
}
```

### `things config init`

Writes the annotated template above, creating the directory if it is not
there. It refuses to overwrite a file that already exists:

```console
$ things config init
Wrote config template to /Users/me/.config/things-cli/config.toml

$ things config init
Error: config file already exists: /Users/me/.config/things-cli/config.toml — pass --force to overwrite
```

Pass `--force` to replace it, or `--config PATH` to write somewhere else.

## When the file is wrong

A problem with the file is reported on its own line, naming the file and
what is wrong with it. It is not a usage mistake, so there is no usage
dump. The command exits `2`.

```console
$ things config path
Error: config file /Users/me/.config/things-cli/config.toml: unknown key "verbose" (valid keys: json, color, hints, db, no_verify, strict_tags, create_tags, assume_yes)
```

```console
$ things config path
Error: config file /Users/me/.config/things-cli/config.toml: key "no_verify" is set twice ("no_verify" and "no-verify" name the same setting)
```

```console
$ things config path
Error: config file /Users/me/.config/things-cli/config.toml: invalid TOML: line 1, column 16: basic strings cannot have new lines
```

```console
$ things config path
Error: config file /Users/me/.config/things-cli/config.toml: key "color" must be one of auto, always, never, got "pink"
```

A `db` path that no longer exists is caught when a command opens the
database, not before, so the commands that never read it still run — and
a `--db` on the command line still overrides a stale entry rather than
tripping over it:

```console
$ things today
Error: config file /Users/me/.config/things-cli/config.toml: db: stat /Users/me/old/main.sqlite: no such file or directory

$ things config path                        # tells you which file to fix
/Users/me/.config/things-cli/config.toml (exists)

$ things --db ~/current/main.sqlite today   # works
```

### The diagnostic commands always run

A file the CLI cannot use stops every command that would have read it,
but not the ones that exist to tell you about it. `things config path`,
`things config show`, `things config init` and `--help` all work against
a broken file:

```console
$ things config path
/Users/me/.config/things-cli/config.toml (exists)
warning: this file cannot be used: config file /Users/me/.config/things-cli/config.toml: invalid TOML: line 1, column 16: basic strings cannot have new lines
```

`config path` still exits `0` — which file is in use is a fact about the
path, not its contents — and puts the warning on stderr. `config show`
names the file and then reports the problem, because there are no values
to show:

```console
$ things config show
config: /Users/me/.config/things-cli/config.toml (exists)
Error: config file /Users/me/.config/things-cli/config.toml: invalid TOML: line 1, column 16: basic strings cannot have new lines
```

`config init` refuses to overwrite as usual, but says why the file is
unusable, and `--force` replaces it with a fresh template:

```console
$ things config init
Error: config file already exists: /Users/me/.config/things-cli/config.toml (unusable as it stands: invalid TOML: line 1, column 16: basic strings cannot have new lines) — pass --force to overwrite

$ things config init --force
Wrote config template to /Users/me/.config/things-cli/config.toml
```

Under `--json` these come out as a JSON object on stdout like any other
failure, so a script sees a structured error either way.

## `assume_yes` and the skill commands

Completing or cancelling a *project* also completes or cancels every task
inside it, so `things complete` asks first. A run that cannot prompt —
stdin is not a terminal (cron, a CI job, `< /dev/null`), or `--json`,
which never prompts whatever stdin is — declines. `assume_yes` answers
that question ahead of time, which is what lets a script or an agent
complete a project.

Piping *output* does not turn the prompt off: `things complete "Roof" |
tee log` still asks on stderr and reads the answer from the terminal.

`--yes` also exists on `skill install` and `skill uninstall`, where it
means "overwrite" and "delete". `assume_yes` deliberately does **not**
reach those: a file written so a script can tick off a project should not
also let `things skill uninstall` remove files without asking. Those two
still prompt, or take the flag directly.

```sh
things skill uninstall claude --yes   # the flag works
                                      # assume_yes = true does not do this for you
```

Keys can be scoped this way in general — `assume_yes` is the only one
that currently is.

## Notes for agents and scripts

A config file can change defaults you would otherwise assume. `json =
true` makes every command emit JSON; `no_verify = true` turns off the
read-back that confirms a `complete` landed; `hints = false` drops the
hint line.

If you are writing something that has to behave the same on any machine,
pass the flags you depend on rather than inheriting them:

```sh
things --json=false today     # plain text, whatever the file says
things --json list today
```

`things config show` reports the file in use and everything it sets,
which is the quickest way to find out why a command on one machine
behaves differently from the same command on another.
