package model

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"
)

const (
	TypeTask    TaskType = 0
	TypeProject TaskType = 1
	// TypeHeading is a project heading — a TMTask row that groups to-dos
	// inside a project. It is never a to-do itself, so lookups exclude it.
	TypeHeading TaskType = 2

	StatusOpen      Status = 0
	StatusCancelled Status = 2
	StatusCompleted Status = 3

	StartInbox   Start = 0
	StartAnytime Start = 1
	StartSomeday Start = 2
)

// enumCodec is the shared JSON codec for a Things enum stored as a small
// integer and rendered as a human-readable string, so scripts and agents never
// have to decode the magic ints. Status, TaskType and Start behave identically
// and each used to spell the behaviour out for itself (issue #215).
//
// The rules every type using it inherits:
//   - String and MarshalJSON render a recognized code as its name.
//   - An unrecognized raw code marshals as its integer rather than collapsing
//     to a lossy "unknown", so the value round-trips.
//   - UnmarshalJSON accepts either the name or the legacy raw integer, and
//     takes an unrecognized integer verbatim as the wire code.
//   - A JSON null is a no-op, per the json.Unmarshaler convention: it leaves
//     the existing value alone rather than coercing it to the zero code.
//   - An unknown name is an error naming the type it failed to decode.
type enumCodec[T ~int] struct {
	// typeName prefixes every error, so a decode failure names the type that
	// rejected the value rather than just the offending token.
	typeName string
	names    map[T]string
	codes    map[string]T
}

// newEnumCodec builds a codec from the name map, which stays the single source
// of truth: the decode direction is derived from it here rather than written
// out a second time, so the two cannot drift. Names have to be distinct — a
// duplicate would leave one of its codes unreachable on decode.
func newEnumCodec[T ~int](typeName string, names map[T]string) enumCodec[T] {
	codes := make(map[string]T, len(names))
	for code, name := range names {
		codes[name] = code
	}
	return enumCodec[T]{typeName: typeName, names: names, codes: codes}
}

func (c enumCodec[T]) nameOf(v T) string {
	if name, ok := c.names[v]; ok {
		return name
	}
	return "unknown"
}

func (c enumCodec[T]) marshal(v T) ([]byte, error) {
	if name, ok := c.names[v]; ok {
		return json.Marshal(name)
	}
	return json.Marshal(int(v))
}

func (c enumCodec[T]) unmarshal(v *T, data []byte) error {
	if string(data) == "null" {
		return nil
	}
	// Try the string name first; on a type mismatch fall back to the raw
	// integer so both the emitted string form and the legacy integer decode. A
	// non-type error (malformed JSON) is surfaced as-is rather than retried as
	// an int.
	var name string
	if err := json.Unmarshal(data, &name); err != nil {
		var typeErr *json.UnmarshalTypeError
		if !errors.As(err, &typeErr) {
			return fmt.Errorf("%s: %w", c.typeName, err)
		}
		var n int
		if err := json.Unmarshal(data, &n); err != nil {
			return fmt.Errorf("%s: %w", c.typeName, err)
		}
		*v = T(n)
		return nil
	}
	code, ok := c.codes[name]
	if !ok {
		return fmt.Errorf("%s: unknown value %q", c.typeName, name)
	}
	*v = code
	return nil
}

// TaskType is a Things3 TMTask type. The underlying integers are the raw
// Things codes (0 = to-do, 1 = project, 2 = heading), but JSON renders the
// human-readable string — see enumCodec for the rules.
//
// Only "task" and "project" ever reach output: every list view pins the type
// in SQL and every lookup applies the notHeading filter, so a heading row is
// never returned (see internal/db/tasks.go). "heading" is defined because the
// codec has to be total over the three codes the database uses.
type TaskType int

// typeCodec is the single source of truth for the name<->code mapping used by
// String, MarshalJSON, and UnmarshalJSON. TypeTask renders as "task", which is
// deliberately not the "to-do" that Things' own JSON URL scheme uses for the
// same concept in a `things import` payload — that payload is Things'
// vocabulary, not the CLI's, and the two are not interchangeable.
var typeCodec = newEnumCodec("TaskType", map[TaskType]string{
	TypeTask:    "task",
	TypeProject: "project",
	TypeHeading: "heading",
})

