package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/alecthomas/kong"

	"github.com/ryanlewis/things-cli/internal/cache"
	"github.com/ryanlewis/things-cli/internal/db"
	"github.com/ryanlewis/things-cli/internal/db/dbtest"
	"github.com/ryanlewis/things-cli/internal/model"
	"github.com/ryanlewis/things-cli/internal/skill"
	"github.com/ryanlewis/things-cli/internal/things"
)

// stubTTY makes isInteractive report that stdin is a terminal for the duration
// of the test, so the --json guard can be exercised without a real TTY.
func stubTTY(t *testing.T, tty bool) {
	t.Helper()
	prev := isInteractive
	isInteractive = func() bool { return tty }
	t.Cleanup(func() { isInteractive = prev })
}

// --json means a machine is reading stdout, so a prompt would hang it: the flag
// implies non-interactive even on a terminal (issue #152).
func TestDepsInteractiveJSONImpliesNonInteractive(t *testing.T) {
	stubTTY(t, true)

	if !(&Deps{}).interactive() {
		t.Error("plain text on a TTY should be interactive")
	}
	if (&Deps{JSON: true}).interactive() {
		t.Error("--json on a TTY must not be interactive")
	}

	stubTTY(t, false)
	if (&Deps{}).interactive() {
		t.Error("a non-TTY should not be interactive")
	}
}

// captureStderr collects what fn writes to os.Stderr. confirmAction and the
// picker write there directly, so this is how a test proves nothing prompted.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	orig := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stderr = w
	var buf bytes.Buffer
	done := make(chan struct{})
	go func() {
		_, _ = io.Copy(&buf, r)
		close(done)
	}()
	fn()
	os.Stderr = orig
	w.Close()
	<-done
	r.Close()
	return buf.String()
}

// Completing or cancelling a project asks for confirmation. Under --json that
// prompt would hang a machine reader, so it must decline without printing.
func TestConfirmActionJSONDeclines(t *testing.T) {
	stubTTY(t, true)

	var answer bool
	prompt := captureStderr(t, func() {
		answer = confirmAction(&Deps{JSON: true}, "Really?")
	})
	if answer {
		t.Error("--json must decline rather than prompt")
	}
	if prompt != "" {
		t.Errorf("--json wrote a prompt: %q", prompt)
	}
}

// seedAmbiguousDB gives two open tasks with the same title, so any substring
// lookup is ambiguous.
func seedAmbiguousDB(t *testing.T) *db.DB {
	t.Helper()
	sqlDB := dbtest.NewSQL(t)
	fx := dbtest.NewFixture(t, sqlDB)
	fx.Todo("amb-1", "Shared title", 0, dbtest.InProject("proj-x"))
	fx.Todo("amb-2", "Shared title", 1)
	fx.Project("proj-x", "Chores", 2)
	return db.NewFromSQL(sqlDB)
}

// On a TTY, --json must return the ambiguity as an error instead of printing
// the picker and blocking on stdin.
func TestResolveTaskJSONDoesNotPrompt(t *testing.T) {
	stubTTY(t, true)
	database := seedAmbiguousDB(t)

	_, err := resolveTask(&Deps{JSON: true}, "Shared", database)
	if err == nil {
		t.Fatal("expected an ambiguity error, not a prompt")
	}
	var ambig *db.AmbiguousTaskError
	if !errors.As(err, &ambig) {
		t.Fatalf("error should carry the candidates: %T: %v", err, err)
	}
	if len(ambig.Matches) != 2 {
		t.Errorf("got %d matches, want 2", len(ambig.Matches))
	}
}

// The plain-text message is unchanged by the typed wrapper.
func TestResolveTaskAmbiguousPlainTextUnchanged(t *testing.T) {
	stubTTY(t, false)
	database := seedAmbiguousDB(t)

	_, err := resolveTask(&Deps{}, "Shared", database)
	if err == nil {
		t.Fatal("expected an ambiguity error")
	}
	msg := err.Error()
	if !strings.HasPrefix(msg, `ambiguous task "Shared" — matches 2 tasks:`) {
		t.Errorf("message changed: %q", msg)
	}
	if !strings.HasSuffix(msg, "Re-run with a UUID or more specific string.") {
		t.Errorf("message changed: %q", msg)
	}
}

