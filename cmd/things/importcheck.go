package main

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ryanlewis/things-cli/internal/clock"
	"github.com/ryanlewis/things-cli/internal/db"
	"github.com/ryanlewis/things-cli/internal/model"
	"github.com/ryanlewis/things-cli/internal/output"
	"github.com/ryanlewis/things-cli/internal/things"
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
	// to is where the payload files the item, and dest is where the CLI
	// resolved that to before the import was sent (see resolveImportDests).
	to   importTo
	dest createdDest
}

// importTo is the destination attributes of an item the payload creates: a
// to-do's list-id, list, heading-id and heading, a project's area-id and
// area. A to-do in the items of a project the payload updates carries that
// project's id as parentID, and one in a project it creates carries that
// project's title as parentTitle.
type importTo struct {
	listID, list, headingID, heading string
	areaID, area                     string
	parentID, parentTitle            string
	nested                           bool
	// The id attributes win over the titles even when empty, so whether
	// the payload gives each one at all matters. A null one counts as not
	// given, as it does to Things.
	hasListID, hasHeadingID, hasAreaID bool
	// listInPayload is set when list is the title of a project the payload
	// creates or renames before this item, and headingInPayload when heading
	// is the title of a heading it creates before this item: Things may file
	// the to-do there, which the database does not have yet.
	listInPayload, headingInPayload bool
	// renamed is the ids of the projects the payload renames before this
	// item: the database still has their old titles.
	renamed []string
	// ignored names the destination attributes a to-do in a project's items
	// gives, which Things ignores.
	ignored []string
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

// resolveImportDests sets each create's dest from its destination attributes,
// as Things files the item (measured in Things 3), and warns about each one
// Things will not file where the payload asks. A to-do in a project's items
// is checked against that project only. For any other to-do a heading-id
// wins: Things files it under that heading, in the heading's project,
// whatever list it names and whatever state the project is in, and ignores
// the heading title whenever a heading-id is given, even an empty or unknown
// one. Next a list-id wins over list, even an empty or unknown one, which
// puts the to-do in the Inbox; a known one is filed into whatever its state
// (see db.AddTarget). A list title goes to the list AddTarget finds; one a
// project the payload creates or renames earlier carries may be either, so
// any list with that title fits. A title that matches nothing, a uuid given
// as list among them, puts it in the Inbox. A heading the list does not have
// is left out, unless the payload creates a heading with that title earlier,
// which it may be. A project's area-id likewise wins over area, and an empty
// or unknown area-id or area leaves it in no area. Things does not trim an
// id, so one with surrounding space matches nothing. A list that cannot be
// read is left unchecked.
func resolveImportDests(d *Deps, database *db.DB, creates []importCreate) {
	warn := func(c importCreate, format string, args ...any) {
		fmt.Fprintf(d.errOut(), "warning: %s: "+format+"\n", append([]any{c.path}, args...)...)
	}
	// note gives add's notes about where Things files an item, once per
	// list or area the payload names however many items go there.
	noted := map[string]bool{}
	note := func(c importCreate, ref, kind string, t db.Target) {
		// Measured in Things 3: a to-do the payload itself completes,
		// filed into a closed project, leaves the project closed.
		for _, n := range targetNotes(ref, kind, t, !c.closed) {
			if !noted[n] {
				noted[n] = true
				fmt.Fprintf(d.errOut(), "note: %s: %s\n", c.path, n)
			}
		}
	}
	// Many items in a payload share a list, area or heading; each is looked
	// up once.
	type areaRes struct {
		target db.Target
		err    error
	}
	areaTargets := map[string]areaRes{}
	areaLookup := func(area string) (db.Target, error) {
		res, ok := areaTargets[area]
		if !ok {
			res.target, res.err = database.AreaTarget(area)
			areaTargets[area] = res
		}
		return res.target, res.err
	}
	type targetKey struct{ list, heading string }
	type targetRes struct {
		target       db.Target
		headingFound bool
		err          error
	}
	targets := map[targetKey]targetRes{}
	lookup := func(list, heading string) targetRes {
		tk := targetKey{list, heading}
		res, ok := targets[tk]
		if !ok {
			res.target, res.headingFound, res.err = database.AddTarget(list, heading)
			targets[tk] = res
		}
		return res
	}
	type headingRes struct {
		project, title string
		found          bool
		err            error
	}
	headings := map[string]headingRes{}
	for i := range creates {
		c := &creates[i]
		to := c.to
		if c.typ == model.TypeProject {
			c.dest = projectImportDest(*c, warn, note, areaLookup)
			continue
		}
		switch {
		case to.parentID != "":
			c.dest = createdDest{checked: true, list: strings.TrimSpace(to.parentID), anyHeading: true}
			continue
		case to.nested:
			// The project is not there before the import, so it can only
			// be matched by title, trimmed as created titles are. Measured
			// in Things 3: the to-do goes in that project, under the heading
			// before it in the items if any, whatever list, list-id,
			// heading-id or heading it gives.
			if len(to.ignored) > 0 {
				warn(*c, "Things files a to-do in a project's items in that project and ignores its %s", strings.Join(to.ignored, ", "))
			}
			c.dest = createdDest{checked: true, list: db.FoldName(strings.TrimSpace(to.parentTitle)), byTitle: true, anyHeading: true}
			continue
		}
		heading := to.heading
		if to.hasHeadingID {
			if id := to.headingID; id != "" {
				h, ok := headings[id]
				if !ok {
					h.project, h.title, h.found, _, h.err = database.HeadingProject(id)
					headings[id] = h
				}
				switch {
				case h.err != nil:
					c.dest = createdDest{}
					continue
				case h.found:
					c.dest = createdDest{checked: true, list: h.project, heading: id}
					if res := lookup(h.project, ""); res.err == nil && res.target.ByUUID {
						note(*c, h.project, "lists", res.target)
					}
					// The heading wins over the list and heading title the
					// payload names, and over any list-id it can find.
					switch {
					case to.hasListID:
						if res := lookup(to.listID, ""); res.err == nil && targetMatches(res.target, to.listID, true) && res.target.UUID != h.project {
							warn(*c, "heading-id %q is in another project; Things will file the to-do there and ignore list-id", id)
						}
					case to.listInPayload:
						warn(*c, "heading-id %q is in another project; Things will file the to-do there and ignore list %q", id, to.list)
					case to.list != "":
						if res := lookup(to.list, ""); res.err == nil && targetMatches(res.target, to.list, false) && res.target.UUID != h.project {
							warn(*c, "heading-id %q is in another project; Things will file the to-do there and ignore list %q", id, to.list)
						}
					}
					if heading != "" && db.FoldCase(heading) != db.FoldCase(h.title) {
						warn(*c, "Things will file the to-do under the heading heading-id %q names and ignore heading %q", id, heading)
					}
					continue
				}
				warn(*c, "Things has no heading with id %q; it will ignore heading-id and heading", id)
			} else if heading != "" {
				warn(*c, "heading-id is empty; Things will ignore heading %q", heading)
			}
			heading = ""
		}
		list, byID := to.list, to.hasListID
		if byID {
			list = to.listID
		}
		switch {
		case byID && list == "":
			warn(*c, "list-id is empty; Things will put the to-do in the Inbox")
			c.dest = createdDest{checked: true}
			continue
		case list == "":
			if heading != "" {
				warn(*c, "heading %q needs a list; Things will ignore it and put the to-do in the Inbox", heading)
			}
			c.dest = createdDest{checked: true}
			continue
		case !byID && to.listInPayload:
			// Things may file it in the project the payload created or
			// renamed, or in a list it already had with that title. It does
			// not trim a list title, so one with surrounding space matches
			// neither.
			c.dest = createdDest{checked: true, list: db.FoldName(list), byTitle: true, anyHeading: heading != ""}
			continue
		}
		res := lookup(list, heading)
		if res.err != nil {
			c.dest = createdDest{}
			continue
		}
		if !targetMatches(res.target, list, byID) {
			warn(*c, "%s", missedTarget("list", "project or area", list, byID, res.target, "put the to-do in the Inbox"))
			c.dest = createdDest{checked: true}
			continue
		}
		if !byID && !res.target.Area && slices.Contains(to.renamed, res.target.UUID) {
			// The title is the project's old one, which an earlier update
			// item renames. Things may no longer find it by that title, so
			// where the to-do goes was not measured (updates need the
			// token) and is left unchecked.
			c.dest = createdDest{}
			continue
		}
		note(*c, list, "lists", res.target)
		c.dest = addDest(res.target)
		switch {
		case to.headingInPayload:
			// It may be the heading the payload creates, which can be the
			// lowest-uuid twin of one the list already has.
			c.dest.anyHeading = true
		case heading != "" && !res.headingFound:
			warn(*c, "%q has no heading %q; Things will add the to-do there without a heading", list, heading)
		}
	}
}

// targetMatches reports whether Things files an item sent ref into target,
// what AddTarget or AreaTarget found for it. Things takes an id attribute by
// uuid only, untrimmed, and a title attribute by title only, while the
// lookups match a uuid trimmed as well as a title.
func targetMatches(target db.Target, ref string, byID bool) bool {
	return target.UUID != "" && target.ByUUID == byID && (!byID || ref == target.UUID)
}

// missedTarget is the warning for an item sent ref, as attr or attr-id
// when byID, that Things will not file in any noun, so will fallback
// instead. target is what the lookup found for ref.
func missedTarget(attr, noun, ref string, byID bool, target db.Target, fallback string) string {
	switch {
	case byID:
		return fmt.Sprintf("Things finds no %s with id %q; it will %s", noun, ref, fallback)
	case target.ByUUID:
		return fmt.Sprintf("%s %q is an id, and Things matches %s by title only; it will %s (use %s-id)", attr, ref, attr, fallback, attr)
	}
	return fmt.Sprintf("Things finds no %s called %q; it will %s", noun, ref, fallback)
}

// projectImportDest is resolveImportDests for a project the payload creates:
// the area its area-id or area names (see db.AreaTarget), or none.
func projectImportDest(c importCreate, warn func(importCreate, string, ...any), note func(importCreate, string, string, db.Target), areaLookup func(string) (db.Target, error)) createdDest {
	to := c.to
	area, byID := to.area, to.hasAreaID
	if byID {
		area = to.areaID
	}
	switch {
	case byID && area == "":
		if to.area != "" {
			warn(c, "area-id is empty; Things will ignore area %q and create the project in no area", to.area)
		}
		return createdDest{checked: true}
	case area == "":
		return createdDest{checked: true}
	}
	target, err := areaLookup(area)
	if err != nil {
		return createdDest{}
	}
	if !targetMatches(target, area, byID) {
		warn(c, "%s", missedTarget("area", "area", area, byID, target, "create the project in no area"))
		return createdDest{checked: true}
	}
	note(c, area, "areas", target)
	return createdDest{checked: true, list: target.UUID}
}

// kind is the CLI's word for the item: "task" or "project".
func (c importCreate) kind() string {
	if c.typ == model.TypeProject {
		return "project"
	}
	return "task"
}

// importCreates collects every to-do and project in a Things JSON payload
// that is not an `operation: update`, at any depth: a project's `items`, and
// the `items` of a project the payload updates, create to-dos too.
// prepareImport refuses a to-do or project with no title, or one whose type
// or operation Things does not take, so every one Things creates in the
// places importShapes checks is here. This walk visits every object, though,
// not only those places, so it can also collect a to-do nested where Things
// creates nothing.
func importCreates(payload []any) []importCreate {
	var creates []importCreate
	// parents maps the path of each item in a project's items to that
	// project. The walk visits a project before its items.
	parents := map[string]importTo{}
	// projects holds FoldName of every project title the payload creates or
	// renames a project to so far, and headings FoldCase of every heading
	// title it creates so far, in payload order.
	projects, headings := map[string]bool{}, map[string]bool{}
	var renamed []string
	walkImportNode(payload, "", func(path string, v map[string]any) {
		op, _ := v["operation"].(string)
		attrs, _ := v["attributes"].(map[string]any)
		switch itemType, _ := v["type"].(string); strings.TrimSpace(itemType) {
		case "project":
			if title, _ := attrs["title"].(string); strings.TrimSpace(title) != "" {
				projects[db.FoldName(strings.TrimSpace(title))] = true
				if id, _ := v["id"].(string); op == "update" && id != "" {
					renamed = append(renamed, strings.TrimSpace(id))
				}
			}
		case "heading":
			if title, _ := attrs["title"].(string); op != "update" && strings.TrimSpace(title) != "" {
				headings[db.FoldCase(strings.TrimSpace(title))] = true
			}
		}
		if itemType, _ := v["type"].(string); strings.TrimSpace(itemType) == "project" {
			parent := importTo{nested: true}
			if op == "update" {
				parent.parentID, _ = v["id"].(string)
			} else {
				parent.parentTitle, _ = attrs["title"].(string)
			}
			items, _ := attrs["items"].([]any)
			for i := range items {
				parents[fmt.Sprintf("%s.attributes.items[%d]", path, i)] = parent
			}
		}
		if op != "" && op != "create" {
			return
		}
		var typ model.TaskType
		switch itemType, _ := v["type"].(string); strings.TrimSpace(itemType) {
		case "to-do":
			typ = model.TypeTask
		case "project":
			typ = model.TypeProject
		default:
			return
		}
		shown, _ := attrs["title"].(string)
		title := strings.TrimSpace(shown)
		if title == "" {
			return
		}
		to, nested := parents[path]
		if nested {
			// Measured for the items of a project the payload creates only.
			if to.parentID == "" {
				for _, name := range []string{"list", "list-id", "heading-id", "heading"} {
					if v, ok := attrs[name]; ok && v != nil {
						to.ignored = append(to.ignored, name)
					}
				}
			}
		} else {
			to.listID, to.hasListID = attrs["list-id"].(string)
			to.list, _ = attrs["list"].(string)
			to.headingID, to.hasHeadingID = attrs["heading-id"].(string)
			to.heading, _ = attrs["heading"].(string)
			to.areaID, to.hasAreaID = attrs["area-id"].(string)
			to.area, _ = attrs["area"].(string)
			if typ == model.TypeTask {
				to.listInPayload = to.list != "" && projects[db.FoldName(to.list)]
				to.headingInPayload = to.heading != "" && headings[db.FoldCase(to.heading)]
				to.renamed = slices.Clip(renamed)
			}
		}
		// A null creation-date is no date at all, as it is to Things, which
		// saves the item as created now. prepareImport has refused any value
		// Things rejects.
		raw := attrs["creation-date"]
		datedAt, _ := parseThingsDate(raw)
		var closedAt time.Time
		completed, _ := attrs["completed"].(bool)
		canceled, _ := attrs["canceled"].(bool)
		if completed || canceled {
			closedAt, _ = parseThingsDate(attrs["completion-date"])
		}
		creates = append(creates, importCreate{path: path, typ: typ, title: title, shown: storedTitle(shown), dated: raw != nil, datedAt: datedAt, closedAt: closedAt, closed: completed || canceled, to: to})
	})
	return creates
}

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
// deadline, YYYY-MM-DD, with a time of day after an @ for when.
var importScheduleDate = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2})(?:@(\d{1,2}:\d{2}))?$`)

// importScheduleNumeric matches a when or deadline that starts as a date
// or a time does, with digits, so checkScheduleValue holds it to the date
// forms rather than passing it to Things as an English phrase.
var importScheduleNumeric = regexp.MustCompile(`^\d`)

// checkScheduleValue reports why value, a `when` or `deadline` in an import
// payload, is one Things would misread, or "" when it is not. It starts from
// the checks `add` makes (things.NormalizeWhen and NormalizeDeadline), which
// refuse a near-miss of a keyword and a keyword as a deadline. Those pass a
// date or time they cannot read through to Things as an English phrase, but
// `add` sends a URL and import a JSON payload, and the payload's grammar is
// narrower: the Things JSON documentation names only the keywords, a date
// and a date and time. Measured in Things 3 (9 Oct 2026), each saved with no
// warning: when 2026-13-01 lands in Today, when 18:00 in Today with no
// reminder, deadline 2026-13-01 on 1 Jan 2026 and deadline someday as no
// deadline. So a value that starts with a digit must be a real date,
// YYYY-MM-DD, or for when a date and time, YYYY-MM-DD@HH:MM. Anything else,
// keywords, weekdays and English phrases, is passed through as `add` passes
// it; those were not measured in a payload.
func checkScheduleValue(name, value string) string {
	v := strings.TrimSpace(value)
	normalize := things.NormalizeWhen
	if name == "deadline" {
		normalize = things.NormalizeDeadline
	}
	if _, err := normalize(v); err != nil {
		// The message names the add flag; the payload has no flags.
		return strings.ReplaceAll(err.Error(), "--", "")
	}
	if !importScheduleNumeric.MatchString(v) {
		return ""
	}
	m := importScheduleDate.FindStringSubmatch(v)
	if m == nil || (name == "deadline" && m[2] != "") {
		if name == "deadline" {
			return "not a date as YYYY-MM-DD"
		}
		return "not a date as YYYY-MM-DD, or a date and time as YYYY-MM-DD@HH:MM"
	}
	if _, err := time.Parse("2006-01-02", m[1]); err != nil {
		return "not a real date"
	}
	if m[2] != "" {
		if _, err := time.Parse("15:04", fmt.Sprintf("%05s", m[2])); err != nil {
			return "not a real time of day"
		}
	}
	return ""
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
		parts = append(parts, fmt.Sprintf("Things saves a when or deadline it cannot read as something else, with no warning (a when of 2026-13-01 or 18:00 lands in Today, a deadline of 2026-13-01 on 1 Jan 2026). A misspelt keyword, or a keyword as a deadline, is refused, and a when or deadline that starts with a digit must be a date as YYYY-MM-DD, or for a when a date and time as YYYY-MM-DD@HH:MM:\n%s",
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

// importVerifyItem is one status change that never landed. wanted and got name
// the statuses either side of the failure; got is absent when there was
// nothing to observe, because the row could not be read or no longer exists.
type importVerifyItem struct {
	Path     string
	ID       string
	Title    string
	Wanted   model.Status
	Got      model.Status
	Observed bool

	err error // the read-back error, verbatim, for the plain-text message
}

// importVerifyError is a partially applied import: the payload reached Things,
// and some of the status changes it asked for did not take, or some of the
// items it created never appeared.
type importVerifyError struct {
	items []importVerifyItem
	total int // status changes the payload requested, landed or not

	// created is the verdict on every item the payload created, so the
	// confirmed ones keep their uuids when the import fails.
	created []importCreated
	search  string // the `things search` command, with the flags this run needs
}

// fails reports whether c, a created item's verdict, makes the import fail:
// the item may be missing (see mayBeMissing), or it was saved without its
// completion-date.
func (c importCreated) fails() bool {
	return c.mayBeMissing() || c.Reason == dateDropped
}

// mayBeMissing reports whether c says a created item may not be there: it
// never appeared, or a dated item's row could pass for it and fewer new items
// appeared than the items that could claim them. These are the items to
// search for, and re-run if they are missing. A shares-dated-title item with
// enough new items for every claimant is there, though which of them it is
// cannot be told, so it does not fail the import.
func (c importCreated) mayBeMissing() bool {
	return c.Reason == "not-found" || (c.Reason == "shares-dated-title" && (c.Present == nil || !*c.Present))
}

// dateDropped is the verdict on a created item saved without the
// completion-date the payload gives it. The item is there, so the import
// fails without asking for it to be created again.
const dateDropped = "completion-date-dropped"

// missing is the created items that are not known to be there: the ones to
// search for and re-run.
func (e *importVerifyError) missing() []importCreated {
	var out []importCreated
	for _, c := range e.created {
		if c.mayBeMissing() {
			out = append(out, c)
		}
	}
	return out
}

func (e *importVerifyError) Error() string {
	var parts []string
	if len(e.items) > 0 {
		lines := make([]string, len(e.items))
		for i, it := range e.items {
			lines[i] = fmt.Sprintf("  %s: %v", it.Path, it.err)
		}
		parts = append(parts, fmt.Sprintf("%d of %d requested status changes did not apply. The rest of the import was still applied; re-run the import with only the failed items, or make the changes in the Things app:\n%s",
			len(e.items), e.total, strings.Join(lines, "\n")))
	}
	missing := e.missing()
	if len(missing) > 0 {
		lines := make([]string, len(missing))
		for i, it := range missing {
			lines[i] = fmt.Sprintf("  %s: %s", it.Path, it.detail)
		}
		cause := "The rest of the import was still applied. Things may have dropped an item that did not appear (check that Things3 is running), or may be slow to save."
		if len(e.items) == e.total && !slices.ContainsFunc(e.created, func(c importCreated) bool {
			return c.Reason != "creation-date" && (c.Reason != "not-found" || len(c.Candidates) > 0)
		}) {
			// Nothing the payload creates appeared (an item given a
			// creation-date is not looked for, so it says nothing either
			// way) and no status change applied, which is also what a
			// payload Things rejected with an error looks like.
			cause = "None of them appeared: Things may have rejected the whole payload (look for an error in the Things window), may not be running, or may be slow to save."
		}
		parts = append(parts, fmt.Sprintf("%d of %d created items did not appear or cannot be confirmed (each line says why). %s Run `%s <title>` for each before re-running the import with only these items; do not retry blindly:\n%s",
			len(missing), len(e.created), cause, e.search, strings.Join(lines, "\n")))
	}
	var dropped []string
	for _, it := range e.created {
		if it.Reason == dateDropped {
			dropped = append(dropped, fmt.Sprintf("  %s: %s", it.Path, it.detail))
		}
	}
	if len(dropped) > 0 {
		parts = append(parts, fmt.Sprintf("%d created items were saved without the completion-date the payload gives. They are there, so do not import them again; set the date in the Things app:\n%s",
			len(dropped), strings.Join(dropped, "\n")))
	}
	if len(e.created) > len(missing)+len(dropped) {
		lines := []string{"The other created items:"}
		for _, it := range e.created {
			if !it.fails() {
				lines = append(lines, "  "+it.line())
			}
		}
		parts = append(parts, strings.Join(lines, "\n"))
	}
	return strings.Join(parts, "\n")
}

// importCreated is the read-back verdict on one item an import created, and
// one entry of the array `import --json` prints. A confirmed item carries the
// uuid it was found under; an unconfirmed one carries the reason, as an
// unconfirmed add does: "no-verify", "unreadable", "ambiguous" (candidates
// lists the new items with its title), or "not-found" (candidates lists the
// ones that did appear when fewer than the payload asked for did). An item
// with a `creation-date` is not checked at all: reason "creation-date". Nor
// is one that shares its type and title with such an item whose date falls
// in the read-back window, when one of its new items is also filed where that
// item goes: reason "shares-dated-title", with candidates listing its new
// items. A confirmed item the payload closes with a completion-date that was
// saved with another stopDate is turned back with reason
// "completion-date-dropped", keeping its uuid.
type importCreated struct {
	Path       string   `json:"path"`
	Kind       string   `json:"kind"`
	Title      string   `json:"title"`
	UUID       string   `json:"uuid,omitempty"`
	Confirmed  bool     `json:"confirmed"`
	Reason     string   `json:"reason,omitempty"`
	Candidates []string `json:"candidates,omitempty"`
	// Present is given on a shares-dated-title item only: true when as many
	// new items appeared as the items that could claim them, so it is there
	// (the import does not fail over it), false when it may be missing.
	Present *bool `json:"present,omitempty"`

	detail string // why a failing item is missing or unconfirmed, for the plain-text error
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
			Path: u.path, ID: u.id, Title: task.Title, Kind: taskKind(task), Blocked: blocked, restricted: blocked,
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
				it = importRefusalItem{Path: path, Kind: "task"}
				if itemType, _ := v["type"].(string); strings.TrimSpace(itemType) == "project" {
					it.Kind = "project"
				}
				attrs, _ := v["attributes"].(map[string]any)
				title, _ := attrs["title"].(string)
				it.Title = strings.TrimSpace(title)
				// An update item names its row by id, and the database
				// has its title.
				if u, ok := updates[path]; ok && u.id != "" {
					it.ID = u.id
					if task := plan.tasks[u.id]; task != nil {
						it.Title, it.Kind = task.Title, taskKind(task)
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

// taskKind is the CLI's word for task: "task" or "project".
func taskKind(task *model.Task) string {
	if task.Type == model.TypeProject {
		return "project"
	}
	return "task"
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

// applyImport sends write, the import, then reads it back: the items it
// created (readBackCreates) and the status changes it asked for
// (verifyImportStatuses). The whole import shares one timeout budget. Every
// created item is reported on stdout, confirmed or not; a created item that
// never appeared, or a status change that did not apply, makes the import
// fail with all of them named. Under --no-verify nothing is read back and
// every created item is reported unconfirmed.
func applyImport(d *Deps, database *db.DB, plan *importPlan, write func() error) error {
	if d.NoVerify {
		if err := write(); err != nil {
			return err
		}
		created := make([]importCreated, len(plan.creates))
		for i, c := range plan.creates {
			created[i] = importCreated{Path: c.path, Kind: c.kind(), Title: c.shownTitle(), Reason: "no-verify"}
		}
		return printImportCreated(d, created)
	}

	var snap createdSnapshot
	var snapErr error
	var types []model.TaskType
	for _, c := range plan.creates {
		if !c.dated && !slices.Contains(types, c.typ) {
			types = append(types, c.typ)
		}
	}
	if len(types) > 0 {
		snap, snapErr = snapshotCreated(database, types)
	}
	if err := write(); err != nil {
		return err
	}
	budget := d.readBackTimeout()
	deadline := time.Now().Add(budget)

	created := readBackCreates(d, database, plan.creates, snap, snapErr, budget)
	failures, total := verifyImportStatuses(database, plan, max(time.Until(deadline), 0), budget)

	// The failures go in the error rather than straight to stderr: under
	// --json the error is rendered as one object on stdout (issue #152) and
	// anything written to stderr never reaches the reader, so a summary
	// pointing at a list printed elsewhere would name detail the consumer
	// cannot see. Every created item's verdict goes in with them, since the
	// success list is not printed beside an error.
	if len(failures) > 0 || slices.ContainsFunc(created, importCreated.fails) {
		return &importVerifyError{
			items: failures, total: total, created: created,
			search: strings.Join(append(append([]string{"things"}, globalFlags(d)...), "search"), " "),
		}
	}
	return printImportCreated(d, created)
}

// readBackCreates finds each item the import created, the way applyAdd finds
// the one an add created (see findCreated), and returns a verdict per item in
// payload order. Each item must also be filed where the payload puts it (see
// resolveImportDests). Items with the same type, title and destination are
// counted together:
// when as many new items appear as the payload asked for, they are confirmed
// and paired with the payload's items in the order Things saved them. More is
// ambiguous and fewer is not found, both naming the new items that did
// appear. A snapshot or read-back that could not read the database at all
// leaves every item unconfirmed as unreadable, with a warning. An item the
// payload gives a creation-date is not looked for, and is reported as not
// checked. When that date is inside the read-back window its row can land
// among the new items, so an item without one whose new items include one
// that also fits where a dated item with its type and title is filed is not
// confirmed, rather than risk confirming it with the dated item's row, and
// names the new items it could be. It fails the import unless a new item
// appeared for each item that could claim one, when all of them are there.
func readBackCreates(d *Deps, database *db.DB, creates []importCreate, snap createdSnapshot, snapErr error, budget time.Duration) []importCreated {
	if len(creates) == 0 {
		return nil
	}
	start := time.Now()
	// dated is where each dated item whose row may land among the new items
	// is filed, by type and title.
	dated := map[createdKey][]createdDest{}
	for _, c := range creates {
		if c.dated && c.landsInWindow(snap.since) {
			dated[c.key()] = append(dated[c.key()], c.dest)
		}
	}
	want := map[createdWant]int{}
	for _, c := range creates {
		if !c.dated {
			want[c.want()]++
		}
	}
	var found map[createdWant][]model.Task
	err := snapErr
	if err == nil && len(want) > 0 {
		var readOK bool
		found, readOK, err = findCreated(database, snap, want, budget)
		if readOK {
			err = nil
		}
	}

	out := make([]importCreated, len(creates))
	paired := map[createdWant]int{}
	for i, c := range creates {
		out[i] = importCreated{Path: c.path, Kind: c.kind(), Title: c.shownTitle()}
		w := c.want()
		matches := found[w]
		uuids := make([]string, len(matches))
		for n, t := range matches {
			uuids[n] = t.UUID
		}
		sharesDated := slices.ContainsFunc(matches, func(t model.Task) bool {
			return slices.ContainsFunc(dated[c.key()], func(dst createdDest) bool { return dst.fits(t) })
		})
		switch n := want[w]; {
		case c.dated:
			out[i].Reason = "creation-date"
		case err != nil && len(dated[c.key()]) > 0:
			// With no rows to see where the dated item went, it may be
			// any new item with the title.
			out[i].Reason, out[i].Present = "shares-dated-title", new(bool)
			out[i].detail = fmt.Sprintf("an item the payload gives a creation-date is also a %s titled %q, and the database could not be read to count the new items with that title, so the item may exist but cannot be confirmed", c.kind(), c.title)
		case err != nil:
			out[i].Reason = "unreadable"
		case sharesDated:
			out[i].Reason, out[i].Candidates = "shares-dated-title", uuids
			// Each item without a creation-date whose new items overlap
			// these, and each dated item that fits one of them, may hold
			// one of the new items they share. When there are enough for
			// every item that could claim them, all are there, and only
			// which is which is unknown.
			rows, claimants := sharedRows(c, creates, found)
			// A dated item can hold one of the rows only when its
			// creation-date is inside the window the rows were read from:
			// datedSlack lets an earlier one mark this item, but its row
			// is never among them.
			for _, other := range creates {
				if other.dated && other.key() == c.key() && !other.datedAt.Before(snap.since) && slices.ContainsFunc(rows, other.dest.fits) {
					claimants++
				}
			}
			present := len(rows) >= claimants
			out[i].Present = &present
			out[i].detail = fmt.Sprintf("an item the payload gives a creation-date is also a %s titled %q, and only %s with that title appeared for the %d items that could be filed there, so this item may exist as one of them but cannot be confirmed (%s)", c.kind(), c.title, plural(len(rows), "new item"), claimants, strings.Join(uuids, ", "))
		case len(matches) == n && n > 1 && !distinctCreationDates(matches):
			// Two saved at the same instant cannot be paired with the
			// payload's items by order, so neither is confirmed.
			out[i].Reason, out[i].Candidates = "ambiguous", uuids
		case len(matches) == n:
			out[i].Confirmed = true
			out[i].UUID = uuids[paired[w]]
			paired[w]++
		case len(matches) > n:
			out[i].Reason, out[i].Candidates = "ambiguous", uuids
		case len(matches) == 0:
			out[i].Reason = "not-found"
			out[i].detail = fmt.Sprintf("no new %s titled %q appeared within %s", c.kind(), c.title, budget)
		default:
			out[i].Reason, out[i].Candidates = "not-found", uuids
			out[i].detail = fmt.Sprintf("only %d of %d new %ss titled %q appeared within %s (%s)", len(matches), n, c.kind(), c.title, budget, strings.Join(uuids, ", "))
		}
	}
	unconfirmShared(out, creates, found)
	checkClosedAt(database, out, creates, found, max(budget-time.Since(start), 0))
	if err != nil && len(want) > 0 && !d.dbWarned {
		fmt.Fprintf(d.errOut(), "warning: cannot read the Things database to confirm the items the import created: %v\n", err)
	}
	return out
}

// sharedRows returns the new items that c, an item without a creation-date,
// competes for with the other items without one of its type and title, and
// how many such items compete for them, c included. An item competes when
// its new items overlap the ones gathered so far, which brings its own new
// items in, so the sets grow until nothing more overlaps: when A shares a
// row with B, and B with C, all three compete for the rows of all three.
func sharedRows(c importCreate, creates []importCreate, found map[createdWant][]model.Task) ([]model.Task, int) {
	var rows []model.Task
	inRows := map[string]bool{}
	add := func(tasks []model.Task) {
		for _, t := range tasks {
			if !inRows[t.UUID] {
				inRows[t.UUID] = true
				rows = append(rows, t)
			}
		}
	}
	add(found[c.want()])
	competes := make([]bool, len(creates))
	for grew := true; grew; {
		grew = false
		for j, other := range creates {
			if competes[j] || other.dated || other.key() != c.key() {
				continue
			}
			theirs := found[other.want()]
			if other.want() != c.want() && !slices.ContainsFunc(theirs, func(t model.Task) bool { return inRows[t.UUID] }) {
				continue
			}
			competes[j], grew = true, true
			add(theirs)
		}
	}
	claimants := 0
	for _, ok := range competes {
		if ok {
			claimants++
		}
	}
	return rows, claimants
}

// unconfirmShared turns back every confirmation whose new item another
// created item can also claim. An item whose destination is loose, unchecked
// or matched by a title several lists may carry, fits a row filed for another
// item with the same kind and title, so the two can claim the same row: a row
// two items were both confirmed with, or a row a loose item was confirmed
// with that fits another item's destination too, confirms neither. They are reported ambiguous with every new item either
// could be, or not-found when fewer new items appeared than the created items
// that could claim them, since then at least one of those never appeared.
func unconfirmShared(out []importCreated, creates []importCreate, found map[createdWant][]model.Task) {
	holds := func(j int, uuid string) bool {
		return !creates[j].dated && slices.ContainsFunc(found[creates[j].want()], func(t model.Task) bool { return t.UUID == uuid })
	}
	shared := make([]bool, len(out))
	for i, c := range out {
		if !c.Confirmed {
			continue
		}
		for j := range out {
			if j == i || creates[j].want() == creates[i].want() || !holds(j, c.UUID) {
				continue
			}
			if creates[i].dest.loose() || (out[j].Confirmed && out[j].UUID == c.UUID) {
				shared[i] = true
				break
			}
		}
	}
	verdicts := map[int]importCreated{}
	for i, c := range out {
		if !shared[i] {
			continue
		}
		var rows []model.Task
		for _, matches := range found {
			if !slices.ContainsFunc(matches, func(t model.Task) bool { return t.UUID == c.UUID }) {
				continue
			}
			for _, t := range matches {
				if !slices.ContainsFunc(rows, func(r model.Task) bool { return r.UUID == t.UUID }) {
					rows = append(rows, t)
				}
			}
		}
		// Oldest first, as the other candidate lists are.
		slices.SortFunc(rows, func(a, b model.Task) int {
			if a.CreationDate != nil && b.CreationDate != nil {
				if n := a.CreationDate.Compare(*b.CreationDate); n != 0 {
					return n
				}
			}
			return strings.Compare(a.UUID, b.UUID)
		})
		candidates := make([]string, len(rows))
		for n, t := range rows {
			candidates[n] = t.UUID
		}
		claimants := 0
		for j := range out {
			if slices.ContainsFunc(candidates, func(uuid string) bool { return holds(j, uuid) }) {
				claimants++
			}
		}
		v := importCreated{Path: c.Path, Kind: c.Kind, Title: c.Title, Reason: "ambiguous", Candidates: candidates}
		if len(rows) < claimants {
			v.Reason = "not-found"
			v.detail = fmt.Sprintf("only %d new %ss titled %q appeared for the %d created items that could be filed there (%s)", len(rows), c.Kind, c.Title, claimants, strings.Join(candidates, ", "))
		}
		verdicts[i] = v
	}
	for i, v := range verdicts {
		out[i] = v
	}
}

// checkClosedAt turns back each confirmation of an item the payload closes
// with a completion-date whose row was saved with another stopDate, keeping
// its uuid: the item is there, without its date. A row with no stopDate yet
// has not settled, so it is read again within budget, and one still without
// it is left confirmed rather than judged. Things keeps the date to the
// fraction of a second; a second's slack costs nothing.
func checkClosedAt(database *db.DB, out []importCreated, creates []importCreate, found map[createdWant][]model.Task, budget time.Duration) {
	stops := map[string]*time.Time{}
	var pending []string
	for i, c := range creates {
		if !out[i].Confirmed || c.closedAt.IsZero() {
			continue
		}
		idx := slices.IndexFunc(found[c.want()], func(t model.Task) bool { return t.UUID == out[i].UUID })
		if idx < 0 {
			continue
		}
		stops[out[i].UUID] = found[c.want()][idx].StopDate
		if stops[out[i].UUID] == nil {
			pending = append(pending, out[i].UUID)
		}
	}
	if len(pending) > 0 {
		_ = pollUntil(budget, func(bool) (bool, error) {
			rows, err := database.GetTasksByUUIDs(pending)
			if err != nil {
				return false, nil
			}
			settled := true
			for _, uuid := range pending {
				if t := rows[uuid]; t != nil && t.StopDate != nil {
					stops[uuid] = t.StopDate
				} else {
					settled = false
				}
			}
			return settled, nil
		})
	}
	for i, c := range creates {
		stop, ok := stops[out[i].UUID]
		if !ok || c.closedAt.IsZero() || stop == nil || stop.Sub(c.closedAt).Abs() < time.Second {
			continue
		}
		out[i].Confirmed, out[i].Reason = false, dateDropped
		out[i].detail = fmt.Sprintf("%s %q (%s) was saved with completion date %s, not %s", c.kind(), c.title, out[i].UUID, stop.UTC().Format(time.RFC3339Nano), c.closedAt.UTC().Format(time.RFC3339Nano))
	}
}

// distinctCreationDates reports whether no two of tasks, oldest first, share a
// creation date, so their order is the order Things saved them in.
func distinctCreationDates(tasks []model.Task) bool {
	for i := 1; i < len(tasks); i++ {
		prev, cur := tasks[i-1].CreationDate, tasks[i].CreationDate
		if prev == nil || cur == nil || !cur.After(*prev) {
			return false
		}
	}
	return true
}

// line renders c the way the plain-text output reports it.
func (c importCreated) line() string {
	switch {
	case c.Confirmed:
		return fmt.Sprintf("Created and confirmed: %s %q (%s)", c.Path, c.Title, c.UUID)
	case len(c.Candidates) > 0:
		return fmt.Sprintf("%s: %s %q (%s)", unconfirmedMsg[c.Reason], c.Path, c.Title, strings.Join(c.Candidates, ", "))
	case c.UUID != "":
		return fmt.Sprintf("%s: %s %q (%s)", unconfirmedMsg[c.Reason], c.Path, c.Title, c.UUID)
	}
	return fmt.Sprintf("%s: %s %q", unconfirmedMsg[c.Reason], c.Path, c.Title)
}

// printImportCreated reports each created item, a line per item or, under
// --json, an array. An import that created nothing prints nothing.
func printImportCreated(d *Deps, created []importCreated) error {
	if len(created) == 0 {
		return nil
	}
	if d.JSON {
		return output.PrintJSON(d.Stdout, created)
	}
	for _, c := range created {
		if _, err := fmt.Fprintln(d.Stdout, c.line()); err != nil {
			return err
		}
	}
	return nil
}

// verifyImportStatuses re-reads every item the payload asked to complete or
// cancel and returns the ones whose status never changed, with the number of
// status changes requested. Things gives the import no per-item result, so
// this is the only way a silently dropped status change in a batch becomes
// visible.
//
// Every item is checked before anything is reported: a payload's later items
// are just as interesting as its first, so the read-back does not stop at the
// first failure. The batch polls for up to wait, what is left of the
// import's budget, and reports budget in its messages.
func verifyImportStatuses(database *db.DB, plan *importPlan, wait, budget time.Duration) ([]importVerifyItem, int) {
	var wants []statusWant
	var items []importVerifyItem
	for _, u := range plan.updates {
		if !u.resolvable() {
			continue
		}
		// An id the pre-write pass could not find was already warned about;
		// Things reports it too, and reading it back would only repeat that.
		task := plan.tasks[u.id]
		if task == nil {
			continue
		}
		want, ok := wantedStatus(u.attrs)
		if !ok {
			continue
		}
		wants = append(wants, statusWant{uuid: u.id, title: task.Title, want: want})
		items = append(items, importVerifyItem{Path: u.path, ID: u.id, Title: task.Title, Wanted: want})
	}
	if len(wants) == 0 {
		return nil, 0
	}

	var failures []importVerifyItem
	for i, res := range verifyStatusesWithin(database, wants, wait, budget) {
		if res.err == nil {
			continue
		}
		failed := items[i]
		failed.Got, failed.Observed, failed.err = res.got, res.observed, res.err
		failures = append(failures, failed)
	}
	return failures, len(wants)
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

// jsonItems renders the unapplied status changes, then the created items
// that never appeared, for the --json error payload.
func (e *importVerifyError) jsonItems() []jsonErrorItem {
	missing := e.missing()
	for _, it := range e.created {
		if it.Reason == dateDropped {
			missing = append(missing, it)
		}
	}
	out := make([]jsonErrorItem, len(e.items), len(e.items)+len(missing))
	for i, it := range e.items {
		out[i] = jsonErrorItem{
			Path:   it.Path,
			ID:     it.ID,
			Title:  it.Title,
			Wanted: it.Wanted.String(),
		}
		if it.Observed {
			out[i].Got = it.Got.String()
		}
	}
	for _, it := range missing {
		out = append(out, jsonErrorItem{
			Path:       it.Path,
			ID:         it.UUID,
			Title:      it.Title,
			Confirmed:  new(bool),
			Reason:     it.Reason,
			Candidates: append([]string(nil), it.Candidates...),
			Present:    it.Present,
		})
	}
	return out
}
