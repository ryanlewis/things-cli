package things

import (
	"os/exec"
	"strings"
	"testing"
)

// applescriptStub captures the script argument passed to osascript and
// optionally returns a failing command producing `stderr` on CombinedOutput.
func applescriptStub(t *testing.T, failStderr string) *string {
	t.Helper()
	var script string
	orig := execCommand
	execCommand = func(name string, args ...string) *exec.Cmd {
		if name != "osascript" {
			t.Errorf("expected osascript, got %q", name)
		}
		if len(args) != 2 || args[0] != "-e" {
			t.Errorf("expected [-e SCRIPT], got %v", args)
		} else {
			script = args[1]
		}
		if failStderr != "" {
			return exec.Command("sh", "-c", "echo -n '"+failStderr+"' >&2; exit 1")
		}
		return exec.Command("true")
	}
	t.Cleanup(func() { execCommand = orig })
	return &script
}

func TestCompleteTaskScript(t *testing.T) {
	script := applescriptStub(t, "")

	if err := CompleteTask("ABC-123"); err != nil {
		t.Fatalf("CompleteTask: %v", err)
	}
	for _, want := range []string{
		`tell application "Things3"`,
		`to do id "ABC-123"`,
		`set status of theToDo to completed`,
		`end tell`,
	} {
		if !strings.Contains(*script, want) {
			t.Errorf("script missing %q:\n%s", want, *script)
		}
	}
}

func TestCompleteProjectScript(t *testing.T) {
	script := applescriptStub(t, "")

	if err := CompleteProject("PRJ-1"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`project id "PRJ-1"`,
		`set status of theProject to completed`,
	} {
		if !strings.Contains(*script, want) {
			t.Errorf("script missing %q:\n%s", want, *script)
		}
	}
}

func TestCancelTaskScript(t *testing.T) {
	script := applescriptStub(t, "")

	if err := CancelTask("ZZZ"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`to do id "ZZZ"`,
		`set status of theToDo to canceled`,
	} {
		if !strings.Contains(*script, want) {
			t.Errorf("script missing %q:\n%s", want, *script)
		}
	}
}

func TestCancelProjectScript(t *testing.T) {
	script := applescriptStub(t, "")

	if err := CancelProject("PRJ-2"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`project id "PRJ-2"`,
		`set status of theProject to canceled`,
	} {
		if !strings.Contains(*script, want) {
			t.Errorf("script missing %q:\n%s", want, *script)
		}
	}
}

func TestLogCompletedScript(t *testing.T) {
	script := applescriptStub(t, "")

	if err := LogCompleted(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`tell application "Things3"`,
		`log completed now`,
		`end tell`,
	} {
		if !strings.Contains(*script, want) {
			t.Errorf("script missing %q:\n%s", want, *script)
		}
	}
}

func TestAppleScriptErrorIncludesOutput(t *testing.T) {
	applescriptStub(t, "script failure output")

	err := CompleteTask("UUID")
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "completing task") {
		t.Errorf("error missing context: %v", err)
	}
	if !strings.Contains(msg, "script failure output") {
		t.Errorf("error missing stderr output: %v", err)
	}
}

func TestAppleScriptString(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"plain", "Work", `"Work"`},
		{"quote", `say "hi"`, `"say \"hi\""`},
		{"backslash", `a\b`, `"a\\b"`},
		{"backslashQuote", `a\"b`, `"a\\\"b"`},
		{"newline", "a\nb", `"a\nb"`},
		{"carriageReturn", "a\rb", `"a\rb"`},
		{"tab", "a\tb", `"a\tb"`},
		{"unicode", "café ✅", `"café ✅"`},
		{"empty", "", `""`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := appleScriptString(c.in); got != c.want {
				t.Errorf("appleScriptString(%q) = %s, want %s", c.in, got, c.want)
			}
		})
	}
}

func TestCreateTagScript(t *testing.T) {
	script := applescriptStub(t, "")

	if err := CreateTag("urgent"); err != nil {
		t.Fatalf("CreateTag: %v", err)
	}
	want := `tell application "Things3"
make new tag with properties {name:"urgent"}
end tell`
	if *script != want {
		t.Errorf("script =\n%s\nwant:\n%s", *script, want)
	}
}

// A tag name is user input, so it must not be able to close the AppleScript
// string literal and have the remainder run as script.
func TestCreateTagEscapesName(t *testing.T) {
	script := applescriptStub(t, "")

	if err := CreateTag(`ev"il\ tag`); err != nil {
		t.Fatalf("CreateTag: %v", err)
	}
	want := `tell application "Things3"
make new tag with properties {name:"ev\"il\\ tag"}
end tell`
	if *script != want {
		t.Errorf("script =\n%s\nwant:\n%s", *script, want)
	}
}

func TestCreateTagError(t *testing.T) {
	applescriptStub(t, "no tag for you")

	err := CreateTag("urgent")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "creating tag") {
		t.Errorf("error lacks context: %v", err)
	}
	if !strings.Contains(err.Error(), "no tag for you") {
		t.Errorf("error drops osascript output: %v", err)
	}
}
