package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/ryanlewis/things-cli/internal/cache"
	"github.com/ryanlewis/things-cli/internal/db"
	"github.com/ryanlewis/things-cli/internal/model"
)

// jsonErrorPayload is the machine-readable form of a command failure. Under
// --json a failing command prints one of these to stdout and exits non-zero,
// so a consumer reading stdout sees a JSON failure rather than English prose,
// and can branch on the "error" token (issue #152). A successful write command
// still prints nothing — only the read commands emit JSON on success.
//
// Error is a stable token: "ambiguous task", "not found", "not a task",
// "not a project", "stale list cache", or "error" for a failure with no
// structure worth naming.
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
	Kind    string           `json:"kind,omitempty"`
	Query   string           `json:"query,omitempty"`
	UUID    string           `json:"uuid,omitempty"`
	Title   string           `json:"title,omitempty"`
	Matches []jsonErrorMatch `json:"matches,omitempty"`
	Items   []jsonErrorItem  `json:"items,omitempty"`
}

// jsonErrorItem is one item of a batch failure. An import acts on many items
// at once, so a caller needs to know which of them failed and why rather than
// reading it back out of the message (issue #161).
//
// Two failures share the shape, and each fills the half that applies:
// a refusal sets Blocked, naming the attributes Things will not accept on that
// item; a read-back failure sets Wanted and Got, naming the status the payload
// asked for and the one the item is still in. Got is empty when there was
// nothing to observe — the row could not be read, or no longer exists.
type jsonErrorItem struct {
	Path    string   `json:"path"`
	ID      string   `json:"id,omitempty"`
	Title   string   `json:"title,omitempty"`
	Blocked []string `json:"blocked,omitempty"`
	Wanted  string   `json:"wanted,omitempty"`
	Got     string   `json:"got,omitempty"`
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
		return payload
	}

	var unapplied *importVerifyError
	if errors.As(err, &unapplied) {
		payload.Error = "import partially applied"
		payload.Items = unapplied.jsonItems()
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

	var stale *staleCacheError
	if errors.As(err, &stale) {
		payload.Error = "stale list cache"
		payload.Kind = "task"
		payload.Query = stale.Query
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
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	// Message is meant to read as the plain-text error does. The default
	// encoder escapes the three HTML-significant characters into \u00xx
	// sequences, which turns a message like `expected "<task>"` into line
	// noise for anyone reading the JSON.
	enc.SetEscapeHTML(false)
	if encErr := enc.Encode(errorPayload(err)); encErr != nil {
		// Encoding a struct of strings can't realistically fail, but a broken
		// stdout can — fall back to the plain line so the failure isn't silent.
		fmt.Fprintf(stderr, "Error: %v\n", err)
	}
}
