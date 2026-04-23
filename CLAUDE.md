# things-cli

CLI for Things3 on macOS. Reads from the Things3 SQLite database (read-only) and writes via macOS URL scheme / AppleScript.

## Workflow

- Never commit to `main`. Work on a branch, ideally a worktree (`git worktree add .worktrees/<topic> -b <topic>`). If you find yourself on `main` with changes, `git switch -c <topic>` before committing.
- `main` only moves via PR merges and release tags (see `/release`).
- Use Conventional Commits.

## Commands

Canonical entry points — CI runs the same targets.

```
make build   # go build -o things ./cmd/things
make install # go install ./cmd/things (into $GOBIN)
make test    # go test -race ./...
make cover   # go test -race -coverprofile=coverage.out ./... + summary
make lint    # golangci-lint run ./... (v2 config in .golangci.yml)
make fmt     # gofmt -w . && goimports -w .
```

## Architecture

- `internal/model/` — shared types (Task, Project, Area, Tag, ChecklistItem) and date codecs (ThingsDate bit-encoding, Core Data timestamps)
- `internal/db/` — SQLite queries via `modernc.org/sqlite` (pure Go, no cgo). Opens DB read-only with `PRAGMA query_only = ON`. `NewFromSQL` exists purely to let test helpers wrap an externally-built `*sql.DB`.
- `internal/db/dbtest/` — test-only helper: `dbtest.NewSQL(t)` returns an in-memory SQLite with the pared-down Things3 schema (embedded via `//go:embed schema.sql`). Use from any package's tests.
- `internal/things/` — write operations: URL scheme (`things:///add`) for task creation, AppleScript for complete/cancel
- `internal/output/` — JSON and plain text rendering. `Print`/`PrintTaskWithChecklist` take an `io.Writer` so tests can capture into `bytes.Buffer`.
- `internal/cache/` — last-list cache in `$HOME/Library/Caches/things-cli`. Tests override with `t.Setenv("HOME", t.TempDir())`.
- `cmd/things/` — `main` package with CLI wiring via `alecthomas/kong`. Lives under `cmd/things/` so `go install ./cmd/things` (or `go install github.com/ryanlewis/things-cli/cmd/things@latest`) produces a binary named `things` rather than `things-cli`.

## Conventions

- Tags in GROUP_CONCAT use unit separator (`char(31)` / `\x1f`) as delimiter to avoid collision with tag content
- Status/type constants live in `model` package — use them instead of magic ints
- No cgo — `modernc.org/sqlite` only

## Testing patterns

- **DB tests**: `sqlDB := dbtest.NewSQL(t)` then either `&DB{db: sqlDB}` (inside `package db`) or `db.NewFromSQL(sqlDB)` (external packages). No filesystem schema reads — `schema.sql` is embedded.
- **`internal/things` tests**: mock `var execCommand = exec.Command` by reassigning it in tests; restore with `t.Cleanup`. Return `exec.Command("true")` for success, `exec.Command("false")` or `exec.Command("sh", "-c", ...)` for failures.
- **`internal/cache` tests**: set `t.Setenv("HOME", t.TempDir())` — the cache dir is derived lazily from `$HOME`, no exported mutable globals.
- **Output tests**: write to a `bytes.Buffer` and assert against captured output. Test both `asJSON=true` (unmarshal + field check) and `asJSON=false` (substring match).
