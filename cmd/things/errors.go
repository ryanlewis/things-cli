package main

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/ryanlewis/things-cli/internal/cache"
	"github.com/ryanlewis/things-cli/internal/db"
	"github.com/ryanlewis/things-cli/internal/model"
	"github.com/ryanlewis/things-cli/internal/output"
)

// jsonErrorPayload is the machine-readable form of a command failure. Under
// --json a failing command prints one of these to stdout and exits non-zero,
// so a consumer reading stdout sees a JSON failure rather than English prose,
// and can branch on the "error" token (issue #152). On success the read
// commands print their result; add, project add, edit and project edit print
// the item, complete and cancel the item they closed, tag add what it
// created, and import a verdict per item it created.
//
// Error is a stable token: "ambiguous task", "not found", "not a task",
// "not a project", "trashed", "stale list cache", "already closed",
// "misfiled", "import refused", "import partially applied", or "error" for a
// failure with no structure worth naming.
// Message is the same text the plain-text path prints, for a human reading
// the JSON.
type jsonErrorPayload struct {
	Error   string `json:"error"`
	Message string `json:"message"`
	// Kind names the sort of thing the failure is about, in the CLI's own
	// vocabulary: "task", "project", "area" or "tag". A to-do is a "task"
	// here as it is in `type` on a task row — no JSON value the CLI emits
	// spells it "to-do", which is the `import` payload's word (issue #219).
	// The prose follows the same vocabulary: Message and the agent brief say
	// "task" too, so a reader meets one word for one thing (issue #245).
	Kind  string `json:"kind,omitempty"`
	Query string `json:"query,omitempty"`
	UUID  string `json:"uuid,omitempty"`
	Title string `json:"title,omitempty"`
	// Landed is where Things filed an item --when did not file as sent.
	Landed  string           `json:"landed,omitempty"`
	Matches []jsonErrorMatch `json:"matches,omitempty"`
	Items   []jsonErrorItem  `json:"items,omitempty"`
	// Created is the verdict on every item a partially applied import
	// created, in the shape a successful import prints, so the confirmed
	// ones keep their uuids.
	Created []importCreated `json:"created,omitempty"`
	// Reason is why a whole import was refused, beside the per-item reasons
	// in Items: "too-many-items" for a payload over the size Things takes
	// without asking.
	Reason string `json:"reason,omitempty"`
}

// jsonErrorItem is one item of a batch failure. An import acts on many items
// at once, so a caller needs to know which of them failed and why rather than
// reading it back out of the message (issue #161).
//
// Three failures share the shape, and each fills the part that applies:
// a refusal sets Blocked, naming the attributes Things will not accept on that
// item; a status read-back failure sets Wanted and Got, naming the status the
// payload asked for and the one the item is still in. Got is empty when there
// was nothing to observe — the row could not be read, or no longer exists. A
// created item that never appeared, or that a dated item's row could pass
// for when too few new items appeared to account for both, sets Confirmed
// (always false), Reason ("not-found" or "shares-dated-title") and
// Candidates, as an unconfirmed add does; one saved
// without the completion-date the payload gives it sets Confirmed (false),
// Reason ("completion-date-dropped") and ID, its uuid, since it is there.
type jsonErrorItem struct {
	Path       string   `json:"path"`
	ID         string   `json:"id,omitempty"`
	Title      string   `json:"title,omitempty"`
	Blocked    []string `json:"blocked,omitempty"`
	Wanted     string   `json:"wanted,omitempty"`
	Got        string   `json:"got,omitempty"`
	Confirmed  *bool    `json:"confirmed,omitempty"`
	Reason     string   `json:"reason,omitempty"`
	Candidates []string `json:"candidates,omitempty"`
	// Present is given on a shares-dated-title item only, as on the
	// created record (see importCreated).
	Present *bool `json:"present,omitempty"`
}

