package things

import (
	"fmt"
	"os/exec"
	"strings"
)

var execCommand = exec.Command

func runAppleScript(script string, context string) error {
	cmd := execCommand("osascript", "-e", script)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s: %w: %s", context, err, out)
	}
	return nil
}

// setStatus sets the status of the item of class ("to do" or "project")
// with id uuid to status ("completed" or "canceled"). context names the
// action in an error.
func setStatus(class, uuid, status, context string) error {
	return runAppleScript(fmt.Sprintf(`tell application "Things3"
set theItem to %s id %s
set status of theItem to %s
end tell`, class, appleScriptString(uuid), status), context)
}

func CompleteTask(uuid string) error {
	return setStatus("to do", uuid, "completed", "completing task")
}

func CompleteProject(uuid string) error {
	return setStatus("project", uuid, "completed", "completing project")
}

func LogCompleted() error {
	return runAppleScript(`tell application "Things3"
log completed now
end tell`, "logging completed items")
}

func CancelTask(uuid string) error {
	return setStatus("to do", uuid, "canceled", "cancelling task")
}

func CancelProject(uuid string) error {
	return setStatus("project", uuid, "canceled", "cancelling project")
}

// appleScriptString renders s as an AppleScript string literal, quotes
// included. A tag name comes straight from the command line, so it has to be
// escaped: an unescaped quote or backslash would end the literal early and
// turn the rest of the name into script. A UUID the CLI read out of the
// database goes through it too, so no script interpolates a raw string.
func appleScriptString(s string) string {
	r := strings.NewReplacer(
		`\`, `\\`,
		`"`, `\"`,
		"\n", `\n`,
		"\r", `\r`,
		"\t", `\t`,
	)
	return `"` + r.Replace(s) + `"`
}

// CreateTag creates a tag in Things. The URL scheme cannot create tags — it
// applies only ones that already exist — so this is the only route.
//
// It does not check first: callers compare against the database (see
// db.UnknownTags) and pass only names that are missing.
func CreateTag(name string) error {
	return runAppleScript(fmt.Sprintf(`tell application "Things3"
make new tag with properties {name:%s}
end tell`, appleScriptString(name)), "creating tag")
}
