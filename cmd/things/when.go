package main

import (
	"fmt"
	"time"

	"github.com/ryanlewis/things-cli/internal/db"
	"github.com/ryanlewis/things-cli/internal/model"
	"github.com/ryanlewis/things-cli/internal/things"
)

// Reminder values for whenPlace.reminder besides a time of day.
const (
	reminderNone = -1 // the value clears the reminder
	reminderKept = -2 // the value leaves the reminder as it is
)

// whenPlace is where Things files an item a --when value is sent for: the
// start and start date the views classify it by, the part of the day, and
// what becomes of its reminder. Measured in Things 3 on 7 Oct 2026 with
// things:///add, add-project and update, on to-dos and projects alike:
//
//   - anytime, or an empty value, files it in Anytime and someday in
//     Someday, both with no start date.
//   - today, evening, today's date and a date already past file it under
//     today, clearing any reminder. evening is the evening part of the day;
//     today and a past date are the rest of it, and today's date leaves the
//     part as it was.
//   - tomorrow, and a later date, schedule it for that day and keep the
//     reminder.
//   - A time sets the reminder: today when the time is still to come,
//     tomorrow when it has passed. A date and time sets it on that date, or
//     today when the date is past. Either files it in the day part, not the
//     evening, when the day is today.
type whenPlace struct {
	start model.Start // the start of an item with no start date
	day   model.ThingsDate
	// bucket is the item's part of the day, -1 when any part fits.
	bucket   int
	reminder int // minutes after midnight, reminderNone or reminderKept
}

// parseClock reads the HH:MM (or H:MM) time --when sends verbatim, in
// minutes after midnight.
func parseClock(s string) (int, bool) {
	t, err := time.Parse("15:04", s)
	if err != nil {
		return 0, false
	}
	return t.Hour()*60 + t.Minute(), true
}

// placeWhen returns where value files an item when Things reads it at now. ok
// is false for a value whose outcome is not worked out here: an English
// phrase, a time other than HH:MM, a time within a minute of now, which lands
// today or tomorrow depending on when Things reads it, or a time within an
// hour of a daylight-saving change, where how Things reads the wall clock was
// not measured.
func placeWhen(value string, now time.Time) (place whenPlace, ok bool) {
	v, err := things.NormalizeWhen(value)
	if err != nil {
		return whenPlace{}, false
	}
	today := model.ThingsDateFromTime(now)
	switch v {
	case "", "anytime":
		return whenPlace{start: model.StartAnytime, reminder: reminderKept}, true
	case "someday":
		return whenPlace{start: model.StartSomeday, reminder: reminderKept}, true
	case "today":
		return whenPlace{day: today, bucket: 0, reminder: reminderNone}, true
	case "evening":
		return whenPlace{day: today, bucket: 1, reminder: reminderNone}, true
	case "tomorrow":
		return whenPlace{day: model.ThingsDateFromTime(now.AddDate(0, 0, 1)), bucket: -1, reminder: reminderKept}, true
	}
	if clock, ok := parseClock(v); ok {
		if nearOffsetChange(now) {
			return whenPlace{}, false
		}
		at := time.Date(now.Year(), now.Month(), now.Day(), clock/60, clock%60, 0, 0, time.Local)
		switch {
		case at.Sub(now) > time.Minute:
			return whenPlace{day: today, bucket: 0, reminder: clock}, true
		case now.Sub(at) > time.Minute:
			return whenPlace{day: model.ThingsDateFromTime(now.AddDate(0, 0, 1)), bucket: -1, reminder: clock}, true
		}
		return whenPlace{}, false
	}
	datePart, clockPart, timed := v, "", false
	if len(v) == len("2006-01-02@15:04") && v[10] == '@' {
		datePart, clockPart, timed = v[:10], v[11:], true
	}
	date, err := time.ParseInLocation("2006-01-02", datePart, time.Local)
	if err != nil {
		return whenPlace{}, false
	}
	day := model.ThingsDateFromTime(date)
	if timed {
		clock, ok := parseClock(clockPart)
		if !ok {
			return whenPlace{}, false
		}
		if day <= today {
			return whenPlace{day: today, bucket: 0, reminder: clock}, true
		}
		return whenPlace{day: day, bucket: -1, reminder: clock}, true
	}
	switch {
	case day < today:
		return whenPlace{day: today, bucket: 0, reminder: reminderNone}, true
	case day == today:
		return whenPlace{day: today, bucket: -1, reminder: reminderNone}, true
	}
	return whenPlace{day: day, bucket: -1, reminder: reminderKept}, true
}

