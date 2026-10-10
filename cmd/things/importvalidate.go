package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
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

// problems is what is wrong with a payload item in one respect: a line for
// each problem, and the attribute each is about.
type problems struct{ lines, names []string }

// add records line, a problem with the attribute name.
func (p *problems) add(name, line string) {
	p.lines = append(p.lines, line)
	p.names = append(p.names, name)
}

// importDates returns the date attributes of item, a payload object, that
// Things rejects the whole payload over (see creationDateShape), as
// `name: value` with the value in JSON, and those later than now, as
// `name: value (instant)`. Measured in Things 3: a bad date on a heading or
// a checklist item rejects the payload as one on a to-do or project does. A
// creation-date or completion-date in the future (measured 9 Oct 2026) is
// saved as now, so the item gets a date the payload did not give it. That
// includes an hour past 23 that rolls into the future, such as today's date
// at 25:00. datedSlack spares a date stamped a moment ahead, which Things
// saves within a minute of what was asked.
func importDates(item map[string]any, now time.Time) (bad, future problems) {
	if itemType, _ := item["type"].(string); !importItemTypes[strings.TrimSpace(itemType)] {
		return bad, future
	}
	attrs, _ := item["attributes"].(map[string]any)
	for _, name := range importDateAttrs {
		raw := attrs[name]
		if raw == nil {
			continue
		}
		switch at, ok := parseThingsDate(raw); {
		case !ok:
			bad.add(name, fmt.Sprintf("%s: %s", name, jsonText(raw)))
		case at.After(now.Add(datedSlack)):
			future.add(name, fmt.Sprintf("%s: %s (%s)", name, jsonText(raw), at.UTC().Format(time.RFC3339)))
		}
	}
	return bad, future
}

