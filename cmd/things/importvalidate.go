package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ryanlewis/things-cli/internal/clock"
	"github.com/ryanlewis/things-cli/internal/db"
	"github.com/ryanlewis/things-cli/internal/model"
	"github.com/ryanlewis/things-cli/internal/things"
)

// importDateAttrs are the date attributes of a to-do or project, created or
// updated, that Things rejects the whole payload over when it cannot read
// them.
var importDateAttrs = []string{"creation-date", "completion-date"}

// badImportDates returns each date attribute of item, a payload object, that
// Things rejects the whole payload over (see creationDateShape), as
// `name: value` with the value in JSON, and the attribute names. Measured in
// Things 3: a bad date on a heading or a checklist item rejects the payload
// as one on a to-do or project does.
func badImportDates(item map[string]any) (lines, names []string) {
	if itemType, _ := item["type"].(string); !importItemTypes[strings.TrimSpace(itemType)] {
		return nil, nil
	}
	attrs, _ := item["attributes"].(map[string]any)
	for _, name := range importDateAttrs {
		raw := attrs[name]
		if _, ok := parseThingsDate(raw); ok || raw == nil {
			continue
		}
		shown, _ := json.Marshal(raw)
		lines = append(lines, fmt.Sprintf("%s: %s", name, shown))
		names = append(names, name)
	}
	return lines, names
}

// futureImportDates returns each date attribute of item, a payload object,
// that is later than now, as badImportDates does, with the instant it reads
// as. Measured in Things 3 (9 Oct 2026): a creation-date or completion-date
// in the future is saved as now, so the item gets a date the payload did not
// give it. That includes an hour past 23 that rolls into the future, such as
// today's date at 25:00. datedSlack spares a date stamped a moment ahead,
// which Things saves within a minute of what was asked.
func futureImportDates(item map[string]any, now time.Time) (lines, names []string) {
	if itemType, _ := item["type"].(string); !importItemTypes[strings.TrimSpace(itemType)] {
		return nil, nil
	}
	attrs, _ := item["attributes"].(map[string]any)
	for _, name := range importDateAttrs {
		at, ok := parseThingsDate(attrs[name])
		if !ok || !at.After(now.Add(datedSlack)) {
			continue
		}
		shown, _ := json.Marshal(attrs[name])
		lines = append(lines, fmt.Sprintf("%s: %s (%s)", name, shown, at.UTC().Format(time.RFC3339)))
		names = append(names, name)
	}
	return lines, names
}

// importScheduleDate is a date as the Things JSON format writes when and
// deadline: YYYY-MM-DD.
var importScheduleDate = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// importScheduleNumeric matches a when or deadline that starts as a date
// or a time does, with digits, so checkScheduleValue holds it to the date
// forms rather than passing it to Things as an English phrase.
var importScheduleNumeric = regexp.MustCompile(`^\d`)

// importClock24 and importClock12 are the times of day the Things
// documentation gives after the @ of a date and time: 21:30 and 9:30PM, and
// its example 6pm.
var (
	importClock24 = regexp.MustCompile(`^(\d{1,2}):(\d{2})$`)
	importClock12 = regexp.MustCompile(`(?i)^(\d{1,2})(?::(\d{2}))?\s?(am|pm)$`)
)

// importTimedWhens are the when keywords the Things documentation allows a
// time after: a date string is today, tomorrow or YYYY-MM-DD, and its
// example is evening@6pm. It ignores the time after anytime and someday.
var importTimedWhens = []string{"today", "tomorrow", "evening"}