// jsonErrorMatch is one candidate of an ambiguous reference — enough for a
// caller to pick one and retry with the UUID.
//
// Type is there because the candidates need not be the same kind of thing: an
// exact title can match a project and a to-do at once, and which one the
// caller meant decides whether the retry is `things edit <uuid>` or
// `things project edit <uuid>` (issue #194).
type jsonErrorMatch struct {
	UUID    string         `json:"uuid"`
	Title   string         `json:"title"`
	Type    model.TaskType `json:"type"`
	Project string         `json:"project,omitempty"`
}

// notFoundError is a lookup that resolved to nothing. Kind names what was
// looked up ("task", "area", "tag") and Query is the reference the user gave.
// msg overrides the rendered text where the caller has something more specific
// to say than "<kind> not found: <query>".
type notFoundError struct {
	Kind  string
	Query string
	msg   string
}

func (e *notFoundError) Error() string {
	if e.msg != "" {
		return e.msg
	}
	return fmt.Sprintf("%s not found: %s", e.Kind, e.Query)
}

// wrongKindError is a reference that resolved to the wrong sort of item for
// the command: a project handed to `edit`, which would otherwise open
// things:///update with a project id and leave Things showing a "does not
// exist" dialog (issue #189), or a task handed to `project edit` (issue
// #191). Kind names what the reference turned out to be, so a caller can
// retry against the right command.
type wrongKindError struct {
	// Token is the stable --json error token for this direction of the
	// mistake — "not a task" or "not a project". It names what the command
	// wanted, while Kind names what it got.
	Token string
	Kind  string
	Query string
	UUID  string
	Title string
	// Retry is the command that does handle this kind, spelled out rather
	// than derived from Kind — "project" happens to read as a command name,
	// but "task" does not.
	Retry string
}

func (e *wrongKindError) Error() string {
	return fmt.Sprintf("%q is a %s; use %s", e.Title, e.Kind, e.Retry)
}

// trashedError is a write refused because its target is in the Trash. Only a
// row number or a uuid can reach a trashed item, since the title lookups skip
// the Trash, and a row number can point at one when the item was trashed in
// Things after the listing was printed. Acting on it would change an item the
// user can no longer see, so `complete`, `cancel`, `edit` and `project edit`
// refuse it and send nothing.
type trashedError struct {
	Kind  string // "task" or "project"
	Query string
	UUID  string
	Title string
	// Done is what the refused write would have made of the item, as in
	// "so it was not completed".
	Done string
}

func (e *trashedError) Error() string {
	return fmt.Sprintf("%q (%s) is in the Trash, so it was not %s; nothing sent. Put it back from the Trash in Things first if you meant this %s", e.Title, e.UUID, e.Done, e.Kind)
}

// closedSwitchError is a status change refused because the item is already
// closed the other way: `cancel` on a completed item, or `complete` on a
// cancelled one (see checkClosed). Nothing was sent, so a caller that meant
// the switch has to make it in Things itself.
type closedSwitchError struct {
	task *model.Task
	want model.Status
}

func (e *closedSwitchError) Error() string {
	return fmt.Sprintf("%q is already %s, so it was not %s; nothing sent", e.task.Title, e.task.Status, e.want)
}

// ambiguousRefError carries a *db.AmbiguousTaskError alongside the multi-line
// "pick one" text resolveTask prints in non-interactive mode. Error() returns
// that text unchanged so the plain-text path is untouched, while errors.As
// still reaches the candidates for the JSON payload.
type ambiguousRefError struct {
	msg   string
	inner *db.AmbiguousTaskError
}

func (e *ambiguousRefError) Error() string { return e.msg }

func (e *ambiguousRefError) Unwrap() error { return e.inner }

// staleCacheError is a numeric reference to a listing old enough that its row
// numbers have probably moved (issue #265). The row is refused rather than
// acted on: the UUID behind it still exists, so acting would silently hit
// whatever item has drifted into that position.
type staleCacheError struct {
	Query string // the reference as typed, e.g. "2"
	Row   int
	Last  cache.LastList
}

