package main

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/ryanlewis/things-cli/internal/cache"
	"github.com/ryanlewis/things-cli/internal/db"
	"github.com/ryanlewis/things-cli/internal/model"
	"github.com/ryanlewis/things-cli/internal/output"
)

func resolveTask(d *Deps, ref string, database *db.DB) (*model.Task, error) {
	// Try numeric index from last list
	if n, err := strconv.Atoi(ref); err == nil && n >= 1 {
		last, cacheErr := cache.ReadLastList()
		if cacheErr == nil && n <= len(last.UUIDs) {
			// The row exists in the cache, so this reference is a row number
			// and nothing else. Refuse it when the listing behind it is old
			// enough that the rows have probably moved (issue #265).
			if last.Stale(time.Now()) {
				return nil, &staleCacheError{Query: ref, Row: n, Last: last}
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

	task, err := database.GetTask(ref)
	if err == nil {
		return task, nil
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
	entry := cache.LastList{WrittenAt: time.Now(), Command: command, UUIDs: uuids}
	if err := cache.WriteLastList(entry); err != nil {
		fmt.Fprintf(d.errOut(), "warning: failed to cache task list: %v\n", err)
	}
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

// globalFlags renders the global flags a re-run of a listing needs to reach the
// same rows. Only --db qualifies: the rest change how a listing prints, not
// which items it holds. A path the config file supplied is left out, since a
// re-run picks that up on its own.
func globalFlags(d *Deps) []string {
	if d.DBPath == "" || d.config().SetsDB(d.DBPath) {
		return nil
	}
	return []string{"--db", shellQuote(d.DBPath)}
}
