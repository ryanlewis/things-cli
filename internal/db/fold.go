package db

import (
	"database/sql/driver"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
	"modernc.org/sqlite"
)

// FoldCase returns s in a form where two names that differ only by case are
// equal, for every script rather than ASCII alone: Things matches "ärger" to
// "Ärger", and SQLite's LIKE and NOCASE do not. The folding is full, as in
// Things, so "Straße" equals "STRASSE" and "ﬁx" equals "FIX"; beyond those
// multi-rune folds, two strings fold to the same value exactly when
// strings.EqualFold says they are equal. So final sigma folds with "σ" and
// "Σ", while the Turkish "ı" and "İ" stay apart from "i" — ToLower(ToUpper(s))
// would have sent both to "i" and let a lookup resolve to a title the user did
// not name. Names that differ only in Unicode normalisation are equal too, as
// in Things: "Café" with a precomposed "é" (NFC) equals "Café" typed as "e"
// plus a combining accent (NFD). Compatibility forms such as fullwidth "Ｃ"
// stay apart, as they do for tags in Things; project and area titles compare
// under FoldName instead. Every other name and title comparison goes through
// this, in Go directly and in SQL through fold(), so the two agree.
func FoldCase(s string) string {
	// Decomposing first is Unicode's canonical caseless match (D145): a
	// precomposed letter folds the same as its parts. Composing the result
	// keeps "e" from matching inside "é" in a substring search.
	return norm.NFC.String(foldRunes(norm.NFD.String(s)))
}

// FoldName is FoldCase for project and area titles, which Things also
// matches across Unicode compatibility forms: fullwidth "Ａ" and circled "Ⓐ"
// equal "A", superscript "²" equals "2", "Ⅳ" equals "IV" and a no-break
// space equals a space. A missing accent still counts, so "Cafe" does not
// equal "Café". Checked in Things 3 with list= and area= in things:///add and
// add-project, in both directions; tags matched none of these, so they keep
// FoldCase. It is Unicode's compatibility caseless match (D146), composed for
// the same reason FoldCase is, and fold_name() is its SQL form.
func FoldName(s string) string {
	return norm.NFKC.String(foldRunes(norm.NFKD.String(FoldCase(s))))
}

// foldRunes folds every rune of s, the multi-rune folds included.
func foldRunes(s string) string {
	if !strings.ContainsFunc(s, hasFullFold) {
		return strings.Map(foldRune, s)
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if f, ok := fullFolds[r]; ok {
			for _, fr := range f {
				b.WriteRune(foldRune(fr))
			}
			continue
		}
		b.WriteRune(foldRune(r))
	}
	return b.String()
}

// hasFullFold reports whether r folds to more than one rune. The first such
// rune is "ß", so ASCII never reaches the map.
func hasFullFold(r rune) bool {
	if r < 0xDF {
		return false
	}
	_, ok := fullFolds[r]
	return ok
}

// foldRune maps r to one member of its case-fold orbit (the runes
// unicode.SimpleFold cycles through, which is what EqualFold compares): the
// smallest, lower-cased when it is ASCII so patterns stay readable, and never
// a combining mark. ASCII has a fast path, since its orbits only reach outside
// ASCII for runes that fold onto it ("K" Kelvin, "ſ"), and those land on the
// same letter below.
func foldRune(r rune) rune {
	if r >= utf8.RuneSelf {
		for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
			if f < r {
				r = f
			}
		}
		// Iota's orbit holds the combining ypogegrammeni, which would compose
		// onto the letter before it; pick the letter instead.
		if r == '\u0345' {
			r = 'ι'
		}
	}
	if 'A' <= r && r <= 'Z' {
		r += 'a' - 'A'
	}
	return r
}

// normName returns s in NFC, the form exact names are compared in: a title
// typed with a combining accent is still the exact name of a title stored
// with the precomposed letter, and the other way round, since Things may
// store either. It changes nothing else, so case still counts.
func normName(s string) string {
	return norm.NFC.String(s)
}

// FoldTag is FoldCase after trimming surrounding space: the key tag names are
// compared under, since Things ignores both differences in a tag. It does not
// trim a project, area or heading title in things:///add, so those compare
// under FoldName (headings under FoldCase) alone.
func FoldTag(s string) string {
	return FoldCase(strings.TrimSpace(s))
}

// fold(text) is FoldCase for SQL: `fold(col) LIKE ?` against a value folded
// in Go ignores case beyond ASCII, and fold_name(text) is FoldName the same
// way. Registration is process-wide, so every
// connection the driver opens has it, test databases included. NULL stays
// NULL, and anything that is not text passes through untouched. nfc(text) is
// normName for SQL in the same way, for `nfc(col) = ?` against a value
// normalised in Go.
func init() {
	sqlite.MustRegisterDeterministicScalarFunction("fold", 1, textFunc(FoldCase))
	sqlite.MustRegisterDeterministicScalarFunction("fold_name", 1, textFunc(FoldName))
	sqlite.MustRegisterDeterministicScalarFunction("nfc", 1, textFunc(normName))
}

// textFunc wraps f as a SQLite scalar function over one text argument.
func textFunc(f func(string) string) func(*sqlite.FunctionContext, []driver.Value) (driver.Value, error) {
	return func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		if s, ok := args[0].(string); ok {
			return f(s), nil
		}
		return args[0], nil
	}
}
