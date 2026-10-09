package main

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/ryanlewis/things-cli/internal/db"
	"github.com/ryanlewis/things-cli/internal/model"
)

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
	// parentClosed is set on a to-do in the items of a project the payload
	// completes or cancels.
	parentClosed bool
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
		itemType, _ := v["type"].(string)
		itemType = strings.TrimSpace(itemType)
		switch itemType {
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
		if itemType == "project" {
			parent := importTo{nested: true}
			if op == "update" {
				parent.parentID, _ = v["id"].(string)
			} else {
				parent.parentTitle, _ = attrs["title"].(string)
			}
			completed, _ := attrs["completed"].(bool)
			canceled, _ := attrs["canceled"].(bool)
			parent.parentClosed = completed || canceled
			items, _ := attrs["items"].([]any)
			for i := range items {
				parents[fmt.Sprintf("%s.attributes.items[%d]", path, i)] = parent
			}
		}
		if op != "" && op != "create" {
			return
		}
		typ, ok := taskTypeOf(itemType)
		if !ok {
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
		when, _ := attrs["when"].(string)
		creates = append(creates, importCreate{path: path, typ: typ, title: title, shown: storedTitle(shown), dated: raw != nil, datedAt: datedAt, closedAt: closedAt, closed: completed || canceled, when: when, to: to})
	})
	return creates
}