// A candidate whose title holds a line break is listed on one line, so it
// cannot read as two candidates.
func TestResolveTaskAmbiguousTitleOnOneLine(t *testing.T) {
	stubTTY(t, false)
	sqlDB := dbtest.NewSQL(t)
	fx := dbtest.NewFixture(t, sqlDB)
	fx.Todo("nl-1", "Pack\nbags", 0)
	fx.Todo("nl-2", "Pack\tboxes", 1)

	_, err := resolveTask(&Deps{}, "Pack", db.NewFromSQL(sqlDB))
	if err == nil {
		t.Fatal("expected an ambiguity error")
	}
	msg := err.Error()
	for _, want := range []string{"  1. Pack bags  [task]  (nl-1)\n", "  2. Pack boxes  [task]  (nl-2)\n"} {
		if !strings.Contains(msg, want) {
			t.Errorf("message missing %q:\n%s", want, msg)
		}
	}
}

func decodePayload(t *testing.T, err error) (jsonErrorPayload, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	renderError(&stdout, &stderr, true, err)
	if stderr.Len() != 0 {
		t.Errorf("--json wrote to stderr: %q", stderr.String())
	}
	var payload jsonErrorPayload
	if jsonErr := json.Unmarshal(stdout.Bytes(), &payload); jsonErr != nil {
		t.Fatalf("stdout is not JSON (%v): %q", jsonErr, stdout.String())
	}
	return payload, stdout.String()
}

func TestRenderErrorAmbiguous(t *testing.T) {
	stubTTY(t, true)
	database := seedAmbiguousDB(t)

	_, err := resolveTask(&Deps{JSON: true}, "Shared", database)
	if err == nil {
		t.Fatal("expected an ambiguity error")
	}
	payload, raw := decodePayload(t, err)

	if payload.Error != "ambiguous task" {
		t.Errorf("error = %q, want %q (%s)", payload.Error, "ambiguous task", raw)
	}
	if payload.Query != "Shared" {
		t.Errorf("query = %q", payload.Query)
	}
	if payload.Message == "" {
		t.Error("message should carry the human text")
	}
	if len(payload.Matches) != 2 {
		t.Fatalf("matches = %+v", payload.Matches)
	}
	byUUID := map[string]jsonErrorMatch{}
	for _, m := range payload.Matches {
		byUUID[m.UUID] = m
	}
	if got := byUUID["amb-1"]; got.Title != "Shared title" || got.Project != "Chores" {
		t.Errorf("amb-1 = %+v", got)
	}
	if got, ok := byUUID["amb-2"]; !ok || got.Project != "" {
		t.Errorf("amb-2 = %+v (ok=%v)", got, ok)
	}
}

func TestRenderErrorTaskNotFound(t *testing.T) {
	stubTTY(t, true)
	database := db.NewFromSQL(dbtest.NewSQL(t))

	_, err := resolveTask(&Deps{JSON: true}, "no-such-task", database)
	if err == nil {
		t.Fatal("expected a not-found error")
	}
	payload, raw := decodePayload(t, err)

	if payload.Error != "not found" {
		t.Errorf("error = %q, want %q (%s)", payload.Error, "not found", raw)
	}
	if payload.Kind != "task" || payload.Query != "no-such-task" {
		t.Errorf("kind = %q, query = %q", payload.Kind, payload.Query)
	}
	if len(payload.Matches) != 0 {
		t.Errorf("matches should be omitted: %s", raw)
	}
}

func TestRenderErrorStaleIndex(t *testing.T) {
	payload, _ := decodePayload(t, &notFoundError{
		Kind:  "task",
		Query: "3",
		msg:   "task #3 no longer exists (stale list cache — re-run list)",
	})
	if payload.Error != "not found" || payload.Kind != "task" || payload.Query != "3" {
		t.Errorf("payload = %+v", payload)
	}
	if !strings.Contains(payload.Message, "stale list cache") {
		t.Errorf("message = %q", payload.Message)
	}
}

