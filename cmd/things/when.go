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
// (db's shownStart), so a dated place is checked by its date alone; what
// Things does with such a row is unmovedKeeps.
func (p whenPlace) holds(t *model.Task) bool {
	if p.day == 0 {
		return t.StartDate == nil && t.Start == p.start
	}
	if t.StartDate == nil || *t.StartDate != p.day {
		return false
	}
	return p.bucket < 0 || t.StartBucket == p.bucket
}

// whenReads reads what whenUnchanged needs beyond the item as listed, each
// only when asked. reminder reads the item's reminder in minutes after
// midnight, reminderNone for none. stored reads the start the row is stored
// with, which db's shownStart hides for a row not yet moved into today.
type whenReads struct {
	reminder func() (int, error)
	stored   func() (model.Start, error)
}

// readWhen reads the item's reminder and stored start from the database.
func readWhen(database *db.DB, uuid string) whenReads {
	return whenReads{
		reminder: func() (int, error) {
			raw, ok, err := database.ReminderTime(uuid)
			if err != nil || !ok {
				return reminderNone, err
			}
			hour, minute := model.ReminderClock(raw)
			return hour*60 + minute, nil
		},
		stored: func() (model.Start, error) { return database.StoredStart(uuid) },
	}
}

// whenUnchanged reports whether value leaves the item where it is, so Things
// records no change for it (see whenPlace). The reminder is read only when
// the value sets or clears one.
func whenUnchanged(value string, task *model.Task, now time.Time, reads whenReads) bool {
	p, ok := placeWhen(value, now)
	if !ok {
		return false
	}
	if unmovedKeeps(p, now, task) && unmoved(task, now, reads.stored) {
		return true
	}
	if !p.holds(task) {
		return false
	}
	if p.reminder == reminderKept {
		return true
	}
	r, err := reads.reminder()
	return err == nil && r == p.reminder
}

// unmoved reports whether task is a row Things has not moved into today yet:
// one db's shownStart reports as Anytime while it is stored as Someday. That
// rewrite already limits it to open, untrashed rows outside a repeating
// template that are dated today or earlier; the date is checked against now
// as well, in case the day turned since the row was read. A start that cannot
// be read counts as moved, which leaves the stricter check in place.
func unmoved(task *model.Task, now time.Time, stored func() (model.Start, error)) bool {
	if task.Start != model.StartAnytime || task.StartDate == nil || *task.StartDate > model.ThingsDateFromTime(now) {
		return false
	}
	s, err := stored()
	return err == nil && s == model.StartSomeday
}

// unmovedKeeps reports whether p, a value read at now, leaves an unmoved row
// (see unmoved) such as task where it is. Measured on 8 Oct 2026 at 00:00 on
// to-dos dated that day, in the day part, with no reminder: today and today's
// date changed nothing, and evening moved the row. The rest is unmeasured and
// chosen so a correct write never reports failure:
//
//   - with a reminder, today and today's date are taken to leave it, rather
//     than clear it as they do on a moved row;
//   - a row dated before today, which Things has not moved for days, is
//     taken to stay on its date for today, today's date or a past date;
//   - a project is taken to behave as a to-do;
//   - a row already in the evening part is taken to stay there for today and
//     today's date, and for evening.
//
// A time sets a reminder, so it is not one of these.
func unmovedKeeps(p whenPlace, now time.Time, task *model.Task) bool {
	if p.reminder != reminderNone || p.day != model.ThingsDateFromTime(now) {
		return false
	}
	return p.bucket != 1 || task.StartBucket == 1
}

// whenCheck is the --when part of a read-back: the value sent and when it was
// sent. before is the item as read before the write when it was a row Things
// had not moved into today yet (unmoved), nil otherwise.
type whenCheck struct {
	value  string
	sent   time.Time
	before *model.Task
}

// holds reports whether t, read at now, is filed where the value puts it. The
// value is read against the day it was sent and the day of the read, so a
// write that crosses midnight is judged by either. A caller passes one now
// for everything it judges together, so the verdict cannot flip between
// checks. A value whose place is not worked out when it was sent holds: a
// time within a minute of the send lands by when Things read it, which a
// later read-back cannot tell.
//
// For an unmoved row, a value unmovedKeeps says leaves it where it is also
// holds when the row is still where it was before the write.
func (c *whenCheck) holds(t *model.Task, now time.Time) bool {
	if c == nil {
		return true
	}
	p, ok := placeWhen(c.value, c.sent)
	if !ok || c.landed(p, c.sent, t) {
		return true
	}
	p, ok = placeWhen(c.value, now)
	return ok && c.landed(p, now, t)
}

// landed reports whether t is where p, read at now, puts it.
func (c *whenCheck) landed(p whenPlace, now time.Time, t *model.Task) bool {
	if p.holds(t) {
		return true
	}
	b := c.before
	return b != nil && unmovedKeeps(p, now, b) && t.StartDate != nil && b.StartDate != nil &&
		*t.StartDate == *b.StartDate && t.StartBucket == b.StartBucket
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
