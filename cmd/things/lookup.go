package main

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/ryanlewis/things-cli/internal/cache"
	"github.com/ryanlewis/things-cli/internal/config"
	"github.com/ryanlewis/things-cli/internal/db"
	"github.com/ryanlewis/things-cli/internal/model"
	"github.com/ryanlewis/things-cli/internal/output"
)

// resolveTask reads ref as a row of the last list, a uuid or a title, and
// returns the one task it names. The order and the guards are in
// docs/content/commands.md under "Inspecting a task"; in short:
//
//   - an empty or blank ref is refused (emptyRefError);
//   - bare digits with no leading zero are a row of the last list, and a
//     cache file that exists but cannot be read refuses them
//     (unreadableCacheError);
//   - a ref shaped like a row without being one (`#12`, `07`, `0`) and a ref
//     shaped like a uuid resolve only by uuid or exact title, never by a
//     title fragment;
//   - anything else is a uuid, an exact title (closed or trashed rows count
//     when no open row has it, so the write refuses them), then a fragment
//     of an open task's title.
func resolveTask(d *Deps, ref string, database *db.DB) (*model.Task, error) {
	return resolveRef(d, ref, database, false)
}

// resolveTaskForWrite is resolveTask for a command that changes the item. An
// exact title carried only by closed or trashed items is refused outright
// (closedTitleError) rather than resolved: the user most likely meant an open
// task with a similar title, and a note on an already-closed item exiting 0
// would read as success to a script. A uuid or a row number that names a
// closed item still resolves, and the write decides what to do with it.
func resolveTaskForWrite(d *Deps, ref string, database *db.DB) (*model.Task, error) {
	return resolveRef(d, ref, database, true)
}

