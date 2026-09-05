package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ryanlewis/things-cli/internal/things"
)

// StrictTags is embedded in every write command that accepts tags. The Things
// URL scheme applies only tags that already exist and ignores the rest without
// reporting it, so by default we warn and let the write through; --strict-tags
// turns that warning into a refusal.
type StrictTags struct {
	StrictTags bool `help:"Fail instead of writing when a tag does not exist in Things." name:"strict-tags"`
}

// verifyTags checks requested tag names against the tags in the Things
// database and reports the ones that don't exist. Without strict it warns on
// stderr and returns nil so the write still happens; with strict it returns an
// error and the caller writes nothing.
func verifyTags(d *Deps, strict bool, names []string) error {
	if len(names) == 0 {
		return nil
	}

	unavailable := func(err error) error {
		if strict {
			return fmt.Errorf("--strict-tags: cannot check tags against the Things database: %w", err)
		}
		fmt.Fprintf(d.errOut(), "warning: could not check tags against the Things database: %v\n", err)
		fmt.Fprintf(d.errOut(), "warning: tags that do not already exist in Things will be dropped without notice\n")
		return nil
	}

	database, err := d.Database()
	if err != nil {
		return unavailable(err)
	}
	unknown, err := database.UnknownTags(names)
	if err != nil {
		return unavailable(err)
	}
	if len(unknown) == 0 {
		return nil
	}

	list := strings.Join(unknown, ", ")
	if strict {
		return fmt.Errorf("these tags do not exist in Things: %s — create them in Things first, or drop --strict-tags to write anyway", list)
	}
	fmt.Fprintf(d.errOut(), "warning: these tags do not exist in Things and will be ignored: %s\n", list)
	fmt.Fprintf(d.errOut(), "warning: Things only applies tags that already exist — create them in Things first, or use --strict-tags to fail instead of dropping them\n")
	return nil
}

// verifyTagStrings checks comma-separated tag values (as passed to --tags /
// --add-tags). nil pointers and empty strings contribute nothing.
func verifyTagStrings(d *Deps, strict bool, values ...*string) error {
	var names []string
	for _, v := range values {
		if v != nil {
			names = append(names, things.SplitTags(*v)...)
		}
	}
	return verifyTags(d, strict, names)
}

// importTags collects every tag named anywhere in a Things JSON payload. Tags
// live under an item's `attributes`, and items nest (a project carries
// `items`), so this walks the whole decoded tree. The payload has already
// been validated as JSON by the caller; anything unparseable yields no tags
// and the import proceeds as before.
func importTags(data []byte) []string {
	var payload any
	if err := json.Unmarshal(data, &payload); err != nil {
		return nil
	}
	var names []string
	walkImportTags(payload, &names)
	return names
}

func walkImportTags(node any, names *[]string) {
	switch v := node.(type) {
	case map[string]any:
		if attrs, ok := v["attributes"].(map[string]any); ok {
			// `add-tags` is the additive form used by `operation: update`
			// items; it carries tag names just like `tags` does.
			*names = append(*names, jsonTagValues(attrs["tags"])...)
			*names = append(*names, jsonTagValues(attrs["add-tags"])...)
		}
		for _, child := range v {
			walkImportTags(child, names)
		}
	case []any:
		for _, child := range v {
			walkImportTags(child, names)
		}
	}
}

// jsonTagValues reads a tag attribute, which Things documents as an array of
// strings but also accepts as a single comma-separated string. Array entries
// are taken verbatim — the array form is what lets a payload name a tag that
// contains a comma, so splitting them would invent unknown tags and, under
// --strict-tags, refuse an import that Things would have applied fine.
func jsonTagValues(node any) []string {
	switch v := node.(type) {
	case []any:
		var out []string
		for _, item := range v {
			s, ok := item.(string)
			if !ok {
				continue
			}
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, s)
			}
		}
		return out
	case string:
		return things.SplitTags(v)
	}
	return nil
}
