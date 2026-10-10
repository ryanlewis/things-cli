package main

import "testing"

// TestCheckScheduleValue pins the import grammar for when and deadline,
// which is narrower than add's (see checkScheduleValue), with the exact
// reason each refused value gets. The rows where the two grammars differ are
// the ones that matter: two or more spaces before am/pm, a one-digit month or
// day, a phrase or weekday before the @, and a bare time or ISO date-time.
func TestCheckScheduleValue(t *testing.T) {
	const (
		badTime   = "not a real time of day after the @"
		badDate   = "not a real date"
		badBefore = "before the @ must be today, tomorrow, evening, a weekday name such as friday or a date as YYYY-MM-DD"
		notDate   = "not a date as YYYY-MM-DD, or a date and time as YYYY-MM-DD@HH:MM"
		ignored   = "Things ignores a time after anytime or someday"
	)
	cases := []struct{ name, value, want string }{
		// 12-hour clocks after the @: at most one space before am/pm.
		{"when", "today@6pm", ""},
		{"when", "today@6 pm", ""},
		{"when", "today@6\tpm", ""},
		{"when", "today@6  pm", badTime},
		{"when", "today@9:30PM", ""},
		{"when", "today@9:30 pm", ""},
		{"when", "today@9:30  pm", badTime},
		{"when", "today@12am", ""},
		{"when", "today@0am", badTime},
		{"when", "today@0:30pm", badTime},
		{"when", "today@13pm", badTime},
		{"when", "today@6:5pm", badTime},
		{"when", "today@18:00am", badTime},
		// 24-hour clocks after the @.
		{"when", "today@18:00", ""},
		{"when", "today@9:05", ""},
		{"when", "today@24:00", badTime},
		{"when", "today@23:60", badTime},
		{"when", "today@1800", badTime},
		{"when", "today@", badTime},
		{"when", "today@noon", badTime},
		// What may come before the @.
		{"when", "Tomorrow@18:00", ""},
		{"when", "EVENING@6pm", ""},
		{"when", " today@18:00 ", ""},
		{"when", "anytime@18:00", ignored},
		{"when", "Someday@6pm", ignored},
		{"when", "someday@25:00", ignored},
		{"when", "2026-10-10@18:00", ""},
		{"when", "2026-1-5@18:00", badBefore},
		{"when", "2026-02-30@18:00", badDate},
		{"when", "2026-13-01@6pm", badDate},
		{"when", "2026-10-10@25:00", badTime},
		{"when", "next friday@18:00", badBefore},
		// A weekday name goes as its date (resolveImportWhens).
		{"when", "friday@6pm", ""},
		{"when", "Friday@25:00", badTime},
		{"when", "fri@6pm", badBefore},
		// No @.
		{"when", "2026-10-10", ""},
		{"when", "2026-1-5", notDate},
		{"when", "2026-02-30", badDate},
		{"when", "2026-10-10T18:00", notDate},
		{"when", "18:00", notDate},
		{"when", "6pm", notDate},
		{"when", "25:00", notDate},
		{"when", "today", ""},
		{"when", "next friday", ""},
		{"when", "tommorow", `unrecognised when value "tommorow" (did you mean "tomorrow"? valid keywords: today, tomorrow, evening, anytime, someday)`},
		// Deadlines.
		{"deadline", "2026-10-10", ""},
		{"deadline", "friday", ""},
		{"deadline", "2026-02-30", badDate},
		{"deadline", "2026-1-5", "not a date as YYYY-MM-DD"},
		{"deadline", "18:00", "not a date as YYYY-MM-DD"},
		{"deadline", "2026-10-10@18:00", "a deadline has no time; give a date as YYYY-MM-DD"},
		{"deadline", "today", `deadline does not accept keywords like "today"; pass a YYYY-MM-DD date`},
	}
	for _, c := range cases {
		if got := checkScheduleValue(c.name, c.value); got != c.want {
			t.Errorf("checkScheduleValue(%q, %q) = %q, want %q", c.name, c.value, got, c.want)
		}
	}
}
