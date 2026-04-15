package things

import (
	"fmt"
	"os/exec"
)

// execCommand is a seam for tests to mock exec.Command.
var execCommand = exec.Command

func runAppleScript(script string, context string) error {
	cmd := execCommand("osascript", "-e", script)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s: %w: %s", context, err, out)
	}
	return nil
}

func CompleteTask(uuid string) error {
	return runAppleScript(fmt.Sprintf(`tell application "Things3"
set theToDo to to do id "%s"
set status of theToDo to completed
end tell`, uuid), "completing task")
}

func CompleteProject(uuid string) error {
	return runAppleScript(fmt.Sprintf(`tell application "Things3"
set theProject to project id "%s"
set status of theProject to completed
end tell`, uuid), "completing project")
}

func CancelTask(uuid string) error {
	return runAppleScript(fmt.Sprintf(`tell application "Things3"
set theToDo to to do id "%s"
set status of theToDo to canceled
end tell`, uuid), "cancelling task")
}
