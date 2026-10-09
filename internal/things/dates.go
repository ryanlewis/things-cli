package things

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

var whenKeywords = []string{"today", "tomorrow", "evening", "anytime", "someday"}

// weekdayWords are natural-language day names Things accepts verbatim. They are
// allowlisted so the typo detector never mistakes them for a keyword — e.g.
// "monday" is Levenshtein distance 2 from "today" and would otherwise be
// rejected as a typo. Abbreviations are included for the same reason.
var weekdayWords = []string{
	"monday", "tuesday", "wednesday", "thursday", "friday", "saturday", "sunday",
	"mon", "tue", "tues", "wed", "thu", "thur", "thurs", "fri", "sat", "sun",
}

func isWhenKeyword(s string) bool {
	return slices.Contains(whenKeywords, s)
}

func isWeekdayWord(s string) bool {
	return slices.Contains(weekdayWords, s)
}

// NormalizeWhen validates and canonicalises a --when value.
//
// Accepted forms:
//   - keyword: today, tomorrow, evening, anytime, someday (case-insensitive)
//   - date: YYYY-MM-DD
//   - time: HH:MM or H:MM[am|pm]
//   - date+time: YYYY-MM-DD@HH:MM
//   - RFC3339: rewritten to YYYY-MM-DD@HH:MM (offset preserved as wall-clock)
//   - English natural-language phrases: passed through verbatim
//
// Inputs within edit distance 2 of a known keyword are rejected as typos
// (e.g. "tommorrow", "evning"); anything else passes through so users can
// keep using NL forms like "friday" or "tonight".
func NormalizeWhen(s string) (string, error) {
	v := strings.TrimSpace(s)
	if v == "" {
		return "", nil
	}
	if low := strings.ToLower(v); isWhenKeyword(low) {
		return low, nil
	}
	if t, ok := parseISO8601(v); ok {
		return t.Format("2006-01-02") + "@" + t.Format("15:04"), nil
	}
	if isWeekdayWord(strings.ToLower(v)) {
		return v, nil
	}
	if err := checkWhenShape(v); err != nil {
		return "", err
	}
	if k, ok := nearKeyword(v); ok {
		return "", fmt.Errorf("unrecognised --when value %q (did you mean %q? valid keywords: %s)", v, k, strings.Join(whenKeywords, ", "))
	}
	return v, nil
}

var (
	// whenDateShape is a value that starts like a date: year, month and day
	// in digits, then anything (an @time, say).
	whenDateShape = regexp.MustCompile(`^(\d{4})-(\d{1,2})-(\d{1,2})(.*)$`)
	// whenClockShape is a value shaped like a time of day, with an optional
	// am or pm.
	whenClockShape = regexp.MustCompile(`(?i)^(\d{1,2}):(\d{2})\s*(am|pm)?$`)
)

// WhenDateOrTime reports whether v, a value NormalizeWhen returned, is a
// date, a time of day, or a date and time (checkWhenShape's shapes), as
// opposed to a keyword or a free phrase.
func WhenDateOrTime(v string) bool {
	if m := whenDateShape.FindStringSubmatch(v); m != nil {
		rest, ok := strings.CutPrefix(m[4], "@")
		if m[4] == "" {
			return true
		}
		return ok && whenClockShape.MatchString(rest)
	}
	return whenClockShape.MatchString(v)
}

