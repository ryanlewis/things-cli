package main

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/alecthomas/kong"

	"github.com/ryanlewis/things-cli/internal/output"
)

// fieldKeys parses a --fields listing and returns each row's keys in the
// order printed.
func fieldKeys(t *testing.T, out string) [][]string {
	t.Helper()
	var rows []json.RawMessage
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("stdout is not a JSON array: %v\n%s", err, out)
	}
	keys := make([][]string, len(rows))
	for i, r := range rows {
		dec := json.NewDecoder(strings.NewReader(string(r)))
		if _, err := dec.Token(); err != nil { // {
			t.Fatal(err)
		}
		for dec.More() {
			tok, err := dec.Token()
			if err != nil {
				t.Fatal(err)
			}
			keys[i] = append(keys[i], tok.(string))
			var skip json.RawMessage
			if err := dec.Decode(&skip); err != nil {
				t.Fatal(err)
			}
		}
	}
	return keys
}

func TestRunListingFields(t *testing.T) {
	cases := map[string]struct {
		args []string
		want []string
	}{
		"bare default view": {[]string{"--json", "--fields", "title,uuid"}, []string{"title", "uuid"}},
		"named view":        {[]string{"--json", "anytime", "--fields", "uuid,projectTitle"}, []string{"uuid", "projectTitle"}},
		"list with filter":  {[]string{"-j", "list", "--project", "Chores", "--fields", "title"}, []string{"title"}},
		"search":            {[]string{"-j", "search", "milk", "--fields", "uuid, title,uuid"}, []string{"uuid", "title"}},
		"projects":          {[]string{"-j", "projects", "--fields", "openCount,title"}, []string{"openCount", "title"}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			stdout, _, err := runStreams(t, seedFullDB(t), c.args...)
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			rows := fieldKeys(t, stdout)
			if len(rows) == 0 {
				t.Fatalf("no rows listed:\n%s", stdout)
			}
			for i, keys := range rows {
				// A row may lack an omitempty key, never carry an extra one
				// or change the order.
				if len(keys) == 0 || !isSubsequence(keys, c.want) {
					t.Errorf("row %d keys = %v, want %v", i, keys, c.want)
				}
			}
		})
	}
}

func isSubsequence(got, of []string) bool {
	j := 0
	for _, k := range got {
		for j < len(of) && of[j] != k {
			j++
		}
		if j == len(of) {
			return false
		}
		j++
	}
	return true
}

// A row whose project is set prints projectTitle where it was asked for.
func TestRunListingFieldsCarryValues(t *testing.T) {
	stdout, _, err := runStreams(t, seedFullDB(t), "--json", "today", "--fields", "projectTitle,title")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	want := "[\n  {\n    \"projectTitle\": \"Chores\",\n    \"title\": \"Buy milk\"\n  }\n]\n"
	if stdout != want {
		t.Errorf("stdout =\n%s\nwant\n%s", stdout, want)
	}
}

// Plain output has its own columns, so --fields there is a mistake to report,
// not a flag to ignore.
func TestRunFieldsNeedsJSON(t *testing.T) {
	for _, args := range [][]string{
		{"--fields", "uuid"},
		{"anytime", "--fields", "uuid"},
		{"projects", "--fields", "uuid"},
		{"search", "milk", "--fields", "uuid"},
	} {
		stdout, _, err := runStreams(t, seedFullDB(t), args...)
		if err == nil || !strings.Contains(err.Error(), "needs --json") {
			t.Errorf("%v: err = %v, want the needs --json refusal", args, err)
		}
		if stdout != "" {
			t.Errorf("%v: stdout = %q, want nothing", args, stdout)
		}
	}
}

func TestRunFieldsRejectsUnknownName(t *testing.T) {
	cases := map[string]struct {
		args  []string
		kind  string
		valid []string
	}{
		"task listing": {[]string{"-j", "anytime", "--fields", "uuid,project"}, "task", output.TaskFields},
		"projects":     {[]string{"-j", "projects", "--fields", "uuid,notes"}, "project", output.ProjectFields},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			stdout, _, err := runStreams(t, seedFullDB(t), c.args...)
			if err == nil {
				t.Fatal("unknown field accepted")
			}
			if stdout != "" {
				t.Errorf("stdout = %q, want nothing before the error", stdout)
			}
			p := errorPayload(err)
			if p.Error != "unknown field" || p.Kind != c.kind {
				t.Errorf("payload error/kind = %q/%q", p.Error, p.Kind)
			}
			if len(p.Unknown) != 1 || !slices.Equal(p.Valid, c.valid) {
				t.Errorf("payload unknown/valid = %v/%v", p.Unknown, p.Valid)
			}
		})
	}
}

// The names are checked before the database is opened, so a bad list is
// reported as such even where there is no database to read.
func TestRunFieldsCheckedBeforeDatabase(t *testing.T) {
	_, _, err := runStreams(t, nil, "--db", "/nonexistent/main.sqlite", "-j", "--fields", "bogus")
	var ufe *output.UnknownFieldError
	if !errors.As(err, &ufe) {
		t.Errorf("err = %v, want the unknown-field error", err)
	}
}

// show prints the full record; --fields is a listing flag only.
func TestShowTakesNoFields(t *testing.T) {
	var cli CLI
	parser, err := kong.New(&cli, parserOptions(nil)...)
	if err != nil {
		t.Fatalf("kong.New: %v", err)
	}
	if _, err := parser.Parse([]string{"-j", "show", "task-1", "--fields", "uuid"}); err == nil {
		t.Error("show accepted --fields")
	}
}
