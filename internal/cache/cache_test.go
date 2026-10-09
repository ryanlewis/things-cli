package cache

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestRoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	written := LastList{
		WrittenAt: time.Now().Truncate(time.Second),
		Command:   `things today --project 'Work'`,
		UUIDs:     []string{"a", "b", "c"},
	}
	if err := WriteLastList(written); err != nil {
		t.Fatalf("WriteLastList: %v", err)
	}
	got, err := ReadLastList()
	if err != nil {
		t.Fatalf("ReadLastList: %v", err)
	}
	if !reflect.DeepEqual(got.UUIDs, written.UUIDs) {
		t.Errorf("uuids = %v, want %v", got.UUIDs, written.UUIDs)
	}
	if got.Command != written.Command {
		t.Errorf("command = %q, want %q", got.Command, written.Command)
	}
	if !got.WrittenAt.Equal(written.WrittenAt) {
		t.Errorf("written at = %v, want %v", got.WrittenAt, written.WrittenAt)
	}
}

func TestWriteOverwrites(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	if err := WriteLastList(LastList{WrittenAt: time.Now(), UUIDs: []string{"a", "b"}}); err != nil {
		t.Fatal(err)
	}
	if err := WriteLastList(LastList{WrittenAt: time.Now(), UUIDs: []string{"x"}}); err != nil {
		t.Fatal(err)
	}
	got, err := ReadLastList()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.UUIDs, []string{"x"}) {
		t.Fatalf("got %v, want [x]", got.UUIDs)
	}
}

func TestReadMissingReturnsError(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	_, err := ReadLastList()
	if !os.IsNotExist(err) {
		t.Fatalf("expected not-exist error, got %v", err)
	}
}

// A cache file that exists but holds no listing is unreadable, not a missing
// cache: a row number must not be resolved as if no listing had run.
func TestReadUnreadableFile(t *testing.T) {
	for name, content := range map[string]string{
		"empty":           "",
		"blank":           " \n\n",
		"truncated JSON":  `{"written_at":"2026-10-09T10:00:00Z","uuids":["a","b`,
		"garbage":         "not a listing at all",
		"binary":          "\x00\x01\x02",
		"wrong JSON type": `{"uuids":"abc"}`,
		"gap in legacy":   "a\n\nb",
	} {
		t.Run(name, func(t *testing.T) {
			writeRaw(t, content)
			_, err := ReadLastList()
			if !errors.Is(err, ErrUnreadable) {
				t.Fatalf("ReadLastList = %v, want ErrUnreadable", err)
			}
			if errors.Is(err, fs.ErrNotExist) {
				t.Errorf("ReadLastList = %v, reads as a missing file", err)
			}
		})
	}
}

// A cache path that cannot be read as a file is unreadable too.
func TestReadUnreadableDirectory(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := os.MkdirAll(path(), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadLastList(); !errors.Is(err, ErrUnreadable) {
		t.Fatalf("ReadLastList = %v, want ErrUnreadable", err)
	}
}

func TestWriteEmptyList(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	if err := WriteLastList(LastList{WrittenAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	got, err := ReadLastList()
	if err != nil {
		t.Fatal(err)
	}
	if got.UUIDs != nil {
		t.Fatalf("got %v, want nil", got.UUIDs)
	}
}

// A cache file written by a things-cli older than 0.8.0 is a bare list of
// UUIDs. Its rows still read back, so the caller can say which row was asked
// for, but with no timestamp they are stale (issue #265).
func TestReadPreTimestampFile(t *testing.T) {
	writeRaw(t, "a\nb\nc\n")

	got, err := ReadLastList()
	if err != nil {
		t.Fatalf("ReadLastList: %v", err)
	}
	if !reflect.DeepEqual(got.UUIDs, []string{"a", "b", "c"}) {
		t.Errorf("uuids = %v, want [a b c]", got.UUIDs)
	}
	if !got.WrittenAt.IsZero() {
		t.Errorf("written at = %v, want the zero time", got.WrittenAt)
	}
	if !got.Stale(time.Now()) {
		t.Error("an undated listing is not stale")
	}
}

func TestStale(t *testing.T) {
	now := time.Now()
	cases := []struct {
		name      string
		writtenAt time.Time
		want      bool
	}{
		{"just written", now, false},
		{"inside the bound", now.Add(-MaxAge + time.Minute), false},
		{"past the bound", now.Add(-MaxAge - time.Minute), true},
		{"yesterday", now.AddDate(0, 0, -1), true},
		{"undated", time.Time{}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l := LastList{WrittenAt: tc.writtenAt, UUIDs: []string{"a"}}
			if got := l.Stale(now); got != tc.want {
				t.Errorf("Stale = %v, want %v", got, tc.want)
			}
		})
	}
}

// writeRaw puts exact bytes in the cache file, for the formats WriteLastList
// no longer produces.
func writeRaw(t *testing.T, content string) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	d := dir()
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "last-list"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The database a listing read round-trips through the file (issue #274).
func TestLastListRecordsDatabase(t *testing.T) {
	t.Setenv("HOME", t.TempDir())

	want := LastList{WrittenAt: time.Now(), DB: "/x/main.sqlite", UUIDs: []string{"u1"}}
	if err := WriteLastList(want); err != nil {
		t.Fatal(err)
	}
	got, err := ReadLastList()
	if err != nil {
		t.Fatal(err)
	}
	if got.DB != want.DB {
		t.Errorf("DB = %q, want %q", got.DB, want.DB)
	}
}
