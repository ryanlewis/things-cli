package model

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestThingsDateRoundTrip(t *testing.T) {
	cases := []struct {
		name    string
		y, m, d int
	}{
		{"typical", 2026, 4, 14},
		{"start of year", 2024, 1, 1},
		{"end of year", 2023, 12, 31},
		{"leap day", 2024, 2, 29},
		{"day 31", 2025, 7, 31},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := time.Date(tc.y, time.Month(tc.m), tc.d, 0, 0, 0, 0, time.Local)
			got := ThingsDateFromTime(in).ToTime()
			if got.Year() != tc.y || int(got.Month()) != tc.m || got.Day() != tc.d {
				t.Fatalf("roundtrip mismatch: in=%s got=%s", in, got)
			}
		})
	}
}

func TestThingsDateString(t *testing.T) {
	d := ThingsDateFromTime(time.Date(2026, 4, 14, 0, 0, 0, 0, time.Local))
	if got, want := d.String(), "2026-04-14"; got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}

func TestThingsDateEncoding(t *testing.T) {
	// Verify the bit layout: year<<16 | month<<12 | day<<7
	d := ThingsDateFromTime(time.Date(2026, 4, 14, 0, 0, 0, 0, time.Local))
	want := ThingsDate(2026<<16 | 4<<12 | 14<<7)
	if d != want {
		t.Fatalf("encoding mismatch: got %d, want %d", d, want)
	}
}

func TestThingsDateMarshalJSON(t *testing.T) {
	d := ThingsDateFromTime(time.Date(2026, 5, 9, 0, 0, 0, 0, time.Local))
	got, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if want := `"2026-05-09"`; string(got) != want {
		t.Fatalf("Marshal = %s, want %s", got, want)
	}
}

func TestThingsDateUnmarshalJSON(t *testing.T) {
	var d ThingsDate
	if err := json.Unmarshal([]byte(`"2026-05-09"`), &d); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if got, want := d.String(), "2026-05-09"; got != want {
		t.Fatalf("Unmarshal -> String = %q, want %q", got, want)
	}
	want := ThingsDateFromTime(time.Date(2026, 5, 9, 0, 0, 0, 0, time.Local))
	if d != want {
		t.Fatalf("Unmarshal value = %d, want %d", d, want)
	}
}

func TestThingsDateUnmarshalInvalid(t *testing.T) {
	cases := []string{
		`"2026/05/09"`,
		`"not a date"`,
		`12345`,
	}
	for _, in := range cases {
		var d ThingsDate
		if err := json.Unmarshal([]byte(in), &d); err == nil {
			t.Errorf("Unmarshal(%s) succeeded, want error", in)
		}
	}
}

func TestThingsDateOmitemptyNil(t *testing.T) {
	type holder struct {
		StartDate *ThingsDate `json:"startDate,omitempty"`
	}
	got, err := json.Marshal(holder{})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(got) != `{}` {
		t.Fatalf("Marshal of nil = %s, want {}", got)
	}
}

func TestThingsDateRoundTripJSON(t *testing.T) {
	in := ThingsDateFromTime(time.Date(2024, 2, 29, 0, 0, 0, 0, time.Local))
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var out ThingsDate
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if in != out {
		t.Fatalf("round-trip mismatch: in=%d out=%d", in, out)
	}
}

func TestUnixTimeRoundTrip(t *testing.T) {
	cases := []time.Time{
		time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC), // epoch
		time.Date(2026, 4, 14, 12, 34, 56, 0, time.UTC),
		time.Date(1969, 6, 15, 8, 0, 0, 0, time.UTC), // pre-epoch
	}
	for _, in := range cases {
		ts := TimeToUnix(in)
		got := UnixToTime(ts)
		if !got.Equal(in) {
			t.Fatalf("roundtrip mismatch: in=%s got=%s (ts=%f)", in, got, ts)
		}
	}
}

func TestUnixTimeEpochZero(t *testing.T) {
	epoch := time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)
	if ts := TimeToUnix(epoch); ts != 0 {
		t.Fatalf("epoch should be 0, got %f", ts)
	}
	if got := UnixToTime(0); !got.Equal(epoch) {
		t.Fatalf("UnixToTime(0) = %s, want %s", got, epoch)
	}
}

