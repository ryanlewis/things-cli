---
title: About
icon: fas fa-info-circle
order: 3
---

`things-cli` is a small Go CLI for [Things3](https://culturedcode.com/things/)
on macOS, written by [Ryan Lewis](https://github.com/ryanlewis).

## How it works

- **Reads** go through `modernc.org/sqlite` (pure Go, no cgo) with
  `PRAGMA query_only = ON`, so the CLI cannot mutate the Things database.
- **Writes** go through the official `things:///add` and
  `things:///update` URL schemes for creating and editing tasks, and
  through AppleScript for completing and cancelling them. This is the
  same interface Things exposes to Shortcuts and other automation tools.
- **Task resolution** accepts a UUID, a title (with interactive
  disambiguation when multiple tasks match), or a numeric index into the
  last listing.

Your data never leaves the machine. Things3 stays the source of truth.

## AI-friendly

Every command speaks JSON (`-j` / `--json`). A bundled agent skill ships
inside the binary — install it once for Claude Code, Codex CLI, or Pi
and your agent learns when to reach for `things` instead of guessing at
AppleScript.

```sh
things skill install claude
things skill show
```

## Source

The full command reference and contributing guide live on
[GitHub](https://github.com/{{ site.repository }}). Issues and pull
requests welcome.

[MIT licensed](https://github.com/{{ site.repository }}/blob/main/LICENSE).