// checkScheduleValue reports why value, a `when` or `deadline` in an import
// payload, is one Things would misread, or "" when it is not. It starts from
// the checks `add` makes (things.NormalizeWhen and NormalizeDeadline), which
// refuse a near-miss of a keyword and a keyword as a deadline. Those pass a
// date or time they cannot read through to Things as an English phrase, but
// `add` sends a URL and import a JSON payload, and the payload's grammar is
// narrower: the Things JSON documentation names only the keywords, a date
// and a date and time. Measured in Things 3 (9 Oct 2026), each saved with no
// warning: when 2026-13-01 lands in Today, when 18:00 in Today with no
// reminder, when tomorrow@25:00 tomorrow with no reminder, when
// someday@18:00 in Someday, deadline 2026-13-01 on 1 Jan 2026 and deadline
// someday as no deadline. So a value that starts with a digit must be a real
// date, YYYY-MM-DD; a deadline has no time; and a when with an @ must be
// today, tomorrow, evening or a real YYYY-MM-DD before it, and a real time of
// day after it. Anything else, keywords, weekdays and English phrases such
// as next friday (measured: filed on that day), is passed through as `add`
// passes it.
func checkScheduleValue(name, value string) string {
	v := strings.TrimSpace(value)
	normalize := things.NormalizeWhen
	if name == "deadline" {
		normalize = things.NormalizeDeadline
	}
	// An impossible date or time is left to the checks below, which name
	// what is wrong with it in the payload's own terms.
	var impossible *things.ImpossibleWhenError
	if _, err := normalize(v); err != nil && !errors.As(err, &impossible) {
		// The message names the add flag; the payload has no flags.
		return strings.ReplaceAll(err.Error(), "--", "")
	}
	if name == "deadline" {
		switch {
		case strings.Contains(v, "@"):
			return "a deadline has no time; give a date as YYYY-MM-DD"
		case !importScheduleNumeric.MatchString(v):
			return ""
		case !importScheduleDate.MatchString(v):
			return "not a date as YYYY-MM-DD"
		}
		return realDate(v)
	}
	day, clockTime, timed := strings.Cut(v, "@")
	if !timed {
		switch {
		case !importScheduleNumeric.MatchString(v):
			return ""
		case !importScheduleDate.MatchString(v):
			return "not a date as YYYY-MM-DD, or a date and time as YYYY-MM-DD@HH:MM"
		}
		return realDate(v)
	}
	switch low := strings.ToLower(day); {
	case low == "anytime" || low == "someday":
		return "Things ignores a time after anytime or someday"
	case slices.Contains(importTimedWhens, low):
	case importScheduleDate.MatchString(day):
		if why := realDate(day); why != "" {
			return why
		}
	default:
		return "before the @ must be today, tomorrow, evening or a date as YYYY-MM-DD"
	}
	if !realClock(clockTime) {
		return "not a real time of day after the @"
	}
	return ""
}

// realDate reports "not a real date" for v, a YYYY-MM-DD that names no day,
// or "" for one that does.
func realDate(v string) string {
	if _, err := time.Parse("2006-01-02", v); err != nil {
		return "not a real date"
	}
	return ""
}

// realClock reports whether v is a time of day as importClock24 or
// importClock12 write it, with each field in range.
func realClock(v string) bool {
	if m := importClock24.FindStringSubmatch(v); m != nil {
		h, _ := strconv.Atoi(m[1])
		mm, _ := strconv.Atoi(m[2])
		return h <= 23 && mm <= 59
	}
	if m := importClock12.FindStringSubmatch(v); m != nil {
		h, _ := strconv.Atoi(m[1])
		mm := 0
		if m[2] != "" {
			mm, _ = strconv.Atoi(m[2])
		}
		return h >= 1 && h <= 12 && mm <= 59
	}
	return false
}

// badImportSchedule returns each when or deadline of item, a payload to-do
// or project created or updated, that Things would misread (see
// checkScheduleValue), as `name: value (why)`, and the attribute names. A
// value that is not a string is left to the type checks.
func badImportSchedule(item map[string]any) (lines, names []string) {
	if itemType, _ := item["type"].(string); !importTaskTypes[strings.TrimSpace(itemType)] {
		return nil, nil
	}
	attrs, _ := item["attributes"].(map[string]any)
	for _, name := range []string{"when", "deadline"} {
		v, ok := attrs[name].(string)
		if !ok {
			continue
		}
		if why := checkScheduleValue(name, v); why != "" {
			shown, _ := json.Marshal(v)
			lines = append(lines, fmt.Sprintf("%s: %s (%s)", name, shown, why))
			names = append(names, name)
		}
	}
	return lines, names
}

