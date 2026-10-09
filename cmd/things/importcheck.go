package main

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ryanlewis/things-cli/internal/model"
)

// unresolvableItemTypes are the payload item types GetTaskByUUID will never
// return a row for, so the repeating check and the read-back skip them rather
// than warn that Things does not know an id it knows perfectly well.
//
// Checklist items live in their own table, not TMTask. Headings are TMTask
// rows but the lookups exclude the heading type (issue #146). Neither can
// repeat or carry a status, so nothing is lost by skipping them.
var unresolvableItemTypes = map[string]bool{
	"checklist-item": true,
	"heading":        true,
}

// importUpdate is one `operation: update` item found in an import payload,
// reduced to the parts the repeating check and the status read-back need.
type importUpdate struct {
	// path locates the item in the payload for error messages, e.g. `[2]` for
	// a top-level item or `[2].attributes.items[0]` for one nested in a
	// project.
	path     string
	itemType string
	id       string
	attrs    map[string]any
}

// resolvable reports whether the item names a row the CLI can look up. Items
// without an id are Things' problem to report, and checklist items and
// headings are not reachable through GetTaskByUUID.
func (u importUpdate) resolvable() bool {
	return u.id != "" && !unresolvableItemTypes[u.itemType]
}

// importPlan is what the pre-write pass learned about a payload: the update
// items in it, and the database rows they point at. Sharing the lookups means
// the read-back afterwards knows each item's title without querying again.
type importPlan struct {
	updates []importUpdate
	tasks   map[string]*model.Task // by id; a nil value means "not in the database"
	creates []importCreate
	missing []string // update items with no row, as `path (id …)`
}

// importUpdates collects every `operation: update` item in a Things JSON
// payload, at any depth.
func importUpdates(payload []any) []importUpdate {
	var updates []importUpdate
	walkImportNode(payload, "", func(path string, v map[string]any) {
		if op, _ := v["operation"].(string); op != "update" {
			return
		}
		id, _ := v["id"].(string)
		itemType, _ := v["type"].(string)
		attrs, _ := v["attributes"].(map[string]any)
		updates = append(updates, importUpdate{
			path:     path,
			itemType: strings.TrimSpace(itemType),
			id:       strings.TrimSpace(id),
			attrs:    attrs,
		})
	})
	return updates
}

// importCreate is one to-do or project an import payload creates. Headings
// and checklist items are created too, but are not read back: neither is
// reachable through the task lookups (see unresolvableItemTypes), and a
// checklist item is saved with its to-do.
type importCreate struct {
	path  string
	typ   model.TaskType
	title string // trimmed
	// shown is the title as Things saves it: untrimmed, with each line
	// feed as a space (storedTitle).
	shown string
	// dated is set when the payload gives the item a `creation-date`.
	// Things saves it with that date, so it is not read back: one in the
	// past never shows up among the items created since the write.
	dated bool
	// datedAt is that creation-date (see parseThingsDate).
	datedAt time.Time
	// closedAt is the completion-date of an item the payload completes or
	// cancels, zero when there is none or the CLI cannot read it. Measured in
	// Things 3: the item is saved with that stopDate, and an open item's
	// completion-date is ignored.
	closedAt time.Time
	// closed is set when the payload completes or cancels the item.
	closed bool
	// when is the item's `when` attribute, "" when it gives none or one
	// that is not a string.
	when string
	// to is where the payload files the item, and dest is where the CLI
	// resolved that to before the import was sent (see resolveImportDests).
	to   importTo
	dest createdDest
}

// datedSlack widens the read-back window for a dated item's creation-date,
// which the payload gives to the second (or coarser), so a date written as
// "now" just before the import still counts as inside it.
const datedSlack = time.Minute

// landsInWindow reports whether c, a dated item, may be saved inside the
// read-back window that starts at since, where its row could pass for a new
// undated item with the same type and title. A zero datedAt might be
// anything, so it may.
func (c importCreate) landsInWindow(since time.Time) bool {
	return c.datedAt.IsZero() || !c.datedAt.Before(since.Add(-datedSlack))
}

