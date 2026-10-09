package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/ryanlewis/things-cli/internal/things"
)

// maxImportItems is the most items one import sends. Measured in Things 3
// (8 Oct 2026): payloads of 100 and 200 to-dos were created straight away,
// while 300 made Things ask "Is this what you intended?" and create nothing
// until someone answered, long after the read-back gave up. The threshold is
// somewhere from 201 to 300 and was not measured more closely, so 200 is the
// largest size known to be safe. Only top-level to-dos were measured, so
// the limit counts the items that create something, at any depth: to-dos,
// projects, and headings in a project's items. Update items, which create
// nothing, do not count, and nor do checklist items, which are part of their
// to-do.
const maxImportItems = 200

// importSlot is where an item sits in a payload, which decides the types
// Things creates there.
type importSlot int

const (
	slotTop importSlot = iota
	slotProjectItems
	slotChecklist
)

// importSlotTypes are the item types Things creates in each slot. Measured
// in Things 3: a heading at the top level, a project in a project's items
// and a to-do in a to-do's checklist-items each make it reject the whole
// payload.
var importSlotTypes = map[importSlot][]string{
	slotTop:          {"to-do", "project"},
	slotProjectItems: {"to-do", "heading"},
	slotChecklist:    {"checklist-item"},
}

var importSlotNames = map[importSlot]string{
	slotTop:          "at the top level",
	slotProjectItems: "in a project's items",
	slotChecklist:    "in a to-do's checklist-items",
}

// importItemTypeNames are every item type Things knows, by the format's word
// for it. Things does not trim a type: "to-do " is rejected.
var importItemTypeNames = []string{"to-do", "project", "heading", "checklist-item"}

// importItemTypes is importItemTypeNames as a set.
var importItemTypes = func() map[string]bool {
	set := map[string]bool{}
	for _, name := range importItemTypeNames {
		set[name] = true
	}
	return set
}()

// importAttrKinds is the JSON kind each attribute of a created item must
// have, null aside, besides the destinations badImportTypes checks. Measured
// in Things 3: a number or string tags, a number notes, when or deadline,
// completed or canceled given as 1 or "true", and a string items or
// checklist-items each make it reject the whole payload. A title that is not
// a string makes it reject the payload too.
var importAttrKinds = []struct{ name, kind string }{
	{"title", "string"},
	{"notes", "string"},
	{"when", "string"},
	{"deadline", "string"},
	{"tags", "array of strings"},
	{"completed", "boolean"},
	{"canceled", "boolean"},
	{"items", "array"},
	{"checklist-items", "array"},
}

// importShape is what is wrong with one payload item's shape, apart from its
// dates and destinations.
type importShape struct {
	// items is each way the item is not one Things takes, as `name: why`:
	// not an object, a type or operation it does not know or allow there,
	// no attributes. itemNames are the attributes they are about.
	items, itemNames []string
	// types is each attribute with the wrong JSON kind, as `name: value`.
	types, typeNames []string
	// ignored is each attribute Things takes on another type of item and
	// ignores on this one, as `name: why`.
	ignored, ignoredNames []string
	// long is each title or notes longer than Things keeps, as
	// `name: N characters`.
	long, longNames []string
	// blank is set for a to-do or project created with no title, or only
	// whitespace. Things creates it, untitled, which an agent almost never
	// means.
	blank bool
}

func (s importShape) empty() bool {
	return len(s.items) == 0 && len(s.types) == 0 && len(s.ignored) == 0 && len(s.long) == 0 && !s.blank
}

// importIgnoredAttrs are the attributes Things takes on one type of created
// item and ignores on another, by the type that ignores them. Measured in
// Things 3 (9 Oct 2026): a to-do's items, whatever their kind, are dropped
// and the to-do is created without them, and a project's checklist-items
// given as a string are dropped too. A project has no checklist, so its
// checklist-items are refused whatever their kind.
var importIgnoredAttrs = map[string]struct{ name, why string }{
	"to-do":   {"items", "Things takes items only on a project, and ignores them on a to-do"},
	"project": {"checklist-items", "Things takes checklist-items only on a to-do, and ignores them on a project"},
}

// importLengthLimits are the longest title and notes a created item can
// have, the limits `add` enforces. Measured in Things 3 (9 Oct 2026): a
// title over 4000 characters is cut to 4000 and the item created. Notes over
// 10000 characters were not measured; they are held to the limit `add` uses.
var importLengthLimits = []struct {
	name string
	max  int
}{
	{"title", things.MaxStringLen},
	{"notes", things.MaxNotesLen},
}

// importShapes walks the items of payload where Things reads them: the top
// level, a project's items, and a to-do's checklist-items. It returns what is
// wrong with each item that has a problem, by path, and the number of items
// that count towards maxImportItems: every object that is not an update item
// or a checklist item.
func importShapes(payload []any) (map[string]importShape, int) {
	shapes := map[string]importShape{}
	count := 0
	var walk func(items []any, prefix string, slot importSlot)
	walk = func(items []any, prefix string, slot importSlot) {
		for i, raw := range items {
			path := fmt.Sprintf("%s[%d]", prefix, i)
			item, ok := raw.(map[string]any)
			if ok && slot != slotChecklist && item["operation"] != "update" {
				count++
			}
			if !ok {
				shown, _ := json.Marshal(raw)
				shapes[path] = importShape{items: []string{fmt.Sprintf("not an object: %s", shown)}}
				continue
			}
			if s := checkImportItem(item, slot); !s.empty() {
				shapes[path] = s
			}
			attrs, _ := item["attributes"].(map[string]any)
			switch item["type"] {
			case "project":
				if sub, ok := attrs["items"].([]any); ok {
					walk(sub, path+".attributes.items", slotProjectItems)
				}
			case "to-do":
				if sub, ok := attrs["checklist-items"].([]any); ok {
					walk(sub, path+".attributes.checklist-items", slotChecklist)
				}
			}
		}
	}
	walk(payload, "", slotTop)
	return shapes, count
}