// importDestAttrs are the destination attributes of a to-do or project.
var importDestAttrs = []string{"list", "list-id", "heading", "heading-id", "area", "area-id"}

// badImportTypes returns each destination attribute of item, a payload object
// that creates a to-do or project, that is neither a string nor null, as
// `name: value` with the value in JSON, and the attribute names. Measured in
// Things 3: a number, boolean or array there makes it reject the whole
// payload. Update items were not measured, so they are not checked.
func badImportTypes(item map[string]any) (lines, names []string) {
	itemType, _ := item["type"].(string)
	if op, _ := item["operation"].(string); !importTaskTypes[strings.TrimSpace(itemType)] || (op != "" && op != "create") {
		return nil, nil
	}
	attrs, _ := item["attributes"].(map[string]any)
	for _, name := range importDestAttrs {
		switch raw := attrs[name]; raw.(type) {
		case nil, string:
		default:
			shown, _ := json.Marshal(raw)
			lines = append(lines, fmt.Sprintf("%s: %s", name, shown))
			names = append(names, name)
		}
	}
	return lines, names
}

// importTaskTypes are the payload item types that are to-dos or projects,
// by the format's word for them.
var importTaskTypes = map[string]bool{"to-do": true, "project": true}

// restrictedImportAttrs names the attributes in an update item's `attributes`
// that Things refuses on a repeating item, in the order the docs list them. It
// is the payload-shaped counterpart of restrictedEdits, which does the same
// job for the `edit` flags.
//
// Presence is enough, whatever the value. The URL scheme docs say of both
// status fields that "this field cannot be updated on repeating to-dos" (and
// the same for repeating projects), so `"completed": false` is refused exactly
// like `"completed": true` — setting a repeating item to incomplete is as much
// an update of that field as completing it.
func restrictedImportAttrs(attrs map[string]any) []string {
	var blocked []string
	for _, name := range []string{"when", "deadline", "completed", "canceled"} {
		if _, ok := attrs[name]; ok {
			blocked = append(blocked, name)
		}
	}
	return blocked
}

// importRefusalItem is one payload item the pre-write check refused, and one
// entry of the `items` array a --json consumer reads (issue #161).
type importRefusalItem struct {
	Path  string
	ID    string
	Title string
	// Kind is "task" or "project", the word Error() puts in the message. It
	// matches the `kind` an error payload carries, though jsonItems does not
	// emit it — the `items` array has no kind field (issue #245). The `import`
	// payload's own spelling of a task is "to-do", the format's word, not the
	// CLI's.
	Kind string
	// Blocked is every attribute refused on the item: the ones Things does
	// not allow on a repeating item, then the dates it rejects, then the
	// when and deadline it would misread, then the dates in the future, then
	// the attributes of the wrong JSON kind, then the type, operation or
	// attributes that make it an item Things does not take, then the
	// attributes Things ignores on it, then the keys given twice, then the
	// title or notes that are too long, then the title when it is blank.
	Blocked []string

	restricted []string // the attributes a repeating item does not allow
	dates      []string // each date Things rejects, as `name: value`
	schedule   []string // each when or deadline Things would misread, as `name: value (why)`
	future     []string // each date in the future, as `name: value (instant)`
	types      []string // each attribute of the wrong JSON kind, as `name: value`
	shape      []string // each way it is not an item Things takes, as `name: why`
	ignored    []string // each attribute Things ignores on it, as `name: why`
	dups       []string // each key the item gives twice
	long       []string // each title or notes too long, as `name: N characters, over M`
	blank      bool     // a to-do or project created with a blank title
}

