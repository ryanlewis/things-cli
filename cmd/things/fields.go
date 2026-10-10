package main

import (
	"fmt"

	"github.com/ryanlewis/things-cli/internal/output"
)

// fieldsHelp is the --fields help text, shared by every listing that takes it.
const fieldsHelp = "With --json, print only these comma-separated keys of each row, in this order, e.g. uuid,title,deadline, or 'all' for the full record. Without it, --json prints a default set that leaves out notes, the parent uuids and Things' bookkeeping. A row leaves out a key it would leave out of the full record. Names are the JSON keys as printed; an unknown one is rejected with the list of valid ones."

// listingFields reads a listing's --fields into the keys to print, or nil
// for the full record or plain output. A JSON listing without --fields
// prints defaults, the default set for its kind. It runs before the database is opened: a flag the
// command cannot honour is a mistake in the invocation, not something to
// report after the work is done.
//
// --fields only shapes JSON. Plain output has its own columns, and quietly
// ignoring the flag there would leave a script believing it had asked for
// something it got.
func listingFields(d *Deps, raw *string, kind string, valid, defaults []string) ([]string, error) {
	if raw == nil {
		if d.JSON {
			return defaults, nil
		}
		return nil, nil
	}
	if !d.JSON {
		return nil, fmt.Errorf("--fields selects keys of the JSON output, so it needs --json (-j)")
	}
	return output.ParseFields(*raw, kind, valid)
}
