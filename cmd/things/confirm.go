package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/ryanlewis/things-cli/internal/db"
)

// errCancelled is returned when the user declines a prompt or closes stdin
// before answering.
var errCancelled = errors.New("cancelled")

// authToken reads the Things auth token for an update. A read error does not
// fail the command — the edit may not need the token — but it is surfaced so
// a later "auth token is required" is not left unexplained.
func authToken(d *Deps, database *db.DB) string {
	token, err := database.GetAuthToken()
	if err != nil {
		fmt.Fprintf(d.errOut(), "warning: could not read Things auth token: %v\n", err)
	}
	return token
}

// ConfirmFlags is embedded in the commands that ask before a project-wide
// write. Kong resolves --yes from the config file's `assume_yes` key as well,
// so the prompt can be turned off once for machine use with the flag still
// deciding each run.
type ConfirmFlags struct {
	Yes bool `help:"Skip the confirmation prompt and proceed." short:"y" name:"yes"`
}

// confirmProjectStatusChange gates a project-wide complete/cancel behind a
// confirmation. When the run cannot prompt — piped stdin, or --json, which
// never prompts — say so instead of returning a bare "cancelled" the caller
// has no way to interpret. assumeYes is the caller's --yes: it answers the
// question up front, which is the only way a machine caller can get through
// this at all.
func confirmProjectStatusChange(d *Deps, assumeYes bool, verb, title string) error {
	if assumeYes {
		return nil
	}
	action := strings.ToLower(verb)
	if !d.interactive() {
		reason := "pass --yes, or re-run in a terminal"
		if d.JSON {
			reason = "pass --yes, or re-run without --json"
		}
		return fmt.Errorf("cancelled: %s project %q needs confirmation, and this run cannot prompt — %s", action, title, reason)
	}
	if !confirmAction(d, fmt.Sprintf("%s project %q? This will also %s all its tasks.", verb, title, action)) {
		return errCancelled
	}
	return nil
}

func confirmAction(d *Deps, msg string) bool {
	if !d.interactive() {
		return false
	}
	line, ok := promptLine(d, fmt.Sprintf("%s [y/N]: ", msg))
	if !ok {
		return false
	}
	answer := strings.ToLower(line)
	return answer == "y" || answer == "yes"
}

// promptLine writes question to stderr and reads one line of the answer from
// stdin, trimmed. It reports false when stdin ends before a line arrives.
// It reads a byte at a time so nothing past the newline is consumed: a
// buffered reader would swallow the input a later prompt needs.
func promptLine(d *Deps, question string) (string, bool) {
	fmt.Fprint(d.errOut(), question)
	var line []byte
	buf := make([]byte, 1)
	for {
		n, err := d.in().Read(buf)
		if n == 1 {
			if buf[0] == '\n' {
				break
			}
			line = append(line, buf[0])
		}
		if err != nil {
			if len(line) == 0 {
				return "", false
			}
			break
		}
	}
	return strings.TrimSpace(string(line)), true
}
