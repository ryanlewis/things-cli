package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ryanlewis/things-cli/internal/model"
	"github.com/ryanlewis/things-cli/internal/things"
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
		if got := whenUnchanged(tc.value, task, now, whenReads{reminder: tc.reminder, stored: func() (model.Start, error) { return model.StartAnytime, nil }}); got != tc.want {
			t.Errorf("%s: whenUnchanged(%q) = %v, want %v", tc.name, tc.value, got, tc.want)
		}
	}
}

// A write sent before midnight and read after it may be filed by either day,
// so the read-back accepts both. A value with no worked-out place holds.
func TestWhenCheckAcrossMidnight(t *testing.T) {
	yesterday := testNow.AddDate(0, 0, -1)
	today := model.ThingsDateFromTime(testNow)
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
		if got := c.holds(&model.Task{Start: model.StartAnytime, StartDate: &day}, testNow); got != tc.want {
			t.Errorf("holds(%q on %s) = %v, want %v", tc.value, day, got, tc.want)
		}
	}
	// A time sent within a minute of itself lands by when Things read it,
	// so a read-back long after cannot judge it and lets it hold.
	sent := testNow.Add(-2 * time.Hour)
	c := &whenCheck{value: sent.Format("15:04"), sent: sent}
	if !c.holds(&model.Task{Start: model.StartAnytime, StartDate: &today}, testNow) {
		t.Error("a time ambiguous when sent must hold")
	}
	if !(*whenCheck)(nil).holds(&model.Task{}, testNow) {
		t.Error("a nil whenCheck must hold")
	}
}

// An edit whose --when Things files somewhere else, or ignores while
// recording the rest of the edit, fails the read-back rather than confirming
// on the modification date alone.
func TestEditReadBackChecksWhen(t *testing.T) {
	today := strconv.Itoa(int(model.ThingsDateFromTime(testNow)))
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
			case !tc.ok && (err == nil || !strings.Contains(err.Error(), `"Buy oat milk" (new-1)`) || !strings.Contains(err.Error(), "before adding it again")):
				t.Fatalf("add = %v, want a --when read-back failure naming new-1", err)
			}
		})
	}
}

// Under --json a misfiled add or edit fails with one JSON object on stdout:
// the error, with the "misfiled" token, the item's uuid and where it landed.
// The command prints nothing of its own before it.
func TestMisfiledJSONIsOneObject(t *testing.T) {
	today := strconv.Itoa(int(model.ThingsDateFromTime(testNow)))
	cases := []struct {
		name string
		args []string
		kind string
		uuid string
		stub func(t *testing.T, sqlDB *sql.DB)
	}{
		{"add", []string{"--json", "add", "Buy oat milk", "--when", "someday"}, "task", "new-1", func(t *testing.T, sqlDB *sql.DB) {
			stubExecAdding(t, sqlDB, createdRow{uuid: "new-1", title: "Buy oat milk", typ: model.TypeTask, extra: "start = 1"})
		}},
		{"projectAdd", []string{"--json", "project", "add", "Launch", "--when", "someday"}, "project", "new-p", func(t *testing.T, sqlDB *sql.DB) {
			stubExecAdding(t, sqlDB, createdRow{uuid: "new-p", title: "Launch", typ: model.TypeProject, extra: "start = 1"})
		}},
		{"edit", []string{"--json", "edit", "one-1", "--when", "evening", "--title", "Post parcel"}, "task", "one-1", func(t *testing.T, sqlDB *sql.DB) {
			stubExecEditing(t, sqlDB, `UPDATE TMTask SET title = 'Post parcel', start = 1, startDate = `+today+`, startBucket = 0 WHERE uuid = 'one-1'`)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fastVerify(t)
			database, sqlDB := seedWritable(t)
			tc.stub(t, sqlDB)

			out, err := runOut(t, database, tc.args...)
			if err == nil {
				t.Fatal("want a misfiled error")
			}
			var stdout, stderr bytes.Buffer
			stdout.WriteString(out)
			renderError(&stdout, &stderr, true, err)
			var payload jsonErrorPayload
			dec := json.NewDecoder(&stdout)
			if jerr := dec.Decode(&payload); jerr != nil {
				t.Fatalf("stdout is not JSON (%v): %q", jerr, out)
			}
			if dec.More() {
				t.Fatalf("stdout holds more than one JSON value: %q", out+stdout.String())
			}
			if payload.Error != "misfiled" || payload.Kind != tc.kind || payload.UUID != tc.uuid || payload.Landed == "" {
				t.Errorf("payload = %+v, want misfiled %s %s with where it landed", payload, tc.kind, tc.uuid)
			}
		})
	}
}