// jsonText is v as JSON, the form the refusal messages quote a payload value
// in. A value decoded from the payload always encodes, so the error is
// dropped.
func jsonText(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// importScheduleDate is a date as the Things JSON format writes when and
// deadline: YYYY-MM-DD.
var importScheduleDate = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// importScheduleNumeric matches a when or deadline that starts as a date
// or a time does, with digits, so checkScheduleValue holds it to the date
// forms rather than passing it to Things as an English phrase.
var importScheduleNumeric = regexp.MustCompile(`^\d`)

// checkScheduleValue reports why value, a `when` or `deadline` in an import
// payload, is one Things would misread, or "" when it is not. It starts from
// the checks `add` makes (things.NormalizeWhen and NormalizeDeadline), which
// refuse a near-miss of a keyword, a keyword as a deadline, and a date or
// time of day that names none (whose reason this words itself). But
// `add` sends a URL and import a JSON payload, and the payload's grammar is
// narrower: the Things JSON documentation names only the keywords, a date
// and a date and time. Measured in Things 3 (9 Oct 2026), each saved with no
// warning: when 2026-13-01 lands in Today, when 18:00 in Today with no
// reminder, when tomorrow@25:00 tomorrow with no reminder, when
// someday@18:00 in Someday, deadline 2026-13-01 on 1 Jan 2026 and deadline
// someday as no deadline. So a value that starts with a digit must be a real
// date, YYYY-MM-DD; a deadline has no time; and a when with an @ must be
// today, tomorrow, evening, a weekday name or a real YYYY-MM-DD before it,
// and a real time of day after it (a weekday name goes as its date,
// resolveImportWhens). Anything else, keywords, weekdays and English phrases
// such as next friday (measured: filed on that day), is passed through as
// `add` passes it.
func checkScheduleValue(name, value string) string {
	v := strings.TrimSpace(value)
	normalize := things.NormalizeWhen
	if name == "deadline" {
		normalize = things.NormalizeDeadline
	}
	// An impossible date or time is kept as its reason, for the checks
	// below to word in the payload's own terms.
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
	// NormalizeWhen has judged the date and the time of day; what is left
	// is the payload's narrower grammar.
	badDate := impossible != nil && impossible.Reason == things.WhenBadDate
	day, _, timed := strings.Cut(v, "@")
	if !timed {
		switch {
		case !importScheduleNumeric.MatchString(v):
			return ""
		case !importScheduleDate.MatchString(v):
			return "not a date as YYYY-MM-DD, or a date and time as YYYY-MM-DD@HH:MM"
		case badDate:
			return "not a real date"
		}
		return ""
	}
	switch {
	case impossible != nil && impossible.Reason == things.WhenTimeIgnored:
		return "Things ignores a time after anytime or someday"
	case things.TimedWhenWord(day):
	case importScheduleDate.MatchString(day):
		if badDate {
			return "not a real date"
		}
	default:
		return "before the @ must be today, tomorrow, evening, a weekday name such as friday or a date as YYYY-MM-DD"
	}
	if impossible != nil && impossible.Reason == things.WhenBadClock {
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

// badImportSchedule returns each when or deadline of item, a payload to-do
// or project created or updated, that Things would misread (see
// checkScheduleValue), as `name: value (why)`. A value that is not a string
// is left to the type checks.
func badImportSchedule(item map[string]any) (bad problems) {
	if _, ok := payloadType(item); !ok {
		return bad
	}
	attrs, _ := item["attributes"].(map[string]any)
	for _, name := range []string{"when", "deadline"} {
		v, ok := attrs[name].(string)
		if !ok {
			continue
		}
		if why := checkScheduleValue(name, v); why != "" {
			bad.add(name, fmt.Sprintf("%s: %s (%s)", name, jsonText(v), why))
		}
	}
	return bad
}

// importDestAttrs are the destination attributes of a to-do or project.
var importDestAttrs = []string{"list", "list-id", "heading", "heading-id", "area", "area-id"}

// badImportTypes returns each destination attribute of item, a payload object
// that creates a to-do or project, that is neither a string nor null, as
// `name: value` with the value in JSON. Measured in Things 3: a number,
// boolean or array there makes it reject the whole payload. Update items
// were not measured, so they are not checked.
func badImportTypes(item map[string]any) (bad problems) {
	_, task := payloadType(item)
	if op, _ := item["operation"].(string); !task || (op != "" && op != "create") {
		return bad
	}
	attrs, _ := item["attributes"].(map[string]any)
	for _, name := range importDestAttrs {
		switch raw := attrs[name]; raw.(type) {
		case nil, string:
		default:
			bad.add(name, fmt.Sprintf("%s: %s", name, jsonText(raw)))
		}
	}
	return bad
}

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
	// Blocked is every attribute refused on the item, in the order of
	// refusalKinds.
	Blocked []string

	// lines is what is wrong with the item, by kind: for kindRepeating the
	// attributes a repeating item does not allow, and for every other kind
	// but kindBlank a line per problem, such as `name: value`.
	lines [numRefusalKinds][]string
	blank bool // a to-do or project created with a blank title
}

// refusalKind is one reason an item is refused. The order is the order
// Error() gives its paragraphs and reason() its words.
type refusalKind int

const (
	kindRepeating refusalKind = iota // an attribute a repeating item does not allow
	kindDate                         // a date Things rejects, as `name: value`
	kindSchedule                     // a when or deadline Things would misread, as `name: value (why)`
	kindFuture                       // a date in the future, as `name: value (instant)`
	kindType                         // an attribute of the wrong JSON kind, as `name: value`
	kindShape                        // a way it is not an item Things takes, as `name: why`
	kindIgnored                      // an attribute Things ignores on it, as `name: why`
	kindDup                          // a key the item gives twice
	kindLong                         // a title or notes too long, as `name: N characters, over M`
	kindBlank                        // a to-do or project created with a blank title
	numRefusalKinds
)

// refusalKinds gives each kind its word in the --json reason and the
// paragraph of the message that lists the items. The repeating paragraph
// counts the items, so Error() writes it.
var refusalKinds = [numRefusalKinds]struct{ reason, header string }{
	kindRepeating: {"repeating", ""},
	kindDate:      {"invalid-date", "Things rejects the whole payload over a creation-date or completion-date that is not a date and time with seconds and a UTC offset, such as 2026-10-05T10:30:00Z or 2026-10-05T10:30:00+02:00"},
	kindSchedule:  {"invalid-date", "Things saves a when or deadline it cannot read as something else, with no warning (a when of 2026-13-01 or 18:00 lands in Today, a deadline of 2026-13-01 on 1 Jan 2026). A misspelt keyword, or a keyword as a deadline, is refused. A when or deadline that starts with a digit must be a date as YYYY-MM-DD, and a deadline has no time. A when with an @ must have today, tomorrow, evening, a weekday name such as friday or a YYYY-MM-DD date before it and a time of day such as 18:00 or 6pm after it"},
	kindFuture:    {"future-date", "Things saves a creation-date or completion-date in the future as now, so the item would not get the date the payload gives. Give a date in the past, or leave the attribute out"},
	kindType:      {"invalid-type", "Things rejects the whole payload over an attribute of the wrong JSON type. title, notes, when, deadline, list, list-id, heading, heading-id, area and area-id must be strings, tags an array of strings, completed and canceled true or false, items and checklist-items arrays (each may be null), and attributes an object"},
	kindShape:     {"invalid-item", "Things rejects the whole payload over an item it does not take: one that is not an object, has no attributes, or whose type or operation it does not know or does not allow there, written exactly (to-do or project at the top level, to-do or heading in a project's items, checklist-item in a to-do's checklist-items; operation create or update)"},
	kindIgnored:   {"invalid-item", "Things ignores these attributes on this type of item and creates it without them, which the payload almost never means"},
	kindDup:       {"duplicate-key", "These items give an attribute twice. Things keeps the first value and the CLI would check the last, so give each attribute once"},
	kindLong:      {"too-long", fmt.Sprintf("Things cuts a title to %d characters, and notes are held to the %d characters add allows, so these would not be saved as given", things.MaxStringLen, things.MaxNotesLen)},
	kindBlank:     {"blank-title", "Things creates a to-do or project with no title, or only whitespace, as an untitled item, so these are refused; give each a title"},
}

// has reports whether the item is refused for kind.
func (it importRefusalItem) has(kind refusalKind) bool {
	if kind == kindBlank {
		return it.blank
	}
	return len(it.lines[kind]) > 0
}

// line is the line of the message that names the item under kind.
func (it importRefusalItem) line(kind refusalKind) string {
	switch kind {
	case kindRepeating:
		return fmt.Sprintf("  %s (id %s): %q is a repeating %s — %s",
			it.Path, it.ID, it.Title, it.Kind, strings.Join(it.lines[kind], ", "))
	case kindBlank:
		return "  " + it.Path
	}
	id := ""
	if it.ID != "" {
		id = " (id " + it.ID + ")"
	}
	return fmt.Sprintf("  %s%s %s", it.Path, id, strings.Join(it.lines[kind], ", "))
}

// reason is why the item is refused: "repeating", "invalid-date",
// "future-date", "invalid-type", "invalid-item", "duplicate-key",
// "too-long", "blank-title", or several of them, space-separated.
func (it importRefusalItem) reason() string {
	var reasons []string
	for kind, k := range refusalKinds {
		if it.has(refusalKind(kind)) && !slices.Contains(reasons, k.reason) {
			reasons = append(reasons, k.reason)
		}
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

func (e *importRefusalError) Error() string {
	var lines [numRefusalKinds][]string
	for _, it := range e.items {
		for kind := range lines {
			if it.has(refusalKind(kind)) {
				lines[kind] = append(lines[kind], it.line(refusalKind(kind)))
			}
		}
	}
	var parts []string
	if e.oversize() {
		parts = append(parts, fmt.Sprintf("The payload creates %d items (update items and checklist items not counted). Above %d, Things stops to ask \"Is this what you intended?\" and creates nothing until someone answers, so the import cannot be read back. Split it into imports of at most %d items each.",
			e.size, maxImportItems, maxImportItems))
	}
	for kind, k := range refusalKinds {
		if len(lines[kind]) == 0 {
			continue
		}
		header := k.header
		if refusalKind(kind) == kindRepeating {
			header = fmt.Sprintf("%d of %d update items change attributes Things does not allow on repeating items, and drops the request silently (%s)",
				len(lines[kind]), e.total, repeatingDocsURL)
		}
		parts = append(parts, header+":\n"+strings.Join(lines[kind], "\n"))
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
		it := importRefusalItem{Path: u.path, ID: u.id, Title: task.Title, Kind: task.Type.String(), Blocked: blocked}
		it.lines[kindRepeating] = blocked
		repeating[u.path] = it
	}
	shapes, size := importShapes(payload)
	now := clock.Now()
	var refusals []importRefusalItem
	walkImportValues(payload, "", func(path string, raw any) {
		it, refused := repeating[path]
		shape := shapes[path]
		dupKeys := dups[path]
		v, _ := raw.(map[string]any)
		var found [numRefusalKinds]problems
		if v != nil {
			found[kindDate], found[kindFuture] = importDates(v, now)
			found[kindSchedule] = badImportSchedule(v)
			found[kindType] = badImportTypes(v)
		}
		found[kindType].lines = append(found[kindType].lines, shape.types.lines...)
		found[kindType].names = append(found[kindType].names, shape.types.names...)
		found[kindShape], found[kindIgnored], found[kindLong] = shape.items, shape.ignored, shape.long
		for _, key := range dupKeys {
			found[kindDup].add(key, key+": given twice")
		}
		if shape.blank {
			// The message names only the path; see importRefusalItem.line.
			found[kindBlank].add("title", "")
		}
		if slices.ContainsFunc(found[:], func(p problems) bool { return len(p.lines) > 0 }) {
			if !refused {
				// Any type but project is reported as a task.
				typ, _ := payloadType(v)
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
			for kind := kindRepeating + 1; kind < numRefusalKinds; kind++ {
				it.Blocked = append(it.Blocked, found[kind].names...)
				it.lines[kind] = found[kind].lines
			}
			it.blank = shape.blank
			// An attribute refused for two reasons, such as a title given
			// twice and too long, is named once.
			it.Blocked = uniqueInOrder(it.Blocked)
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