// A numeric ref refused for age gets its own token, so an agent can tell "the
// row numbers moved, re-list" apart from "the item is gone" (issue #265).
func TestRenderErrorStaleListCache(t *testing.T) {
	payload, raw := decodePayload(t, &staleCacheError{
		cacheRef: cacheRef{Query: "3"},
		Row:      3,
		Last: cache.LastList{
			WrittenAt: time.Now().Add(-3 * 24 * time.Hour),
			Command:   "things today",
			UUIDs:     []string{"a", "b", "c"},
		},
	})
	if payload.Error != "stale list cache" {
		t.Errorf("error = %q, want %q (%s)", payload.Error, "stale list cache", raw)
	}
	if payload.Kind != "task" || payload.Query != "3" {
		t.Errorf("payload = %+v", payload)
	}
	if !strings.Contains(payload.Message, "things today") {
		t.Errorf("message = %q, want the listing named", payload.Message)
	}
}

// Anything without a classification still comes back as JSON, so a consumer
// reading stdout never has to fall back to parsing stderr.
func TestRenderErrorGeneric(t *testing.T) {
	payload, raw := decodePayload(t, fmt.Errorf("querying tasks: disk gone"))
	if payload.Error != "error" {
		t.Errorf("error = %q, want %q (%s)", payload.Error, "error", raw)
	}
	if payload.Message != "querying tasks: disk gone" {
		t.Errorf("message = %q", payload.Message)
	}
	if payload.Kind != "" || payload.Query != "" {
		t.Errorf("unclassified payload should carry no kind/query: %+v", payload)
	}
}

