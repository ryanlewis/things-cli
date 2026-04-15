# things-cli

CLI for Things3 on macOS. Reads from the Things3 SQLite database (read-only) and writes via macOS URL scheme / AppleScript.

## Build

```
go build -o things .
```

## Architecture

- `internal/model/` — shared types (Task, Project, Area, Tag, ChecklistItem) and date codecs (ThingsDate bit-encoding, Core Data timestamps)
- `internal/db/` — SQLite queries via `modernc.org/sqlite` (pure Go, no cgo). Opens DB read-only with `PRAGMA query_only = ON`
- `internal/things/` — write operations: URL scheme (`things:///add`) for task creation, AppleScript for complete/cancel
- `internal/output/` — JSON (default) and plain text (`--plain`) rendering
- `main.go` — CLI wiring with `alecthomas/kong`

## Conventions

- Tags in GROUP_CONCAT use unit separator (`char(31)` / `\x1f`) as delimiter to avoid collision with tag content
- Status/type constants live in `model` package — use them instead of magic ints
- No cgo — `modernc.org/sqlite` only
