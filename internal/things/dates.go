package things

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

var whenKeywords = []string{"today", "tomorrow", "evening", "anytime", "someday"}

func isWhenKeyword(s string) bool {
	return slices.Contains(whenKeywords, s)
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
	if k, ok := nearKeyword(v); ok {
		return "", fmt.Errorf("unrecognised --when value %q (did you mean %q? valid keywords: %s)", v, k, strings.Join(whenKeywords, ", "))
	}
	return v, nil
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
			curr[j] = min3(curr[j-1]+1, prev[j]+1, prev[j-1]+cost)
		}
		prev, curr = curr, prev
	}
	return prev[len(br)]
}

func min3(a, b, c int) int {
	m := a
	if b < m {
		m = b
	}
	if c < m {
		m = c
	}
	return m
}
