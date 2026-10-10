package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestKongDoctor(t *testing.T) {
	_, ctx := parse(t, "doctor")
	if ctx.Command() != "doctor" {
		t.Errorf("ctx.Command() = %q, want doctor", ctx.Command())
	}
}

func runDoctor(t *testing.T, d *Deps) (doctorReport, error) {
	t.Helper()
	var stdout bytes.Buffer
	d.JSON = true
	d.Stdout = &stdout
	err := (&DoctorCmd{}).Run(d)
	t.Cleanup(d.Close)
	var report doctorReport
	if decErr := json.Unmarshal(stdout.Bytes(), &report); decErr != nil {
		t.Fatalf("decode: %v\n%s", decErr, stdout.String())
	}
	return report, err
}

// A failed check still prints the whole report, then fails without a second
// error object after it, so stdout stays one JSON document.
func TestDoctorJSONNotFound(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	report, err := runDoctor(t, &Deps{})
	if !errors.Is(err, errReported) {
		t.Fatalf("err = %v, want errReported", err)
	}
	if report.OK || report.Status != "not_found" || report.Source != "automatic" {
		t.Errorf("report = %+v, want not_found from automatic discovery", report)
	}
	if report.Error == "" || report.Pattern == "" || report.Container == "" {
		t.Errorf("report = %+v, want the error, pattern and container", report)
	}
}

func TestDoctorJSONDiscovered(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, "Library", "Group Containers",
		"JLMPQHK86H.com.culturedcode.ThingsMac", "ThingsData-ABC",
		"Things Database.thingsdatabase")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "main.sqlite")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	report, err := runDoctor(t, &Deps{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !report.OK || report.Status != "ok" || report.Database != path {
		t.Errorf("report = %+v, want ok at %s", report, path)
	}
}

func TestDoctorJSONExplicitDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.sqlite")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	report, err := runDoctor(t, &Deps{DBPath: path})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !report.OK || report.Status != "ok" || report.Source != "flag" || report.Database != path {
		t.Errorf("report = %+v, want ok from the flag", report)
	}
}

func TestDoctorJSONExplicitMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gone.sqlite")
	report, err := runDoctor(t, &Deps{DBPath: path})
	if !errors.Is(err, errReported) {
		t.Fatalf("err = %v, want errReported", err)
	}
	if report.OK || report.Status != "not_found" {
		t.Errorf("report = %+v, want not_found", report)
	}
}

// SQLite would report this as SQLITE_CANTOPEN (14); doctor names the
// refusal.
func TestDoctorJSONExplicitPermissionDenied(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads a mode-000 file")
	}
	path := filepath.Join(t.TempDir(), "locked.sqlite")
	if err := os.WriteFile(path, nil, 0o000); err != nil {
		t.Fatal(err)
	}
	report, err := runDoctor(t, &Deps{DBPath: path})
	if !errors.Is(err, errReported) {
		t.Fatalf("err = %v, want errReported", err)
	}
	if report.Status != "permission_denied" || !strings.Contains(report.Error, "permission denied") {
		t.Errorf("report = %+v, want permission_denied keeping the OS error", report)
	}
}

func TestDoctorJSONDirectory(t *testing.T) {
	report, err := runDoctor(t, &Deps{DBPath: t.TempDir()})
	if !errors.Is(err, errReported) {
		t.Fatalf("err = %v, want errReported", err)
	}
	if report.Status != "error" || !strings.Contains(report.Error, "is a directory") {
		t.Errorf("report = %+v, want an error naming the directory", report)
	}
}

func TestDoctorPlainReport(t *testing.T) {
	var stdout bytes.Buffer
	printDoctorReport(&Deps{Stdout: &stdout}, doctorReport{
		Status: "permission_denied",
		Source: "automatic",
		Error:  "operation not permitted",
	})
	want := "status:    permission_denied\nsource:    automatic\nerror:     operation not permitted\n"
	if stdout.String() != want {
		t.Errorf("output = %q, want %q", stdout.String(), want)
	}
}

// A file that is not an SQLite database opens lazily without complaint, so
// doctor has to read the header to catch it.
func TestDoctorJSONNotADatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.sqlite")
	if err := os.WriteFile(path, []byte(strings.Repeat("not a database ", 20)), 0o600); err != nil {
		t.Fatal(err)
	}
	report, err := runDoctor(t, &Deps{DBPath: path})
	if !errors.Is(err, errReported) {
		t.Fatalf("err = %v, want errReported", err)
	}
	if report.OK || report.Status != "error" {
		t.Errorf("report = %+v, want a failed check", report)
	}
}
