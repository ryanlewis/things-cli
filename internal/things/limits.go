package things

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Limits enforced by the Things URL scheme. Checking client-side turns
// silent truncation / opaque app failures into clean errors.
//
// Rate limit: `add` is capped at 250 items per 10-second rolling window
// app-side. That check is deferred until `things import` (or another
// bulk-add surface) lands — single-item `add`/`add-project` invocations
// can't realistically hit it.
const (
	MaxNotesLen       = 10000
	MaxChecklistItems = 100
	MaxStringLen      = 4000
)

func validateLen(field, v string, limit int) error {
	if n := utf8.RuneCountInString(v); n > limit {
		return fmt.Errorf("%s: %d characters exceeds the %d-character limit", field, n, limit)
	}
	return nil
}

func validateNotes(field, v string) error { return validateLen(field, v, MaxNotesLen) }

func validateString(field, v string) error { return validateLen(field, v, MaxStringLen) }

func validateChecklist(field, v string) error {
	if v == "" {
		return nil
	}
	// TrimRight so a trailing newline isn't counted as an extra item.
	trimmed := strings.TrimRight(v, "\n")
	n := strings.Count(trimmed, "\n") + 1
	if n > MaxChecklistItems {
		return fmt.Errorf("%s: %d items exceeds the %d-item limit", field, n, MaxChecklistItems)
	}
	for _, item := range strings.Split(trimmed, "\n") {
		if c := utf8.RuneCountInString(item); c > MaxStringLen {
			return fmt.Errorf("%s: item %q (%d characters) exceeds the %d-character limit", field, truncate(item), c, MaxStringLen)
		}
	}
	return nil
}

func validateTags(field, v string) error {
	if v == "" {
		return nil
	}
	for _, t := range SplitTags(v) {
		if c := utf8.RuneCountInString(t); c > MaxStringLen {
			return fmt.Errorf("%s: tag %q (%d characters) exceeds the %d-character limit", field, truncate(t), c, MaxStringLen)
		}
	}
	return nil
}

// truncate returns s capped at 40 runes, with an ellipsis appended when
// truncation occurred. Slicing by rune (not byte) keeps multi-byte
// characters intact so the truncated value is always valid UTF-8.
func truncate(s string) string {
	const max = 40
	i := 0
	for idx := range s {
		if i == max {
			return s[:idx] + "…"
		}
		i++
	}
	return s
}

// opt runs check on *p, and passes when p is nil.
func opt(check func(field, v string) error, field string, p *string) error {
	if p == nil {
		return nil
	}
	return check(field, *p)
}

func firstErr(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

// validate checks the fields shared by `update` and `update-project`.
func (c UpdateCommon) validate() error {
	return firstErr(
		opt(validateString, "title", c.Title),
		opt(validateNotes, "notes", c.Notes),
		opt(validateNotes, "prepend-notes", c.PrependNotes),
		opt(validateNotes, "append-notes", c.AppendNotes),
		opt(validateTags, "tags", c.Tags),
		opt(validateTags, "add-tags", c.AddTags),
	)
}

func validateUpdate(p UpdateParams) error {
	return firstErr(
		p.validate(),
		opt(validateChecklist, "checklist", p.Checklist),
		opt(validateChecklist, "prepend-checklist", p.PrependChecklist),
		opt(validateChecklist, "append-checklist", p.AppendChecklist),
		opt(validateString, "list", p.List),
		opt(validateString, "heading", p.Heading),
	)
}

func validateUpdateProject(p UpdateProjectParams) error {
	return firstErr(
		p.validate(),
		opt(validateString, "area", p.Area),
	)
}