// errorPayloadCases is one error of every type errorPayload classifies, with
// the variants that fill different fields, and the exact --json output each
// prints, compacted. errorPayload maps types onto the wire format, so a change
// there must leave every one of these byte for byte as it is.
var errorPayloadCases = []struct {
	name string
	err  error
	want string
}{
	{"ambiguous", &db.AmbiguousTaskError{Query: "Shared", Matches: []model.Task{
		{UUID: "u1", Title: "Shared one", ProjectTitle: "Chores"},
		{UUID: "u2", Title: "Shared two", Type: model.TypeProject},
	}},
		`{"error":"ambiguous task","message":"ambiguous task: \"Shared\" matches 2 tasks","kind":"task","query":"Shared","matches":[{"uuid":"u1","title":"Shared one","type":"task","project":"Chores"},{"uuid":"u2","title":"Shared two","type":"project"}]}`},
	{"ambiguous ref", &ambiguousRefError{msg: "pick one", inner: &db.AmbiguousTaskError{Query: "Shared", Matches: []model.Task{{UUID: "u1", Title: "Shared one"}}}},
		`{"error":"ambiguous task","message":"pick one","kind":"task","query":"Shared","matches":[{"uuid":"u1","title":"Shared one","type":"task"}]}`},
	{"task not found", &db.TaskNotFoundError{Query: "nope"},
		`{"error":"not found","message":"task not found: nope","kind":"task","query":"nope"}`},
	{"import refused", &importRefusalError{items: []importRefusalItem{{Path: "items[0]", ID: "i1", Title: "T", Kind: "task", Blocked: []string{"title"}, blank: true}}, total: 1, size: 1},
		`{"error":"import refused","message":"Things creates a to-do or project with no title, or only whitespace, as an untitled item, so these are refused; give each a title:\n  items[0]\nNothing was sent to Things — fix these and run the import again, or make the changes in the Things app.","items":[{"path":"items[0]","id":"i1","title":"T","blocked":["title"],"reason":"blank-title"}]}`},
	{"import refused oversize", &importRefusalError{total: maxImportItems + 1, size: maxImportItems + 1},
		`{"error":"import refused","message":"The payload creates 201 items (update items and checklist items not counted). Above 200, Things stops to ask \"Is this what you intended?\" and creates nothing until someone answers, so the import cannot be read back. Split it into imports of at most 200 items each.\nNothing was sent to Things — fix these and run the import again, or make the changes in the Things app.","reason":"too-many-items"}`},
	{"import partially applied", &importVerifyError{
		items:   []importVerifyItem{{Path: "items[0]", ID: "i1", Title: "T", Wanted: model.StatusCompleted, err: errors.New("still open")}},
		total:   1,
		created: []importCreated{{Path: "items[1]", Kind: "task", Title: "New", UUID: "n1", Confirmed: true, Reason: "creation-date"}},
	},
		`{"error":"import partially applied","message":"1 of 1 requested status changes did not apply. The rest of the import was still applied; re-run the import with only the failed items, or make the changes in the Things app:\n  items[0]: still open\nThe other created items:\n  Created and confirmed: items[1] \"New\" (n1)","items":[{"path":"items[0]","id":"i1","title":"T","wanted":"completed"}],"created":[{"path":"items[1]","kind":"task","title":"New","uuid":"n1","confirmed":true,"reason":"creation-date"}]}`},
	{"not a project", &wrongKindError{Token: "not a project", Kind: "task", Query: "q", UUID: "u1", Title: "T", Retry: "edit"},
		`{"error":"not a project","message":"\"T\" is a task; use edit","kind":"task","query":"q","uuid":"u1","title":"T"}`},
	{"wrong kind without token", &wrongKindError{Kind: "project", Query: "q", UUID: "u1", Title: "T", Retry: "project edit"},
		`{"error":"error","message":"\"T\" is a project; use project edit","kind":"project","query":"q","uuid":"u1","title":"T"}`},
	{"trashed", &trashedError{Kind: "task", Query: "q", UUID: "u1", Title: "T", Done: "completed"},
		`{"error":"trashed","message":"\"T\" (u1) is in the Trash, so it was not completed; nothing sent. Put it back from the Trash in Things first if you meant this task","kind":"task","query":"q","uuid":"u1","title":"T"}`},
	{"trashed project", &trashedError{Kind: "task", Query: "q", UUID: "u1", Title: "T", Done: "completed", Project: "P"},
		`{"error":"trashed","message":"\"T\" (u1) was not completed: its project \"P\" is in the Trash; nothing sent. Restore the project in Things first if you meant this task","kind":"task","query":"q","uuid":"u1","title":"T","project":"P"}`},
	{"misfiled", &misfiledError{msg: "filed elsewhere", kind: "task", title: "T", uuid: "u1", landed: "inbox"},
		`{"error":"misfiled","message":"filed elsewhere","kind":"task","uuid":"u1","title":"T","landed":"inbox"}`},
	{"stale cache", &staleCacheError{cacheRef: cacheRef{Query: "2"}, Row: 2},
		`{"error":"stale list cache","message":"task #2 comes from a stale list cache written by an older things-cli; re-run your listing and use the new row number, or pass the task's uuid.","kind":"task","query":"2"}`},
	{"other db cache", &otherDBCacheError{cacheRef: cacheRef{Query: "2"}, Row: 2, Last: cache.LastList{DB: "/a.sqlite"}, Current: "/b.sqlite"},
		`{"error":"stale list cache","message":"task #2 comes from a listing of a different database: the listing read /a.sqlite, and this command reads /b.sqlite. Re-run the listing against this database and use the new row number, or pass the task's uuid.","kind":"task","query":"2"}`},
	{"unreadable cache", &unreadableCacheError{cacheRef: cacheRef{Query: "2"}, Err: cache.ErrUnreadable},
		`{"error":"stale list cache","message":"\"2\" may be a row of the last list, but the last-list cache could not be read; run a listing again or use the task's title or uuid","kind":"task","query":"2"}`},
	{"empty ref", &emptyRefError{Query: " "},
		`{"error":"empty reference","message":"the task reference is empty; pass a row number, a uuid or a title","kind":"task","query":" "}`},
	{"closed title", &closedTitleError{Query: "T", Task: model.Task{UUID: "u1", Title: "T", Status: model.StatusCompleted}},
		`{"error":"already closed","message":"no open task is titled \"T\": the only task with that title, \"T\" (u1), is already completed. Nothing sent, and no open task with a similar title was touched; pass its uuid to act on a particular one","kind":"task","query":"T","uuid":"u1","title":"T"}`},
	{"closed title trashed", &closedTitleError{Query: "T", Task: model.Task{UUID: "u1", Title: "T", Type: model.TypeProject, Trashed: true}},
		`{"error":"trashed","message":"no open task is titled \"T\": the only project with that title, \"T\" (u1), is in the Trash. Nothing sent, and no open task with a similar title was touched; pass its uuid to act on a particular one","kind":"project","query":"T","uuid":"u1","title":"T"}`},
	{"closed title project trashed", &closedTitleError{Query: "T", Task: model.Task{UUID: "u1", Title: "T", ProjectTrashed: true, ProjectTitle: "P"}},
		`{"error":"trashed","message":"no open task is titled \"T\": the only task with that title, \"T\" (u1), is in the Trash with its project \"P\". Nothing sent, and no open task with a similar title was touched; pass its uuid to act on a particular one","kind":"task","query":"T","uuid":"u1","title":"T","project":"P"}`},
	{"closed title matches", &closedTitleError{Query: "T", Task: model.Task{UUID: "u1", Title: "T", Status: model.StatusCancelled}, Matches: []model.Task{
		{UUID: "u1", Title: "T", Status: model.StatusCancelled},
		{UUID: "u2", Title: "T", Status: model.StatusCompleted},
	}},
		`{"error":"already closed","message":"no open task is titled \"T\": 2 closed or trashed items have that title, the most recent being \"T\" (u1), which is already cancelled. Nothing sent, and no open task with a similar title was touched; pass its uuid to act on a particular one","kind":"task","query":"T","uuid":"u1","title":"T","matches":[{"uuid":"u1","title":"T","type":"task"},{"uuid":"u2","title":"T","type":"task"}]}`},
	{"closed switch", &closedSwitchError{task: &model.Task{UUID: "u1", Title: "T", Type: model.TypeProject, Status: model.StatusCompleted}, want: model.StatusCancelled},
		`{"error":"already closed","message":"\"T\" is already completed, so it was not cancelled; nothing sent","kind":"project","uuid":"u1","title":"T"}`},
	{"blank title", &blankTitleError{Kind: "project", Done: "added"},
		`{"error":"blank-title","message":"the title is blank, so the project was not added; nothing sent. Things would leave it untitled: give it a title","kind":"project"}`},
	{"not found", &notFoundError{Kind: "area", Query: "Work"},
		`{"error":"not found","message":"area not found: Work","kind":"area","query":"Work"}`},
	{"wrapped", fmt.Errorf("editing: %w", &trashedError{Kind: "project", Query: "q", UUID: "u1", Title: "T", Done: "edited"}),
		`{"error":"trashed","message":"editing: \"T\" (u1) is in the Trash, so it was not edited; nothing sent. Put it back from the Trash in Things first if you meant this project","kind":"project","query":"q","uuid":"u1","title":"T"}`},
	{"generic", errors.New("disk gone"),
		`{"error":"error","message":"disk gone"}`},
}

