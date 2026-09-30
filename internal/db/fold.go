package db

import (
	"database/sql/driver"
	"strings"

	"modernc.org/sqlite"
)

// FoldCase returns s in a form where two names that differ only by case are
// equal, for every script rather than ASCII alone: Things matches "ärger" to
// "Ärger", and SQLite's LIKE and NOCASE do not. Upper-casing first sends every
// case variant of a letter to one capital, so final sigma folds too — ToLower
// alone leaves "ς" and turns "Σ" into "σ". Every name and title comparison goes
// through this, in Go directly and in SQL through fold(), so the two agree.
func FoldCase(s string) string {
	return strings.ToLower(strings.ToUpper(s))
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
