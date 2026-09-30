package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestKongDoctor(t *testing.T) {
	_, ctx := parse(t, "doctor")
	if ctx.Command() != "doctor" {
		t.Errorf("ctx.Command() = %q, want doctor", ctx.Command())
	}
}

func TestDoctorJSONNotFound(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var stdout bytes.Buffer
	d := &Deps{JSON: true, Stdout: &stdout}
	if err := (&DoctorCmd{}).Run(d); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var report doctorReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode: %v\n%s", err, stdout.String())
	}
	if report.OK || report.Status != "not_found" || report.Source != "automatic" {
		t.Errorf("unexpected report: %+v", report)
	}
	if report.Error == "" || report.Pattern == "" || report.Container == "" {
		t.Errorf("incomplete failure report: %+v", report)
	}
}

func TestDoctorJSONExplicitDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "main.sqlite")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	d := &Deps{DBPath: path, JSON: true, Stdout: &stdout}
	if err := (&DoctorCmd{}).Run(d); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var report doctorReport
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("decode: %v\n%s", err, stdout.String())
	}
	if !report.OK || report.Status != "ok" || report.Source != "flag" || report.Database != path {
		t.Errorf("unexpected report: %+v", report)
	}
}

func TestDoctorPlainPermissionReport(t *testing.T) {
	var stdout bytes.Buffer
	d := &Deps{Stdout: &stdout}
	printDoctorReport(d, doctorReport{
		Status: "permission_denied",
		Source: "automatic",
		Error:  "operation not permitted",
	})
	want := "status: permission_denied\nsource: automatic\nerror: operation not permitted\n"
	if stdout.String() != want {
		t.Errorf("output = %q, want %q", stdout.String(), want)
	}
}