// reason is why the item is refused: "repeating", "invalid-date",
// "future-date", "invalid-type", "invalid-item", "duplicate-key",
// "too-long", "blank-title", or several of them, space-separated.
func (it importRefusalItem) reason() string {
	var reasons []string
	if len(it.restricted) > 0 {
		reasons = append(reasons, "repeating")
	}
	if len(it.dates) > 0 || len(it.schedule) > 0 {
		reasons = append(reasons, "invalid-date")
	}
	if len(it.future) > 0 {
		reasons = append(reasons, "future-date")
	}
	if len(it.types) > 0 {
		reasons = append(reasons, "invalid-type")
	}
	if len(it.shape) > 0 || len(it.ignored) > 0 {
		reasons = append(reasons, "invalid-item")
	}
	if len(it.dups) > 0 {
		reasons = append(reasons, "duplicate-key")
	}
	if len(it.long) > 0 {
		reasons = append(reasons, "too-long")
	}
	if it.blank {
		reasons = append(reasons, "blank-title")
	}
	return strings.Join(reasons, " ")
}

// tooManyItems is the reason a payload with more than maxImportItems items
// is refused, given as the reason of the whole --json error.
const tooManyItems = "too-many-items"

// importRefusalError is the whole-payload refusal. It carries the offending
// items, one entry each, so --json can hand them over one by one instead of a
// consumer having to parse them back out of the message.
type importRefusalError struct {
	items []importRefusalItem
	total int // update items in the payload, offending or not
	size  int // items counting towards maxImportItems
}

// oversize reports whether the payload is refused for its size.
func (e *importRefusalError) oversize() bool { return e.size > maxImportItems }

