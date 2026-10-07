package main

import (
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ryanlewis/things-cli/internal/model"
)

// placeWhen gives where Things 3 filed throwaway items for each --when form
// on 7 Oct 2026, read at 15:00 on that day.
func TestPlaceWhen(t *testing.T) {
	now := time.Date(2026, 10, 7, 15, 0, 0, 0, time.Local)
	day := func(d int) model.ThingsDate {
		return model.ThingsDateFromTime(time.Date(2026, 10, d, 0, 0, 0, 0, time.Local))
	}
	cases := []struct {
		value string
		want  whenPlace
		ok    bool
	}{
		{"", whenPlace{start: model.StartAnytime, reminder: reminderKept}, true},
		{"Anytime", whenPlace{start: model.StartAnytime, reminder: reminderKept}, true},
		{"someday", whenPlace{start: model.StartSomeday, reminder: reminderKept}, true},
		{"today", whenPlace{day: day(7), bucket: 0, reminder: reminderNone}, true},
		{"evening", whenPlace{day: day(7), bucket: 1, reminder: reminderNone}, true},
		{"tomorrow", whenPlace{day: day(8), bucket: -1, reminder: reminderKept}, true},
		{"2026-10-07", whenPlace{day: day(7), bucket: -1, reminder: reminderNone}, true},
		{"2026-10-05", whenPlace{day: day(7), bucket: 0, reminder: reminderNone}, true},
		{"2026-10-09", whenPlace{day: day(9), bucket: -1, reminder: reminderKept}, true},
		{"18:30", whenPlace{day: day(7), bucket: 0, reminder: 18*60 + 30}, true},
		{"8:00", whenPlace{day: day(8), bucket: -1, reminder: 8 * 60}, true},
		{"15:00", whenPlace{}, false},
		{"15:01", whenPlace{}, false},
		{"2026-10-07@08:00", whenPlace{day: day(7), bucket: 0, reminder: 8 * 60}, true},
		{"2026-10-05@10:00", whenPlace{day: day(7), bucket: 0, reminder: 10 * 60}, true},
		{"2026-10-09@10:00", whenPlace{day: day(9), bucket: -1, reminder: 10 * 60}, true},
		{"2026-10-09T10:00:00+01:00", whenPlace{day: day(9), bucket: -1, reminder: 10 * 60}, true},
		{"next friday", whenPlace{}, false},
		{"friday", whenPlace{}, false},
		{"6pm", whenPlace{}, false},
	}
	for _, tc := range cases {
		got, ok := placeWhen(tc.value, now)
		if ok != tc.ok || ok && got != tc.want {
			t.Errorf("placeWhen(%q) = %+v, %v, want %+v, %v", tc.value, got, ok, tc.want, tc.ok)
		}
	}
}

// A --when time the item already has as its reminder, on the day it lands, is
// no change: Things records none for it. Any other time, or a reminder that
// cannot be read, is a change.
func TestWhenUnchangedTime(t *testing.T) {
	now := time.Date(2026, 10, 7, 15, 0, 0, 0, time.Local)
	today := model.ThingsDateFromTime(now)
	tomorrow := model.ThingsDateFromTime(now.AddDate(0, 0, 1))
	reminder := func(min int, err error) func() (int, error) { return func() (int, error) { return min, err } }
	cases := []struct {
		name     string
		value    string
		day      model.ThingsDate
		bucket   int
		reminder func() (int, error)
		want     bool
	}{
		{"same time today", "18:30", today, 0, reminder(18*60+30, nil), true},
		{"same time evening", "18:30", today, 1, reminder(18*60+30, nil), false},
		{"other time", "18:45", today, 0, reminder(18*60+30, nil), false},
		{"no reminder", "18:30", today, 0, reminder(reminderNone, nil), false},
		{"unreadable", "18:30", today, 0, reminder(0, errors.New("busy")), false},
		{"passed time tomorrow", "08:00", tomorrow, 0, reminder(8*60, nil), true},
		{"passed time today", "08:00", today, 0, reminder(8*60, nil), false},
		{"date and time", tomorrow.String() + "@08:00", tomorrow, 0, reminder(8*60, nil), true},
		{"now", "15:00", today, 0, reminder(15*60, nil), false},
	}
	for _, tc := range cases {
		day := tc.day
		task := &model.Task{Start: model.StartAnytime, StartDate: &day, StartBucket: tc.bucket}
		if got := whenUnchanged(tc.value, task, now, tc.reminder); got != tc.want {
			t.Errorf("%s: whenUnchanged(%q) = %v, want %v", tc.name, tc.value, got, tc.want)
		}
	}
}