func TestErrorPayloadGolden(t *testing.T) {
	for _, tc := range errorPayloadCases {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			renderError(&stdout, &stderr, true, tc.err)
			// Compact drops only the indentation PrintJSON adds; the keys,
			// their order and every value stay as printed.
			var got bytes.Buffer
			if err := json.Compact(&got, stdout.Bytes()); err != nil {
				t.Fatalf("stdout is not JSON (%v): %q", err, stdout.String())
			}
			if got.String() != tc.want {
				t.Errorf("--json output changed\ngot:  %s\nwant: %s", got.String(), tc.want)
			}
		})
	}
}

// errorPayload takes the outermost payloader in err's chain. That agrees with
// the fixed order it used to check the types in only while no payloader wraps
// another.
func TestNoPayloaderWrapsAnother(t *testing.T) {
	for _, tc := range errorPayloadCases {
		var p payloader
		if !errors.As(tc.err, &p) {
			continue
		}
		for inner := errors.Unwrap(p.(error)); inner != nil; inner = errors.Unwrap(inner) {
			if _, ok := inner.(payloader); ok {
				t.Errorf("%s: %T wraps payloader %T", tc.name, p, inner)
			}
		}
	}
}

// The message is meant to read as the plain-text line does, so the encoder
// must not turn <, > and & into escape sequences.
func TestRenderErrorDoesNotEscapeHTML(t *testing.T) {
	var stdout, stderr bytes.Buffer
	renderError(&stdout, &stderr, true, fmt.Errorf(`expected "<task>" & more`))

	if !strings.Contains(stdout.String(), `expected \"<task>\" & more`) {
		t.Errorf("message was escaped: %s", stdout.String())
	}
}

