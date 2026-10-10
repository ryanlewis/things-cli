package output

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"slices"
	"strings"

	"github.com/ryanlewis/things-cli/internal/model"
)

// TaskFields and ProjectFields are the names --fields takes on a task listing
// and on `things projects`: the JSON keys a row of that kind can carry, in
// the order the full record prints them. They are read off the struct tags,
// so a field added to the model is selectable without a second list to keep
// in step.
var (
	TaskFields    = jsonKeys(reflect.TypeFor[model.Task]())
	ProjectFields = jsonKeys(reflect.TypeFor[model.Project]())
)

// jsonKeys lists the keys encoding/json writes for struct type t. The model
// types have no embedded or untagged exported fields, so the tag name is
// the whole rule.
func jsonKeys(t reflect.Type) []string {
	var keys []string
	for f := range t.Fields() {
		if !f.IsExported() {
			continue
		}
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "-" {
			continue
		}
		if name == "" {
			name = f.Name
		}
		keys = append(keys, name)
	}
	return keys
}

// UnknownFieldError is a --fields list naming a key the rows do not have.
// Kind is "task" or "project", the kind of row the listing prints.
type UnknownFieldError struct {
	Kind    string
	Unknown []string
	Valid   []string
}

func (e *UnknownFieldError) Error() string {
	quoted := make([]string, len(e.Unknown))
	for i, u := range e.Unknown {
		quoted[i] = fmt.Sprintf("%q", u)
	}
	noun := "field"
	if len(e.Unknown) > 1 {
		noun = "fields"
	}
	return fmt.Sprintf("--fields: unknown %s %s for a %s listing; valid fields: %s",
		noun, strings.Join(quoted, ", "), e.Kind, strings.Join(e.Valid, ", "))
}

// ParseFields reads a --fields value: a comma-separated list of keys from
// valid. Each name is trimmed and a repeat is dropped, keeping the first, so
// the result is the order the caller asked for. A name not in valid is an
// *UnknownFieldError naming every such name; a list with no names at all is
// refused too, since printing empty objects is never what was meant.
func ParseFields(raw, kind string, valid []string) ([]string, error) {
	var fields, unknown []string
	for name := range strings.SplitSeq(raw, ",") {
		name = strings.TrimSpace(name)
		if name == "" || slices.Contains(fields, name) || slices.Contains(unknown, name) {
			continue
		}
		if !slices.Contains(valid, name) {
			unknown = append(unknown, name)
			continue
		}
		fields = append(fields, name)
	}
	if len(unknown) > 0 {
		// A copy, so a caller holding the error cannot rewrite the shared
		// TaskFields or ProjectFields it was given.
		return nil, &UnknownFieldError{Kind: kind, Unknown: unknown, Valid: slices.Clone(valid)}
	}
	if len(fields) == 0 {
		return nil, fmt.Errorf("--fields names no fields; valid fields: %s", strings.Join(valid, ", "))
	}
	return fields, nil
}

// PrintTaskFields prints tasks as the JSON array --json does, each row cut
// down to fields in that order. A field the full record would omit on a row
// (an empty omitempty value) is omitted here too.
func PrintTaskFields(w io.Writer, tasks []model.Task, fields []string) error {
	return printFields(w, tasks, fields)
}

// PrintProjectFields is PrintTaskFields for a project listing.
func PrintProjectFields(w io.Writer, projects []model.Project, fields []string) error {
	return printFields(w, projects, fields)
}

// printFields projects each row's marshalled form rather than the struct, so
// the values are encoded exactly as the full record encodes them.
func printFields[T any](w io.Writer, rows []T, fields []string) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	// As in PrintJSON: the values are read as text, so "R&D" stays as
	// written. The outer encoder keeps that setting when it re-indents.
	enc.SetEscapeHTML(false)
	if err := enc.Encode(rows); err != nil {
		return err
	}
	var full []map[string]json.RawMessage
	if err := json.Unmarshal(buf.Bytes(), &full); err != nil {
		return err
	}
	out := make([]fieldRow, len(full))
	for i, values := range full {
		out[i] = fieldRow{keys: fields, values: values}
	}
	return PrintJSON(w, out)
}

// fieldRow is one projected row: keys in the order asked for, each written
// only when the full record carries it.
type fieldRow struct {
	keys   []string
	values map[string]json.RawMessage
}

func (r fieldRow) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	first := true
	for _, k := range r.keys {
		v, ok := r.values[k]
		if !ok {
			continue
		}
		if !first {
			b.WriteByte(',')
		}
		first = false
		key, err := json.Marshal(k)
		if err != nil {
			return nil, err
		}
		b.Write(key)
		b.WriteByte(':')
		b.Write(v)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}
