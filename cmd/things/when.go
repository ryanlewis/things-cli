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

// whenPhrase reports whether value goes to Things as a free phrase: one that
// is none of the keywords or weekday names, a time, a date or a date and
// time (things.KnownWhenWord, things.WhenDateOrTime), so
// Things reads it with its own English parser, or ignores it. A phrase
// Things understands gives the item a start date; one it ignores leaves the
// item where it would be with no --when.
func whenPhrase(value string) bool {
	v, err := things.NormalizeWhen(value)
	if err != nil || v == "" {
		return false
	}
	return !things.KnownWhenWord(v) && !things.WhenDateOrTime(v)
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
// part of the day, with p read on the day today. A row Things has not moved
// into today yet reads as Anytime (db's shownStart), so a dated place is
// checked by its date alone; what Things does with such a row is
// unmovedKeeps. A row in Today from an earlier day (carriedOver) is filed
// today.
func (p whenPlace) holds(t *model.Task, today model.ThingsDate) bool {
	if p.day == 0 {
		return t.StartDate == nil && t.Start == p.start
	}
	if t.StartDate == nil {
		return false
	}
	day := *t.StartDate
	if p.day == today && carriedOver(t, today) {
		day = today
	}
	if day != p.day {
		return false
	}
	return p.bucket < 0 || t.StartBucket == p.bucket
}

// carriedOver reports whether t is in Today from a day before today: an
// open, untrashed Anytime row dated earlier, which db's todayScheduled lists
// under today. An item in Today from an earlier day keeps that day as its
// start date; Things does not move it forward. Daily backups from 6 to 9 Oct
// 2026 held one such to-do at start 1 dated 5 Oct throughout, and the live
// database held 19 open ones dated 5 to 7 Oct. A row Things has not moved
// into today yet (unmoved) reads as Anytime too, and is in Today the same
// way. A closed or trashed row is not in Today however it is dated. What
// Things does with --when on a carried-over row is carriedMeasured and
// unmovedKeeps.
func carriedOver(t *model.Task, today model.ThingsDate) bool {
	return t.Status == model.StatusOpen && !t.Trashed &&
		t.Start == model.StartAnytime && t.StartDate != nil && *t.StartDate < today
}

// carriedMeasured reports whether v, a --when value normalised by
// things.NormalizeWhen, on task, a carriedOver row, is the case measured on 9
// Oct 2026: a to-do in the day part, sent today, which Things left as it
// was. On such a row with no reminder that is a certain no-op. The same day,
// today's date on such a row with a reminder cleared the reminder and kept
// the earlier date; without one it was not measured, so it is sent and read
// back like the other cases (unmovedKeeps).
func carriedMeasured(v string, task *model.Task) bool {
	return v == "today" && task.Type == model.TypeTask && task.StartBucket == 0
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
// the value sets or clears one. On a row Things has not moved into today yet
// (unmoved), or one in Today from an earlier day (carriedOver), only the case
// measured is certain; the others are sent and read back (unmovedKeeps). So
// is a row of either shape whose stored start cannot be read.
func whenUnchanged(value string, task *model.Task, now time.Time, reads whenReads) bool {
	p, ok := placeWhen(value, now)
	if !ok {
		return false
	}
	v, _ := things.NormalizeWhen(value) // placeWhen has accepted it
	switch heldIn(task, now, reads.stored) {
	case heldUnmoved:
		if !unmovedMeasured(v, task, now) {
			return false
		}
	case heldCarried:
		if !carriedMeasured(v, task) {
			return false
		}
	case heldUnknown:
		return false
	}
	if !p.holds(task, model.ThingsDateFromTime(now)) {
		return false
	}
	if p.reminder == reminderKept {
		return true
	}
	r, err := reads.reminder()
	return err == nil && r == p.reminder
}

// held is which kind of row in Today by its start date heldIn finds: one
// whose --when outcome was measured only in part, so the rest is read back.
type held int

const (
	heldNone    held = iota // any other row
	heldUnmoved             // a row Things has not moved into today yet
	heldCarried             // a row in Today from an earlier day (carriedOver)
	heldUnknown             // a row dated before today whose stored start cannot be read
)

// heldIn reports which kind of row task is, read at now.
//
// An unmoved row is one db's shownStart reports as Anytime while it is stored
// as Someday. That rewrite already limits it to open, untrashed rows outside
// a repeating template that are dated today or earlier; the date is checked
// against now as well, in case the day turned since the row was read. A
// carried-over row is stored as Anytime and dated before today. Both report
// Anytime, so only the stored start tells them apart. It is read last, after
// the checks that need no query, and only for a row that passes them. A row
// dated today whose start cannot be read counts as moved, which leaves the
// stricter check in place; one dated earlier is heldUnknown, which is never
// a certain no-op.
func heldIn(task *model.Task, now time.Time, stored func() (model.Start, error)) held {
	today := model.ThingsDateFromTime(now)
	if task.Start != model.StartAnytime || task.StartDate == nil || *task.StartDate > today {
		return heldNone
	}
	carried := carriedOver(task, today)
	if !carried && *task.StartDate != today {
		return heldNone // closed or trashed, so in no list by its start date
	}
	s, err := stored()
	switch {
	case err == nil && s == model.StartSomeday:
		return heldUnmoved
	case !carried:
		return heldNone
	case err != nil:
		return heldUnknown
	}
	return heldCarried
}

// unmovedMeasured reports whether v, a --when value normalised by
// things.NormalizeWhen, on task, an unmoved row, is the case measured on 8
// Oct 2026 at 00:00: a to-do dated today, in the day part, sent today or
// today's date, which Things left as it was. On such a row with no reminder
// that is a certain no-op. evening moved the same row.
func unmovedMeasured(v string, task *model.Task, now time.Time) bool {
	if v != "today" && v != now.Format("2006-01-02") {
		return false
	}
	return task.Type == model.TypeTask && task.StartDate != nil &&
		*task.StartDate == model.ThingsDateFromTime(now) && task.StartBucket == 0
}

// unmovedKeeps reports whether p, a value read at now, may leave an unmoved
// row (see heldIn) such as task where it is, or a carriedOver row, which is
// read back the same way outside carriedMeasured. Outside unmovedMeasured these
// are unmeasured: a row with a reminder, a row dated before today that Things
// has not moved for days, a project, and a row in the evening part, sent
// today, today's date, a past date, or evening on an evening row. Such an
// edit is not a certain no-op, since Things may move the row and the item
// printed would be stale, so it is sent and read back, and the read-back
// accepts either outcome: the row where the value puts it, or the row where
// it was (whenCheck). Chosen so a correct write never reports failure; the
// cost is the read-back budget when Things leaves the row. A time sets a
// reminder, so it is not one of these.
func unmovedKeeps(p whenPlace, now time.Time, task *model.Task) bool {
	if p.reminder != reminderNone || p.day != model.ThingsDateFromTime(now) {
		return false
	}
	return p.bucket != 1 || task.StartBucket == 1
}

// whenCheck is the --when part of a read-back: the value sent and when it was
// sent. before is the item as read before the write when it was a row Things
// had not moved into today yet or one in Today from an earlier day (heldIn),
// nil otherwise; carried says it was the second. whenOnly says the edit
// changes nothing but --when, so such a row Things left as it was, with
// nothing recorded, is the edit applied (unmovedKeeps).
type whenCheck struct {
	value    string
	sent     time.Time
	before   *model.Task
	carried  bool
	whenOnly bool
	// phraseOnly says the edit changes nothing but --when, and its value is
	// a free phrase (whenPhrase). Things records nothing for a phrase it
	// ignores, so the read-back could only wait out its budget: the edit is
	// reported unconfirmed at once instead.
	phraseOnly bool
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
	return p.holds(t, model.ThingsDateFromTime(now)) || c.kept(p, now, t)
}

// kept reports whether t, an unmoved or carried-over row before the write, is
// still where it was, for a value p, read at now, that may leave it there
// (unmovedKeeps). Every such value clears a reminder, and on a carried-over
// row Things was measured clearing it, so one that still has its reminder
// was not given the write: it is not kept.
func (c *whenCheck) kept(p whenPlace, now time.Time, t *model.Task) bool {
	b := c.before
	if b == nil || !unmovedKeeps(p, now, b) || c.carried && t.ReminderTime != nil {
		return false
	}
	return t.StartDate != nil && b.StartDate != nil &&
		*t.StartDate == *b.StartDate && t.StartBucket == b.StartBucket
}

// keptQuietly reports whether a --when-only edit of an unmoved row ended with
// the row where it was and nothing recorded, which the read-back accepts as
// applied (unmovedKeeps).
func (c *whenCheck) keptQuietly(t *model.Task, now time.Time) bool {
	if c == nil || !c.whenOnly {
		return false
	}
	for _, at := range []time.Time{c.sent, now} {
		if p, ok := placeWhen(c.value, at); ok && c.kept(p, at, t) {
			return true
		}
	}
	return false
}

// whenVerdict is the read-back's verdict on a --when or an import's when,
// for a new item: whether Things filed the item where the value puts it.
type whenVerdict int

const (
	// whenFiled is an item filed where the value puts it, or one sent
	// with no value.
	whenFiled whenVerdict = iota
	// whenNotFiled is an item filed somewhere else.
	whenNotFiled
	// whenNotUnderstood is an item sent a free phrase (whenPhrase) that
	// has no start date: Things did not understand the phrase.
	whenNotUnderstood
)

// verdict judges t, a new item read at now, against c. add and import both
// judge a new item by it, so they cannot drift apart. A nil c is no --when,
// which every item is filed by.
func (c *whenCheck) verdict(t *model.Task, now time.Time) whenVerdict {
	switch {
	case !c.holds(t, now):
		return whenNotFiled
	case c != nil && whenPhrase(c.value) && t.StartDate == nil:
		return whenNotUnderstood
	}
	return whenFiled
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

// newMisfiled is the misfiledError for an item of type typ titled title,
// with uuid, that Things filed at landed; msg is the whole message.
func newMisfiled(typ model.TaskType, title, uuid, landed, msg string) *misfiledError {
	return &misfiledError{msg: msg, kind: typ.String(), title: title, uuid: uuid, landed: landed}
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