// The misfiled add's advice searches before a retry, keeps the global flags,
// and moves a project with `project edit`.
func TestMisfiledAddHint(t *testing.T) {
	fastVerify(t)
	database, sqlDB := seedWritable(t)
	stubExecAdding(t, sqlDB, createdRow{uuid: "new-p", title: "Launch", typ: model.TypeProject, extra: "start = 1"})
	d := &Deps{DB: database, DBPath: "/tmp/x.sqlite", Stdout: io.Discard, Stderr: io.Discard}

	err := applyAdd(d, model.TypeProject, "Launch", createdDest{}, "someday", func() error {
		return things.AddProject(things.AddProjectParams{Title: "Launch", When: "someday"})
	})
	if err == nil {
		t.Fatal("want a misfiled error")
	}
	for _, want := range []string{"things --db /tmp/x.sqlite search Launch", "things --db /tmp/x.sqlite project edit new-p --when", "before adding it again"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not say %q", err, want)
		}
	}
}

// Within an hour of a daylight-saving change a time is not placed: how
// Things reads the wall clock then was not measured.
func TestPlaceWhenNearOffsetChange(t *testing.T) {
	london, err := time.LoadLocation("Europe/London")
	if err != nil {
		t.Skipf("no zone data: %v", err)
	}
	prev := time.Local
	time.Local = london
	t.Cleanup(func() { time.Local = prev })

	// British Summer Time ends at 01:00 UTC on 25 Oct 2026.
	change := time.Date(2026, 10, 25, 1, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		now  time.Time
		want bool
	}{
		{change.Add(-30 * time.Minute), false},
		{change.Add(50 * time.Minute), false},
		{change.Add(-3 * time.Hour), true},
		{change.Add(3 * time.Hour), true},
	} {
		if _, ok := placeWhen("12:30", tc.now); ok != tc.want {
			t.Errorf("placeWhen(12:30) at %s: ok = %v, want %v", tc.now, ok, tc.want)
		}
	}
	if _, ok := placeWhen("today", change); !ok {
		t.Error("a keyword is placed whatever the clock")
	}
}

