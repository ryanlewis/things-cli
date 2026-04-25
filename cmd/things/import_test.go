package main

import (
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ryanlewis/things-cli/internal/things"
)

func stubExec(t *testing.T) *[]string {
	t.Helper()
	var captured []string
	prev := things.SetExecCommandForTest(func(name string, args ...string) *exec.Cmd {
		captured = append([]string{name}, args...)
		return exec.Command("true")
	})
	t.Cleanup(func() { things.SetExecCommandForTest(prev) })
	return &captured
}

func TestValidateImportJSON(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		wantErr string
	}{
		{"valid", `[{"type":"to-do","attributes":{"title":"x"}}]`, ""},
		{"notArray", `{"type":"to-do"}`, "must be a JSON array"},
		{"emptyArray", `[]`, "empty"},
		{"syntax", `[{"type":}]`, "invalid JSON at line 1"},
		{"multilineSyntax", "[\n  {\"a\": 1,},\n]", "invalid JSON at line"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateImportJSON([]byte(c.input))
			if c.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("want error containing %q, got %v", c.wantErr, err)
			}
		})
	}
}

func TestRunImportFromFile(t *testing.T) {
	database := seedFullDB(t)
	captured := stubExec(t)

	dir := t.TempDir()
	path := filepath.Join(dir, "payload.json")
	payload := `[{"type":"to-do","attributes":{"title":"Hi"}}]`
	if err := os.WriteFile(path, []byte(payload), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := runWith(t, database, "import", "--file", path, "--reveal"); err != nil {
		t.Fatalf("runWith: %v", err)
	}
	if len(*captured) < 3 {
		t.Fatalf("expected open invocation, got %v", *captured)
	}
	parsed, err := url.Parse((*captured)[2])
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	if !strings.HasPrefix((*captured)[2], "things:///json?") {
		t.Errorf("expected json URL, got %q", (*captured)[2])
	}
	q := parsed.Query()
	if q.Get("data") != payload {
		t.Errorf("data = %q, want %q", q.Get("data"), payload)
	}
	if q.Get("reveal") != "true" {
		t.Errorf("reveal = %q", q.Get("reveal"))
	}
}

func TestRunImportFromStdin(t *testing.T) {
	database := seedFullDB(t)
	captured := stubExec(t)

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	payload := `[{"type":"project","attributes":{"title":"P"}}]`
	go func() {
		_, _ = w.Write([]byte(payload))
		w.Close()
	}()
	orig := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = orig; r.Close() })

	if err := runWith(t, database, "import"); err != nil {
		t.Fatalf("runWith: %v", err)
	}
	parsed, _ := url.Parse((*captured)[2])
	if got := parsed.Query().Get("data"); got != payload {
		t.Errorf("data = %q, want %q", got, payload)
	}
}

func TestRunImportInvalidJSON(t *testing.T) {
	database := seedFullDB(t)
	stubExec(t)

	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(path, []byte(`[{"type":}]`), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	err := runWith(t, database, "import", "--file", path)
	if err == nil || !strings.Contains(err.Error(), "invalid JSON") {
		t.Fatalf("expected invalid JSON error, got %v", err)
	}
}

func TestRunImportEmptyPayload(t *testing.T) {
	database := seedFullDB(t)
	stubExec(t)

	dir := t.TempDir()
	path := filepath.Join(dir, "empty.json")
	if err := os.WriteFile(path, []byte("   \n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	err := runWith(t, database, "import", "--file", path)
	if err == nil || !strings.Contains(err.Error(), "empty payload") {
		t.Fatalf("expected empty-payload error, got %v", err)
	}
}
