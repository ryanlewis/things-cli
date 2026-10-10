package main

import (
	"strings"

	"github.com/ryanlewis/things-cli/internal/db"
)

// Things matches a list, area or heading title without trimming it, so
// ` Errands ` misses the list called Errands. add, project add and edit
// send a name as given when Things matches it as given, which keeps a title
// that really has surrounding space reachable, and otherwise send it trimmed
// when that matches. A name neither form matches goes as given, and the
// caller warns about it. import does not trim: its payload is Things' own
// format and goes as given.

// addTargetName is db.AddTarget with that rule applied to list and heading.
// It returns the list and heading to send.
func addTargetName(database *db.DB, list, heading string) (target db.Target, headingFound bool, sentList, sentHeading string, err error) {
	target, headingFound, err = database.AddTarget(list, heading)
	if err != nil {
		return db.Target{}, false, list, heading, err
	}
	sentList, sentHeading = list, heading
	if trimmed, ok := trimmedName(list); ok && target.UUID == "" {
		t, f, err := database.AddTarget(trimmed, heading)
		if err != nil {
			return db.Target{}, false, list, heading, err
		}
		if t.UUID != "" {
			target, headingFound, sentList = t, f, trimmed
		}
	}
	if trimmed, ok := trimmedName(heading); ok && target.UUID != "" && !headingFound {
		id, err := database.HeadingTarget(target.UUID, trimmed)
		if err != nil {
			return db.Target{}, false, list, heading, err
		}
		if id != "" {
			target.Heading, headingFound, sentHeading = id, true, trimmed
		}
	}
	return target, headingFound, sentList, sentHeading, nil
}

// areaTargetName is db.AreaTarget with that rule applied to area. It returns
// the area to send.
func areaTargetName(database *db.DB, area string) (db.Target, string, error) {
	t, err := database.AreaTarget(area)
	if err != nil {
		return db.Target{}, area, err
	}
	if trimmed, ok := trimmedName(area); ok && t.UUID == "" {
		tt, err := database.AreaTarget(trimmed)
		if err != nil {
			return db.Target{}, area, err
		}
		if tt.UUID != "" {
			return tt, trimmed, nil
		}
	}
	return t, area, nil
}

// trimmedName is name without surrounding space, and whether that differs
// from name and is still a name.
func trimmedName(name string) (string, bool) {
	t := strings.TrimSpace(name)
	return t, t != name && t != ""
}