// On a row Things has not moved into today yet, the cases not measured
// (unmovedKeeps) are sent and read back, and the read-back accepts either
// outcome: Things moving the row where --when puts it, or leaving it with
// nothing recorded, once the budget runs out. The edit never prints the row
// before Things has had the write.
func TestEditUnmovedRowAcceptsEitherOutcome(t *testing.T) {
	now := testNow
	today := int(model.ThingsDateFromTime(now))
	old := int(model.ThingsDateFromTime(now.AddDate(0, 0, -3)))
	past := now.AddDate(0, 0, -1).Format("2006-01-02")
	type row struct {
		typ      model.TaskType
		date     int
		bucket   int
		reminder any
	}
	rows := map[string]row{
		"reminder": {model.TypeTask, today, 0, 1207959552},
		"old":      {model.TypeTask, old, 0, nil},
		"evening":  {model.TypeTask, today, 1, nil},
		"project":  {model.TypeProject, today, 0, nil},
		"today":    {model.TypeTask, today, 0, nil},
	}
	cases := []struct {
		row, value string
		bucket     int // the part of the day the value moves the row to
	}{
		{"reminder", "today", 0},
		{"reminder", now.Format("2006-01-02"), 0},
		{"old", "today", 0},
		{"old", past, 0},
		{"evening", "today", 0},
		{"evening", "evening", 1},
		{"project", "today", 0},
		{"today", past, 0},
	}
	for _, tc := range cases {
		for _, moves := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s %s moves=%v", tc.row, tc.value, moves), func(t *testing.T) {
				fastVerify(t)
				database, sqlDB := seedWritable(t)
				r := rows[tc.row]
				if _, err := sqlDB.Exec(`INSERT INTO TMTask (uuid, title, type, status, trashed, start, startDate, startBucket, reminderTime) VALUES ('un-1', 'Not moved', ?, 0, 0, 2, ?, ?, ?)`,
					int(r.typ), r.date, r.bucket, r.reminder); err != nil {
					t.Fatal(err)
				}
				var calls *int
				if moves {
					calls = stubExecEditing(t, sqlDB, fmt.Sprintf(`UPDATE TMTask SET start = 1, startDate = %d, startBucket = %d, reminderTime = NULL WHERE uuid = 'un-1'`, today, tc.bucket))
				} else {
					calls = stubExecDropping(t)
				}
				cmd := []string{"--json", "edit", "un-1", "--when", tc.value}
				if r.typ == model.TypeProject {
					cmd = []string{"--json", "project", "edit", "un-1", "--when", tc.value}
				}

				out, err := runOut(t, database, cmd...)
				if err != nil {
					t.Fatalf("%v: %v", cmd, err)
				}
				if *calls != 1 {
					t.Errorf("issued %d writes, want one", *calls)
				}
				var got model.Task
				if err := json.Unmarshal([]byte(out), &got); err != nil {
					t.Fatalf("unmarshal %q: %v", out, err)
				}
				wantDate := model.ThingsDate(r.date)
				if moves {
					wantDate = model.ThingsDate(today)
				}
				if got.StartDate == nil || *got.StartDate != wantDate {
					t.Errorf("printed start date %v, want %v", got.StartDate, wantDate)
				}
			})
		}
	}
}

// With another field in the edit, an unmoved row left where it was still
// needs the rest of the edit recorded: nothing recorded at all is a dropped
// edit.
func TestEditUnmovedRowWithOtherFieldsNeedsAChange(t *testing.T) {
	fastVerify(t)
	database, sqlDB := seedWritable(t)
	old := int(model.ThingsDateFromTime(testNow.AddDate(0, 0, -3)))
	if _, err := sqlDB.Exec(`UPDATE TMTask SET start = 2, startDate = ?, startBucket = 0 WHERE uuid = 'one-1'`, old); err != nil {
		t.Fatal(err)
	}
	stubExecDropping(t)
	if _, err := runOut(t, database, "edit", "one-1", "--when", "today", "--title", "Post parcel"); err == nil || !strings.Contains(err.Error(), "did not apply") {
		t.Fatalf("edit = %v, want a dropped-edit error", err)
	}

	stubExecEditing(t, sqlDB, `UPDATE TMTask SET title = 'Post parcel' WHERE uuid = 'one-1'`)
	if _, err := runOut(t, database, "edit", "one-1", "--when", "today", "--title", "Post parcel"); err != nil {
		t.Fatalf("edit with the title recorded and the row kept: %v", err)
	}
}

