package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/ryanlewis/things-cli/internal/db"
	"github.com/ryanlewis/things-cli/internal/output"
)

type DoctorCmd struct{}

type doctorReport struct {
	OK        bool     `json:"ok"`
	Status    string   `json:"status"`
	Source    string   `json:"source"`
	Home      string   `json:"home,omitempty"`
	Container string   `json:"container,omitempty"`
	Pattern   string   `json:"pattern,omitempty"`
	Database  string   `json:"database,omitempty"`
	Matches   []string `json:"matches,omitempty"`
	Error     string   `json:"error,omitempty"`
}

// Run reports whether the database can be discovered and opened read-only.
// A diagnosed access problem is data, not a command failure: the report is
// still useful and --json callers can branch on ok/status without receiving a
// second error object in place of it.
func (c *DoctorCmd) Run(d *Deps) error {
	report := diagnoseDatabase(d)
	if d.JSON {
		return output.Print(d.Stdout, report, true)
	}
	printDoctorReport(d, report)
	return nil
}

func diagnoseDatabase(d *Deps) doctorReport {
	if d.DBPath != "" {
		source := "flag"
		if d.config().SetsDB(d.DBPath) {
			source = "config"
		}
		return diagnoseDatabaseFile(d.DBPath, source)
	}

	diagnosis, err := db.DiagnoseDBPath()
	report := doctorReport{
		Status:    diagnosis.Status,
		Source:    "automatic",
		Home:      diagnosis.Home,
		Container: diagnosis.Container,
		Pattern:   diagnosis.Pattern,
		Database:  diagnosis.Database,
		Matches:   diagnosis.Matches,
	}
	if err != nil {
		report.Error = err.Error()
		return report
	}
	return confirmDatabaseOpen(report)
}

func diagnoseDatabaseFile(path, source string) doctorReport {
	report := doctorReport{Status: "ok", Source: source, Database: path}
	info, err := os.Stat(path)
	switch {
	case err == nil && info.IsDir():
		report.Status = "invalid"
		report.Error = fmt.Sprintf("%s is a directory, not a database file", path)
		return report
	case err == nil:
		return confirmDatabaseOpen(report)
	case errors.Is(err, fs.ErrPermission):
		report.Status = "permission_denied"
	case errors.Is(err, fs.ErrNotExist):
		report.Status = "not_found"
	default:
		report.Status = "error"
	}
	report.Error = err.Error()
	return report
}

func confirmDatabaseOpen(report doctorReport) doctorReport {
	database, err := db.Open(report.Database)
	if err != nil {
		report.Status = "open_failed"
		if errors.Is(err, fs.ErrPermission) {
			report.Status = "permission_denied"
		}
		report.Error = err.Error()
		return report
	}
	_ = database.Close()
	report.OK = true
	report.Status = "ok"
	return report
}

func printDoctorReport(d *Deps, report doctorReport) {
	fmt.Fprintf(d.Stdout, "status: %s\n", report.Status)
	fmt.Fprintf(d.Stdout, "source: %s\n", report.Source)
	if report.Home != "" {
		fmt.Fprintf(d.Stdout, "home: %s\n", report.Home)
	}
	if report.Container != "" {
		fmt.Fprintf(d.Stdout, "container: %s\n", report.Container)
	}
	if report.Pattern != "" {
		fmt.Fprintf(d.Stdout, "pattern: %s\n", report.Pattern)
	}
	if report.Database != "" {
		fmt.Fprintf(d.Stdout, "database: %s\n", report.Database)
	}
	if report.Error != "" {
		fmt.Fprintf(d.Stdout, "error: %s\n", report.Error)
	}
}
