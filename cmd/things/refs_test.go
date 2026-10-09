package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ryanlewis/things-cli/internal/db"
	"github.com/ryanlewis/things-cli/internal/db/dbtest"
)

// An exact title on a closed or trashed row wins over a substring match on an
// open one: the write is refused, non-zero, naming the most recent row with
// that title, and the open twin whose title merely contains it is left alone.
// Only an open row with the same exact title beats it.
func TestExactTitleOnClosedRowBeatsSubstring(t *testing.T) {
	cases := []struct {
		name  string
		seed  func(fx *dbtest.Fixture)
		args  []string
		token string
		uuid  string // the row the refusal names
		count string // the count the message gives, if several
	}{
		{
			name: "completed exact title, complete",
			seed: func(fx *dbtest.Fixture) {
				fx.Todo("zz-base", "zz base", 20, dbtest.Completed(1))
			},
			args:  []string{"complete", "zz base"},
			token: "already closed",
			uuid:  "zz-base",
		},
		{
			name: "completed exact title, edit",
			seed: func(fx *dbtest.Fixture) {
				fx.Todo("zz-base", "zz base", 20, dbtest.Completed(1))
			},
			args:  []string{"edit", "zz base", "--when", "tomorrow"},
			token: "already closed",
			uuid:  "zz-base",
		},
		{
			name: "completed exact-case title beside an open case variant",
			seed: func(fx *dbtest.Fixture) {
				fx.Todo("zz-base", "zz base", 20, dbtest.Completed(1))
				fx.Todo("zz-upper", "ZZ BASE", 21, dbtest.Anytime())
			},
			args:  []string{"complete", "zz base"},
			token: "already closed",
			uuid:  "zz-base",
		},
		{
			name: "completed exact title, cancel",
			seed: func(fx *dbtest.Fixture) {
				fx.Todo("zz-base", "zz base", 20, dbtest.Completed(1))
			},
			args:  []string{"cancel", "zz base"},
			token: "already closed",
			uuid:  "zz-base",
		},
		{
			name: "cancelled exact title, complete",
			seed: func(fx *dbtest.Fixture) {
				fx.Todo("zz-base", "zz base", 20, dbtest.Cancelled(1))
			},
			args:  []string{"complete", "zz base"},
			token: "already closed",
			uuid:  "zz-base",
		},
		{
			name: "trashed exact title",
			seed: func(fx *dbtest.Fixture) {
				fx.Todo("zz-base", "zz base", 20, dbtest.Trashed())
			},
			args:  []string{"complete", "zz base"},
			token: "trashed",
			uuid:  "zz-base",
		},
		{
			name: "trashed and completed exact title",
			seed: func(fx *dbtest.Fixture) {
				fx.Todo("zz-base", "zz base", 20, dbtest.Trashed(), dbtest.Completed(1))
			},
			args:  []string{"complete", "zz base"},
			token: "trashed",
			uuid:  "zz-base",
		},
		{
			name: "exact title in a trashed project",
			seed: func(fx *dbtest.Fixture) {
				fx.Project("zz-proj", "zz binned", 19, dbtest.Trashed())
				fx.Todo("zz-base", "zz base", 20, dbtest.Anytime(), dbtest.InProject("zz-proj"))
			},
			args:  []string{"complete", "zz base"},
			token: "trashed",
			uuid:  "zz-base",
		},
		{
			name: "exact title under a heading of a trashed project",
			seed: func(fx *dbtest.Fixture) {
				fx.Project("zz-proj", "zz binned", 18, dbtest.Trashed())
				fx.Heading("zz-head", "zz heading", 19, dbtest.InProject("zz-proj"))
				fx.Todo("zz-base", "zz base", 20, dbtest.Anytime(), dbtest.UnderHeading("zz-head"))
			},
			args:  []string{"edit", "zz base", "--notes", "x"},
			token: "trashed",
			uuid:  "zz-base",
		},
		{
			name: "several closed exact titles, the latest named",
			seed: func(fx *dbtest.Fixture) {
				fx.Todo("zz-base", "zz base", 20, dbtest.Completed(1))
				fx.Todo("zz-base-2", "zz base", 21, dbtest.Cancelled(3))
				fx.Todo("zz-base-3", "zz base", 22, dbtest.Completed(2))
			},
			args:  []string{"complete", "zz base"},
			token: "already closed",
			uuid:  "zz-base-2",
			count: "3 closed or trashed items",
		},
		{
			name: "several closed exact titles, the latest trashed",
			seed: func(fx *dbtest.Fixture) {
				fx.Todo("zz-base", "zz base", 20, dbtest.Completed(1))
				fx.Todo("zz-base-2", "zz base", 21, dbtest.Completed(5), dbtest.Trashed())
			},
			args:  []string{"cancel", "zz base"},
			token: "trashed",
			uuid:  "zz-base-2",
			count: "2 closed or trashed items",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fastVerify(t)
			isolateHome(t)
			database, sqlDB := seedWritable(t)
			fx := dbtest.NewFixture(t, sqlDB)
			fx.Todo("zz-extra", "zz base extra", 30, dbtest.Anytime())
			tc.seed(fx)
			calls := stubExecDropping(t)

			stdout, _, err := runStreams(t, database, tc.args...)
			if *calls != 0 {
				t.Errorf("issued %d write(s); the open twin %q must not be touched", *calls, "zz base extra")
			}
			if stdout != "" {
				t.Errorf("stdout = %q, want nothing on a refusal", stdout)
			}
			if err == nil {
				t.Fatalf("run %v: want a %q refusal", tc.args, tc.token)
			}
			payload, raw := decodePayload(t, err)
			if payload.Error != tc.token {
				t.Errorf("JSON error = %q, want %q (%s)", payload.Error, tc.token, raw)
			}
			if payload.UUID != tc.uuid {
				t.Errorf("JSON uuid = %q, want %q (%s)", payload.UUID, tc.uuid, raw)
			}
			if !strings.Contains(err.Error(), "no open task with a similar title was touched") {
				t.Errorf("message %q does not say the open task was left alone", err.Error())
			}
			if tc.count != "" && !strings.Contains(err.Error(), tc.count) {
				t.Errorf("message %q does not give the count %q", err.Error(), tc.count)
			}
		})
	}
}

