package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

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
		return fmt.Errorf("cancelled")
	}
	return nil
}

func confirmAction(d *Deps, msg string) bool {
	if !d.interactive() {
		return false
	}
	fmt.Fprintf(os.Stderr, "%s [y/N]: ", msg)
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return false
	}
	answer := strings.TrimSpace(strings.ToLower(scanner.Text()))
	return answer == "y" || answer == "yes"
}