// Regression for the "+31 years" bug: Things stores creationDate/stopDate as
// Unix-epoch seconds, but they were being decoded against the Core Data 2001
// reference date, shifting every absolute timestamp 31 years into the future.
// The raw value below came from a real TMTask row created 2026-08-09.
func TestUnixTimeNotCoreDataEpoch(t *testing.T) {
	got := UnixToTime(1786235005.119778)
	want := time.Date(2026, 8, 9, 0, 23, 25, 0, time.UTC)
	if got.Year() != want.Year() {
		t.Fatalf("UnixToTime decoded into year %d, want %d (Core Data epoch regression)", got.Year(), want.Year())
	}
	if got.Sub(want).Abs() > time.Second {
		t.Fatalf("UnixToTime(1786235005.119778) = %s, want ~%s", got, want)
	}
}

func TestStatusMarshalJSON(t *testing.T) {
	cases := []struct {
		status Status
		want   string
	}{
		{StatusOpen, `"open"`},
		{StatusCancelled, `"cancelled"`},
		{StatusCompleted, `"completed"`},
		{Status(99), `99`}, // unrecognized code preserved as its raw int
	}
	for _, tc := range cases {
		got, err := json.Marshal(tc.status)
		if err != nil {
			t.Fatalf("Marshal(%d): %v", tc.status, err)
		}
		if string(got) != tc.want {
			t.Errorf("Marshal(%d) = %s, want %s", tc.status, got, tc.want)
		}
	}
}

func TestStatusUnmarshalJSON(t *testing.T) {
	cases := []struct {
		in   string
		want Status
	}{
		{`"open"`, StatusOpen},
		{`"cancelled"`, StatusCancelled},
		{`"completed"`, StatusCompleted},
		{`0`, StatusOpen},      // legacy integer input
		{`2`, StatusCancelled}, // legacy integer input (the non-obvious code)
		{`3`, StatusCompleted}, // legacy integer input
		{`99`, Status(99)},     // unrecognized raw code taken verbatim
	}
	for _, tc := range cases {
		var s Status
		if err := json.Unmarshal([]byte(tc.in), &s); err != nil {
			t.Fatalf("Unmarshal(%s): %v", tc.in, err)
		}
		if s != tc.want {
			t.Errorf("Unmarshal(%s) = %d, want %d", tc.in, s, tc.want)
		}
	}
	// A JSON null is a no-op: it must leave the existing value untouched rather
	// than silently coercing it to Status(0) ("open").
	pre := StatusCompleted
	if err := json.Unmarshal([]byte(`null`), &pre); err != nil {
		t.Fatalf("Unmarshal(null): %v", err)
	}
	if pre != StatusCompleted {
		t.Errorf("Unmarshal(null) = %d, want %d (unchanged)", pre, StatusCompleted)
	}
	// Unknown string names are rejected. A non-string, non-number token does
	// reach the integer branch — encoding/json reports it as an
	// UnmarshalTypeError, which is the same error a legacy integer produces —
	// but it has to fail there rather than decode to anything.
	for _, bad := range []string{`"bogus"`, `{}`, `[1]`} {
		var s Status
		if err := json.Unmarshal([]byte(bad), &s); err == nil {
			t.Errorf("Unmarshal(%s) succeeded, want error", bad)
		}
	}
}

func TestStatusRoundTripJSON(t *testing.T) {
	// Both a recognized status and an unrecognized raw code must round-trip.
	// StatusOpen is the zero value, and the field carries no omitempty, so it
	// is emitted as "open" and has to decode back rather than being skipped.
	for _, want := range []Status{StatusOpen, StatusCancelled, StatusCompleted, Status(99)} {
		in := Task{Title: "t", Status: want}
		data, err := json.Marshal(in)
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		var out Task
		if err := json.Unmarshal(data, &out); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		if out.Status != want {
			t.Errorf("round-trip status = %d, want %d", out.Status, want)
		}
	}
}

// The three wire names are a public contract: agents and jq filters match on
// them, so a rename is a breaking change and has to fail here first (issue
// #208).
func TestTaskTypeMarshalJSON(t *testing.T) {
	cases := []struct {
		taskType TaskType
		want     string
	}{
		{TypeTask, `"task"`},
		{TypeProject, `"project"`},
		{TypeHeading, `"heading"`},
		{TaskType(99), `99`}, // unrecognized code preserved as its raw int
	}
	for _, tc := range cases {
		got, err := json.Marshal(tc.taskType)
		if err != nil {
			t.Fatalf("Marshal(%d): %v", tc.taskType, err)
		}
		if string(got) != tc.want {
			t.Errorf("Marshal(%d) = %s, want %s", tc.taskType, got, tc.want)
		}
	}
}