// Plain-text mode is untouched: the line still goes to stderr, and stdout stays
// empty so a `things ... > file` redirect is unaffected.
func TestRenderErrorPlainTextUnchanged(t *testing.T) {
	var stdout, stderr bytes.Buffer
	renderError(&stdout, &stderr, false, fmt.Errorf("boom"))

	if stdout.Len() != 0 {
		t.Errorf("plain text wrote to stdout: %q", stdout.String())
	}
	if stderr.String() != "Error: boom\n" {
		t.Errorf("stderr = %q", stderr.String())
	}
}

// Every command that resolves a task reference reports ambiguity as JSON under
// --json, rather than printing the picker and waiting on stdin (issue #152).
func TestCommandsEmitJSONErrors(t *testing.T) {
	cases := []struct {
		name      string
		args      []string
		wantError string
		wantKind  string
		wantQuery string
	}{
		{"show ambiguous", []string{"--json", "show", "Shared"}, "ambiguous task", "task", "Shared"},
		{"edit ambiguous", []string{"--json", "edit", "Shared", "--title", "New"}, "ambiguous task", "task", "Shared"},
		{"complete ambiguous", []string{"--json", "complete", "Shared"}, "ambiguous task", "task", "Shared"},
		{"cancel ambiguous", []string{"--json", "cancel", "Shared"}, "ambiguous task", "task", "Shared"},
		{"open ambiguous", []string{"--json", "open", "Shared"}, "ambiguous task", "task", "Shared"},
		{"show missing", []string{"--json", "show", "no-such-ref"}, "not found", "task", "no-such-ref"},
		{"complete missing", []string{"--json", "complete", "no-such-ref"}, "not found", "task", "no-such-ref"},
		{"open missing area", []string{"--json", "open", "--area", "Nowhere"}, "not found", "area", "Nowhere"},
		{"open missing tag", []string{"--json", "open", "--tag", "nope"}, "not found", "tag", "nope"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stubTTY(t, true)
			database := seedAmbiguousDB(t)

			err := runWith(t, database, tc.args...)
			if err == nil {
				t.Fatalf("run %v: expected an error", tc.args)
			}
			payload, raw := decodePayload(t, err)
			if payload.Error != tc.wantError {
				t.Errorf("error = %q, want %q (%s)", payload.Error, tc.wantError, raw)
			}
			if payload.Kind != tc.wantKind {
				t.Errorf("kind = %q, want %q", payload.Kind, tc.wantKind)
			}
			if payload.Query != tc.wantQuery {
				t.Errorf("query = %q, want %q", payload.Query, tc.wantQuery)
			}
		})
	}
}

// The title a project and a to-do share reaches the caller as an ambiguity
// from both commands, rather than resolving to one row and advising a retry
// that acts on the other (issue #194). The kind rides along on each candidate
// so the caller knows which command the uuid it picks belongs to.
func TestSharedTitleIsAmbiguousForBothCommands(t *testing.T) {
	for _, args := range [][]string{
		{"--json", "edit", "Chores", "--title", "New"},
		{"--json", "project", "edit", "Chores", "--title", "New"},
	} {
		t.Run(args[1], func(t *testing.T) {
			stubTTY(t, true)
			sqlDB := dbtest.NewSQL(t)
			fx := dbtest.NewFixture(t, sqlDB)
			fx.Project("proj-chores", "Chores", 0)
			fx.Todo("todo-chores", "Chores", 1)

			err := runWith(t, db.NewFromSQL(sqlDB), args...)
			if err == nil {
				t.Fatalf("run %v: expected an error", args)
			}
			payload, raw := decodePayload(t, err)
			if payload.Error != "ambiguous task" {
				t.Fatalf("error = %q, want %q (%s)", payload.Error, "ambiguous task", raw)
			}
			byUUID := map[string]jsonErrorMatch{}
			for _, m := range payload.Matches {
				byUUID[m.UUID] = m
			}
			if len(byUUID) != 2 {
				t.Fatalf("matches = %+v, want both rows", payload.Matches)
			}
			if got := byUUID["proj-chores"].Type; got != model.TypeProject {
				t.Errorf("proj-chores type = %v (%s)", got, raw)
			}
			if got := byUUID["todo-chores"].Type; got != model.TypeTask {
				t.Errorf("todo-chores type = %v (%s)", got, raw)
			}
		})
	}
}

