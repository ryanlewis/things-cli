package main

import (
	"errors"
	"fmt"
	"io/fs"

	"github.com/ryanlewis/things-cli/internal/db"
	"github.com/ryanlewis/things-cli/internal/output"
)

type DoctorCmd struct{}

// doctorReport is what `things doctor` prints. Status is a stable token:
// "ok", "not_found", "permission_denied", "multiple" or "error". Source says
// where the database path came from: "automatic", "flag" or "config".
type doctorReport struct {
	OK        bool     `json:"ok"`
	Status    string   `json:"status"`
	Source    string   `json:"source"`
	Container string   `json:"container,omitempty"`
	Pattern   string   `json:"pattern,omitempty"`
	Matches   []string `json:"matches,omitempty"`
	Database  string   `json:"database,omitempty"`
	Error     string   `json:"error,omitempty"`
}

// errReported fails a command whose output already says what went wrong, so
// main exits non-zero without printing a second error after it.
var errReported = errors.New("failure already reported")

// Run checks that the database can be found and opened read-only. It reads
// no task data. The full report goes to stdout either way; a failed check
// then exits 1, so `things doctor && ...` behaves like any other command.
func (c *DoctorCmd) Run(d *Deps) error {
	report := diagnose(d)
	if d.JSON {
		if err := output.PrintJSON(d.Stdout, report); err != nil {
			return err
		}
	} else {
		printDoctorReport(d, report)
	}
	if !report.OK {
		return errReported
	}
	return nil
}

func diagnose(d *Deps) doctorReport {
	report := doctorReport{Source: "automatic"}
	path := d.DBPath
	if path != "" {
		report.Source = "flag"
		if d.config().SetsDB(path) {
			report.Source = "config"
		}
	} else {
		diagnosis, err := db.DiagnoseDBPath()
		report.Container = diagnosis.Container
		report.Pattern = diagnosis.Pattern
		report.Matches = diagnosis.Matches
		if err != nil {
			return report.fail(err)
		}
		path = diagnosis.Database
	}
	report.Database = path
	if _, err := d.openPath(path); err != nil {
		return report.fail(err)
	}
	report.OK = true
	report.Status = "ok"
	return report
}

func (r doctorReport) fail(err error) doctorReport {
	var multiple *db.MultipleError
	switch {
	case errors.Is(err, fs.ErrPermission):
		r.Status = "permission_denied"
	case errors.As(err, &multiple):
		r.Status = "multiple"
	case errors.Is(err, fs.ErrNotExist):
		r.Status = "not_found"
	default:
		r.Status = "error"
	}
	r.Error = err.Error()
	return r
}

func printDoctorReport(d *Deps, r doctorReport) {
	line := func(label, value string) {
		if value != "" {
			fmt.Fprintf(d.Stdout, "%-10s %s\n", label+":", value)
		}
	}
	line("status", r.Status)
	line("source", r.Source)
	line("container", r.Container)
	line("pattern", r.Pattern)
	if len(r.Matches) > 1 {
		for _, m := range r.Matches {
			line("match", m)
		}
	}
	line("database", r.Database)
	line("error", r.Error)
}
