package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

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
	// Refuse before the tag check, which writes to Things under
	// --create-tags: a date Things rejects, or an `operation: update` item
	// that would change an attribute Things drops silently on a repeating
	// item.
	plan, err := prepareImport(database, payload, importDuplicateKeys(data))
	if err != nil {
		return err
	}
	if _, err := verifyTags(d, c.TagFlags, importTags(payload)); err != nil {
		return err
	}
	plan.warnMissing(d)
	resolveImportDests(d, database, plan.creates)
	// A token read error is only a warning: the payload may be create-only.
	token := authToken(d, database)
	return applyImport(d, database, plan, func(sent time.Time) error {
		return things.ImportJSON(string(resolveImportWhens(data, sent)), token, c.Reveal)
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

// resolveImportWhens returns data, a payload decodeImportJSON accepted, with
// each when in an item's attributes rewritten as things.ResolveWhen sends it
// at now, as add, project add and edit send --when: today@6pm becomes today's
// date at 18:00. The rest of the payload goes byte for byte as given. It
// walks the tokens as importDuplicateKeys does.
func resolveImportWhens(data []byte, now time.Time) []byte {
	dec := json.NewDecoder(bytes.NewReader(data))
	var out []byte
	copied := 0
	// walk reads one value: the value of key in an object that is itself
	// the value of in ("" for both in an array or at the top level).
	var walk func(in, key string) error
	walk = func(in, key string) error {
		start := dec.InputOffset()
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		switch v := tok.(type) {
		case string:
			if key != "when" || in != "attributes" {
				return nil
			}
			sent := things.ResolveWhen(v, now)
			if sent == v {
				return nil
			}
			// start is the end of the key; the value follows its colon.
			for start < int64(len(data)) && data[start] != '"' {
				start++
			}
			quoted, _ := json.Marshal(sent)
			out = append(append(out, data[copied:start]...), quoted...)
			copied = int(dec.InputOffset())
		case json.Delim:
			for dec.More() {
				in, name := "", ""
				if v == '{' {
					k, err := dec.Token()
					if err != nil {
						return err
					}
					in, name = key, k.(string)
				}
				if err := walk(in, name); err != nil {
					return err
				}
			}
			_, err = dec.Token() // the closing delimiter
			return err
		}
		return nil
	}
	// The payload has decoded, so the walk does not fail.
	_ = walk("", "")
	if out == nil {
		return data
	}
	return append(out, data[copied:]...)
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
