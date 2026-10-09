package main

import (
	"database/sql"
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

// seedTrashedProject adds a project in the Trash holding one to-do directly
// and one under a heading, and a live project with a to-do as the control.
// Things leaves a trashed project's to-dos with trashed = 0.
func seedTrashedProject(t *testing.T) (*db.DB, *sql.DB) {
	t.Helper()
	isolateHome(t)
	database, sqlDB := seedWritable(t)
	fx := dbtest.NewFixture(t, sqlDB)
	fx.Project("binproj-2", "Binned move", 5, dbtest.Trashed())
	fx.Heading("binhead-2", "Packing", 6, dbtest.InProject("binproj-2"))
	fx.Todo("binchild-1", "Book van", 7, dbtest.Anytime(), dbtest.InProject("binproj-2"))
	fx.Todo("binchild-2", "Buy boxes", 8, dbtest.Anytime(), dbtest.UnderHeading("binhead-2"))
	fx.Project("liveproj-1", "Garden", 9)
	fx.Todo("livechild-1", "Mow lawn", 10, dbtest.Anytime(), dbtest.InProject("liveproj-1"))
	return database, sqlDB
}

// A to-do whose project, directly or through its heading, is in the Trash is
// refused by uuid like a trashed to-do: Things shows it only in the Trash,
// with the project, though its own row is not trashed.
func TestWritesRefuseToDoInTrashedProject(t *testing.T) {
	cases := []struct {
		name string
		args []string
		ref  string
		uuid string
		done string
	}{
		{"complete in project", []string{"complete", "binchild-1"}, "binchild-1", "binchild-1", "completed"},
		{"complete under heading", []string{"complete", "binchild-2"}, "binchild-2", "binchild-2", "completed"},
		{"cancel in project", []string{"cancel", "binchild-1"}, "binchild-1", "binchild-1", "cancelled"},
		{"cancel under heading", []string{"cancel", "binchild-2"}, "binchild-2", "binchild-2", "cancelled"},
		{"edit in project", []string{"edit", "binchild-1", "--title", "New"}, "binchild-1", "binchild-1", "edited"},
		{"edit under heading", []string{"edit", "binchild-2", "--notes", "x"}, "binchild-2", "binchild-2", "edited"},
		{"edit complete under heading", []string{"edit", "binchild-2", "--complete"}, "binchild-2", "binchild-2", "edited"},
		{"edit move out", []string{"edit", "binchild-1", "--list", "Garden"}, "binchild-1", "binchild-1", "edited"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fastVerify(t)
			database, _ := seedTrashedProject(t)
			calls := stubExecDropping(t)

			stdout, _, err := runStreams(t, database, tc.args...)
			if err == nil {
				t.Fatalf("run %v: want a refusal for a to-do in a trashed project", tc.args)
			}
			if *calls != 0 {
				t.Errorf("issued %d write(s); a to-do in a trashed project must not reach Things", *calls)
			}
			if stdout != "" {
				t.Errorf("stdout = %q, want nothing on a refusal", stdout)
			}
			want := "was not " + tc.done + `: its project "Binned move" is in the Trash`
			if !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), tc.uuid) {
				t.Errorf("error = %v, want it to name %s and contain %q", err, tc.uuid, want)
			}
			var trashed *trashedError
			if !errors.As(err, &trashed) {
				t.Fatalf("error = %T, want a *trashedError", err)
			}

			payload, raw := decodePayload(t, err)
			if payload.Error != "trashed" {
				t.Errorf("JSON error = %q, want %q (%s)", payload.Error, "trashed", raw)
			}
			if payload.UUID != tc.uuid || payload.Query != tc.ref || payload.Kind != "task" {
				t.Errorf("JSON payload = %s, want uuid %s, query %q and kind task", raw, tc.uuid, tc.ref)
			}
		})
	}
}

// A title skips a to-do in a trashed project, as it skips a trashed row. With
// the same title in a live project the title is not ambiguous: it names the
// live one, the only one Things shows outside the Trash.
func TestTitleSkipsToDoInTrashedProject(t *testing.T) {
	t.Run("only match", func(t *testing.T) {
		fastVerify(t)
		database, _ := seedTrashedProject(t)
		calls := stubExecDropping(t)

		_, _, err := runStreams(t, database, "complete", "Book van")
		if err == nil || *calls != 0 {
			t.Fatalf("complete \"Book van\" = %v with %d write(s), want not found and nothing sent", err, *calls)
		}
		payload, raw := decodePayload(t, err)
		if payload.Error != "not found" {
			t.Errorf("JSON error = %q, want %q (%s)", payload.Error, "not found", raw)
		}
	})
	t.Run("shared with live", func(t *testing.T) {
		fastVerify(t)
		database, sqlDB := seedTrashedProject(t)
		dbtest.NewFixture(t, sqlDB).Todo("livechild-2", "Book van", 11, dbtest.Anytime(), dbtest.InProject("liveproj-1"))
		stubExecApplying(t, sqlDB, "livechild-2", 3)

		out, err := runOut(t, database, "complete", "Book van")
		if err != nil {
			t.Fatalf("complete \"Book van\": %v, want the live to-do closed", err)
		}
		if !strings.Contains(out, "livechild-2") {
			t.Errorf("stdout = %q, want the live to-do", out)
		}
	})
}

// The control: a to-do in a live project still closes and edits.
func TestWritesAllowToDoInLiveProject(t *testing.T) {
	for _, tc := range []struct {
		cmd    string
		status int
	}{{"complete", 3}, {"cancel", 2}} {
		t.Run(tc.cmd, func(t *testing.T) {
			fastVerify(t)
			database, sqlDB := seedTrashedProject(t)
			stubExecApplying(t, sqlDB, "livechild-1", tc.status)

			if _, err := runOut(t, database, tc.cmd, "livechild-1"); err != nil {
				t.Fatalf("%s livechild-1: %v, want it closed", tc.cmd, err)
			}
		})
	}
	t.Run("edit", func(t *testing.T) {
		fastVerify(t)
		database, _ := seedTrashedProject(t)
		calls := stubExecDropping(t)

		_, _, err := runStreams(t, database, "edit", "livechild-1", "--title", "Mow the lawn")
		var trashed *trashedError
		if errors.As(err, &trashed) {
			t.Fatalf("edit livechild-1 = %v, want no trashed refusal", err)
		}
		if *calls != 1 {
			t.Errorf("issued %d writes, want the edit sent once", *calls)
		}
	})
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
