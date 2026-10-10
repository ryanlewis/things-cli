package main

import (
	"fmt"

	"github.com/ryanlewis/things-cli/internal/db"
)

// The warnings add, edit, project and import give when Things cannot find
// the list, area or heading an item was sent to. Agents read this wording,
// so each command builds it here rather than by hand. Where the rules differ
// between commands, the caller decides; these only say it.

// noTarget is the warning for ref, sent by uuid when byID and by title
// otherwise, when Things finds no noun ("project or area", or "area") for
// it. next says what Things does instead. It says "finds no" rather than
// "has no": the check is of how Things matches the name, and an item can
// exist that Things will still not match, such as one by its uuid sent as a
// title.
func noTarget(noun, ref string, byID bool, next string) string {
	if byID {
		return fmt.Sprintf("Things finds no %s with id %q; %s", noun, ref, next)
	}
	return fmt.Sprintf("Things finds no %s called %q; %s", noun, ref, next)
}

// headingNeedsList is the warning for a heading sent with no list to file it
// in. name is the flag or field ("--heading", "heading"), needs what would
// give it a list, and next what Things does with the to-do instead.
func headingNeedsList(name, heading, needs, next string) string {
	return fmt.Sprintf("%s %q needs %s; Things will ignore it and %s", name, heading, needs, next)
}

// listName is how a warning names the list ref led to: by its title when
// ref was the list's uuid, since that is the name the user knows it by, and
// as given otherwise.
func listName(ref string, t db.Target) string {
	if t.ByUUID && t.Title != "" {
		return t.Title
	}
	return ref
}

// noHeading is the warning for a heading title list does not have. next is
// what Things does with the to-do instead.
func noHeading(list, heading, next string) string {
	return fmt.Sprintf("%q has no heading %q; Things will %s", list, heading, next)
}

// noHeadingID is the warning for a heading-id Things cannot find. next is
// what Things does instead.
func noHeadingID(id, next string) string {
	return fmt.Sprintf("Things has no heading with id %q; %s", id, next)
}
