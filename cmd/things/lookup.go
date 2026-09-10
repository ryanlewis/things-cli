package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/ryanlewis/things-cli/internal/cache"
	"github.com/ryanlewis/things-cli/internal/db"
	"github.com/ryanlewis/things-cli/internal/model"
)

func resolveTask(d *Deps, ref string, database *db.DB) (*model.Task, error) {
	// Try numeric index from last list
	if n, err := strconv.Atoi(ref); err == nil && n >= 1 {
		uuids, cacheErr := cache.ReadLastList()
		if cacheErr == nil && n <= len(uuids) {
			t, err := database.GetTaskByUUID(uuids[n-1])
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
			fmt.Fprintf(&b, "  %d. %s  [%s]  (%s)\n", i+1, m.Title, m.Type, m.UUID)
		}
		fmt.Fprint(&b, "Re-run with a UUID or more specific string.")
		// Wrap rather than replace: the plain-text reader gets the rendered
		// list, while --json still reaches the candidates behind it
		// (issue #152).
		return nil, &ambiguousRefError{msg: b.String(), inner: ambig}
	}

	// Interactive: prompt user to pick
	fmt.Fprintf(os.Stderr, "Multiple tasks match %q:\n", ambig.Query)
	for i, m := range ambig.Matches {
		project := ""
		if m.ProjectTitle != "" {
			project = "  (" + m.ProjectTitle + ")"
		}
		fmt.Fprintf(os.Stderr, "  %d. %s  [%s]%s\n", i+1, m.Title, m.Type, project)
	}
	fmt.Fprintf(os.Stderr, "Pick [1-%d]: ", len(ambig.Matches))

	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return nil, fmt.Errorf("cancelled")
	}
	choice, err := strconv.Atoi(strings.TrimSpace(scanner.Text()))
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
func cacheTaskUUIDs(d *Deps, tasks []model.Task) {
	if d.JSON {
		return
	}
	uuids := make([]string, len(tasks))
	for i, t := range tasks {
		uuids[i] = t.UUID
	}
	if err := cache.WriteLastList(uuids); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to cache task list: %v\n", err)
	}
}