// creationDateShape is the shape of every `creation-date` Things was seen to
// take: a date, T, a time to the second with an optional fraction, and a UTC
// offset. Things takes one-digit fields, a lowercase z, offsets such as +2,
// +02:0 or +01:00:00 (read as +01:00) too, and trailing spaces, and rejects
// the whole payload over a value without this shape: a date with no time, a
// time with no seconds or no offset, a lowercase t, a comma before the
// fraction, a leading space, or a number. The groups are the year, month,
// day, hour, minute, second, fraction, and the offset's sign, hours, minutes
// and seconds.
//
// The CLI is stricter than Things on malformed dates. Measured in Things 3
// (9 Oct 2026): it takes +012, ZZ, a five-digit year, T100:00:00 and +100
// by guessing at them or dropping the date, which saves the item with a
// date the payload did not give and no word of it. So the year is held to
// four digits, the other date and time fields to two, and the offset to the
// forms dateOffset takes, and anything else is refused.
var creationDateShape = regexp.MustCompile(`^(\d{1,4})-(\d{1,2})-(\d{1,2})T(\d{1,2}):(\d{1,2}):(\d{1,2})(?:\.(\d+))?(?:[Zz]|([+-])(\d+)(?::(\d+))?(?::(\d+))?)$`)

// maxDateOffset is the largest UTC offset parseThingsDate takes. Things was
// not measured between +14 and +200; 18 hours is the widest offset Foundation
// time zones allow, so nothing a real zone uses is refused.
const maxDateOffset = 18 * time.Hour

// parseThingsDate reads a `creation-date` or `completion-date`, reporting
// false for a value Things rejects the whole payload over: one without the
// creationDateShape shape, or one with the shape that names no real instant.
//
// Measured in Things 3 (8 Oct 2026): month 13 and offset +200 are rejected,
// while hour 25 is taken and rolled into the next day. So the month must be
// 1 to 12, the day 1 to 31, and the offset written as one or two digits of
// hours, four digits of hours and minutes, or hours, minutes and seconds
// separated by colons, no wider than maxDateOffset. An hour, minute or second
// past its range rolls over, as Things does with the hour. A day past the end
// of a shorter month was not measured, and rolls over the same way. Things
// saves a date that rolls into the future as now, as it does any future
// date, which futureImportDates refuses.
func parseThingsDate(raw any) (time.Time, bool) {
	str, ok := raw.(string)
	if !ok {
		return time.Time{}, false
	}
	m := creationDateShape.FindStringSubmatch(strings.TrimRight(str, " "))
	if m == nil {
		return time.Time{}, false
	}
	var n [6]int
	for i := range n {
		v, err := strconv.Atoi(m[i+1])
		if err != nil {
			return time.Time{}, false
		}
		n[i] = v
	}
	year, month, day, hour, minute, second := n[0], n[1], n[2], n[3], n[4], n[5]
	if month < 1 || month > 12 || day < 1 || day > 31 {
		return time.Time{}, false
	}
	var nsec int
	if frac := m[7]; frac != "" {
		frac = (frac + "000000000")[:9]
		nsec, _ = strconv.Atoi(frac)
	}
	zone := time.UTC
	if sign := m[8]; sign != "" {
		offset, ok := dateOffset(m[9], m[10], m[11])
		if !ok {
			return time.Time{}, false
		}
		if sign == "-" {
			offset = -offset
		}
		zone = time.FixedZone("", int(offset/time.Second))
	}
	return time.Date(year, time.Month(month), day, hour, minute, second, nsec, zone), true
}

// dateOffset reads the hours, minutes and seconds of a creation-date's UTC
// offset, as creationDateShape splits them, reporting false for a form or a
// size parseThingsDate does not take.
func dateOffset(hours, minutes, seconds string) (time.Duration, bool) {
	if minutes == "" {
		switch len(hours) {
		case 1, 2:
		case 4:
			hours, minutes = hours[:2], hours[2:]
		default:
			return 0, false
		}
	}
	if len(hours) > 2 || len(minutes) > 2 || len(seconds) > 2 {
		return 0, false
	}
	var parts [3]int
	for i, s := range []string{hours, minutes, seconds} {
		if s == "" {
			continue
		}
		v, err := strconv.Atoi(s)
		if err != nil {
			return 0, false
		}
		parts[i] = v
	}
	if parts[1] > 59 || parts[2] > 59 {
		return 0, false
	}
	offset := time.Duration(parts[0])*time.Hour + time.Duration(parts[1])*time.Minute + time.Duration(parts[2])*time.Second
	return offset, offset <= maxDateOffset
}