// search doesn't resolve a reference, so its failures are unclassified — they
// still have to reach a --json consumer as JSON rather than a bare stderr line.
func TestSearchErrorEmitsJSON(t *testing.T) {
	database := seedAmbiguousDB(t)
	database.Close()

	err := runWith(t, database, "--json", "search", "Shared")
	if err == nil {
		t.Fatal("expected a query error against a closed database")
	}
	payload, raw := decodePayload(t, err)
	if payload.Error != "error" {
		t.Errorf("error = %q, want %q (%s)", payload.Error, "error", raw)
	}
	if payload.Message == "" {
		t.Errorf("message should carry the failure: %s", raw)
	}
}

// A parse error is still a failure a --json consumer has to read off stdout,
// so main has to know the flag was asked for before kong has parsed it.
func TestJSONRequested(t *testing.T) {
	cases := []struct {
		args []string
		want bool
	}{
		{[]string{"show", "milk"}, false},
		{[]string{"--json", "show"}, true},
		{[]string{"show", "milk", "--json"}, true},
		{[]string{"show", "-j"}, true},
		{[]string{"--json=true", "show"}, true},
		{[]string{"--json=false", "show"}, false},
		{[]string{"--json", "--json=false", "show"}, false},
		{[]string{"add", "--", "--json"}, false},
		// kong's bool mapper takes true/1/yes and false/0/no, case
		// insensitively, so these are the same flag spelled differently.
		{[]string{"--json=1", "show"}, true},
		{[]string{"--json=yes", "show"}, true},
		{[]string{"--json=YES", "show"}, true},
		{[]string{"--json", "--json=0", "show"}, false},
		{[]string{"--json", "--json=no", "show"}, false},
		{[]string{"--json", "--json=No", "show"}, false},
		// Values kong rejects: it will fail the parse, so the mode is left
		// alone rather than guessed at. "T" is one of these — strconv.ParseBool
		// would take it, kong does not.
		{[]string{"--json=T", "show"}, false},
		{[]string{"--json=maybe", "show"}, false},
		// kong clusters boolean shorts, so -j can arrive inside one. Scanning
		// stops at the first short that takes a value, because that one takes
		// the rest of the cluster as its value.
		{[]string{"-jv"}, true},
		{[]string{"-vj", "show"}, true},
		{[]string{"list", "-pj"}, false},
		{[]string{"import", "-fj"}, false},
		{[]string{"-j=false", "show"}, false},
		{[]string{"-vj=false", "show"}, false},
		{[]string{"-jv=false", "show"}, true},
		{[]string{"-", "show"}, false},
		{[]string{"--jsonish", "show"}, false},
	}
	for _, tc := range cases {
		if got := jsonRequested(false, tc.args); got != tc.want {
			t.Errorf("jsonRequested(false, %v) = %v, want %v", tc.args, got, tc.want)
		}
	}

	// The config file supplies the starting value, so argv has to be able to
	// turn it off — and a value kong will reject leaves it alone rather than
	// silently flipping the mode.
	if !jsonRequested(true, []string{"show"}) {
		t.Error("config default should carry through")
	}
	if jsonRequested(true, []string{"--json=false", "show"}) {
		t.Error("argv should be able to turn the config default off")
	}
	if jsonRequested(true, []string{"--json=no", "show"}) {
		t.Error("argv should be able to turn the config default off with =no")
	}
	if !jsonRequested(true, []string{"--json=maybe", "show"}) {
		t.Error("a value kong would reject should leave the mode alone")
	}
}

