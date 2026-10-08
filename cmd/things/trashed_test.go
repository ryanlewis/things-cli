package main

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ryanlewis/things-cli/internal/db"
	"github.com/ryanlewis/things-cli/internal/db/dbtest"
)

// seedTrashed adds a to-do and a project in the Trash to seedWritable's
// database, and a fresh listing whose row 1 is the trashed to-do: the state a
// user is in after listing and then trashing that row in Things.
func seedTrashed(t *testing.T) *db.DB {
	t.Helper()
	isolateHome(t)
	database, sqlDB := seedWritable(t)
	fx := dbtest.NewFixture(t, sqlDB)
	fx.Todo("bin-1", "Binned errand", 5, dbtest.Anytime(), dbtest.Trashed())
	fx.Project("binproj-1", "Binned project", 6, dbtest.Trashed())
	seedCache(t, time.Minute, "things today", "bin-1", "one-1")
	return database
}

// A row number or uuid that lands on an item in the Trash is refused by every
// write that takes a task ref, and nothing reaches Things (R1 of the 8 Oct
// 2026 adversarial run: `complete 1` completed a trashed item, exit 0).
func TestWritesRefuseTrashedItem(t *testing.T) {
	cases := []struct {
		name string
		args []string
		ref  string
		uuid string
		done string
	}{
		{"complete by row", []string{"complete", "1"}, "1", "bin-1", "completed"},
		{"complete by uuid", []string{"complete", "bin-1"}, "bin-1", "bin-1", "completed"},
		{"cancel by row", []string{"cancel", "1"}, "1", "bin-1", "cancelled"},
		{"cancel by uuid", []string{"cancel", "bin-1"}, "bin-1", "bin-1", "cancelled"},
		{"edit title", []string{"edit", "1", "--title", "New"}, "1", "bin-1", "edited"},
		{"edit complete", []string{"edit", "bin-1", "--complete"}, "bin-1", "bin-1", "edited"},
		{"project edit", []string{"project", "edit", "binproj-1", "--title", "New"}, "binproj-1", "binproj-1", "edited"},
		{"project complete", []string{"complete", "binproj-1", "-y"}, "binproj-1", "binproj-1", "completed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fastVerify(t)
			database := seedTrashed(t)
			calls := stubExecDropping(t)

			stdout, _, err := runStreams(t, database, tc.args...)
			if err == nil {
				t.Fatalf("run %v: want a refusal for an item in the Trash", tc.args)
			}
			if *calls != 0 {
				t.Errorf("issued %d write(s); an item in the Trash must not reach Things", *calls)
			}
			if stdout != "" {
				t.Errorf("stdout = %q, want nothing on a refusal", stdout)
			}
			if !strings.Contains(err.Error(), "is in the Trash, so it was not "+tc.done) || !strings.Contains(err.Error(), tc.uuid) {
				t.Errorf("error = %v, want it to name %s and say it is in the Trash", err, tc.uuid)
			}
			var trashed *trashedError
			if !errors.As(err, &trashed) {
				t.Fatalf("error = %T, want a *trashedError", err)
			}

			payload, raw := decodePayload(t, err)
			if payload.Error != "trashed" {
				t.Errorf("JSON error = %q, want %q (%s)", payload.Error, "trashed", raw)
			}
			if payload.UUID != tc.uuid || payload.Query != tc.ref || payload.Title == "" {
				t.Errorf("JSON payload = %s, want uuid %s, query %q and the title", raw, tc.uuid, tc.ref)
			}
		})
	}
}

// show still reaches an item in the Trash, by row or uuid, and says so.
func TestShowMarksTrashedItem(t *testing.T) {
	database := seedTrashed(t)

	for _, ref := range []string{"1", "bin-1"} {
		out, err := runOut(t, database, "show", ref)
		if err != nil {
			t.Fatalf("show %s: %v", ref, err)
		}
		if !strings.Contains(out, "Binned errand") || !strings.Contains(out, "Open (in Trash)") {
			t.Errorf("show %s = %q, want the item marked as in the Trash", ref, out)
		}
	}

	out, err := runOut(t, database, "--json", "show", "bin-1")
	if err != nil {
		t.Fatalf("show --json: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("show --json is not JSON (%v): %q", err, out)
	}
	if got["trashed"] != true {
		t.Errorf("show --json trashed = %v, want true", got["trashed"])
	}
}

// complete and cancel print the item they closed, as `edit --complete` does,
// in plain and --json; under --no-verify they say the change is unconfirmed.
func TestCompleteCancelPrintClosedItem(t *testing.T) {
	cases := []struct {
		cmd    string
		status int
		plain  string
		json   string
	}{
		{"complete", 3, "Completed", "completed"},
		{"cancel", 2, "Cancelled", "cancelled"},
	}
	for _, tc := range cases {
		t.Run(tc.cmd+" plain", func(t *testing.T) {
			fastVerify(t)
			database, sqlDB := seedWritable(t)
			stubExecApplying(t, sqlDB, "one-1", tc.status)

			out, err := runOut(t, database, tc.cmd, "one-1")
			if err != nil {
				t.Fatalf("%s: %v", tc.cmd, err)
			}
			for _, want := range []string{"Post letter", "one-1", "Status:   " + tc.plain} {
				if !strings.Contains(out, want) {
					t.Errorf("stdout = %q, want it to contain %q", out, want)
				}
			}
		})
		t.Run(tc.cmd+" json", func(t *testing.T) {
			fastVerify(t)
			database, sqlDB := seedWritable(t)
			stubExecApplying(t, sqlDB, "one-1", tc.status)

			out, err := runOut(t, database, "--json", tc.cmd, "one-1")
			if err != nil {
				t.Fatalf("%s --json: %v", tc.cmd, err)
			}
			var got map[string]any
			if err := json.Unmarshal([]byte(out), &got); err != nil {
				t.Fatalf("stdout is not JSON (%v): %q", err, out)
			}
			if got["uuid"] != "one-1" || got["status"] != tc.json {
				t.Errorf("JSON = %s, want uuid one-1 with status %q", out, tc.json)
			}
		})
		t.Run(tc.cmd+" no-verify", func(t *testing.T) {
			database, _ := seedWritable(t)
			stubExecDropping(t)

			out, err := runOut(t, database, "--no-verify", tc.cmd, "one-1")
			if err != nil {
				t.Fatalf("%s --no-verify: %v", tc.cmd, err)
			}
			if !strings.Contains(out, "not confirmed") || !strings.Contains(out, "one-1") {
				t.Errorf("stdout = %q, want an unconfirmed note naming the item", out)
			}

			out, err = runOut(t, database, "--json", "--no-verify", tc.cmd, "one-1")
			if err != nil {
				t.Fatalf("%s --json --no-verify: %v", tc.cmd, err)
			}
			var got unconfirmedEdit
			if err := json.Unmarshal([]byte(out), &got); err != nil {
				t.Fatalf("stdout is not JSON (%v): %q", err, out)
			}
			if got.UUID != "one-1" || got.Confirmed || got.Reason != "no-verify" {
				t.Errorf("JSON = %s, want an unconfirmed one-1 with reason no-verify", out)
			}
		})
	}
}