func resolveRef(d *Deps, ref string, database *db.DB, write bool) (*model.Task, error) {
	// A blank ref is a title fragment of every task, and an exact title of
	// every untitled one: `show ''` must not pick one of those.
	if strings.TrimSpace(ref) == "" {
		return nil, &emptyRefError{Query: ref}
	}

	// Try numeric index from last list. Surrounding space does not stop a
	// number from being one: ` 12` is row 12, or refused as not a row, and
	// never a title fragment.
	kind, digits := classifyRef(ref)
	var last cache.LastList
	var cacheErr error
	if kind != refPlain {
		last, cacheErr = cache.ReadLastList()
	}
	// A cache file that is there but cannot be read is not the same as no
	// listing: the user may well have meant a row of it, so a bare number
	// is refused rather than tried as a title.
	if kind == refRow && errors.Is(cacheErr, cache.ErrUnreadable) {
		return nil, &unreadableCacheError{cacheRef: cacheRef{Query: ref}, Err: cacheErr}
	}
	if n, err := strconv.Atoi(digits); kind == refRow && err == nil && n >= 1 {
		if cacheErr == nil && n <= len(last.UUIDs) {
			// The row exists in the cache, so this reference is a row number
			// and nothing else. Refuse it when the listing behind it is old
			// enough that the rows have probably moved (issue #265).
			if last.Stale(time.Now()) {
				return nil, &staleCacheError{cacheRef: cacheRef{Query: ref}, Row: n, Last: last}
			}
			// Nor is a listing of another database a guide to this one
			// (issue #274).
			if !cacheFromThisDB(d, last) {
				return nil, &otherDBCacheError{cacheRef: cacheRef{Query: ref}, Row: n, Last: last, Current: d.dbIdentity()}
			}
			t, err := database.GetTaskByUUID(last.UUIDs[n-1])
			if err != nil {
				return nil, err
			}
			if t != nil {
				return t, nil
			}
			return nil, &notFoundError{
				Kind:  "task",
				Query: ref,
				msg:   fmt.Sprintf("task #%d no longer exists (stale list cache — re-run list)", n),
			}
		}
	}

	// An all-digit ref that was not a row in the list is not a title
	// fragment either: `12` past the end of a 10-row list must not complete
	// "Chapter 12 notes" (issue #375). Only an exact title or uuid is taken.
	// Nor is a ref shaped like a row number that is not one, such as `+12`,
	// `#12.`, `(12)` or `No. 12`: as a fragment, `#12` would complete a task
	// that mentions issue #12.
	//
	// A uuid-shaped ref that is no item's uuid is held to the same rule.
	// uuids match byte for byte while titles ignore case, so a uuid typed
	// in the wrong case would otherwise reach a task whose title merely
	// contains it.
	uuidShaped := kind == refPlain && looksLikeUUID(strings.TrimSpace(ref))
	lookup := database.GetTask
	if kind != refPlain || uuidShaped {
		lookup = database.GetTaskExact
	}
	task, err := lookup(ref)
	var notFound *db.TaskNotFoundError
	if (kind != refPlain || uuidShaped) && errors.As(err, &notFound) && strings.TrimSpace(ref) != ref {
		// The space around ` 2026 ` is not part of the title "2026".
		task, err = lookup(strings.TrimSpace(ref))
	}
	if err == nil {
		return task, nil
	}

	// Only closed or trashed items carry the exact title. A read shows the
	// one, or offers them as candidates; a write refuses.
	var closedTitle *db.ClosedTitleError
	if errors.As(err, &closedTitle) {
		if write {
			return nil, newClosedTitleError(ref, closedTitle.Matches)
		}
		if len(closedTitle.Matches) == 1 {
			return &closedTitle.Matches[0], nil
		}
		err = &db.AmbiguousTaskError{Query: closedTitle.Query, Matches: closedTitle.Matches}
	}

	switch {
	case kind == refRow && errors.As(err, &notFound):
		return nil, notARowError(ref, last, cacheErr)
	case kind == refRowLike && errors.As(err, &notFound):
		return nil, markedRowError(ref, digits, last, cacheErr)
	case uuidShaped && errors.As(err, &notFound):
		return nil, &notFoundError{
			Kind:  "task",
			Query: ref,
			msg:   fmt.Sprintf("%q looks like a uuid, but no item has that uuid and no task has exactly that title; uuids are case-sensitive, so copy the uuid exactly as a listing prints it. If you meant part of a title, pass a shorter or longer part of it, or the full title", ref),
		}
	}

	var ambig *db.AmbiguousTaskError
	if !errors.As(err, &ambig) {
		return nil, err
	}

	if !d.interactive() {
		var b strings.Builder
		fmt.Fprintf(&b, "ambiguous task %q — matches %d tasks:\n", ambig.Query, len(ambig.Matches))
		for i, m := range ambig.Matches {
			fmt.Fprintf(&b, "  %d. %s  [%s]  (%s)\n", i+1, output.OneLine(m.Title), m.Type, m.UUID)
		}
		fmt.Fprint(&b, "Re-run with a UUID or more specific string.")
		// Wrap rather than replace: the plain-text reader gets the rendered
		// list, while --json still reaches the candidates behind it
		// (issue #152).
		return nil, &ambiguousRefError{msg: b.String(), inner: ambig}
	}

	// Interactive: prompt user to pick
	fmt.Fprintf(d.errOut(), "Multiple tasks match %q:\n", ambig.Query)
	for i, m := range ambig.Matches {
		project := ""
		if m.ProjectTitle != "" {
			project = "  (" + output.OneLine(m.ProjectTitle) + ")"
		}
		fmt.Fprintf(d.errOut(), "  %d. %s  [%s]%s\n", i+1, output.OneLine(m.Title), m.Type, project)
	}

	line, ok := promptLine(d, fmt.Sprintf("Pick [1-%d]: ", len(ambig.Matches)))
	if !ok {
		return nil, errCancelled
	}
	choice, err := strconv.Atoi(line)
	if err != nil || choice < 1 || choice > len(ambig.Matches) {
		return nil, fmt.Errorf("invalid choice")
	}
	return &ambig.Matches[choice-1], nil
}

// newClosedTitleError builds the refusal for a write whose ref is an exact
// title carried only by closed or trashed items, naming the most recent of
// them: the one most likely meant, if any was.
func newClosedTitleError(ref string, matches []model.Task) *closedTitleError {
	latest := matches[0]
	for _, m := range matches[1:] {
		if recency(m).After(recency(latest)) {
			latest = m
		}
	}
	return &closedTitleError{Query: ref, Task: latest, Matches: matches}
}

// recency is when a task last changed as far as the row records it: Things'
// modification date, which trashing bumps as well as closing, else its stop
// date, else its creation date.
func recency(t model.Task) time.Time {
	switch {
	case t.ModificationDate != nil:
		return *t.ModificationDate
	case t.StopDate != nil:
		return *t.StopDate
	case t.CreationDate != nil:
		return *t.CreationDate
	}
	return time.Time{}
}

// refKind is how a task ref is read before any lookup.
type refKind int

const (
	// refPlain is a title, a title fragment or a uuid.
	refPlain refKind = iota
	// refRow is bare digits: a row number in the last list.
	refRow
	// refRowLike is shaped like a row number without being one, such as
	// `#12`, `(12)` or `No. 12`. It never matches a title fragment.
	refRowLike
)

