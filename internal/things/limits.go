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

func validateNotes(field, v string) error {
	if n := utf8.RuneCountInString(v); n > MaxNotesLen {
		return fmt.Errorf("%s: %d characters exceeds the %d-character limit", field, n, MaxNotesLen)
	}
	return nil
}

func validateString(field, v string) error {
	if n := utf8.RuneCountInString(v); n > MaxStringLen {
		return fmt.Errorf("%s: %d characters exceeds the %d-character limit", field, n, MaxStringLen)
	}
	return nil
}

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
	for _, t := range strings.Split(v, ",") {
		t = strings.TrimSpace(t)
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

func optString(field string, p *string) error {
	if p == nil {
		return nil
	}
	return validateString(field, *p)
}

func optNotes(field string, p *string) error {
	if p == nil {
		return nil
	}
	return validateNotes(field, *p)
}

func optTags(field string, p *string) error {
	if p == nil {
		return nil
	}
	return validateTags(field, *p)
}

func optChecklist(field string, p *string) error {
	if p == nil {
		return nil
	}
	return validateChecklist(field, *p)
}

func firstErr(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

func validateAdd(p AddParams) error {
	return firstErr(
		validateString("title", p.Title),
		validateNotes("notes", p.Notes),
		validateTags("tags", p.Tags),
		validateChecklist("checklist", p.Checklist),
		validateString("list", p.List),
		validateString("heading", p.Heading),
	)
}

func validateAddProject(p AddProjectParams) error {
	return firstErr(
		validateString("title", p.Title),
		validateNotes("notes", p.Notes),
		validateTags("tags", p.Tags),
		validateString("area", p.Area),
	)
}

func validateUpdate(p UpdateParams) error {
	return firstErr(
		optString("title", p.Title),
		optNotes("notes", p.Notes),
		optNotes("prepend-notes", p.PrependNotes),
		optNotes("append-notes", p.AppendNotes),
		optTags("tags", p.Tags),
		optTags("add-tags", p.AddTags),
		optChecklist("checklist", p.Checklist),
		optChecklist("prepend-checklist", p.PrependChecklist),
		optChecklist("append-checklist", p.AppendChecklist),
		optString("list", p.List),
		optString("heading", p.Heading),
	)
}

func validateUpdateProject(p UpdateProjectParams) error {
	return firstErr(
		optString("title", p.Title),
		optNotes("notes", p.Notes),
		optNotes("prepend-notes", p.PrependNotes),
		optNotes("append-notes", p.AppendNotes),
		optTags("tags", p.Tags),
		optTags("add-tags", p.AddTags),
		optString("area", p.Area),
	)
}