func (e *staleCacheError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "task #%d comes from a stale list cache", e.Row)
	if e.Last.WrittenAt.IsZero() {
		// A file written before 0.8.0 records no time, so there is nothing
		// to age it by and no listing to name.
		b.WriteString(" written by an older things-cli; re-run your listing")
	} else {
		fmt.Fprintf(&b, ": the rows were listed %s ago, older than the %s a row number is good for", overDuration(time.Since(e.Last.WrittenAt)), humanDuration(cache.MaxAge))
		if e.Last.Command != "" {
			fmt.Fprintf(&b, ". Re-run `%s`", e.Last.Command)
		} else {
			b.WriteString(". Re-run your listing")
		}
	}
	b.WriteString(" and use the new row number, or pass the task's uuid.")
	return b.String()
}

// otherDBCacheError is a numeric reference to a listing that read a different
// database from the one this command reads (issue #274). The listing may be
// fresh, but its rows describe the other database, so they are refused.
type otherDBCacheError struct {
	Query   string // the reference as typed, e.g. "2"
	Row     int
	Last    cache.LastList
	Current string // the resolved path of this command's database
}

func (e *otherDBCacheError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "task #%d comes from a listing of a different database", e.Row)
	listing := "the listing"
	if e.Last.Command != "" {
		listing = fmt.Sprintf("`%s`", e.Last.Command)
	}
	if e.Last.DB == "" {
		// Written before the database was recorded: nothing to name.
		fmt.Fprintf(&b, ": %s was written by an older things-cli that did not record its database", listing)
	} else {
		fmt.Fprintf(&b, ": %s read %s", listing, e.Last.DB)
	}
	if e.Current != "" {
		fmt.Fprintf(&b, ", and this command reads %s", e.Current)
	}
	b.WriteString(". Re-run the listing against this database and use the new row number, or pass the task's uuid.")
	return b.String()
}

// humanDuration renders a span the way the error message needs to read it:
// coarse, and never more precise than the reader can act on. Duration's own
// String would print "4h0m0s".
func humanDuration(d time.Duration) string {
	switch {
	case d < time.Hour:
		return plural(int(d.Minutes()), "minute")
	case d < 48*time.Hour:
		return plural(int(d.Hours()), "hour")
	default:
		return plural(int(d.Hours()/24), "day")
	}
}

// overDuration renders a span that is known to exceed the bound it is being
// compared against. humanDuration truncates, so a listing 4h05m old would
// otherwise read as "4 hours ago, older than the 4 hours a row number is good
// for" — a sentence that contradicts itself in the commonest case of all, a
// row number used shortly after it expired. Saying "over 4 hours" keeps the
// comparison true without inventing precision.
func overDuration(d time.Duration) string {
	unit := 24 * time.Hour
	switch {
	case d < time.Hour:
		unit = time.Minute
	case d < 48*time.Hour:
		unit = time.Hour
	}
	if d%unit != 0 {
		return "over " + humanDuration(d)
	}
	return humanDuration(d)
}

func plural(n int, unit string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, unit)
	}
	return fmt.Sprintf("%d %ss", n, unit)
}