// rowMarkers are the words that may lead a row-like ref, as in `No. 12`,
// compared case-insensitively. Each may be followed by a dot.
var rowMarkers = []string{"no", "nr", "num", "number", "n°", "nº", "№"}

// classifyRef reads ref, ignoring surrounding space, and returns its kind and,
// for a row or row-like ref, its digits as a slice of ref.
//
// A row is bare digits with no leading zero. Listings never print `07.`, so
// `07`, `00` and `0` are row-like: they reach a task titled exactly that, and
// nothing else.
//
// A row-like ref is an optional `(`, then an optional marker (`#`, `+`, `-`
// or one of rowMarkers), then one run of ASCII digits, then nothing but `.`,
// `)` or space. Anything else stays plain, so a date, a time, an amount or a
// version (`2026-10-07`, `12:30`, `$100`, `1.2.3`) is still a title fragment.
func classifyRef(ref string) (refKind, string) {
	s := strings.TrimSpace(ref)
	start, end := digitRun(s)
	if start == 0 && end == len(s) && end > 0 {
		if s[0] == '0' {
			return refRowLike, s
		}
		return refRow, s
	}
	if start < 0 || strings.TrimRight(s[end:], ". )") != "" {
		return refPlain, ""
	}
	if !rowMarker(strings.TrimSpace(s[:start])) {
		return refPlain, ""
	}
	return refRowLike, s[start:end]
}

// looksLikeUUID reports whether ref, as typed, has the shape of a Things
// uuid: 21 or 22 ASCII letters and digits, as Things 3 writes them, or the
// 36-character hex form with dashes (8-4-4-4-12). Such a ref is never read as
// a title fragment.
func looksLikeUUID(ref string) bool {
	switch len(ref) {
	case 21, 22:
		for _, r := range ref {
			if !isASCIIAlnum(r) {
				return false
			}
		}
		return true
	case 36:
		for i, r := range ref {
			switch i {
			case 8, 13, 18, 23:
				if r != '-' {
					return false
				}
			default:
				if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
					return false
				}
			}
		}
		return true
	}
	return false
}

// digitRun returns the bounds of the first run of ASCII digits in s, or -1, -1
// when there is none.
func digitRun(s string) (int, int) {
	start := strings.IndexFunc(s, isASCIIDigit)
	if start < 0 {
		return -1, -1
	}
	end := strings.IndexFunc(s[start:], func(r rune) bool { return !isASCIIDigit(r) })
	if end < 0 {
		return start, len(s)
	}
	return start, start + end
}

func isASCIIDigit(r rune) bool { return r >= '0' && r <= '9' }

func isASCIIAlnum(r rune) bool {
	return isASCIIDigit(r) || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z'
}

// rowMarker reports whether prefix, the text before a row-like ref's digits
// with the space around it trimmed, is an optional `(` and then at most one
// marker.
func rowMarker(prefix string) bool {
	prefix = strings.TrimSpace(strings.TrimPrefix(prefix, "("))
	switch prefix {
	case "", "#", "+", "-":
		return true
	}
	return slices.Contains(rowMarkers, strings.ToLower(strings.TrimSuffix(prefix, ".")))
}

// markedRowError refuses a row-like ref that is not the exact title of a
// task. It suggests the bare number only when the last list has a row N.
func markedRowError(ref, digits string, last cache.LastList, cacheErr error) error {
	msg := fmt.Sprintf("%q is not a row reference and no task has exactly that title; ", ref)
	if n, err := strconv.Atoi(digits); err == nil && cacheErr == nil && n >= 1 && n <= len(last.UUIDs) {
		msg += fmt.Sprintf("if you meant row %d of the last list, use %d, otherwise ", n, n)
	}
	msg += "pass the task's uuid or full title"
	return &notFoundError{Kind: "task", Query: ref, msg: msg}
}

// notARowError refuses an all-digit ref that is not a row in the last list and
// is not the exact title of a task (issue #375). It reads as a not-found, and
// says to re-run the list rather than leave the user to guess what was tried.
func notARowError(ref string, last cache.LastList, cacheErr error) error {
	msg := fmt.Sprintf("%q is not a row in the last list", ref)
	if cacheErr != nil {
		msg = fmt.Sprintf("no task is titled %q, and there is no last list for it to be a row of", ref)
	} else if len(last.UUIDs) > 0 {
		msg += fmt.Sprintf(" (it has %s)", plural(len(last.UUIDs), "row"))
	}
	if cacheErr == nil && last.Command != "" {
		msg += fmt.Sprintf(". Re-run `%s` and use the new row number, or pass the task's uuid or full title.", last.Command)
	} else {
		msg += ". Re-run your listing and use the row number, or pass the task's uuid or full title."
	}
	return &notFoundError{Kind: "task", Query: ref, msg: msg}
}