func TestTaskTypeUnmarshalJSON(t *testing.T) {
	cases := []struct {
		in   string
		want TaskType
	}{
		{`"task"`, TypeTask},
		{`"project"`, TypeProject},
		{`"heading"`, TypeHeading},
		{`0`, TypeTask},      // legacy integer input
		{`1`, TypeProject},   // legacy integer input
		{`2`, TypeHeading},   // legacy integer input
		{`99`, TaskType(99)}, // unrecognized raw code taken verbatim
	}
	for _, tc := range cases {
		var tt TaskType
		if err := json.Unmarshal([]byte(tc.in), &tt); err != nil {
			t.Fatalf("Unmarshal(%s): %v", tc.in, err)
		}
		if tt != tc.want {
			t.Errorf("Unmarshal(%s) = %d, want %d", tc.in, tt, tc.want)
		}
	}
	// A JSON null is a no-op: it must leave the existing value untouched rather
	// than silently coercing it to TaskType(0) ("task").
	pre := TypeProject
	if err := json.Unmarshal([]byte(`null`), &pre); err != nil {
		t.Fatalf("Unmarshal(null): %v", err)
	}
	if pre != TypeProject {
		t.Errorf("Unmarshal(null) = %d, want %d (unchanged)", pre, TypeProject)
	}
	// Unknown string names are rejected. A non-string, non-number token does
	// reach the integer branch — encoding/json reports it as an
	// UnmarshalTypeError, which is the same error a legacy integer produces —
	// but it has to fail there rather than decode to anything.
	for _, bad := range []string{`"bogus"`, `{}`, `[1]`} {
		var tt TaskType
		if err := json.Unmarshal([]byte(bad), &tt); err == nil {
			t.Errorf("Unmarshal(%s) succeeded, want error", bad)
		}
	}
}

func TestTaskTypeRoundTripJSON(t *testing.T) {
	// Both a recognized type and an unrecognized raw code must round-trip.
	for _, want := range []TaskType{TypeTask, TypeProject, TypeHeading, TaskType(99)} {
		in := Task{Title: "t", Type: want}
		data, err := json.Marshal(in)
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		var out Task
		if err := json.Unmarshal(data, &out); err != nil {
			t.Fatalf("Unmarshal: %v", err)
		}
		if out.Type != want {
			t.Errorf("round-trip type = %d, want %d", out.Type, want)
		}
	}
}

func TestTaskTypeString(t *testing.T) {
	cases := []struct {
		taskType TaskType
		want     string
	}{
		{TypeTask, "task"},
		{TypeProject, "project"},
		{TypeHeading, "heading"},
		{TaskType(99), "unknown"},
	}
	for _, tc := range cases {
		if got := tc.taskType.String(); got != tc.want {
			t.Errorf("TaskType(%d).String() = %q, want %q", int(tc.taskType), got, tc.want)
		}
	}
}

// TestStatusString was missing while TaskType had one, which is exactly the
// asymmetry issue #215 set out to remove: the two types now share a codec, so
// both sides of it need the same coverage.
func TestStatusString(t *testing.T) {
	cases := []struct {
		status Status
		want   string
	}{
		{StatusOpen, "open"},
		{StatusCancelled, "cancelled"},
		{StatusCompleted, "completed"},
		{Status(99), "unknown"},
	}
	for _, tc := range cases {
		if got := tc.status.String(); got != tc.want {
			t.Errorf("Status(%d).String() = %q, want %q", int(tc.status), got, tc.want)
		}
	}
}

// The three wire names are a public contract the same way `type`'s are: agents
// and jq filters match on them, so a rename is a breaking change and has to
// fail here first (issue #241).
func TestStartMarshalJSON(t *testing.T) {
	cases := []struct {
		start Start
		want  string
	}{
		{StartInbox, `"inbox"`},
		{StartAnytime, `"anytime"`},
		{StartSomeday, `"someday"`},
		{Start(99), `99`}, // unrecognized code preserved as its raw int
	}
	for _, tc := range cases {
		got, err := json.Marshal(tc.start)
		if err != nil {
			t.Fatalf("Marshal(%d): %v", tc.start, err)
		}
		if string(got) != tc.want {
			t.Errorf("Marshal(%d) = %s, want %s", tc.start, got, tc.want)
		}
	}
}

