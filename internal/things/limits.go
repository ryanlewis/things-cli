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

// runeLen counts characters as the URL-scheme docs do — multi-byte UTF-8
// (emoji, CJK) shouldn't trip the limit earlier than a non-ASCII user expects.
func runeLen(s string) int { return utf8.RuneCountInString(s) }

func validateNotes(field, v string) error {
	if n := runeLen(v); n > MaxNotesLen {
		return fmt.Errorf("%s: %d characters exceeds the %d-character limit", field, n, MaxNotesLen)
	}
	return nil
}

func validateString(field, v string) error {
	if n := runeLen(v); n > MaxStringLen {
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
		if c := runeLen(item); c > MaxStringLen {
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
		if c := runeLen(t); c > MaxStringLen {
			return fmt.Errorf("%s: tag %q (%d characters) exceeds the %d-character limit", field, truncate(t), c, MaxStringLen)
		}
	}
	return nil
}

func truncate(s string) string {
	const max = 40
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

func opt(p *string, check func(string) error) error {
	if p == nil {
		return nil
	}
	return check(*p)
}

func validateAdd(p AddParams) error {
	if err := validateString("title", p.Title); err != nil {
		return err
	}
	if err := validateNotes("notes", p.Notes); err != nil {
		return err
	}
	if err := validateTags("tags", p.Tags); err != nil {
		return err
	}
	if err := validateChecklist("checklist", p.Checklist); err != nil {
		return err
	}
	if err := validateString("list", p.List); err != nil {
		return err
	}
	return validateString("heading", p.Heading)
}

func validateAddProject(p AddProjectParams) error {
	if err := validateString("title", p.Title); err != nil {
		return err
	}
	if err := validateNotes("notes", p.Notes); err != nil {
		return err
	}
	if err := validateTags("tags", p.Tags); err != nil {
		return err
	}
	return validateString("area", p.Area)
}

func validateUpdate(p UpdateParams) error {
	if err := opt(p.Title, func(s string) error { return validateString("title", s) }); err != nil {
		return err
	}
	if err := opt(p.Notes, func(s string) error { return validateNotes("notes", s) }); err != nil {
		return err
	}
	if err := opt(p.PrependNotes, func(s string) error { return validateNotes("prepend-notes", s) }); err != nil {
		return err
	}
	if err := opt(p.AppendNotes, func(s string) error { return validateNotes("append-notes", s) }); err != nil {
		return err
	}
	if err := opt(p.Tags, func(s string) error { return validateTags("tags", s) }); err != nil {
		return err
	}
	if err := opt(p.AddTags, func(s string) error { return validateTags("add-tags", s) }); err != nil {
		return err
	}
	if err := opt(p.Checklist, func(s string) error { return validateChecklist("checklist", s) }); err != nil {
		return err
	}
	if err := opt(p.PrependChecklist, func(s string) error { return validateChecklist("prepend-checklist", s) }); err != nil {
		return err
	}
	if err := opt(p.AppendChecklist, func(s string) error { return validateChecklist("append-checklist", s) }); err != nil {
		return err
	}
	if err := opt(p.List, func(s string) error { return validateString("list", s) }); err != nil {
		return err
	}
	return opt(p.Heading, func(s string) error { return validateString("heading", s) })
}

func validateUpdateProject(p UpdateProjectParams) error {
	if err := opt(p.Title, func(s string) error { return validateString("title", s) }); err != nil {
		return err
	}
	if err := opt(p.Notes, func(s string) error { return validateNotes("notes", s) }); err != nil {
		return err
	}
	if err := opt(p.PrependNotes, func(s string) error { return validateNotes("prepend-notes", s) }); err != nil {
		return err
	}
	if err := opt(p.AppendNotes, func(s string) error { return validateNotes("append-notes", s) }); err != nil {
		return err
	}
	if err := opt(p.Tags, func(s string) error { return validateTags("tags", s) }); err != nil {
		return err
	}
	if err := opt(p.AddTags, func(s string) error { return validateTags("add-tags", s) }); err != nil {
		return err
	}
	return opt(p.Area, func(s string) error { return validateString("area", s) })
}
