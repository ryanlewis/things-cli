package main

import (
	"errors"
	"fmt"
	"os"
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

func resolveTask(d *Deps, ref string, database *db.DB) (*model.Task, error) {
	// Try numeric index from last list
	rowRef := isRowRef(ref)
	var last cache.LastList
	var cacheErr error
	if rowRef {
		last, cacheErr = cache.ReadLastList()
	}
	if n, err := strconv.Atoi(ref); rowRef && err == nil && n >= 1 {
		if cacheErr == nil && n <= len(last.UUIDs) {
			// The row exists in the cache, so this reference is a row number
			// and nothing else. Refuse it when the listing behind it is old
			// enough that the rows have probably moved (issue #265).
			if last.Stale(time.Now()) {
				return nil, &staleCacheError{Query: ref, Row: n, Last: last}
			}
			// Nor is a listing of another database a guide to this one
			// (issue #274).
			if !cacheFromThisDB(d, last) {
				return nil, &otherDBCacheError{Query: ref, Row: n, Last: last, Current: d.dbIdentity()}
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
	// Nor is `+12` or `#12`, which reads as a row number but is not one: as
	// a fragment, `#12` would complete a task that mentions issue #12.
	markedRow, markedDigits := isMarkedRowRef(ref)
	lookup := database.GetTask
	if rowRef || markedRow {
		lookup = database.GetTaskExact
	}
	task, err := lookup(ref)
	if err == nil {
		return task, nil
	}

	var notFound *db.TaskNotFoundError
	switch {
	case rowRef && errors.As(err, &notFound):
		return nil, notARowError(ref, last, cacheErr)
	case markedRow && errors.As(err, &notFound):
		return nil, &notFoundError{
			Kind:  "task",
			Query: ref,
			msg:   fmt.Sprintf("%q is not a row reference; use %s for a row of the last list, or pass the task's uuid or full title", ref, markedDigits),
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

// isRowRef reports whether ref is spelled like a row number: one or more ASCII
// digits and nothing else.
func isRowRef(ref string) bool {
	if ref == "" {
		return false
	}
	for _, r := range ref {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// isMarkedRowRef reports whether ref is spelled like a row number marked with
// a leading + or #, after optional space, and returns the digits. Only bare
// digits are row numbers; these are not, and do not match a title fragment.
func isMarkedRowRef(ref string) (bool, string) {
	s := strings.TrimLeftFunc(ref, unicode.IsSpace)
	if s == "" || (s[0] != '+' && s[0] != '#') || !isRowRef(s[1:]) {
		return false, ""
	}
	return true, s[1:]
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