// errorPayload classifies err into the JSON shape. It is the single place that
// maps Go error types onto the wire format — add a case here rather than
// formatting JSON at a call site.
func errorPayload(err error) jsonErrorPayload {
	payload := jsonErrorPayload{Error: "error", Message: err.Error()}

	var ambig *db.AmbiguousTaskError
	if errors.As(err, &ambig) {
		payload.Error = "ambiguous task"
		payload.Kind = "task"
		payload.Query = ambig.Query
		payload.Matches = matchList(ambig.Matches)
		return payload
	}

	var notFoundTask *db.TaskNotFoundError
	if errors.As(err, &notFoundTask) {
		payload.Error = "not found"
		payload.Kind = "task"
		payload.Query = notFoundTask.Query
		return payload
	}

	// Batch import failures. The two are kept apart because the recovery
	// differs: a refusal sent nothing, so the payload can be fixed and re-run
	// whole, while a partially applied import already changed things and must
	// be re-run with only the items named here.
	var refused *importRefusalError
	if errors.As(err, &refused) {
		payload.Error = "import refused"
		payload.Items = refused.jsonItems()
		if refused.oversize() {
			payload.Reason = tooManyItems
		}
		return payload
	}

	var unapplied *importVerifyError
	if errors.As(err, &unapplied) {
		payload.Error = "import partially applied"
		payload.Items = unapplied.jsonItems()
		payload.Created = unapplied.created
		return payload
	}

	var wrongKind *wrongKindError
	if errors.As(err, &wrongKind) {
		// Token is what a caller branches on, so an unset one would ship
		// `"error": ""` — a value no consumer can match. Fall back to the
		// generic token instead.
		if wrongKind.Token != "" {
			payload.Error = wrongKind.Token
		}
		payload.Kind = wrongKind.Kind
		payload.Query = wrongKind.Query
		payload.UUID = wrongKind.UUID
		payload.Title = wrongKind.Title
		return payload
	}

	var trashed *trashedError
	if errors.As(err, &trashed) {
		payload.Error = "trashed"
		payload.Kind = trashed.Kind
		payload.Query = trashed.Query
		payload.UUID = trashed.UUID
		payload.Title = trashed.Title
		return payload
	}

	var misfiled *misfiledError
	if errors.As(err, &misfiled) {
		payload.Error = "misfiled"
		payload.Kind = misfiled.kind
		payload.UUID = misfiled.uuid
		payload.Title = misfiled.title
		payload.Landed = misfiled.landed
		return payload
	}

	var stale *staleCacheError
	if errors.As(err, &stale) {
		payload.Error = "stale list cache"
		payload.Kind = "task"
		payload.Query = stale.Query
		return payload
	}

	// The same token as a stale cache: the remedy is the same, re-list and use
	// the uuid.
	var otherDB *otherDBCacheError
	if errors.As(err, &otherDB) {
		payload.Error = "stale list cache"
		payload.Kind = "task"
		payload.Query = otherDB.Query
		return payload
	}

	var closedSwitch *closedSwitchError
	if errors.As(err, &closedSwitch) {
		payload.Error = "already closed"
		payload.Kind = closedSwitch.task.Type.String()
		payload.UUID = closedSwitch.task.UUID
		payload.Title = closedSwitch.task.Title
		return payload
	}

	var notFound *notFoundError
	if errors.As(err, &notFound) {
		payload.Error = "not found"
		payload.Kind = notFound.Kind
		payload.Query = notFound.Query
		return payload
	}

	return payload
}

func matchList(tasks []model.Task) []jsonErrorMatch {
	out := make([]jsonErrorMatch, len(tasks))
	for i, t := range tasks {
		out[i] = jsonErrorMatch{UUID: t.UUID, Title: t.Title, Type: t.Type, Project: t.ProjectTitle}
	}
	return out
}

// renderError writes a failed command's error. Under --json it goes to stdout
// as a single JSON object so the consumer parsing stdout sees the failure;
// otherwise it keeps the plain "Error: ..." line on stderr unchanged.
func renderError(stdout, stderr io.Writer, asJSON bool, err error) {
	if !asJSON {
		fmt.Fprintf(stderr, "Error: %v\n", err)
		return
	}
	// PrintJSON leaves &, < and > unescaped, so Message reads as the plain-text
	// error does: `expected "<task>"` stays as written.
	if encErr := output.PrintJSON(stdout, errorPayload(err)); encErr != nil {
		// Encoding a struct of strings can't realistically fail, but a broken
		// stdout can — fall back to the plain line so the failure isn't silent.
		fmt.Fprintf(stderr, "Error: %v\n", err)
	}
}