// kongBool has to agree with kong's own bool mapper: the pre-parse read of
// --json decides how a parse failure is rendered, so a value kong accepts must
// not be ignored here, and one it rejects must not flip the mode.
func TestKongBoolMatchesKong(t *testing.T) {
	for _, v := range []string{"true", "1", "yes", "TRUE", "Yes", "false", "0", "no", "No", "t", "T", "f", "maybe", ""} {
		var cli CLI
		parser, err := kong.New(&cli, kong.Name("things"),
			kong.Vars{
				"builtin_lists": strings.Join(things.BuiltinLists, ", "),
				"skill_agents":  skill.AgentNames(),
			},
			helpVars,
		)
		if err != nil {
			t.Fatalf("kong.New: %v", err)
		}
		_, parseErr := parser.Parse([]string{"--json=" + v, "projects"})

		got, ok := kongBool(v)
		if ok != (parseErr == nil) {
			t.Errorf("kongBool(%q) accepted = %v, but kong parse error = %v", v, ok, parseErr)
			continue
		}
		if ok && got != cli.JSON {
			t.Errorf("kongBool(%q) = %v, kong parsed %v", v, got, cli.JSON)
		}
	}
}

// boolShorts is hand-maintained, so check it still describes the real grammar:
// every letter in it must be boolean on every command that uses it, and no
// value-taking flag may share a letter with it.
func TestBoolShortsMatchesGrammar(t *testing.T) {
	var cli CLI
	parser, err := kong.New(&cli, kong.Name("things"),
		kong.Vars{
			"builtin_lists": strings.Join(things.BuiltinLists, ", "),
			"skill_agents":  skill.AgentNames(),
		},
		helpVars,
	)
	if err != nil {
		t.Fatalf("kong.New: %v", err)
	}

	// letter -> whether every flag spelled with it takes no value.
	allBool := map[byte]bool{}
	var walk func(n *kong.Node)
	walk = func(n *kong.Node) {
		for _, f := range n.Flags {
			if f.Short == 0 {
				continue
			}
			c := byte(f.Short)
			isBool := f.IsBool()
			if prev, seen := allBool[c]; seen {
				isBool = isBool && prev
			}
			allBool[c] = isBool
		}
		for _, child := range n.Children {
			walk(child)
		}
	}
	walk(parser.Model.Node)

	for c := range boolShorts {
		if !allBool[c] {
			t.Errorf("boolShorts has %q, but the grammar has a value-taking flag spelled -%c", c, c)
		}
	}
	for c, isBool := range allBool {
		if isBool && !boolShorts[c] && c != 'h' {
			t.Errorf("-%c is a boolean short the grammar knows and boolShorts does not — a cluster like -%cj would be missed", c, c)
		}
	}
}

// Declining for lack of a terminal is not the same as a user answering "n" —
// under --json the payload has to say why, or the caller just sees "cancelled".
func TestConfirmProjectStatusChangeNonInteractiveExplains(t *testing.T) {
	stubTTY(t, true)

	err := confirmProjectStatusChange(&Deps{JSON: true}, false, "Complete", "Chores")
	if err == nil {
		t.Fatal("--json must decline")
	}
	if !strings.Contains(err.Error(), "cancelled") {
		t.Errorf("message should still read as cancelled: %q", err)
	}
	if !strings.Contains(err.Error(), "cannot prompt") {
		t.Errorf("message should explain why: %q", err)
	}
	if !strings.Contains(err.Error(), "without --json") {
		t.Errorf("--json run should point at the flag: %q", err)
	}

	// Piped stdin without --json hits the same branch; the advice there is to
	// use a terminal, not to drop a flag that was never passed.
	stubTTY(t, false)
	err = confirmProjectStatusChange(&Deps{}, false, "Cancel", "Chores")
	if err == nil {
		t.Fatal("a non-terminal run must decline")
	}
	if strings.Contains(err.Error(), "--json") {
		t.Errorf("should not mention a flag the caller did not pass: %q", err)
	}
	if !strings.Contains(err.Error(), "in a terminal") {
		t.Errorf("message should explain why: %q", err)
	}
}
