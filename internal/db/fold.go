package db

import (
	"database/sql/driver"
	"strings"
	"unicode"
	"unicode/utf8"

	"modernc.org/sqlite"
)

// FoldCase returns s in a form where two names that differ only by case are
// equal, for every script rather than ASCII alone: Things matches "ärger" to
// "Ärger", and SQLite's LIKE and NOCASE do not. Two strings fold to the same
// value exactly when strings.EqualFold says they are equal, so final sigma
// folds with "σ" and "Σ", while the Turkish "ı" and "İ" stay apart from "i" —
// ToLower(ToUpper(s)) would have sent both to "i" and let a lookup resolve to
// a title the user did not name. Every name and title comparison goes through
// this, in Go directly and in SQL through fold(), so the two agree.
func FoldCase(s string) string {
	return strings.Map(foldRune, s)
}

// foldRune maps r to one member of its case-fold orbit (the runes
// unicode.SimpleFold cycles through, which is what EqualFold compares): the
// smallest, lower-cased when it is ASCII so patterns stay readable. ASCII has
// a fast path, since its orbits only reach outside ASCII for runes that fold
// onto it ("K" Kelvin, "ſ"), and those land on the same letter below.
func foldRune(r rune) rune {
	if r >= utf8.RuneSelf {
		for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
			if f < r {
				r = f
			}
		}
	}
	if 'A' <= r && r <= 'Z' {
		r += 'a' - 'A'
	}
	return r
}

// FoldTag is FoldCase after trimming surrounding space: the key tag and area
// names are compared under, since Things ignores both differences.
func FoldTag(s string) string {
	return FoldCase(strings.TrimSpace(s))
}

// fold(text) is FoldCase for SQL: `fold(col) LIKE ?` against a value folded
// in Go ignores case beyond ASCII. Registration is process-wide, so every
// connection the driver opens has it, test databases included. NULL stays
// NULL, and anything that is not text passes through untouched.
func init() {
	sqlite.MustRegisterDeterministicScalarFunction("fold", 1,
		func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
			if s, ok := args[0].(string); ok {
				return FoldCase(s), nil
			}
			return args[0], nil
		})
}
