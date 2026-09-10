package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/ryanlewis/things-cli/internal/things"
)

type ImportCmd struct {
	File   string `help:"Read JSON payload from this file instead of stdin." short:"f" type:"existingfile"`
	Reveal bool   `help:"Reveal the first created/updated item in Things after import."`

	TagFlags
}

func (c *ImportCmd) Run(d *Deps) error {
	database, err := d.Database()
	if err != nil {
		return err
	}
	var data []byte
	if c.File != "" {
		data, err = os.ReadFile(c.File)
		if err != nil {
			return fmt.Errorf("reading %s: %w", c.File, err)
		}
	} else {
		if isInteractive() {
			return fmt.Errorf("no JSON on stdin and no --file given")
		}
		data, err = io.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("reading stdin: %w", err)
		}
	}
	if err := validateImportJSON(data); err != nil {
		return err
	}
	if err := verifyTags(d, c.TagFlags, importTags(data)); err != nil {
		return err
	}
	// Refuse before anything is sent if any `operation: update` item would
	// change an attribute Things drops silently on a repeating item.
	plan, err := prepareImport(d, database, data)
	if err != nil {
		return err
	}
	token, err := database.GetAuthToken()
	if err != nil {
		// Don't fail the import — the payload may be create-only and not need
		// the token at all — but surface the read error so users debugging an
		// `operation: update` failure aren't left guessing.
		fmt.Fprintf(d.errOut(), "warning: could not read Things auth token: %v\n", err)
	}
	if err := things.ImportJSON(string(data), token, c.Reveal); err != nil {
		return err
	}
	return verifyImportStatuses(d, database, plan)
}

// validateImportJSON checks the payload is a non-empty JSON array — the shape
// the Things JSON URL scheme requires — without allocating on the happy path.
// On syntax errors it falls back to a full decode purely to extract the byte
// offset, which it converts to line/column so the user can jump to the bad
// byte in their editor.
func validateImportJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return fmt.Errorf("empty payload")
	}
	if !json.Valid(data) {
		// Re-decode to get an offset for the error message; this is the slow
		// path (only on invalid input) so the allocation doesn't matter.
		var v any
		err := json.Unmarshal(data, &v)
		var syn *json.SyntaxError
		if errors.As(err, &syn) {
			line, col := offsetToLineCol(data, syn.Offset)
			return fmt.Errorf("invalid JSON at line %d, column %d: %s", line, col, syn.Error())
		}
		return fmt.Errorf("invalid JSON: %w", err)
	}
	if trimmed[0] != '[' {
		return fmt.Errorf("payload must be a JSON array of items")
	}
	// Valid JSON starting with `[` is at minimum `[]`, so len >= 2.
	if len(bytes.TrimSpace(trimmed[1:len(trimmed)-1])) == 0 {
		return fmt.Errorf("payload array is empty")
	}
	return nil
}

func offsetToLineCol(data []byte, offset int64) (int, int) {
	if offset < 0 {
		offset = 0
	}
	if int(offset) > len(data) {
		offset = int64(len(data))
	}
	prefix := data[:offset]
	line := 1 + bytes.Count(prefix, []byte{'\n'})
	col := int(offset) - bytes.LastIndexByte(prefix, '\n')
	return line, col
}
