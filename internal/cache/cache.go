// Package cache stores the UUIDs of the last plain-text listing, so a later
// numeric reference (`things complete 2`) resolves against the rows the reader
// just saw.
package cache

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// MaxAge is how long a cached listing backs a numeric reference. Past it the
// row numbers are refused rather than acted on (issue #265): they are a
// convenience for a person reading a listing in front of them, and a listing
// from an earlier day describes a Today that has since rolled over. Four hours
// covers a sitting at the terminal without carrying yesterday's numbering into
// today.
const MaxAge = 4 * time.Hour

// LastList is the cache file's content: which rows a listing printed, when,
// and the command that printed them.
type LastList struct {
	// WrittenAt is when the listing ran. The zero value means the file
	// records no time — it was written by a things-cli older than 0.8.0 —
	// and counts as stale.
	WrittenAt time.Time `json:"written_at"`
	// Command is the listing to re-run for fresh numbers, rendered as the
	// user could type it (`things today --project "Work"`). Empty when the
	// file does not record one.
	Command string   `json:"command,omitempty"`
	UUIDs   []string `json:"uuids"`
}

// Stale reports whether the listing is too old for its row numbers to be
// acted on at now.
func (l LastList) Stale(now time.Time) bool {
	if l.WrittenAt.IsZero() {
		return true
	}
	return now.Sub(l.WrittenAt) > MaxAge
}

func dir() string {
	return filepath.Join(os.Getenv("HOME"), "Library", "Caches", "things-cli")
}

func path() string {
	return filepath.Join(dir(), "last-list")
}

// WriteLastList replaces the cache with l. The caller stamps WrittenAt: an
// unstamped write reads back as stale, which fails towards refusing a numeric
// reference rather than trusting one.
func WriteLastList(l LastList) error {
	d := dir()
	if err := os.MkdirAll(d, 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(l)
	if err != nil {
		return err
	}
	return os.WriteFile(path(), append(data, '\n'), 0o644)
}

// ReadLastList returns the cached listing. A file left by a things-cli older
// than 0.8.0 is a bare list of UUIDs with no timestamp; it is read as one
// rather than reported as corrupt, so those rows still resolve — as a stale
// listing, since there is no time to judge them by.
func ReadLastList() (LastList, error) {
	data, err := os.ReadFile(path())
	if err != nil {
		return LastList{}, err
	}
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return LastList{}, nil
	}
	if trimmed[0] != '{' {
		return LastList{UUIDs: strings.Split(string(trimmed), "\n")}, nil
	}
	var l LastList
	if err := json.Unmarshal(trimmed, &l); err != nil {
		return LastList{}, err
	}
	return l, nil
}