// One open row with the exact title wins over closed rows that share it, as
// `add` treats a title that also exists completed. A fragment still only
// matches open rows, and a fragment on its own never reaches a closed row.
func TestExactTitleOpenRowWins(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	sqlDB := dbtest.NewSQL(t)
	fx := dbtest.NewFixture(t, sqlDB)
	fx.Todo("old-1", "Pay rent", 1, dbtest.Completed(1))
	fx.Todo("old-2", "Pay rent", 2, dbtest.Trashed())
	fx.Todo("open-1", "Pay rent", 3, dbtest.Anytime())
	fx.Todo("deposit", "Pay rent deposit", 4, dbtest.Anytime())
	fx.Todo("gone", "Renew passport", 5, dbtest.Completed(1))
	database := db.NewFromSQL(sqlDB)

	for ref, want := range map[string]string{
		"Pay rent":         "open-1",
		"rent deposit":     "deposit",
		"Renew passport":   "gone",
		"Pay rent deposit": "deposit",
	} {
		got, err := resolveTask(&Deps{}, ref, database)
		if err != nil || got.UUID != want {
			t.Errorf("resolveTask(%q) = %+v, %v, want %s", ref, got, err, want)
		}
	}
	var nf *db.TaskNotFoundError
	if got, err := resolveTask(&Deps{}, "passport", database); !errors.As(err, &nf) {
		t.Errorf("resolveTask(fragment of a closed title) = %+v, %v, want not found", got, err)
	}
}

// A uuid or a row number that names a completed item keeps the exit-0 note:
// only the title path is refused.
func TestClosedItemByUUIDOrRowStillNoted(t *testing.T) {
	for _, ref := range []string{"zz-base", "1"} {
		t.Run(ref, func(t *testing.T) {
			fastVerify(t)
			isolateHome(t)
			database, sqlDB := seedWritable(t)
			fx := dbtest.NewFixture(t, sqlDB)
			fx.Todo("zz-base", "zz base", 20, dbtest.Completed(1))
			fx.Todo("zz-extra", "zz base extra", 30, dbtest.Anytime())
			seedCache(t, time.Minute, "things today", "zz-base")
			calls := stubExecDropping(t)

			_, stderr, err := runStreams(t, database, "complete", ref)
			if err != nil || *calls != 0 {
				t.Fatalf("complete %s = %v with %d write(s), want exit 0 and nothing sent", ref, err, *calls)
			}
			if !strings.Contains(stderr, "already completed") {
				t.Errorf("stderr = %q, want an already-completed note", stderr)
			}
		})
	}
}