func (t TaskType) String() string { return typeCodec.nameOf(t) }

// MarshalJSON renders a recognized type as its string name
// ("task"/"project"/"heading"), and an unrecognized raw Things code as its
// integer.
func (t TaskType) MarshalJSON() ([]byte, error) { return typeCodec.marshal(t) }

// UnmarshalJSON accepts either a type name or the raw Things integer,
// mirroring MarshalJSON so values round-trip.
func (t *TaskType) UnmarshalJSON(data []byte) error { return typeCodec.unmarshal(t, data) }

// Status is a Things3 task/project status. The underlying integers are the
// raw Things codes (0 = open, 2 = cancelled, 3 = completed — note there is no
// 1), but JSON renders the human-readable string — see enumCodec for the
// rules.
type Status int

// statusCodec is the single source of truth for the name<->code mapping used
// by String, MarshalJSON, and UnmarshalJSON.
var statusCodec = newEnumCodec("Status", map[Status]string{
	StatusOpen:      "open",
	StatusCancelled: "cancelled",
	StatusCompleted: "completed",
})

func (s Status) String() string { return statusCodec.nameOf(s) }

// MarshalJSON renders a recognized status as its string name
// ("open"/"cancelled"/"completed"), and an unrecognized raw Things code as its
// integer.
func (s Status) MarshalJSON() ([]byte, error) { return statusCodec.marshal(s) }

// UnmarshalJSON accepts either a status name or the raw Things integer,
// mirroring MarshalJSON so values round-trip.
func (s *Status) UnmarshalJSON(data []byte) error { return statusCodec.unmarshal(s, data) }

// Start is the Things3 list an item belongs to when nothing else has claimed
// it. The underlying integers are the raw Things codes (0 = inbox, 1 =
// anytime, 2 = someday), but JSON renders the human-readable string — see
// enumCodec for the rules.
//
// Start alone does not say which of the app's lists an item shows up in,
// because a start date changes the answer for two of the three codes. An
// anytime item with a date for today is in Today; a someday item with a date
// is in Upcoming, and only an undated one is in Someday. The view queries in
// internal/db/tasks.go pair start with startDate for exactly that reason.
type Start int

// startCodec is the single source of truth for the name<->code mapping used by
// String, MarshalJSON, and UnmarshalJSON. The three names are the app's own
// list names, lowercased.
var startCodec = newEnumCodec("Start", map[Start]string{
	StartInbox:   "inbox",
	StartAnytime: "anytime",
	StartSomeday: "someday",
})

func (s Start) String() string { return startCodec.nameOf(s) }

// MarshalJSON renders a recognized start as its string name
// ("inbox"/"anytime"/"someday"), and an unrecognized raw Things code as its
// integer.
func (s Start) MarshalJSON() ([]byte, error) { return startCodec.marshal(s) }

// UnmarshalJSON accepts either a start name or the raw Things integer,
// mirroring MarshalJSON so values round-trip.
func (s *Start) UnmarshalJSON(data []byte) error { return startCodec.unmarshal(s, data) }

// ThingsDate is a bit-encoded date: year<<16 | month<<12 | day<<7.
type ThingsDate int64

func (d ThingsDate) ToTime() time.Time {
	year := int(d >> 16)
	month := time.Month((int(d) >> 12) & 0xF)
	day := (int(d) >> 7) & 0x1F
	return time.Date(year, month, day, 0, 0, 0, 0, time.Local)
}

func ThingsDateFromTime(t time.Time) ThingsDate {
	return ThingsDate(t.Year()<<16 | int(t.Month())<<12 | t.Day()<<7)
}

func (d ThingsDate) String() string {
	return d.ToTime().Format("2006-01-02")
}

// MarshalJSON renders the date as YYYY-MM-DD so jq/agents/scripts see a real
// date rather than the bit-encoded int.
func (d ThingsDate) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.String())
}