func TestStartUnmarshalJSON(t *testing.T) {
	cases := []struct {
		in   string
		want Start
	}{
		{`"inbox"`, StartInbox},
		{`"anytime"`, StartAnytime},
		{`"someday"`, StartSomeday},
		{`0`, StartInbox},   // legacy integer input
		{`1`, StartAnytime}, // legacy integer input
		{`2`, StartSomeday}, // legacy integer input
		{`99`, Start(99)},   // unrecognized raw code taken verbatim
	}
	for _, tc := range cases {
		var s Start
		if err := json.Unmarshal([]byte(tc.in), &s); err != nil {
			t.Fatalf("Unmarshal(%s): %v", tc.in, err)
		}
		if s != tc.want {
			t.Errorf("Unmarshal(%s) = %d, want %d", tc.in, s, tc.want)
		}
	}
	// A JSON null is a no-op: it must leave the existing value untouched rather
	// than silently coercing it to Start(0) ("inbox"), which would move an item
	// into the Inbox on a partial decode.
	pre := StartSomeday
	if err := json.Unmarshal([]byte(`null`), &pre); err != nil {
		t.Fatalf("Unmarshal(null): %v", err)
	}
	if pre != StartSomeday {
		t.Errorf("Unmarshal(null) = %d, want %d (unchanged)", pre, StartSomeday)
	}
	for _, bad := range []string{`"bogus"`, `{}`, `[1]`} {
		var s Start
		if err := json.Unmarshal([]byte(bad), &s); err == nil {
			t.Errorf("Unmarshal(%s) succeeded, want error", bad)
		}
	}
}

func TestStartRoundTripJSON(t *testing.T) {
	// Both a recognized start and an unrecognized raw code must round-trip, on
	// a project as well as a to-do: the two carry the field under the same name
	// and the same codec (issue #202).
	for _, want := range []Start{StartInbox, StartAnytime, StartSomeday, Start(99)} {
		task := Task{Title: "t", Start: want}
		data, err := json.Marshal(task)
		if err != nil {
			t.Fatalf("Marshal task: %v", err)
		}
		var gotTask Task
		if err := json.Unmarshal(data, &gotTask); err != nil {
			t.Fatalf("Unmarshal task: %v", err)
		}
		if gotTask.Start != want {
			t.Errorf("round-trip task start = %d, want %d", gotTask.Start, want)
		}

		project := Project{Title: "p", Start: want}
		data, err = json.Marshal(project)
		if err != nil {
			t.Fatalf("Marshal project: %v", err)
		}
		var gotProject Project
		if err := json.Unmarshal(data, &gotProject); err != nil {
			t.Fatalf("Unmarshal project: %v", err)
		}
		if gotProject.Start != want {
			t.Errorf("round-trip project start = %d, want %d", gotProject.Start, want)
		}
	}
}

func TestStartString(t *testing.T) {
	cases := []struct {
		start Start
		want  string
	}{
		{StartInbox, "inbox"},
		{StartAnytime, "anytime"},
		{StartSomeday, "someday"},
		{Start(99), "unknown"},
	}
	for _, tc := range cases {
		if got := tc.start.String(); got != tc.want {
			t.Errorf("Start(%d).String() = %q, want %q", int(tc.start), got, tc.want)
		}
	}
}

// Each type has to hand the shared codec its own name. Nothing else notices if
// one of them is wired up with another type's name — every other test only
// asks whether decoding failed, not what it said — so a Start value rejected as
// "Status: unknown value" would pass the whole suite while sending a caller to
// the wrong field (issue #215).
func TestEnumCodecErrorsNameTheirOwnType(t *testing.T) {
	cases := []struct {
		typeName string
		decode   func([]byte) error
	}{
		{"Status", func(b []byte) error { var v Status; return json.Unmarshal(b, &v) }},
		{"TaskType", func(b []byte) error { var v TaskType; return json.Unmarshal(b, &v) }},
		{"Start", func(b []byte) error { var v Start; return json.Unmarshal(b, &v) }},
	}
	for _, tc := range cases {
		t.Run(tc.typeName, func(t *testing.T) {
			// An unknown name, and a token that is neither name nor number, are
			// the two error paths. Both have to carry the prefix.
			for _, bad := range []string{`"bogus"`, `{}`} {
				err := tc.decode([]byte(bad))
				if err == nil {
					t.Fatalf("Unmarshal(%s) succeeded, want error", bad)
				}
				if want := tc.typeName + ": "; !strings.HasPrefix(err.Error(), want) {
					t.Errorf("Unmarshal(%s) error = %q, want prefix %q", bad, err, want)
				}
			}
		})
	}
}