// `show` on an exact title that several closed rows share offers them as
// candidates, and never the open row whose title contains it.
func TestShowExactTitleOnSeveralClosedRows(t *testing.T) {
	isolateHome(t)
	database, sqlDB := seedWritable(t)
	fx := dbtest.NewFixture(t, sqlDB)
	fx.Todo("zz-base", "zz base", 20, dbtest.Completed(1))
	fx.Todo("zz-base-2", "zz base", 21, dbtest.Trashed())
	fx.Todo("zz-extra", "zz base extra", 30, dbtest.Anytime())

	_, _, err := runStreams(t, database, "show", "zz base")
	payload, raw := decodePayload(t, err)
	if payload.Error != "ambiguous task" || len(payload.Matches) != 2 {
		t.Fatalf("show = %s, want the two closed rows as candidates", raw)
	}
	for _, m := range payload.Matches {
		if m.UUID == "zz-extra" {
			t.Errorf("candidates include the open twin (%s)", raw)
		}
	}
}

// `show` on an exact title that only a closed row carries shows that row
// rather than an open one whose title contains it.
func TestShowExactTitleOnClosedRow(t *testing.T) {
	isolateHome(t)
	database, sqlDB := seedWritable(t)
	fx := dbtest.NewFixture(t, sqlDB)
	fx.Todo("zz-base", "zz base", 20, dbtest.Completed(1))
	fx.Todo("zz-extra", "zz base extra", 30, dbtest.Anytime())

	out, err := runOut(t, database, "--json", "show", "zz base")
	if err != nil {
		t.Fatalf("show: %v", err)
	}
	if !strings.Contains(out, `"zz-base"`) || strings.Contains(out, "zz-extra") {
		t.Errorf("show output = %s, want the completed zz-base", out)
	}
}

// Digits with a leading zero, and a bare 0, are never a row: listings print
// no leading zeros. They reach a task titled exactly that and nothing else,
// and suggest the bare row number when the last list has that row.
func TestLeadingZeroRefIsNotARow(t *testing.T) {
	uuids := make([]string, 19)
	for i := range uuids {
		uuids[i] = fmt.Sprintf("row-%d", i+1)
	}
	seed := func(t *testing.T) *db.DB {
		t.Helper()
		t.Setenv("HOME", t.TempDir())
		seedCache(t, time.Minute, "things today", uuids...)
		sqlDB := dbtest.NewSQL(t)
		fx := dbtest.NewFixture(t, sqlDB)
		for i, u := range uuids {
			fx.Todo(u, fmt.Sprintf("#%d", 10+i), i)
		}
		fx.Todo("title-07", "07", 50)
		fx.Todo("mentions", "Call 02 0 00 007 back", 51)
		return db.NewFromSQL(sqlDB)
	}

	cases := []struct {
		ref  string
		want string // uuid, or "" for not found
		hint string // row hint expected in the error, or ""
	}{
		{"07", "title-07", ""},
		{" 07 ", "title-07", ""},
		{"02", "", "use 2,"},
		{"007", "", "use 7,"},
		{"00", "", ""},
		{"0", "", ""},
		{"019", "", "use 19,"},
	}
	for _, tc := range cases {
		t.Run(tc.ref, func(t *testing.T) {
			database := seed(t)
			got, err := resolveTask(&Deps{}, tc.ref, database)
			if tc.want != "" {
				if err != nil || got.UUID != tc.want {
					t.Fatalf("resolveTask(%q) = %+v, %v, want %s", tc.ref, got, err, tc.want)
				}
				return
			}
			var nf *notFoundError
			if !errors.As(err, &nf) {
				t.Fatalf("resolveTask(%q) = %+v, %v, want not found", tc.ref, got, err)
			}
			if !strings.Contains(err.Error(), "not a row reference") {
				t.Errorf("message %q does not say it is not a row", err.Error())
			}
			if tc.hint != "" && !strings.Contains(err.Error(), tc.hint) {
				t.Errorf("message %q does not suggest %q", err.Error(), tc.hint)
			}
			if tc.hint == "" && strings.Contains(err.Error(), "if you meant row") {
				t.Errorf("message %q suggests a row that does not exist", err.Error())
			}
		})
	}

	// The control: the same rows without the zero are rows.
	database := seed(t)
	if got, err := resolveTask(&Deps{}, "7", database); err != nil || got.UUID != "row-7" {
		t.Errorf("resolveTask(7) = %+v, %v, want row-7", got, err)
	}
}