// shownTitle is the title the output reports for c: the one Things saves.
func (c importCreate) shownTitle() string {
	if c.shown == "" {
		return c.title
	}
	return c.shown
}

func (c importCreate) key() createdKey { return newCreatedKey(c.typ, c.title) }

func (c importCreate) want() createdWant { return createdWant{key: c.key(), dest: c.dest} }

// payloadType is the CLI's type for item, a payload object whose trimmed
// type is to-do or project. ok is false for any other type, and typ is then
// TypeTask.
func payloadType(item map[string]any) (typ model.TaskType, ok bool) {
	switch itemType, _ := item["type"].(string); strings.TrimSpace(itemType) {
	case "to-do":
		return model.TypeTask, true
	case "project":
		return model.TypeProject, true
	}
	return model.TypeTask, false
}

// walkImportNode calls visit for every object in a decoded Things JSON
// payload with the path that locates it. Items nest — a project carries
// `items`, a to-do carries `checklist-items` — so this walks the whole tree.
func walkImportNode(node any, path string, visit func(path string, item map[string]any)) {
	walkImportValues(node, path, func(path string, v any) {
		if item, ok := v.(map[string]any); ok {
			visit(path, item)
		}
	})
}

// walkImportValues is walkImportNode for every value in the payload, not
// only objects, so an item that is not an object is reached too.
func walkImportValues(node any, path string, visit func(path string, v any)) {
	visit(path, node)
	switch v := node.(type) {
	case map[string]any:
		// Walk keys in a fixed order: Go randomises map iteration, and the
		// order items are discovered in decides the order they are reported.
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			walkImportValues(v[key], path+"."+key, visit)
		}
	case []any:
		for i, child := range v {
			walkImportValues(child, fmt.Sprintf("%s[%d]", path, i), visit)
		}
	}
}

// wantedStatus reports the status an update item asks Things to move the item
// to. Both status fields are two-way, per the `update` command's parameter
// table at repeatingDocsURL, which is worth quoting because the cross-status
// cases are the surprising ones:
//
//	completed: "Complete a to-do or set a to-do to incomplete. […] Setting
//	           completed=false on a canceled to-do will also mark it as
//	           incomplete."
//	canceled:  "Cancel a to-do or set a to-do to incomplete. […] Setting
//	           canceled=false on a completed to-do will also mark it as
//	           incomplete."
//
// So a literal `false` asks for `open` whichever status the item is in — the
// two sentences above cover the mismatched pairs explicitly — and a dropped
// reopen is as invisible as a dropped completion. The item's current status is
// therefore not needed here.
//
// `canceled` "Takes priority over `completed`", so it decides the outcome
// whenever it is present — with one exception. The two entries disagree about
// `canceled: false` alongside `completed: true`: `canceled` claims priority
// outright, while `completed` says it is ignored only "if `canceled` is also
// set to `true`". Rather than guess, that one combination is left unverified;
// guessing wrong would fail an import that Things applied correctly. It is
// still refused up front on a repeating target, where the outcome does not
// matter because neither field may be updated at all.
func wantedStatus(attrs map[string]any) (model.Status, bool) {
	completed, hasCompleted := attrs["completed"].(bool)
	canceled, hasCanceled := attrs["canceled"].(bool)
	switch {
	case hasCanceled && hasCompleted && !canceled && completed:
		return model.StatusOpen, false
	case hasCanceled:
		if canceled {
			return model.StatusCancelled, true
		}
		return model.StatusOpen, true
	case hasCompleted:
		if completed {
			return model.StatusCompleted, true
		}
		return model.StatusOpen, true
	}
	return model.StatusOpen, false
}

// warnMissing warns about each update item the database does not have. It
// runs once nothing else can refuse the import: the warning promises that
// Things will report the unknown id itself, which is only true when the
// payload is actually sent.
func (p *importPlan) warnMissing(d *Deps) {
	for _, m := range p.missing {
		fmt.Fprintf(d.errOut(), "warning: %s is not in the Things database — Things will report this itself\n", m)
	}
}