// uniqueInOrder returns names with each later repeat of a name dropped.
func uniqueInOrder(names []string) []string {
	seen := make(map[string]bool, len(names))
	out := names[:0:0]
	for _, n := range names {
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	return out
}

// appendItemLine adds the line naming an item at path, with id, and what is
// wrong with it, to lines, or returns lines as they are when nothing is.
func appendItemLine(lines []string, path, id string, what []string) []string {
	if len(what) == 0 {
		return lines
	}
	return append(lines, fmt.Sprintf("  %s%s %s", path, id, strings.Join(what, ", ")))
}

func (e *importRefusalError) Error() string {
	var repeating, dates, schedule, future, types, shapes, ignored, dups, long, blanks []string
	for _, it := range e.items {
		id := ""
		if it.ID != "" {
			id = " (id " + it.ID + ")"
		}
		if len(it.restricted) > 0 {
			repeating = append(repeating, fmt.Sprintf("  %s (id %s): %q is a repeating %s — %s",
				it.Path, it.ID, it.Title, it.Kind, strings.Join(it.restricted, ", ")))
		}
		dates = appendItemLine(dates, it.Path, id, it.dates)
		schedule = appendItemLine(schedule, it.Path, id, it.schedule)
		future = appendItemLine(future, it.Path, id, it.future)
		types = appendItemLine(types, it.Path, id, it.types)
		shapes = appendItemLine(shapes, it.Path, id, it.shape)
		ignored = appendItemLine(ignored, it.Path, id, it.ignored)
		dups = appendItemLine(dups, it.Path, id, it.dups)
		long = appendItemLine(long, it.Path, id, it.long)
		if it.blank {
			blanks = append(blanks, "  "+it.Path)
		}
	}
	var parts []string
	if e.oversize() {
		parts = append(parts, fmt.Sprintf("The payload creates %d items (update items and checklist items not counted). Above %d, Things stops to ask \"Is this what you intended?\" and creates nothing until someone answers, so the import cannot be read back. Split it into imports of at most %d items each.",
			e.size, maxImportItems, maxImportItems))
	}
	if len(repeating) > 0 {
		parts = append(parts, fmt.Sprintf("%d of %d update items change attributes Things does not allow on repeating items, and drops the request silently (%s):\n%s",
			len(repeating), e.total, repeatingDocsURL, strings.Join(repeating, "\n")))
	}
	if len(dates) > 0 {
		parts = append(parts, fmt.Sprintf("Things rejects the whole payload over a creation-date or completion-date that is not a date and time with seconds and a UTC offset, such as 2026-10-05T10:30:00Z or 2026-10-05T10:30:00+02:00:\n%s",
			strings.Join(dates, "\n")))
	}
	if len(schedule) > 0 {
		parts = append(parts, fmt.Sprintf("Things saves a when or deadline it cannot read as something else, with no warning (a when of 2026-13-01 or 18:00 lands in Today, a deadline of 2026-13-01 on 1 Jan 2026). A misspelt keyword, or a keyword as a deadline, is refused. A when or deadline that starts with a digit must be a date as YYYY-MM-DD, and a deadline has no time. A when with an @ must have today, tomorrow, evening or a YYYY-MM-DD date before it and a time of day such as 18:00 or 6pm after it:\n%s",
			strings.Join(schedule, "\n")))
	}
	if len(future) > 0 {
		parts = append(parts, fmt.Sprintf("Things saves a creation-date or completion-date in the future as now, so the item would not get the date the payload gives. Give a date in the past, or leave the attribute out:\n%s",
			strings.Join(future, "\n")))
	}
	if len(types) > 0 {
		parts = append(parts, fmt.Sprintf("Things rejects the whole payload over an attribute of the wrong JSON type. title, notes, when, deadline, list, list-id, heading, heading-id, area and area-id must be strings, tags an array of strings, completed and canceled true or false, items and checklist-items arrays (each may be null), and attributes an object:\n%s",
			strings.Join(types, "\n")))
	}
	if len(shapes) > 0 {
		parts = append(parts, fmt.Sprintf("Things rejects the whole payload over an item it does not take: one that is not an object, has no attributes, or whose type or operation it does not know or does not allow there, written exactly (to-do or project at the top level, to-do or heading in a project's items, checklist-item in a to-do's checklist-items; operation create or update):\n%s",
			strings.Join(shapes, "\n")))
	}
	if len(ignored) > 0 {
		parts = append(parts, fmt.Sprintf("Things ignores these attributes on this type of item and creates it without them, which the payload almost never means:\n%s",
			strings.Join(ignored, "\n")))
	}
	if len(dups) > 0 {
		parts = append(parts, fmt.Sprintf("These items give an attribute twice. Things keeps the first value and the CLI would check the last, so give each attribute once:\n%s",
			strings.Join(dups, "\n")))
	}
	if len(long) > 0 {
		parts = append(parts, fmt.Sprintf("Things cuts a title to %d characters, and notes are held to the %d characters add allows, so these would not be saved as given:\n%s",
			things.MaxStringLen, things.MaxNotesLen, strings.Join(long, "\n")))
	}
	if len(blanks) > 0 {
		parts = append(parts, fmt.Sprintf("Things creates a to-do or project with no title, or only whitespace, as an untitled item, so these are refused; give each a title:\n%s",
			strings.Join(blanks, "\n")))
	}
	parts = append(parts, "Nothing was sent to Things — fix these and run the import again, or make the changes in the Things app.")
	return strings.Join(parts, "\n")
}

// prepareImport is the pre-write pass over an import payload. It refuses the
// whole import when any `operation: update` item would change an attribute
// Things drops silently on a repeating to-do or project — the same check
// `edit`, `complete` and `cancel` make, applied per item — when any item
// gives a date, attribute or shape Things rejects the whole payload over
// (see importShapes), when a to-do or project is created with a blank title,
// or when the payload has more than maxImportItems items.
//
// The refusal is all-or-nothing on purpose: the URL scheme takes one payload
// and gives no per-item result, so there is no way to send the rest and report
// what was skipped. Refusing before anything is sent leaves the user with a
// payload they can fix and re-run.
func prepareImport(database *db.DB, payload []any, dups map[string][]string) (*importPlan, error) {
	plan := &importPlan{updates: importUpdates(payload), tasks: map[string]*model.Task{}, creates: importCreates(payload)}

	// One query for the whole payload rather than one per item (issue #167).
	var ids []string
	for _, u := range plan.updates {
		if u.resolvable() {
			ids = append(ids, u.id)
		}
	}
	found, err := database.GetTasksByUUIDs(ids)
	if err != nil {
		return nil, fmt.Errorf("checking the payload against the Things database: %w", err)
	}
	// An id with no row maps to a nil task, which is what the loop below and
	// the read-back afterwards both read as "not in the database".
	for _, id := range ids {
		plan.tasks[id] = found[id]
	}

	// An item is refused when it changes an attribute Things does not allow
	// on a repeating item, or gives a date or destination Things rejects:
	// one entry per item, in payload order, so one run reports every reason.
	repeating := map[string]importRefusalItem{}
	updates := map[string]importUpdate{}
	for _, u := range plan.updates {
		updates[u.path] = u
		if !u.resolvable() {
			continue
		}
		task := plan.tasks[u.id]
		if task == nil {
			plan.missing = append(plan.missing, fmt.Sprintf("%s (id %s)", u.path, u.id))
			continue
		}
		blocked := restrictedImportAttrs(u.attrs)
		if len(blocked) == 0 || !task.Repeating {
			continue
		}
		repeating[u.path] = importRefusalItem{
			Path: u.path, ID: u.id, Title: task.Title, Kind: task.Type.String(), Blocked: blocked, restricted: blocked,
		}
	}
	shapes, size := importShapes(payload)
	now := clock.Now()
	var refusals []importRefusalItem
	walkImportValues(payload, "", func(path string, raw any) {
		it, refused := repeating[path]
		shape := shapes[path]
		dupKeys := dups[path]
		v, _ := raw.(map[string]any)
		var dateLines, dateNames, schedLines, schedNames, futureLines, futureNames, typeLines, typeNames []string
		if v != nil {
			dateLines, dateNames = badImportDates(v)
			schedLines, schedNames = badImportSchedule(v)
			futureLines, futureNames = futureImportDates(v, now)
			typeLines, typeNames = badImportTypes(v)
		}
		if len(dateLines) > 0 || len(schedLines) > 0 || len(futureLines) > 0 || len(typeLines) > 0 || len(dupKeys) > 0 || !shape.empty() {
			if !refused {
				typ := model.TypeTask
				if itemType, _ := v["type"].(string); strings.TrimSpace(itemType) == "project" {
					typ = model.TypeProject
				}
				it = importRefusalItem{Path: path, Kind: typ.String()}
				attrs, _ := v["attributes"].(map[string]any)
				title, _ := attrs["title"].(string)
				it.Title = strings.TrimSpace(title)
				// An update item names its row by id, and the database
				// has its title.
				if u, ok := updates[path]; ok && u.id != "" {
					it.ID = u.id
					if task := plan.tasks[u.id]; task != nil {
						it.Title, it.Kind = task.Title, task.Type.String()
					}
				}
			}
			it.Blocked = slices.Concat(it.Blocked, dateNames, schedNames, futureNames, typeNames, shape.typeNames, shape.itemNames, shape.ignoredNames, dupKeys, shape.longNames)
			if shape.blank {
				it.Blocked = append(it.Blocked, "title")
			}
			// An attribute refused for two reasons, such as a title given
			// twice and too long, is named once.
			it.Blocked = uniqueInOrder(it.Blocked)
			it.dates, it.schedule, it.future, it.types = dateLines, schedLines, futureLines, append(typeLines, shape.types...)
			it.shape, it.ignored, it.blank = shape.items, shape.ignored, shape.blank
			it.long = shape.long
			for _, key := range dupKeys {
				it.dups = append(it.dups, fmt.Sprintf("%s: given twice", key))
			}
			refused = true
		}
		if refused {
			refusals = append(refusals, it)
		}
	})
	if len(refusals) > 0 || size > maxImportItems {
		return nil, &importRefusalError{items: refusals, total: len(plan.updates), size: size}
	}
	return plan, nil
}

// jsonItems renders the refused items for the --json error payload. Blocked is
// copied rather than shared: the payload outlives the error only in tests, but
// aliasing a slice into the wire format invites a caller to mutate it.
func (e *importRefusalError) jsonItems() []jsonErrorItem {
	out := make([]jsonErrorItem, len(e.items))
	for i, it := range e.items {
		out[i] = jsonErrorItem{
			Path:    it.Path,
			ID:      it.ID,
			Title:   it.Title,
			Blocked: append([]string(nil), it.Blocked...),
			Reason:  it.reason(),
		}
	}
	return out
}