func TestClassifyRefLeadingZero(t *testing.T) {
	for ref, want := range map[string]refKind{
		"0": refRowLike, "00": refRowLike, "07": refRowLike, " 07 ": refRowLike,
		"7": refRow, "10": refRow, "100": refRow,
	} {
		if kind, _ := classifyRef(ref); kind != want {
			t.Errorf("classifyRef(%q) = %d, want %d", ref, kind, want)
		}
	}
}

// A cache file that exists but cannot be read refuses a bare number instead
// of treating it as no listing, which would resolve `12` to a task titled
// "12". A missing file keeps the documented fallthrough to an exact title.
func TestUnreadableCacheRefusesRowRef(t *testing.T) {
	writeCache := func(t *testing.T, content string) {
		t.Helper()
		dir := filepath.Join(os.Getenv("HOME"), "Library", "Caches", "things-cli")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "last-list"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cases := map[string]func(t *testing.T){
		"empty":          func(t *testing.T) { writeCache(t, "") },
		"blank":          func(t *testing.T) { writeCache(t, "\n  \n") },
		"garbage":        func(t *testing.T) { writeCache(t, "this is not a listing") },
		"truncated JSON": func(t *testing.T) { writeCache(t, `{"written_at":"2026-10-09T10:00:00Z","uuids":["abc-123","ye`) },
		"binary":         func(t *testing.T) { writeCache(t, "\x00\xff\x01") },
		"directory": func(t *testing.T) {
			p := filepath.Join(os.Getenv("HOME"), "Library", "Caches", "things-cli", "last-list")
			if err := os.MkdirAll(p, 0o755); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			setup(t)
			database := seedNumericTitleDB(t)

			for _, ref := range []string{"12", "2026", " 1 "} {
				got, err := resolveTask(&Deps{}, ref, database)
				var unreadable *unreadableCacheError
				if !errors.As(err, &unreadable) {
					t.Fatalf("resolveTask(%q) = %+v, %v, want an unreadable-cache refusal", ref, got, err)
				}
				for _, want := range []string{"could not be read", "run a listing again", "uuid"} {
					if !strings.Contains(err.Error(), want) {
						t.Errorf("message %q does not mention %q", err.Error(), want)
					}
				}
				payload, raw := decodePayload(t, err)
				if payload.Error != "stale list cache" || payload.Query != ref || payload.Kind != "task" {
					t.Errorf("JSON payload = %s, want stale list cache on %q", raw, ref)
				}
			}

			// A ref that is not a bare number does not depend on the
			// cache, so it still resolves.
			if got, err := resolveTask(&Deps{}, "Chapter", database); err != nil || got.UUID != "ch-12" {
				t.Errorf("resolveTask(Chapter) = %+v, %v, want ch-12", got, err)
			}
		})
	}

	t.Run("missing file still falls through to an exact title", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		got, err := resolveTask(&Deps{}, "2026", seedNumericTitleDB(t))
		if err != nil || got.UUID != "year-2026" {
			t.Errorf("resolveTask(2026) = %+v, %v, want year-2026", got, err)
		}
	})
}

// A uuid-shaped ref that is no item's uuid, such as a real uuid typed in the
// wrong case, matches an exact title only: uuids are byte-exact while titles
// ignore case, so a substring match would land on a decoy whose title holds
// the string.
func TestUUIDShapedRefSkipsSubstring(t *testing.T) {
	const real = "NAmXd4kencwJqJwnyKR3gu"
	const legacy = "8E3A6B44-1C2D-4E5F-9A0B-1C2D3E4F5A6B"
	t.Setenv("HOME", t.TempDir())
	sqlDB := dbtest.NewSQL(t)
	fx := dbtest.NewFixture(t, sqlDB)
	fx.Todo(real, "The real one", 1)
	fx.Todo("decoy", "Paste namxd4kencwjqjwnykr3gu and 8e3a6b44-1c2d-4e5f-9a0b-1c2d3e4f5a6b here", 2)
	fx.Todo("exact", "zq1eBe5neLDmcu7czCnzc", 3)
	fx.Todo(legacy, "Legacy uuid row", 4)
	database := db.NewFromSQL(sqlDB)

	for _, ref := range []string{
		strings.ToLower(real),
		strings.ToUpper(real),
		"namxd4kencwjqjwnykr3g", // 21 chars, a fragment of the decoy
		strings.ToLower(legacy),
	} {
		t.Run(ref, func(t *testing.T) {
			got, err := resolveTask(&Deps{}, ref, database)
			var nf *notFoundError
			if !errors.As(err, &nf) {
				t.Fatalf("resolveTask(%q) = %+v, %v, want not found", ref, got, err)
			}
			if !strings.Contains(err.Error(), "case-sensitive") {
				t.Errorf("message %q does not say uuids are case-sensitive", err.Error())
			}
			payload, raw := decodePayload(t, err)
			if payload.Error != "not found" || payload.Query != ref {
				t.Errorf("JSON payload = %s, want not found on %q", raw, ref)
			}
		})
	}

	for ref, want := range map[string]string{
		real:                    real,
		legacy:                  legacy,
		"zq1eBe5neLDmcu7czCnzc": "exact", // uuid-shaped, but an exact title
		"namxd4kencwjqjwnykr3":  "decoy", // 20 chars: not uuid-shaped
		"Paste namxd4kencwjqj":  "decoy", // has a space: not uuid-shaped
		"The real":              real,
	} {
		got, err := resolveTask(&Deps{}, ref, database)
		if err != nil || got.UUID != want {
			t.Errorf("resolveTask(%q) = %+v, %v, want %s", ref, got, err, want)
		}
	}
}

