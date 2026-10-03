package db

import (
	"fmt"
	"strings"

	"github.com/ryanlewis/things-cli/internal/model"
)

// FindTagUUID resolves a tag reference (UUID or title) to its UUID,
// returning "" when no row matches. Titles match ignoring case, as Things
// matches them; matchRef says which row wins.
func (d *DB) FindTagUUID(ref string) (string, error) {
	tags, err := d.ListTags()
	if err != nil {
		return "", fmt.Errorf("finding tag: %w", err)
	}
	rows := make([]uuidTitle, len(tags))
	for i, t := range tags {
		rows[i] = uuidTitle{t.UUID, t.Title}
	}
	return matchRef(rows, ref), nil
}

func (d *DB) ListTags() ([]model.Tag, error) {
	query := `
		SELECT uuid, COALESCE(title, ''), COALESCE(shortcut, ''), COALESCE(parent, '')
		FROM TMTag
		-- uuid last so the order is total: two tags sharing an "index" would
		-- otherwise list in an order SQLite does not define (issue #221).
		ORDER BY "index" ASC, uuid ASC
	`
	rows, err := d.db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("querying tags: %w", err)
	}
	defer rows.Close()

	tags := []model.Tag{}
	for rows.Next() {
		var t model.Tag
		if err := rows.Scan(&t.UUID, &t.Title, &t.Shortcut, &t.ParentUUID); err != nil {
			return nil, fmt.Errorf("scanning tag: %w", err)
		}
		tags = append(tags, t)
	}
	return tags, rows.Err()
}

// UnknownTags returns the requested names that have no matching tag in the
// Things database, preserving the caller's order and dropping duplicates.
//
// The Things URL scheme applies only tags that already exist and silently
// ignores the rest, so callers use this to warn before a write. Matching is
// case-insensitive: Things treats tag names that differ only in case as the
// same tag, and a false "unknown tag" would be worse than a missed warning.
func (d *DB) UnknownTags(names []string) ([]string, error) {
	if len(names) == 0 {
		return nil, nil
	}
	tags, err := d.ListTags()
	if err != nil {
		return nil, err
	}
	existing := make(map[string]struct{}, len(tags))
	for _, t := range tags {
		existing[FoldTag(t.Title)] = struct{}{}
	}

	var unknown []string
	seen := make(map[string]struct{}, len(names))
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		key := FoldTag(n)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		if _, ok := existing[key]; !ok {
			unknown = append(unknown, n)
		}
	}
	return unknown, nil
}

type uuidTitle struct{ uuid, title string }

// matchRef returns the UUID of the row that ref names, or "" when none does.
// A UUID match wins, then an exact title, then the first title equal to ref
// under FoldTag, in the order given (callers pass Things' list order). A blank
// ref never folds onto an untitled row.
func matchRef(rows []uuidTitle, ref string) string {
	key, name := FoldTag(ref), normName(ref)
	var exact, folded string
	for _, r := range rows {
		switch {
		case r.uuid == ref:
			return r.uuid
		case exact == "" && normName(r.title) == name:
			exact = r.uuid
		case folded == "" && key != "" && FoldTag(r.title) == key:
			folded = r.uuid
		}
	}
	if exact != "" {
		return exact
	}
	return folded
}
