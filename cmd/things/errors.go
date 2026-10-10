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
// "empty reference", "misfiled", "blank-title", "import refused", "import
// partially applied", or "error" for a failure with no structure worth
// naming. "blank-title" is spelt as the import reason for the same
// refusal, so a caller matches one string for both.
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
	// Project names the trashed project a refused to-do is in, on a
	// "trashed" error about a to-do whose project is in the Trash.
	Project string `json:"project,omitempty"`
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
// item, and Reason, why: "repeating", "invalid-date", "future-date",
// "invalid-type", "invalid-item", "duplicate-key", "too-long" or
// "blank-title", several separated by a space; a status read-back failure
// sets Wanted and Got, naming the status the payload asked for and the one
// the item is still in. Got is empty when there
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
	// Landed is given on a misfiled created item only, as on the created
	// record (see importCreated).
	Landed string `json:"landed,omitempty"`
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

func (e *notFoundError) fillPayload(p *jsonErrorPayload) {
	p.Error = "not found"
	p.Kind = e.Kind
	p.Query = e.Query
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

func (e *wrongKindError) fillPayload(p *jsonErrorPayload) {
	// Token is what a caller branches on, so an unset one would ship
	// `"error": ""` — a value no consumer can match. Fall back to the
	// generic token instead.
	if e.Token != "" {
		p.Error = e.Token
	}
	p.Kind = e.Kind
	p.Query = e.Query
	p.UUID = e.UUID
	p.Title = e.Title
}

// trashedError is a write refused because its target is in the Trash. A row
// number, a uuid or an exact title no open item carries can reach a trashed
// item; a title fragment never does. A row number can point at one when the
// item was trashed in Things after the listing was printed. Acting on it
// would change an item the user can no longer see, so `complete`, `cancel`,
// `edit` and `project edit` refuse it and send nothing.
//
// A to-do whose project is in the Trash is hidden in Things the same way, so
// it gets the same refusal and the same "trashed" token, with Project naming
// the trashed project. Things would accept the write; the refusal keeps the
// CLI to what the user can see.
type trashedError struct {
	Kind  string // "task" or "project"
	Query string
	UUID  string
	Title string
	// Done is what the refused write would have made of the item, as in
	// "so it was not completed".
	Done string
	// Project is the title of the trashed project the to-do is in, or empty
	// when the item itself is in the Trash.
	Project string
}

func (e *trashedError) Error() string {
	if e.Project != "" {
		return fmt.Sprintf("%q (%s) was not %s: its project %q is in the Trash; nothing sent. Restore the project in Things first if you meant this %s", e.Title, e.UUID, e.Done, e.Project, e.Kind)
	}
	return fmt.Sprintf("%q (%s) is in the Trash, so it was not %s; nothing sent. Put it back from the Trash in Things first if you meant this %s", e.Title, e.UUID, e.Done, e.Kind)
}

func (e *trashedError) fillPayload(p *jsonErrorPayload) {
	p.Error = "trashed"
	p.Kind = e.Kind
	p.Query = e.Query
	p.UUID = e.UUID
	p.Title = e.Title
	p.Project = e.Project
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

func (e *closedSwitchError) fillPayload(p *jsonErrorPayload) {
	p.Error = "already closed"
	p.Kind = e.task.Type.String()
	p.UUID = e.task.UUID
	p.Title = e.task.Title
}

// closedTitleError is a write whose reference is an exact title that only
// closed or trashed items carry. Task is the most recent of them and Count
// how many there are. Nothing is sent: the user most likely meant an open
// task whose title is similar, and a substring match on one would have been
// a guess. Under --json the token is "trashed" when Task is in the Trash (or
// its project is), and "already closed" otherwise.
type closedTitleError struct {
	Query string
	Task  model.Task
	// Matches is every closed or trashed item with the title, Task among
	// them, listed under --json when there is more than one so a caller can
	// pick by uuid.
	Matches []model.Task
}

func (e *closedTitleError) trashed() bool { return e.Task.Trashed || e.Task.ProjectTrashed }

func (e *closedTitleError) Error() string {
	state := "already " + e.Task.Status.String()
	switch {
	case e.Task.Trashed:
		state = "in the Trash"
	case e.Task.ProjectTrashed:
		state = fmt.Sprintf("in the Trash with its project %q", e.Task.ProjectTitle)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "no open task is titled %q: ", e.Query)
	if len(e.Matches) <= 1 {
		fmt.Fprintf(&b, "the only %s with that title, %q (%s), is %s", e.Task.Type, e.Task.Title, e.Task.UUID, state)
	} else {
		fmt.Fprintf(&b, "%d closed or trashed items have that title, the most recent being %q (%s), which is %s", len(e.Matches), e.Task.Title, e.Task.UUID, state)
	}
	b.WriteString(". Nothing sent, and no open task with a similar title was touched; pass its uuid to act on a particular one")
	return b.String()
}

func (e *closedTitleError) fillPayload(p *jsonErrorPayload) {
	p.Error = "already closed"
	if e.trashed() {
		p.Error = "trashed"
	}
	p.Kind = e.Task.Type.String()
	p.Query = e.Query
	p.UUID = e.Task.UUID
	p.Title = e.Task.Title
	if e.Task.ProjectTrashed && !e.Task.Trashed {
		p.Project = e.Task.ProjectTitle
	}
	if len(e.Matches) > 1 {
		p.Matches = matchList(e.Matches)
	}
}

// blankTitleError is an add or edit refused because the title is empty or
// only whitespace. Things takes such a title and leaves the item untitled,
// which `import` refuses as "blank-title" (importShape); add, project add,
// edit and project edit refuse it the same way, before anything is sent.
type blankTitleError struct {
	Kind string // "task" or "project"
	Done string // what the write would have done: "added" or "edited"
}

func (e *blankTitleError) Error() string {
	return fmt.Sprintf("the title is blank, so the %s was not %s; nothing sent. Things would leave it untitled: give it a title", e.Kind, e.Done)
}

func (e *blankTitleError) fillPayload(p *jsonErrorPayload) {
	p.Error = "blank-title"
	p.Kind = e.Kind
}

// refuseBlankTitle returns a blankTitleError when title is empty or only
// whitespace, the test importShape makes.
func refuseBlankTitle(title, kind, done string) error {
	if strings.TrimSpace(title) == "" {
		return &blankTitleError{Kind: kind, Done: done}
	}
	return nil
}

// ambiguousRefError carries a *db.AmbiguousTaskError alongside the multi-line
// "pick one" text resolveTask prints in non-interactive mode. Error() returns
// that text unchanged so the plain-text path is untouched, while errors.As
// still reaches the candidates for the JSON payload.
type ambiguousRefError struct {
	msg   string
	kind  string // "task", or "project" for a project lookup
	inner *db.AmbiguousTaskError
}

func (e *ambiguousRefError) Error() string { return e.msg }

func (e *ambiguousRefError) Unwrap() error { return e.inner }

// refNoun is the item kind a ref error names: kind, or "task" when unset.
func refNoun(kind string) string {
	if kind == "" {
		return "task"
	}
	return kind
}

// cacheRef is what the errors refusing a row number over the last-list cache
// share: staleCacheError, otherDBCacheError and unreadableCacheError embed
// it. They share the "stale list cache" token too, because the remedy is the
// same for all three: re-list and use the new row number or the uuid.
// Re-listing also rewrites a cache file that could not be read.
type cacheRef struct {
	Kind  string // "task", or "project" for a project lookup
	Query string // the reference as typed, e.g. "2"
}

func (e *cacheRef) fillPayload(p *jsonErrorPayload) {
	p.Error = "stale list cache"
	p.Kind = refNoun(e.Kind)
	p.Query = e.Query
}

// staleCacheError is a numeric reference to a listing old enough that its row
// numbers have probably moved (issue #265). The row is refused rather than
// acted on: the UUID behind it still exists, so acting would silently hit
// whatever item has drifted into that position.
type staleCacheError struct {
	cacheRef
	Row  int
	Last cache.LastList
}

func (e *staleCacheError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s #%d comes from a stale list cache", refNoun(e.Kind), e.Row)
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
	fmt.Fprintf(&b, " and use the new row number, or pass the %s's uuid.", refNoun(e.Kind))
	return b.String()
}

// unreadableCacheError is a bare-number reference while the last-list cache
// file exists but cannot be read (cache.ErrUnreadable). There is no telling
// which row the number meant, and trying it as a title instead could close a
// task titled with that number, so it is refused.
type unreadableCacheError struct {
	cacheRef
	Err error
}

func (e *unreadableCacheError) Error() string {
	return fmt.Sprintf("%q may be a row of the last list, but %v; run a listing again or use the %s's title or uuid", e.Query, e.Err, refNoun(e.Kind))
}

func (e *unreadableCacheError) Unwrap() error { return e.Err }

// emptyRefError is a task reference that is empty or only space. As a title
// it would match every task, or every untitled one, so it is refused before
// any lookup.
type emptyRefError struct {
	Kind  string // "task", or "project" for a project lookup
	Query string
}

func (e *emptyRefError) Error() string {
	return fmt.Sprintf("the %s reference is empty; pass a row number, a uuid or a title", refNoun(e.Kind))
}

func (e *emptyRefError) fillPayload(p *jsonErrorPayload) {
	p.Error = "empty reference"
	p.Kind = refNoun(e.Kind)
	p.Query = e.Query
}

// otherDBCacheError is a numeric reference to a listing that read a different
// database from the one this command reads (issue #274). The listing may be
// fresh, but its rows describe the other database, so they are refused.
type otherDBCacheError struct {
	cacheRef
	Row     int
	Last    cache.LastList
	Current string // the resolved path of this command's database
}

func (e *otherDBCacheError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s #%d comes from a listing of a different database", refNoun(e.Kind), e.Row)
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
	fmt.Fprintf(&b, ". Re-run the listing against this database and use the new row number, or pass the %s's uuid.", refNoun(e.Kind))
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

// payloader is an error that fills its own part of the --json error payload:
// the token, and whichever of the other fields it has to give.
type payloader interface {
	fillPayload(*jsonErrorPayload)
}

// errorPayload classifies err into the JSON shape. Each error type here fills
// its own fields through payloader, and errorPayload maps internal/db's: a new
// error type implements payloader rather than formatting JSON at a call site.
//
// The first error in err's chain that is a payloader fills the payload. No
// error here wraps another payloader, so there is only ever one to find. An
// ambiguousRefError wraps a db error, which is checked first.
func errorPayload(err error) jsonErrorPayload {
	payload := jsonErrorPayload{Error: "error", Message: err.Error()}

	// internal/db knows nothing of the wire format, so its errors are
	// mapped here rather than by a method of theirs.
	var ambig *db.AmbiguousTaskError
	if errors.As(err, &ambig) {
		payload.Error = "ambiguous task"
		payload.Kind = "task"
		if ref := (*ambiguousRefError)(nil); errors.As(err, &ref) && ref.kind != "" {
			payload.Kind = ref.kind
		}
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

	var p payloader
	if errors.As(err, &p) {
		p.fillPayload(&payload)
	}
	return payload
}

// The errors below are defined beside the code that raises them, in
// importcheck.go and when.go; their payloads are filled here so the wire
// format stays in one file.

// Batch import failures. The two are kept apart because the recovery differs:
// a refusal sent nothing, so the payload can be fixed and re-run whole, while
// a partially applied import already changed things and must be re-run with
// only the items named here.
func (e *importRefusalError) fillPayload(p *jsonErrorPayload) {
	p.Error = "import refused"
	p.Items = e.jsonItems()
	if e.oversize() {
		p.Reason = tooManyItems
	}
}

func (e *importVerifyError) fillPayload(p *jsonErrorPayload) {
	p.Error = "import partially applied"
	p.Items = e.jsonItems()
	p.Created = e.created
}

func (e *misfiledError) fillPayload(p *jsonErrorPayload) {
	p.Error = "misfiled"
	p.Kind = e.kind
	p.UUID = e.uuid
	p.Title = e.title
	p.Landed = e.landed
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
