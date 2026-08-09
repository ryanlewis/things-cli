package model

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"
)

const (
	TypeTask    = 0
	TypeProject = 1

	StatusOpen      Status = 0
	StatusCancelled Status = 2
	StatusCompleted Status = 3

	StartInbox   = 0
	StartAnytime = 1
	StartSomeday = 2
)

// Status is a Things3 task/project status. The underlying integers are the
// raw Things codes (0 = open, 2 = cancelled, 3 = completed — note there is no
// 1), but JSON renders the human-readable string so scripts and agents never
// have to decode the magic ints.
type Status int

// statusNames is the single source of truth for the name<->code mapping used
// by String, MarshalJSON, and UnmarshalJSON.
var statusNames = map[Status]string{
	StatusOpen:      "open",
	StatusCancelled: "cancelled",
	StatusCompleted: "completed",
}

func (s Status) String() string {
	if name, ok := statusNames[s]; ok {
		return name
	}
	return "unknown"
}

// MarshalJSON renders a recognized status as its string name
// ("open"/"cancelled"/"completed"). An unrecognized raw Things code is
// preserved as its integer so the value round-trips losslessly rather than
// collapsing to a lossy "unknown" string.
func (s Status) MarshalJSON() ([]byte, error) {
	if name, ok := statusNames[s]; ok {
		return json.Marshal(name)
	}
	return json.Marshal(int(s))
}

// UnmarshalJSON accepts either a status name or the raw Things integer,
// mirroring MarshalJSON so values round-trip. Names are matched strictly
// against the known set; integers are taken verbatim as the raw wire code.
func (s *Status) UnmarshalJSON(data []byte) error {
	// Per the json.Unmarshaler convention, a JSON null is a no-op: leave the
	// existing value untouched rather than silently coercing it to Status(0)
	// ("open").
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
			return fmt.Errorf("Status: %w", err)
		}
		var n int
		if err := json.Unmarshal(data, &n); err != nil {
			return fmt.Errorf("Status: %w", err)
		}
		*s = Status(n)
		return nil
	}
	for st, n := range statusNames {
		if n == name {
			*s = st
			return nil
		}
	}
	return fmt.Errorf("Status: unknown value %q", name)
}

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
	return time.Unix(int64(sec), int64(frac*float64(time.Second))).UTC()
}

// TimeToUnix converts a time.Time to a Things absolute timestamp.
func TimeToUnix(t time.Time) float64 {
	return float64(t.UnixNano()) / float64(time.Second)
}

type Task struct {
	UUID         string      `json:"uuid"`
	Title        string      `json:"title"`
	Notes        string      `json:"notes,omitempty"`
	Type         int         `json:"type"`
	Status       Status      `json:"status"`
	Start        int         `json:"start"`
	StartBucket  int         `json:"startBucket"`
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
}

type ChecklistItem struct {
	UUID     string     `json:"uuid"`
	Title    string     `json:"title"`
	Status   Status     `json:"status"`
	StopDate *time.Time `json:"stopDate,omitempty"`
	Index    int        `json:"index"`
}

type Project struct {
	UUID      string   `json:"uuid"`
	Title     string   `json:"title"`
	Status    Status   `json:"status"`
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
