package db

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/ryanlewis/things-cli/internal/model"
)

// FindTagUUID resolves a tag reference (UUID or title) to its UUID,
// returning "" when no row matches.
func (d *DB) FindTagUUID(ref string) (string, error) {
	var uuid string
	err := d.db.QueryRow(
		`SELECT uuid FROM TMTag WHERE uuid = ? OR title = ? LIMIT 1`,
		ref, ref,
	).Scan(&uuid)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("finding tag: %w", err)
	}
	return uuid, nil
}

func (d *DB) ListTags() ([]model.Tag, error) {
	query := `
		SELECT uuid, COALESCE(title, ''), COALESCE(shortcut, ''), COALESCE(parent, '')
		FROM TMTag
		ORDER BY "index" ASC
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
		existing[foldTag(t.Title)] = struct{}{}
	}

	var unknown []string
	seen := make(map[string]struct{}, len(names))
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		key := foldTag(n)
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

func foldTag(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}