// A row Things filed under today on an earlier day keeps start = 1 and that
// day's date; Things does not move it forward, and the app shows it in
// Today. Measured on 9 Oct 2026: --when today on such a row was a no-op, and today's date on one with a reminder cleared
// the reminder and kept the earlier date. Both are filed today, so the edit
// is confirmed, not a dropped or misfiled one. A no-op still sends the URL;
// the dropping stub fails it unless the edit skips the read-back wait.
func TestEditCarriedOverRow(t *testing.T) {
	today := int(model.ThingsDateFromTime(testNow))
	yesterday := int(model.ThingsDateFromTime(testNow.AddDate(0, 0, -1)))
	todayDate := testNow.Format("2006-01-02")
	const reminder = 1207959552
	seed := func(t *testing.T, sqlDB *sql.DB, start, date, bucket int, reminder any) {
		t.Helper()
		if _, err := sqlDB.Exec(`INSERT INTO TMTask (uuid, title, type, status, trashed, start, startDate, startBucket, reminderTime, userModificationDate) VALUES ('co-1', 'Carried over', 0, 0, 0, ?, ?, ?, ?, 1)`,
			start, date, bucket, reminder); err != nil {
			t.Fatal(err)
		}
	}
	startDate := func(t *testing.T, out string) int {
		t.Helper()
		var got model.Task
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatalf("unmarshal %q: %v", out, err)
		}
		if got.StartDate == nil {
			t.Fatalf("printed no start date: %s", out)
		}
		return int(*got.StartDate)
	}

	t.Run("today is a no-op", func(t *testing.T) {
		failOnWait(t)
		database, sqlDB := seedWritable(t)
		seed(t, sqlDB, 1, yesterday, 0, nil)
		calls := stubExecDropping(t)
		out, err := runOut(t, database, "--json", "edit", "co-1", "--when", "today")
		if err != nil {
			t.Fatalf("edit: %v", err)
		}
		if *calls != 1 {
			t.Errorf("issued %d writes, want one", *calls)
		}
		if got := startDate(t, out); got != yesterday {
			t.Errorf("printed start date %v, want %v", got, yesterday)
		}
	})

	for _, tc := range []struct {
		name  string
		moves bool
	}{{"today's date keeps the date", false}, {"today's date moves the date", true}} {
		t.Run(tc.name, func(t *testing.T) {
			fastVerify(t)
			database, sqlDB := seedWritable(t)
			seed(t, sqlDB, 1, yesterday, 0, reminder)
			want := yesterday
			apply := `UPDATE TMTask SET reminderTime = NULL WHERE uuid = 'co-1'`
			if tc.moves {
				want = today
				apply = fmt.Sprintf(`UPDATE TMTask SET startDate = %d, reminderTime = NULL WHERE uuid = 'co-1'`, today)
			}
			calls := stubExecEditing(t, sqlDB, apply)
			out, err := runOut(t, database, "--json", "edit", "co-1", "--when", todayDate)
			if err != nil {
				t.Fatalf("edit: %v", err)
			}
			if *calls != 1 {
				t.Errorf("issued %d writes, want one", *calls)
			}
			if got := startDate(t, out); got != want {
				t.Errorf("printed start date %v, want %v", got, want)
			}
		})
	}

	// The cases not measured are sent and read back, and confirmed whether
	// Things moves the row to today or leaves it where it was, with nothing
	// recorded.
	for _, tc := range []struct {
		name, value string
		reminder    any
	}{
		{"today's date", todayDate, nil},
		{"past date", testNow.AddDate(0, 0, -2).Format("2006-01-02"), nil},
	} {
		for _, moves := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s moves=%v", tc.name, moves), func(t *testing.T) {
				fastVerify(t)
				database, sqlDB := seedWritable(t)
				seed(t, sqlDB, 1, yesterday, 0, tc.reminder)
				want := yesterday
				if moves {
					want = today
					stubExecEditing(t, sqlDB, fmt.Sprintf(`UPDATE TMTask SET startDate = %d, reminderTime = NULL WHERE uuid = 'co-1'`, today))
				} else {
					stubExecDropping(t)
				}
				out, err := runOut(t, database, "--json", "edit", "co-1", "--when", tc.value)
				if err != nil {
					t.Fatalf("edit: %v", err)
				}
				if got := startDate(t, out); got != want {
					t.Errorf("printed start date %v, want %v", got, want)
				}
			})
		}
	}

	// Every value that files the row today clears its reminder, and Things
	// was measured clearing it on such a row, so a row that still has its
	// reminder with nothing recorded was not given the write: the edit did
	// not apply. With the reminder cleared it is confirmed.
	for _, value := range []string{"today", todayDate, testNow.AddDate(0, 0, -2).Format("2006-01-02")} {
		t.Run("reminder kept with "+value, func(t *testing.T) {
			fastVerify(t)
			database, sqlDB := seedWritable(t)
			seed(t, sqlDB, 1, yesterday, 0, reminder)
			stubExecDropping(t)
			_, err := runOut(t, database, "--json", "edit", "co-1", "--when", value)
			if err == nil || !strings.Contains(err.Error(), "edit did not apply:") {
				t.Fatalf("edit = %v, want a dropped-edit error", err)
			}
		})
	}
	t.Run("reminder cleared with today", func(t *testing.T) {
		fastVerify(t)
		database, sqlDB := seedWritable(t)
		seed(t, sqlDB, 1, yesterday, 0, reminder)
		stubExecEditing(t, sqlDB, `UPDATE TMTask SET reminderTime = NULL WHERE uuid = 'co-1'`)
		if _, err := runOut(t, database, "--json", "edit", "co-1", "--when", "today"); err != nil {
			t.Fatalf("edit: %v", err)
		}
	})

	// A closed or trashed row is in no list by its start date, so its
	// earlier date is not today: --when today on it is a change that waits.
	for _, tc := range []struct{ name, set string }{
		{"completed", "status = 3"},
		{"trashed", "trashed = 1"},
	} {
		t.Run(tc.name+" is not a no-op", func(t *testing.T) {
			fastVerify(t)
			database, sqlDB := seedWritable(t)
			seed(t, sqlDB, 1, yesterday, 0, nil)
			if _, err := sqlDB.Exec(`UPDATE TMTask SET ` + tc.set + ` WHERE uuid = 'co-1'`); err != nil {
				t.Fatal(err)
			}
			stubExecDropping(t)
			_, err := runOut(t, database, "--json", "edit", "co-1", "--when", "today")
			if err == nil || !strings.Contains(err.Error(), "did not apply") {
				t.Fatalf("edit = %v, want the read-back to wait and fail", err)
			}
		})
	}

	// Filed anywhere but today is still misfiled: the earlier date counts
	// as today only for a value that files the row today.
	for _, tc := range []struct {
		name, value, apply string
	}{
		{"today filed in Someday", "today", `UPDATE TMTask SET start = 2, startDate = NULL, reminderTime = NULL WHERE uuid = 'co-1'`},
		{"tomorrow kept in today", "tomorrow", ``},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fastVerify(t)
			database, sqlDB := seedWritable(t)
			seed(t, sqlDB, 1, yesterday, 0, reminder)
			stubExecEditing(t, sqlDB, tc.apply)
			_, err := runOut(t, database, "--json", "edit", "co-1", "--when", tc.value)
			var misfiled *misfiledError
			if !errors.As(err, &misfiled) {
				t.Fatalf("edit = %v, want a misfiled error", err)
			}
		})
	}

	// A start = 2 row dated today, which Things has not moved yet, keeps
	// the unmoved-row rules: no reminder is a no-op, and with one the edit is
	// sent and confirmed when Things leaves the row as it was.
	t.Run("unmoved today no-op", func(t *testing.T) {
		failOnWait(t)
		database, sqlDB := seedWritable(t)
		seed(t, sqlDB, 2, today, 0, nil)
		calls := stubExecDropping(t)
		if _, err := runOut(t, database, "--json", "edit", "co-1", "--when", "today"); err != nil {
			t.Fatalf("edit: %v", err)
		}
		if *calls != 1 {
			t.Errorf("issued %d writes, want one", *calls)
		}
	})
	t.Run("unmoved today with a reminder", func(t *testing.T) {
		fastVerify(t)
		database, sqlDB := seedWritable(t)
		seed(t, sqlDB, 2, today, 0, reminder)
		calls := stubExecDropping(t)
		if _, err := runOut(t, database, "--json", "edit", "co-1", "--when", "today"); err != nil {
			t.Fatalf("edit: %v", err)
		}
		if *calls != 1 {
			t.Errorf("issued %d writes, want one", *calls)
		}
	})
}

