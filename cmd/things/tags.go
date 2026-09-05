package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/ryanlewis/things-cli/internal/db"
	"github.com/ryanlewis/things-cli/internal/output"
	"github.com/ryanlewis/things-cli/internal/things"
)

// TagFlags is embedded in every write command that accepts tags. The Things
// URL scheme applies only tags that already exist and ignores the rest without
// reporting it, so by default we warn and let the write through; --strict-tags
// turns that warning into a refusal, and --create-tags creates the missing tags
// over AppleScript so the write can apply them. The two contradict each other,
// so kong rejects them together.
type TagFlags struct {
	StrictTags bool `help:"Fail instead of writing when a tag does not exist in Things." name:"strict-tags" xor:"tag-policy"`
	CreateTags bool `help:"Create tags that do not exist in Things before writing." name:"create-tags" xor:"tag-policy"`
}

// verifyTags checks requested tag names against the tags in the Things
// database and deals with the ones that don't exist. By default it warns on
// stderr and returns nil so the write still happens; --strict-tags turns that
// into an error and the caller writes nothing; --create-tags creates them and
// lets the write proceed with every tag applied.
func verifyTags(d *Deps, flags TagFlags, names []string) error {
	if len(names) == 0 {
		return nil
	}

	unavailable := func(err error) error {
		switch {
		case flags.StrictTags:
			return fmt.Errorf("--strict-tags: cannot check tags against the Things database: %w", err)
		case flags.CreateTags:
			return fmt.Errorf("--create-tags: cannot check tags against the Things database: %w", err)
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

	if flags.CreateTags {
		return createTags(d, unknown)
	}

	list := strings.Join(unknown, ", ")
	if flags.StrictTags {
		return fmt.Errorf("these tags do not exist in Things: %s — create them in Things first, run `things tag add %s`, or drop --strict-tags to write anyway", list, list)
	}
	fmt.Fprintf(d.errOut(), "warning: these tags do not exist in Things and will be ignored: %s\n", list)
	fmt.Fprintf(d.errOut(), "warning: Things only applies tags that already exist — create them with --create-tags or `things tag add`, or use --strict-tags to fail instead of dropping them\n")
	return nil
}

// createTags makes each missing tag in Things over AppleScript. It runs before
// the URL-scheme write that wants to apply them: osascript returns only once
// Things has the tag, so the write that follows finds it by name. No read-back
// is needed — that would only prove the SQLite database had caught up, which
// the URL scheme does not depend on.
func createTags(d *Deps, names []string) error {
	var created []string
	for _, name := range names {
		if err := things.CreateTag(name); err != nil {
			if len(created) > 0 {
				fmt.Fprintf(d.errOut(), "created in Things before the failure: %s\n", strings.Join(created, ", "))
			}
			return fmt.Errorf("creating tag %q: %w", name, err)
		}
		created = append(created, name)
	}
	fmt.Fprintf(d.errOut(), "created in Things: %s\n", strings.Join(created, ", "))
	return nil
}

// verifyTagStrings checks comma-separated tag values (as passed to --tags /
// --add-tags). nil pointers and empty strings contribute nothing.
func verifyTagStrings(d *Deps, flags TagFlags, values ...*string) error {
	var names []string
	for _, v := range values {
		if v != nil {
			names = append(names, things.SplitTags(*v)...)
		}
	}
	return verifyTags(d, flags, names)
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

// TagCmd groups the tag write commands. Listing tags stays on the top-level
// `things tags`.
type TagCmd struct {
	Add TagAddCmd `cmd:"" help:"Create tags in Things."`
}

type TagAddCmd struct {
	Names []string `arg:"" required:"" help:"Tag names to create. Names that already exist are skipped."`
}

// tagAddResult is the --json shape of `things tag add`.
type tagAddResult struct {
	Created []string `json:"created"`
	Skipped []string `json:"skipped"`
}

func (c *TagAddCmd) Run(d *Deps) error {
	database, err := d.Database()
	if err != nil {
		return err
	}

	names := dedupeTagNames(c.Names)
	if len(names) == 0 {
		return fmt.Errorf("tag add: no tag names given")
	}

	// UnknownTags matches case-insensitively, so "work" is reported as
	// existing when Things already has "Work" and we don't create a near
	// duplicate the user would have to merge by hand.
	missing, err := database.UnknownTags(names)
	if err != nil {
		return err
	}
	toCreate := make(map[string]struct{}, len(missing))
	for _, n := range missing {
		toCreate[foldTagName(n)] = struct{}{}
	}
	skipped := []string{}
	for _, n := range names {
		if _, ok := toCreate[foldTagName(n)]; !ok {
			skipped = append(skipped, n)
		}
	}

	created := []string{}
	for _, name := range missing {
		if err := things.CreateTag(name); err != nil {
			if len(created) > 0 {
				fmt.Fprintf(d.errOut(), "created before the failure: %s\n", strings.Join(created, ", "))
			}
			return fmt.Errorf("creating tag %q: %w", name, err)
		}
		created = append(created, name)
	}

	if len(created) > 0 && !d.NoVerify {
		if err := verifyTagsCreated(database, created); err != nil {
			return err
		}
	}

	if d.JSON {
		return output.Print(d.Stdout, tagAddResult{Created: created, Skipped: skipped}, true)
	}
	if len(created) > 0 {
		fmt.Fprintf(d.Stdout, "created: %s\n", strings.Join(created, ", "))
	}
	if len(skipped) > 0 {
		fmt.Fprintf(d.Stdout, "already exists: %s\n", strings.Join(skipped, ", "))
	}
	return nil
}

// verifyTagsCreated re-reads the tag list until every name shows up, so a
// creation Things dropped is not reported as success. Things writes the tag to
// its SQLite database a moment after AppleScript returns, hence the poll.
// --no-verify skips it.
func verifyTagsCreated(database *db.DB, names []string) error {
	deadline := time.Now().Add(verifyTimeout)
	for {
		missing, err := database.UnknownTags(names)
		switch {
		case err != nil:
			if !time.Now().Before(deadline) {
				return fmt.Errorf("verifying tag creation: %w", err)
			}
		case len(missing) == 0:
			return nil
		case !time.Now().Before(deadline):
			return fmt.Errorf("tag creation did not apply: %s still missing from the Things database after %s. Things accepted the command and then dropped it silently — check that Things3 is running",
				strings.Join(missing, ", "), verifyTimeout)
		}
		verifySleep(verifyInterval)
	}
}

// dedupeTagNames trims the requested names, drops empties, and collapses ones
// that differ only in case — Things treats those as the same tag.
func dedupeTagNames(names []string) []string {
	var out []string
	seen := make(map[string]struct{}, len(names))
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		key := foldTagName(n)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, n)
	}
	return out
}

func foldTagName(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}
