package main

import (
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ryanlewis/things-cli/internal/clock"
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

func TestDecodeImportJSON(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		wantErr string
	}{
		{"valid", `[{"type":"to-do","attributes":{"title":"x"}}]`, ""},
		{"notArray", `{"type":"to-do"}`, "must be a JSON array"},
		{"emptyArray", `[]`, "empty"},
		{"blank", " \n", "empty payload"},
		{"syntax", `[{"type":}]`, "invalid JSON at line 1"},
		{"multilineSyntax", "[\n  {\"a\": 1,},\n]", "invalid JSON at line"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := decodeImportJSON([]byte(c.input))
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

	if err := runWith(t, database, "--no-verify", "import", "--file", path, "--reveal"); err != nil {
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

	if err := runWith(t, database, "--no-verify", "import"); err != nil {
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

// import reads its payload through Deps.Stdin, like the prompts do, so a
// caller that supplies stdin is the one read.
func TestRunImportReadsDepsStdin(t *testing.T) {
	database := seedFullDB(t)
	captured := stubExec(t)
	stubTTY(t, false)
	payload := `[{"type":"project","attributes":{"title":"P"}}]`
	d := &Deps{DB: database, Stdin: strings.NewReader(payload), Stdout: io.Discard, Stderr: io.Discard, NoVerify: true}

	if err := (&ImportCmd{}).Run(d); err != nil {
		t.Fatalf("import: %v", err)
	}
	parsed, _ := url.Parse((*captured)[2])
	if got := parsed.Query().Get("data"); got != payload {
		t.Errorf("data = %q, want %q", got, payload)
	}
}

// Each when in an item's attributes goes as add sends --when: a date,
// keyword or weekday with a time as YYYY-MM-DD@HH:MM, a weekday alone as its
// date and evening with a time as evening@HH:MM, in a project's items and an
// update alike. Every other value, and every other byte, goes as given.
func TestResolveImportWhens(t *testing.T) {
	now := time.Date(2026, 10, 10, 9, 0, 0, 0, time.Local)
	for _, tc := range []struct{ name, in, want string }{
		{"todayAt", `[{"type":"to-do","attributes":{"title":"a","when":"today@6pm"}}]`,
			`[{"type":"to-do","attributes":{"title":"a","when":"2026-10-10@18:00"}}]`},
		{"spacing kept", "[ {\"type\": \"to-do\",\n  \"attributes\" : { \"when\" :  \"tomorrow@9:30am\" , \"title\":\"a\"} } ]",
			"[ {\"type\": \"to-do\",\n  \"attributes\" : { \"when\" :  \"2026-10-11@09:30\" , \"title\":\"a\"} } ]"},
		{"nested and update", `[{"type":"project","attributes":{"title":"P","when":"2026-10-12@7pm","items":[{"type":"to-do","attributes":{"title":"a","when":"today@18:00"}}]}},{"type":"to-do","operation":"update","id":"x","attributes":{"when":"tomorrow@8am"}}]`,
			`[{"type":"project","attributes":{"title":"P","when":"2026-10-12@19:00","items":[{"type":"to-do","attributes":{"title":"a","when":"2026-10-10@18:00"}}]}},{"type":"to-do","operation":"update","id":"x","attributes":{"when":"2026-10-11@08:00"}}]`},
		{"unresolved kept", `[{"type":"to-do","attributes":{"title":"today@6pm","when":"evening@18:00","notes":"when"}},{"type":"to-do","attributes":{"when":"today"}}]`,
			`[{"type":"to-do","attributes":{"title":"today@6pm","when":"evening@18:00","notes":"when"}},{"type":"to-do","attributes":{"when":"today"}}]`},
		{"weekday and evening", `[{"type":"to-do","attributes":{"when":"friday@9pm"}},{"type":"to-do","attributes":{"when":"saturday"}},{"type":"to-do","attributes":{"when":"evening@6pm"}},{"type":"to-do","attributes":{"when":"fri"}}]`,
			`[{"type":"to-do","attributes":{"when":"2026-10-16@21:00"}},{"type":"to-do","attributes":{"when":"2026-10-17"}},{"type":"to-do","attributes":{"when":"evening@18:00"}},{"type":"to-do","attributes":{"when":"fri"}}]`},
		{"outside attributes", `[{"type":"to-do","when":"today@6pm","attributes":{"title":"a"}}]`,
			`[{"type":"to-do","when":"today@6pm","attributes":{"title":"a"}}]`},
		{"escapes kept", `[{"type":"to-do","attributes":{"title":"é \"q\"","when":"today@6pm","tags":[1,2.50]}}]`,
			`[{"type":"to-do","attributes":{"title":"é \"q\"","when":"2026-10-10@18:00","tags":[1,2.50]}}]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := string(resolveImportWhens([]byte(tc.in), now)); got != tc.want {
				t.Errorf("got  %s\nwant %s", got, tc.want)
			}
		})
	}
}

// The payload sent carries the resolved when.
func TestImportSendsResolvedWhen(t *testing.T) {
	database := seedFullDB(t)
	captured := stubExec(t)
	payload := `[{"type":"to-do","attributes":{"title":"Hi","when":"tomorrow@6pm"}}]`
	path := filepath.Join(t.TempDir(), "payload.json")
	if err := os.WriteFile(path, []byte(payload), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := runWith(t, database, "--no-verify", "import", "--file", path); err != nil {
		t.Fatalf("runWith: %v", err)
	}
	parsed, err := url.Parse((*captured)[2])
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	want := `"when":"` + clock.Now().AddDate(0, 0, 1).Format("2006-01-02") + `@18:00"`
	if got := parsed.Query().Get("data"); !strings.Contains(got, want) {
		t.Errorf("data = %s, want %s", got, want)
	}
}