// checkImportItem checks the shape of item, which sits in slot. Every item
// must have a type Things knows and an operation of create or update, both
// exactly as written. A created item must also have a type Things creates in
// that slot, and attributes, each of the kind importAttrKinds gives, none
// Things ignores on its type (importIgnoredAttrs), and a title and notes
// within importLengthLimits, and a created to-do or project a title. Update items were not measured past
// their type, operation and attributes, so their attributes are not checked.
// A null attribute counts as not given, as it does to Things for the
// destinations.
func checkImportItem(item map[string]any, slot importSlot) importShape {
	var s importShape
	bad := func(name, format string, args ...any) {
		s.items = append(s.items, name+": "+fmt.Sprintf(format, args...))
		s.itemNames = append(s.itemNames, name)
	}
	shown := func(v any) string {
		b, _ := json.Marshal(v)
		return string(b)
	}

	create := true
	switch op := item["operation"].(type) {
	case nil, string:
		switch op {
		case nil, "create":
		case "update":
			create = false
		default:
			bad("operation", "%s is not create or update", shown(op))
		}
	default:
		bad("operation", "%s is not create or update", shown(op))
	}

	itemType, _ := item["type"].(string)
	switch {
	case !importItemTypes[itemType]:
		allowed := importSlotTypes[slot]
		if !create {
			allowed = importItemTypeNames
		}
		bad("type", "%s is not %s", shown(item["type"]), strings.Join(allowed, " or "))
	case create && !slices.Contains(importSlotTypes[slot], itemType):
		bad("type", "%s is not allowed %s, only %s", shown(itemType), importSlotNames[slot], strings.Join(importSlotTypes[slot], " or "))
	}

	rawAttrs := item["attributes"]
	attrs, isObject := rawAttrs.(map[string]any)
	switch {
	case rawAttrs == nil && create:
		bad("attributes", "missing")
		return s
	case rawAttrs == nil:
		return s
	case !isObject:
		s.types = append(s.types, "attributes: "+shown(rawAttrs))
		s.typeNames = append(s.typeNames, "attributes")
		return s
	case !create:
		return s
	}

	ignored, hasIgnored := importIgnoredAttrs[itemType]
	if hasIgnored && attrs[ignored.name] != nil {
		s.ignored = append(s.ignored, fmt.Sprintf("%s: %s", ignored.name, ignored.why))
		s.ignoredNames = append(s.ignoredNames, ignored.name)
	}
	for _, a := range importAttrKinds {
		raw := attrs[a.name]
		if raw == nil || hasKind(raw, a.kind) || (hasIgnored && a.name == ignored.name) {
			continue
		}
		s.types = append(s.types, fmt.Sprintf("%s: %s", a.name, shown(raw)))
		s.typeNames = append(s.typeNames, a.name)
	}
	for _, l := range importLengthLimits {
		if v, ok := attrs[l.name].(string); ok {
			if n := utf8.RuneCountInString(v); n > l.max {
				s.long = append(s.long, fmt.Sprintf("%s: %d characters, over %d", l.name, n, l.max))
				s.longNames = append(s.longNames, l.name)
			}
		}
	}
	if itemType == "to-do" || itemType == "project" {
		if title, ok := attrs["title"].(string); (ok || attrs["title"] == nil) && strings.TrimSpace(title) == "" {
			s.blank = true
		}
	}
	return s
}

// hasKind reports whether v, a decoded JSON value, is of kind, one of the
// kinds importAttrKinds names.
func hasKind(v any, kind string) bool {
	switch kind {
	case "string":
		_, ok := v.(string)
		return ok
	case "boolean":
		_, ok := v.(bool)
		return ok
	case "array":
		_, ok := v.([]any)
		return ok
	case "array of strings":
		list, ok := v.([]any)
		return ok && !slices.ContainsFunc(list, func(e any) bool {
			_, isString := e.(string)
			return !isString
		})
	}
	return false
}

// importDuplicateKeys finds every object in data, a payload that decodes,
// that gives one key twice, and returns the keys by the path of the item
// they belong to: a key repeated in an item's attributes belongs to that
// item, and any other to the object it is in. Paths are in the form
// walkImportValues gives. Measured in Things 3 (9 Oct 2026): with title
// given twice Things keeps the first, while encoding/json keeps the last, so
// the read-back would look for a title Things never saved.
func importDuplicateKeys(data []byte) map[string][]string {
	dups := map[string][]string{}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var walk func(path string) error
	walk = func(path string) error {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		delim, ok := tok.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for dec.More() {
				keyTok, err := dec.Token()
				if err != nil {
					return err
				}
				key, _ := keyTok.(string)
				if seen[key] {
					item := strings.TrimSuffix(path, ".attributes")
					if !slices.Contains(dups[item], key) {
						dups[item] = append(dups[item], key)
					}
				}
				seen[key] = true
				if err := walk(path + "." + key); err != nil {
					return err
				}
			}
		case '[':
			for i := 0; dec.More(); i++ {
				if err := walk(fmt.Sprintf("%s[%d]", path, i)); err != nil {
					return err
				}
			}
		}
		_, err = dec.Token() // the closing delimiter
		return err
	}
	// The payload has decoded, so the walk does not fail.
	_ = walk("")
	return dups
}