// holds files a row carried over from an earlier day under today, in its
// part of the day, only for a place that is today.
func TestWhenPlaceHoldsCarriedOver(t *testing.T) {
	today := model.ThingsDateFromTime(testNow)
	yesterday := model.ThingsDateFromTime(testNow.AddDate(0, 0, -1))
	tomorrow := model.ThingsDateFromTime(testNow.AddDate(0, 0, 1))
	for _, tc := range []struct {
		name   string
		place  whenPlace
		start  model.Start
		bucket int
		want   bool
	}{
		{"today on day part", whenPlace{day: today, bucket: 0}, model.StartAnytime, 0, true},
		{"today's date on day part", whenPlace{day: today, bucket: -1}, model.StartAnytime, 0, true},
		{"today on evening", whenPlace{day: today, bucket: 0}, model.StartAnytime, 1, false},
		{"evening on day part", whenPlace{day: today, bucket: 1}, model.StartAnytime, 0, false},
		{"evening on evening", whenPlace{day: today, bucket: 1}, model.StartAnytime, 1, true},
		{"tomorrow", whenPlace{day: tomorrow, bucket: -1}, model.StartAnytime, 0, false},
		{"someday row", whenPlace{day: today, bucket: -1}, model.StartSomeday, 0, false},
	} {
		day := yesterday
		task := &model.Task{Start: tc.start, StartDate: &day, StartBucket: tc.bucket}
		if got := tc.place.holds(task, today); got != tc.want {
			t.Errorf("%s: holds = %v, want %v", tc.name, got, tc.want)
		}
	}
	day := yesterday
	for _, task := range []*model.Task{
		{Status: model.StatusCompleted, Start: model.StartAnytime, StartDate: &day},
		{Status: model.StatusCancelled, Start: model.StartAnytime, StartDate: &day},
		{Trashed: true, Start: model.StartAnytime, StartDate: &day},
	} {
		if (whenPlace{day: today, bucket: 0}).holds(task, today) {
			t.Errorf("holds a closed or trashed row dated yesterday (%+v) as filed today", task)
		}
	}
}