// checkWhenShape refuses a value shaped like a date or a time of day that
// names none: a month or day that does not exist, or an hour or minute out
// of range. Things cannot read such a value either, and drops it without a
// word. Any other value passes, English phrases included, since which of
// those Things understands is not known here.
func checkWhenShape(v string) error {
	clock := v
	if m := whenDateShape.FindStringSubmatch(v); m != nil {
		year, _ := strconv.Atoi(m[1])
		month, _ := strconv.Atoi(m[2])
		day, _ := strconv.Atoi(m[3])
		if month < 1 || month > 12 || day < 1 || day > time.Date(year, time.Month(month)+1, 0, 0, 0, 0, 0, time.UTC).Day() {
			return fmt.Errorf("invalid --when value %q: %s-%s-%s is not a date (use YYYY-MM-DD)", v, m[1], m[2], m[3])
		}
		if strings.HasPrefix(m[4], "T") {
			// parseISO8601 has turned it down already.
			return fmt.Errorf("invalid --when value %q: not an RFC3339 timestamp (use YYYY-MM-DDTHH:MM:SSZ or an offset such as +01:00)", v)
		}
		rest, ok := strings.CutPrefix(m[4], "@")
		if !ok {
			return nil
		}
		clock = rest
	}
	m := whenClockShape.FindStringSubmatch(clock)
	if m == nil {
		return nil
	}
	hour, _ := strconv.Atoi(m[1])
	minute, _ := strconv.Atoi(m[2])
	maxHour, minHour := 23, 0
	if m[3] != "" {
		maxHour, minHour = 12, 1
	}
	if hour < minHour || hour > maxHour || minute > 59 {
		return fmt.Errorf("invalid --when value %q: %s is not a time of day (use HH:MM)", v, clock)
	}
	return nil
}

// ParseListDate parses a list-filter date (--on/--from/--to). Accepts
// YYYY-MM-DD or RFC3339 (reduced to its date portion). Returns midnight in
// the local timezone — callers convert into whichever representation they
// need (e.g. model.ThingsDateFromTime). The flag name is used in error
// messages so the user knows which input was bad.
func ParseListDate(flag, s string) (time.Time, error) {
	v := strings.TrimSpace(s)
	if v == "" {
		return time.Time{}, fmt.Errorf("--%s: date is empty", flag)
	}
	if t, ok := parseISO8601(v); ok {
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.Local), nil
	}
	t, err := time.ParseInLocation("2006-01-02", v, time.Local)
	if err != nil {
		return time.Time{}, fmt.Errorf("--%s: invalid date %q (expected YYYY-MM-DD)", flag, v)
	}
	return t, nil
}

// NormalizeDeadline validates and canonicalises a --deadline value. The URL
// scheme accepts a date (YYYY-MM-DD) or English natural-language phrase.
// RFC3339 inputs are reduced to their date component since deadlines have
// no time-of-day.
func NormalizeDeadline(s string) (string, error) {
	v := strings.TrimSpace(s)
	if v == "" {
		return "", nil
	}
	if t, ok := parseISO8601(v); ok {
		return t.Format("2006-01-02"), nil
	}
	if isWhenKeyword(strings.ToLower(v)) {
		return "", fmt.Errorf("--deadline does not accept keywords like %q; pass a YYYY-MM-DD date", v)
	}
	return v, nil
}

var iso8601Layouts = [...]string{time.RFC3339Nano, time.RFC3339}

func parseISO8601(s string) (time.Time, bool) {
	// All accepted layouts have `YYYY-MM-DDTHH:MM:SS` as a prefix; cheap byte
	// checks let the common non-ISO inputs (keywords, dates, NL phrases) skip
	// the time.Parse failures.
	if len(s) < 20 || s[4] != '-' || s[7] != '-' || s[10] != 'T' {
		return time.Time{}, false
	}
	for _, layout := range iso8601Layouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// nearKeyword reports whether s is likely a typo of a `when` keyword. The
// comparison is case-insensitive and uses Levenshtein distance ≤ 2 — enough
// to catch "tommorrow"/"evning"/"todya" without flagging unrelated NL words
// like "friday" or "tonight".
func nearKeyword(s string) (string, bool) {
	low := strings.ToLower(s)
	for _, k := range whenKeywords {
		if low == k {
			continue
		}
		if levenshtein(low, k) <= 2 {
			return k, true
		}
	}
	return "", false
}

func levenshtein(a, b string) int {
	if a == b {
		return 0
	}
	ar, br := []rune(a), []rune(b)
	prev := make([]int, len(br)+1)
	curr := make([]int, len(br)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ar); i++ {
		curr[0] = i
		for j := 1; j <= len(br); j++ {
			cost := 1
			if ar[i-1] == br[j-1] {
				cost = 0
			}
			curr[j] = min(curr[j-1]+1, prev[j]+1, prev[j-1]+cost)
		}
		prev, curr = curr, prev
	}
	return prev[len(br)]
}