// A write sent before midnight and read after it may be filed by either day,
// so the read-back accepts both. A value with no worked-out place holds.
func TestWhenCheckAcrossMidnight(t *testing.T) {
	yesterday := time.Now().AddDate(0, 0, -1)
	today := model.ThingsDateFromTime(time.Now())
	before := model.ThingsDateFromTime(yesterday)
	for _, tc := range []struct {
		value string
		day   model.ThingsDate
		want  bool
	}{
		{"today", today, true},
		{"today", before, true},
		{"today", today + 128, false},
		{"next friday", before, true},
	} {
		day := tc.day
		c := &whenCheck{value: tc.value, sent: yesterday}
		if got := c.holds(&model.Task{Start: model.StartAnytime, StartDate: &day}); got != tc.want {
			t.Errorf("holds(%q on %s) = %v, want %v", tc.value, day, got, tc.want)
		}
	}
	// A time sent within a minute of itself lands by when Things read it,
	// so a read-back long after cannot judge it and lets it hold.
	sent := time.Now().Add(-2 * time.Hour)
	c := &whenCheck{value: sent.Format("15:04"), sent: sent}
	if !c.holds(&model.Task{Start: model.StartAnytime, StartDate: &today}) {
		t.Error("a time ambiguous when sent must hold")
	}
	if !(*whenCheck)(nil).holds(&model.Task{}) {
		t.Error("a nil whenCheck must hold")
	}
}

// An edit whose --when Things files somewhere else, or ignores while
// recording the rest of the edit, fails the read-back rather than confirming
// on the modification date alone.
func TestEditReadBackChecksWhen(t *testing.T) {
	today := strconv.Itoa(int(model.ThingsDateFromTime(time.Now())))
	for _, tc := range []struct {
		name  string
		apply string
		ok    bool
	}{
		{"filed", `UPDATE TMTask SET start = 1, startDate = ` + today + `, startBucket = 0 WHERE uuid = 'one-1'`, true},
		{"evening instead", `UPDATE TMTask SET start = 1, startDate = ` + today + `, startBucket = 1 WHERE uuid = 'one-1'`, false},
		{"ignored", `UPDATE TMTask SET title = 'Post parcel' WHERE uuid = 'one-1'`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fastVerify(t)
			database, sqlDB := seedWritable(t)
			stubExecEditing(t, sqlDB, tc.apply)

			_, err := runOut(t, database, "edit", "one-1", "--when", "today", "--title", "Post parcel")
			switch {
			case tc.ok && err != nil:
				t.Fatalf("edit: %v", err)
			case !tc.ok && (err == nil || !strings.Contains(err.Error(), `--when "today" did not file it there`)):
				t.Fatalf("edit = %v, want a --when read-back failure", err)
			}
		})
	}
}

// An add whose --when Things files somewhere else reports the item it made,
// so the caller does not add it again.
func TestAddReadBackChecksWhen(t *testing.T) {
	for _, tc := range []struct {
		name  string
		extra string
		ok    bool
	}{
		{"filed", "start = 2", true},
		{"anytime instead", "start = 1", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fastVerify(t)
			database, sqlDB := seedWritable(t)
			stubExecAdding(t, sqlDB, createdRow{uuid: "new-1", title: "Buy oat milk", typ: model.TypeTask, extra: tc.extra})

			_, err := runOut(t, database, "add", "Buy oat milk", "--when", "someday")
			switch {
			case tc.ok && err != nil:
				t.Fatalf("add: %v", err)
			case !tc.ok && (err == nil || !strings.Contains(err.Error(), `created "Buy oat milk" (new-1)`) || !strings.Contains(err.Error(), "Do not add it again")):
				t.Fatalf("add = %v, want a --when read-back failure naming new-1", err)
			}
		})
	}
}
