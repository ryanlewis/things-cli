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
		// Not d.interactive(): under --json it is false even on a terminal,
		// and the import would block reading the keyboard.
		if d.stdinTTY() {
			return fmt.Errorf("no JSON on stdin and no --file given")
		}
		data, err = io.ReadAll(d.in())
		if err != nil {
			return fmt.Errorf("reading stdin: %w", err)
		}
	}
	payload, err := decodeImportJSON(data)
	if err != nil {
		return err
	}
	// A creation-date Things rejects is refused before the tag check, which
	// writes to Things under --create-tags.
	creates, err := importCreates(payload)
	if err != nil {
		return err
	}
	if _, err := verifyTags(d, c.TagFlags, importTags(payload)); err != nil {
		return err
	}
	// Refuse before anything is sent if any `operation: update` item would
	// change an attribute Things drops silently on a repeating item.
	plan, err := prepareImport(d, database, payload, creates)
	if err != nil {
		return err
	}
	// A token read error is only a warning: the payload may be create-only.
	token := authToken(d, database)
	return applyImport(d, database, plan, func() error {
		return things.ImportJSON(string(data), token, c.Reveal)
	})
}

// decodeImportJSON decodes the payload once for every check that reads it,
// and checks it is a non-empty JSON array, the shape the Things JSON URL
// scheme requires. A syntax error is reported with its line and column so
// the user can jump to the bad byte in their editor.
func decodeImportJSON(data []byte) ([]any, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, fmt.Errorf("empty payload")
	}
	var payload any
	if err := json.Unmarshal(data, &payload); err != nil {
		var syn *json.SyntaxError
		if errors.As(err, &syn) {
			line, col := offsetToLineCol(data, syn.Offset)
			return nil, fmt.Errorf("invalid JSON at line %d, column %d: %s", line, col, syn.Error())
		}
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	items, ok := payload.([]any)
	if !ok {
		return nil, fmt.Errorf("payload must be a JSON array of items")
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("payload array is empty")
	}
	return items, nil
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