func TestLooksLikeUUID(t *testing.T) {
	for ref, want := range map[string]bool{
		"NAmXd4kencwJqJwnyKR3gu":               true,
		"zq1eBe5neLDmcu7czCnzc":                true,
		"8E3A6B44-1C2D-4E5F-9A0B-1C2D3E4F5A6B": true,
		"8e3a6b44-1c2d-4e5f-9a0b-1c2d3e4f5a6b": true,
		"NAmXd4kencwJqJwnyKR3g":                true,
		"NAmXd4kencwJqJwnyKR3":                 false,
		"NAmXd4kencwJqJwnyKR3guX":              false,
		"NAmXd4kencwJqJwnyKR3g-":               false,
		" NAmXd4kencwJqJwnyKR3g":               false,
		"8E3A6B44-1C2D-4E5F-9A0B-1C2D3E4F5A6G": false,
		"8E3A6B441C2D-4E5F-9A0B-1C2D3E4F5A6B-": false,
		"":                                     false,
	} {
		if got := looksLikeUUID(ref); got != want {
			t.Errorf("looksLikeUUID(%q) = %v, want %v", ref, got, want)
		}
	}
}

// An empty or blank ref is refused before any lookup, so it can neither be
// reported as ambiguous between untitled to-dos nor resolve to the only one.
func TestEmptyRefIsRefused(t *testing.T) {
	for _, untitled := range []int{1, 2} {
		for _, ref := range []string{"", " ", "\t", " \n "} {
			t.Run(fmt.Sprintf("%d untitled/%q", untitled, ref), func(t *testing.T) {
				fastVerify(t)
				isolateHome(t)
				database, sqlDB := seedWritable(t)
				fx := dbtest.NewFixture(t, sqlDB)
				for i := range untitled {
					fx.Todo(fmt.Sprintf("untitled-%d", i), "", 40+i, dbtest.Anytime())
				}
				calls := stubExecDropping(t)

				for _, cmd := range [][]string{{"show", ref}, {"complete", ref}, {"cancel", ref}, {"edit", ref, "--notes", "x"}} {
					_, _, err := runStreams(t, database, cmd...)
					var empty *emptyRefError
					if !errors.As(err, &empty) {
						t.Fatalf("run %q = %v, want an empty-reference refusal", cmd, err)
					}
					payload, raw := decodePayload(t, err)
					if payload.Error != "empty reference" || payload.Kind != "task" {
						t.Errorf("JSON payload = %s, want empty reference", raw)
					}
				}
				if *calls != 0 {
					t.Errorf("issued %d write(s) for an empty ref", *calls)
				}
			})
		}
	}
}

// A ref with space inside or around it is otherwise left as typed: trailing
// space twins are distinct titles.
func TestPlainRefIsNotTrimmed(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	sqlDB := dbtest.NewSQL(t)
	fx := dbtest.NewFixture(t, sqlDB)
	fx.Todo("plain", "Work", 1)
	fx.Todo("spaced", "Work ", 2)
	database := db.NewFromSQL(sqlDB)
	for ref, want := range map[string]string{"Work": "plain", "Work ": "spaced"} {
		got, err := resolveTask(&Deps{}, ref, database)
		if err != nil || got.UUID != want {
			t.Errorf("resolveTask(%q) = %+v, %v, want %s", ref, got, err, want)
		}
	}
}