func (d *ThingsDate) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return fmt.Errorf("ThingsDate: %w", err)
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return fmt.Errorf("ThingsDate: %w", err)
	}
	*d = ThingsDateFromTime(t)
	return nil
}

// UnixToTime converts a Things absolute timestamp (fractional seconds since
// the Unix epoch, as stored in creationDate/stopDate/userModificationDate) to
// time.Time. Despite the Core Data heritage of the schema, Things stores these
// REAL columns against 1970, not Apple's 2001 reference date.
func UnixToTime(ts float64) time.Time {
	sec, frac := math.Modf(ts)
	return time.Unix(int64(sec), int64(math.Round(frac*float64(time.Second)))).UTC()
}

// TimeToUnix converts a time.Time to a Things absolute timestamp.
func TimeToUnix(t time.Time) float64 {
	return float64(t.UnixNano()) / float64(time.Second)
}

type Task struct {
	UUID   string   `json:"uuid"`
	Title  string   `json:"title"`
	Notes  string   `json:"notes,omitempty"`
	Type   TaskType `json:"type"`
	Status Status   `json:"status"`
	Start  Start    `json:"start"`

	// StartBucket is the Evening split within a scheduled day: 1 is the app's
	// This Evening section, 0 is everything else. It stays a raw integer where
	// Start became a named type, because only the 1 has a name in Things'
	// vocabulary (`--when evening`) and naming the 0 would assert an invented
	// token on every row that is not an evening row (issue #241).
	StartBucket int `json:"startBucket"`

	StartDate    *ThingsDate `json:"startDate,omitempty"`
	Deadline     *ThingsDate `json:"deadline,omitempty"`
	StopDate     *time.Time  `json:"stopDate,omitempty"`
	CreationDate *time.Time  `json:"creationDate,omitempty"`
	Trashed      bool        `json:"trashed"`
	ProjectUUID  string      `json:"projectUUID,omitempty"`
	ProjectTitle string      `json:"projectTitle,omitempty"`
	AreaUUID     string      `json:"areaUUID,omitempty"`
	AreaTitle    string      `json:"areaTitle,omitempty"`
	HeadingUUID  string      `json:"headingUUID,omitempty"`
	HeadingTitle string      `json:"headingTitle,omitempty"`
	Tags         []string    `json:"tags,omitempty"`
	Index        int         `json:"index"`
	TodayIndex   int         `json:"todayIndex"`

	// Repeating marks an item Things treats as a repeating to-do or project.
	// Things refuses status, when, deadline and duplicate updates on these,
	// silently, so callers need to know before they try.
	Repeating bool `json:"repeating,omitempty"`
}

type ChecklistItem struct {
	UUID     string     `json:"uuid"`
	Title    string     `json:"title"`
	Status   Status     `json:"status"`
	StopDate *time.Time `json:"stopDate,omitempty"`
	Index    int        `json:"index"`
}

type Project struct {
	UUID  string `json:"uuid"`
	Title string `json:"title"`

	Status Status `json:"status"`

	// Start, StartBucket, StartDate and Deadline carry the same encodings and
	// JSON names as the matching Task fields, so a caller reads a scheduled
	// project the way it reads a scheduled to-do (issue #202).
	Start       Start       `json:"start"`
	StartBucket int         `json:"startBucket"`
	StartDate   *ThingsDate `json:"startDate,omitempty"`
	Deadline    *ThingsDate `json:"deadline,omitempty"`

	AreaUUID  string   `json:"areaUUID,omitempty"`
	AreaTitle string   `json:"areaTitle,omitempty"`
	Tags      []string `json:"tags,omitempty"`
	TaskCount int      `json:"taskCount"`
	OpenCount int      `json:"openCount"`
}

type Area struct {
	UUID    string `json:"uuid"`
	Title   string `json:"title"`
	Visible bool   `json:"visible"`
}

type Tag struct {
	UUID       string `json:"uuid"`
	Title      string `json:"title"`
	Shortcut   string `json:"shortcut,omitempty"`
	ParentUUID string `json:"parentUUID,omitempty"`
}