// On a row carried over from an earlier day, only the case measured is a
// certain no-op: --when today on a to-do in the day part with no reminder.
// The rest are sent and read back.
func TestWhenUnchangedCarriedOver(t *testing.T) {
	yesterday := model.ThingsDateFromTime(testNow.AddDate(0, 0, -1))
	noReminder := func() (int, error) { return reminderNone, nil }
	at := func(min int) func() (int, error) { return func() (int, error) { return min, nil } }
	later := testNow.Add(2 * time.Hour)
	for _, tc := range []struct {
		name     string
		value    string
		typ      model.TaskType
		bucket   int
		reminder func() (int, error)
		want     bool
	}{
		{"today", "today", model.TypeTask, 0, noReminder, true},
		{"today with a reminder", "today", model.TypeTask, 0, at(9 * 60), false},
		{"today's date", testNow.Format("2006-01-02"), model.TypeTask, 0, noReminder, false},
		{"past date", testNow.AddDate(0, 0, -2).Format("2006-01-02"), model.TypeTask, 0, noReminder, false},
		{"evening on evening", "evening", model.TypeTask, 1, noReminder, false},
		{"today on a project", "today", model.TypeProject, 0, noReminder, false},
		{"same reminder time", later.Format("15:04"), model.TypeTask, 0, at(later.Hour()*60 + later.Minute()), false},
	} {
		day := yesterday
		task := &model.Task{Type: tc.typ, Start: model.StartAnytime, StartDate: &day, StartBucket: tc.bucket}
		reads := whenReads{reminder: tc.reminder, stored: func() (model.Start, error) { return model.StartAnytime, nil }}
		if got := whenUnchanged(tc.value, task, testNow, reads); got != tc.want {
			t.Errorf("%s: whenUnchanged(%q) = %v, want %v", tc.name, tc.value, got, tc.want)
		}
	}

	// A start that cannot be read leaves an unmoved row dated before today
	// looking carried over; neither is a certain no-op then.
	day := yesterday
	task := &model.Task{Type: model.TypeTask, Start: model.StartAnytime, StartDate: &day}
	unreadable := whenReads{reminder: noReminder, stored: func() (model.Start, error) { return 0, errors.New("busy") }}
	if whenUnchanged("today", task, testNow, unreadable) {
		t.Error("whenUnchanged(today) on a row whose stored start cannot be read = true, want false")
	}
}

// failOnWait fails the test if the edit polls for a read-back. The budget is
// long enough that the first round never expires, so any read-back pauses,
// and the pause stops the test. A certain no-op prints the item without
// polling.
func failOnWait(t *testing.T) {
	t.Helper()
	timeout, sleep := verifyTimeout, verifySleep
	verifyTimeout = time.Hour
	verifySleep = func(time.Duration) { t.Fatal("the edit polled for a read-back; a certain no-op prints at once") }
	t.Cleanup(func() { verifyTimeout, verifySleep = timeout, sleep })
}