// cacheTaskUUIDs records the listing's task UUIDs so a later numeric ref
// resolves against the rows the user just read.
//
// Under --json it does nothing (issue #246). JSON output carries no row
// numbers, so a JSON listing can never be the listing a numeric ref refers
// back to — but writing the cache would still renumber whatever a plain
// listing displayed a moment earlier, and the cache is one shared file per
// machine, so an agent's JSON run could silently move the rows a person is
// working from. The existing cache is left in place rather than cleared: that
// earlier plain listing is still the one its reader can see.
//
// The guard lives here, not at the call sites, so ListCmd and SearchCmd
// cannot drift apart.
//
// command is the listing as the user could re-type it, recorded alongside the
// UUIDs so a later numeric ref that has gone stale can name the listing to
// re-run (issue #265).
func cacheTaskUUIDs(d *Deps, command string, tasks []model.Task) {
	if d.JSON {
		return
	}
	uuids := make([]string, len(tasks))
	for i, t := range tasks {
		uuids[i] = t.UUID
	}
	entry := cache.LastList{WrittenAt: time.Now(), Command: command, DB: d.dbIdentity(), UUIDs: uuids}
	if err := cache.WriteLastList(entry); err != nil {
		fmt.Fprintf(d.errOut(), "warning: failed to cache task list: %v\n", err)
	}
}

// cacheFromThisDB reports whether the cached listing read the database this
// command reads. A file that records no database was written before the field
// existed. It counts as this database only when the database was discovered
// rather than named, since that is the one such a listing almost always read.
// Against a --db path it is refused, never guessed.
func cacheFromThisDB(d *Deps, last cache.LastList) bool {
	if last.DB == "" {
		return d.DBPath == ""
	}
	current := d.dbIdentity()
	if last.DB == current {
		return true
	}
	// Resolved paths can still differ for one file: the default macOS volume
	// ignores case, so `Main.sqlite` and `main.sqlite` name the same database
	// without EvalSymlinks folding them together.
	return sameFile(last.DB, current)
}

// sameFile reports whether two paths name one existing file.
func sameFile(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	ai, err := os.Stat(a)
	if err != nil {
		return false
	}
	bi, err := os.Stat(b)
	if err != nil {
		return false
	}
	return os.SameFile(ai, bi)
}

// shellQuote renders one argument as the user would have to type it. Anything
// that is not a bare word is wrapped in single quotes, which a shell takes
// literally: double quotes would still expand `$rate` or a backtick, and an
// unquoted value breaks on a space or on the metacharacters an ordinary name
// carries — an area called `R&D` pasted back unquoted runs two commands. An
// embedded quote becomes the usual '\” dance.
func shellQuote(s string) string {
	// A leading = is quoted too: zsh expands `=ls` to a command path, so = is
	// only a bare character mid-value.
	if s != "" && !strings.HasPrefix(s, "=") && !strings.ContainsFunc(s, needsShellQuoting) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// needsShellQuoting reports whether r has any meaning to a shell. It is an
// allow-list: anything outside it gets quoted, so a character the list forgot
// is merely quoted unnecessarily rather than left live. Non-ASCII letters such
// as the é in an area called café are not special to a shell; non-ASCII
// whitespace and non-printing runes are quoted.
func needsShellQuoting(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return false
	case r > unicode.MaxASCII:
		// Letters like the é in café are plain word characters. Spaces
		// (NBSP, U+2028) and non-printing runes (ZWSP, controls) are not:
		// they split or hide in a pasted command, so they get quoted.
		return unicode.IsSpace(r) || !unicode.IsPrint(r)
	}
	return !strings.ContainsRune("-_./:=@+,%", r)
}

// globalFlags renders the global flags a printed command needs to read the
// same database as this run, for a listing's re-run and the search hints.
// Only --db and --config qualify: the rest change how a command prints, not
// which items it reads. A --db the config file supplied is left out, since a
// re-run picks that up on its own. --config is kept when the flag named the
// file, since that file may be what set the database.
func globalFlags(d *Deps) []string {
	var flags []string
	cfg := d.config()
	if d.DBPath != "" && !cfg.SetsDB(d.DBPath) {
		flags = append(flags, "--db", shellQuote(d.DBPath))
	}
	if cfg.Source == config.SourceFlag {
		flags = append(flags, "--config", shellQuote(cfg.Path))
	}
	return flags
}
