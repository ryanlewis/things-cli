package db

import (
	"os"
	"testing"
	"time"

	"github.com/ryanlewis/things-cli/internal/clock"
)

// testNow is the one instant these tests call now. Fixtures build "today" and
// "closed today" from it, and TestMain pins the clock the queries read at it,
// so the two agree on the day even when a run crosses midnight.
var testNow = time.Now()

func TestMain(m *testing.M) {
	clock.Pin(testNow)
	os.Exit(m.Run())
}