// nearOffsetChange reports whether the local UTC offset an hour before now
// differs from the one an hour after: a daylight-saving change within the
// hour either side.
func nearOffsetChange(now time.Time) bool {
	_, before := now.Add(-time.Hour).In(time.Local).Zone()
	_, after := now.Add(time.Hour).In(time.Local).Zone()
	return before != after
}

// holds reports whether t is filed where p says, by its start, start date and
// part of the day. A row Things has not moved into today yet reads as Anytime
// (db's shownStart), so a dated place is checked by its date alone.
func (p whenPlace) holds(t *model.Task) bool {
	if p.day == 0 {
		return t.StartDate == nil && t.Start == p.start
	}
	if t.StartDate == nil || *t.StartDate != p.day {
		return false
	}
	return p.bucket < 0 || t.StartBucket == p.bucket
}

// whenUnchanged reports whether value leaves the item where it is, so Things
// records no change for it (see whenPlace). reminder reads the item's
// reminder in minutes after midnight, reminderNone for none; it is asked only
// when the value sets or clears one.
func whenUnchanged(value string, task *model.Task, now time.Time, reminder func() (int, error)) bool {
	p, ok := placeWhen(value, now)
	if !ok || !p.holds(task) {
		return false
	}
	if p.reminder == reminderKept {
		return true
	}
	r, err := reminder()
	return err == nil && r == p.reminder
}

// readReminder reads the item's reminder for whenUnchanged.
func readReminder(database *db.DB, uuid string) func() (int, error) {
	return func() (int, error) {
		raw, ok, err := database.ReminderTime(uuid)
		if err != nil || !ok {
			return reminderNone, err
		}
		hour, minute := model.ReminderClock(raw)
		return hour*60 + minute, nil
	}
}

// whenCheck is the --when part of a read-back: the value sent and when it was
// sent.
type whenCheck struct {
	value string
	sent  time.Time
}

// holds reports whether t, read at now, is filed where the value puts it. The
// value is read against the day it was sent and the day of the read, so a
// write that crosses midnight is judged by either. A caller passes one now
// for everything it judges together, so the verdict cannot flip between
// checks. A value whose place is not worked out when it was sent holds: a
// time within a minute of the send lands by when Things read it, which a
// later read-back cannot tell.
func (c *whenCheck) holds(t *model.Task, now time.Time) bool {
	if c == nil {
		return true
	}
	p, ok := placeWhen(c.value, c.sent)
	if !ok || p.holds(t) {
		return true
	}
	p, ok = placeWhen(c.value, now)
	return ok && p.holds(t)
}

// misfiledError is an add or edit that Things applied but filed somewhere
// other than --when put it. The item exists, so the JSON error carries its
// uuid and where it landed for a caller to act on.
type misfiledError struct {
	msg    string
	kind   string // "task" or "project"
	title  string
	uuid   string
	landed string
}

func (e *misfiledError) Error() string { return e.msg }

// kindWord is the error payload's word for an item of type typ.
func kindWord(typ model.TaskType) string {
	if typ == model.TypeProject {
		return "project"
	}
	return "task"
}

// describeStart says where t is filed, for an error about a --when that did
// not land.
func describeStart(t *model.Task) string {
	switch {
	case t.StartDate != nil && t.StartBucket == 1:
		return "scheduled for the evening of " + t.StartDate.String()
	case t.StartDate != nil:
		return "scheduled for " + t.StartDate.String()
	}
	return fmt.Sprintf("in %s with no start date", t.Start)
}
