package output

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"golang.org/x/term"

	"github.com/ryanlewis/things-cli/internal/model"
)

// SetColorMode reconfigures lipgloss's default renderer based on the user's
// --color flag. "auto" defers to lipgloss/termenv, which detects TTY and
// honours NO_COLOR. "always" forces TrueColor; "never" forces Ascii.
func SetColorMode(mode string) error {
	switch mode {
	case "", "auto":
		// no-op — lipgloss self-detects from os.Stdout
	case "always":
		lipgloss.SetColorProfile(termenv.TrueColor)
	case "never":
		lipgloss.SetColorProfile(termenv.Ascii)
	default:
		return fmt.Errorf("invalid --color mode %q (want auto|always|never)", mode)
	}
	return nil
}

// Palette. Lipgloss strips ANSI when writing to non-TTY, so existing
// substring-based tests remain valid.
var (
	statusOpenStyle      = lipgloss.NewStyle()
	statusDoneStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("2")).Faint(true)
	statusCancelledStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Faint(true)

	titleDimStyle = lipgloss.NewStyle().Faint(true).Strikethrough(true)
	projectStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	areaStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Faint(true)
	tagStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	starStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("3")).Bold(true)

	dateOverdueStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Bold(true)
	dateTodayStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("3")).Bold(true)
	dateSoonStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	dimStyle         = lipgloss.NewStyle().Faint(true)

	headerStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("6"))
	labelStyle  = lipgloss.NewStyle().Bold(true)
)

// nowFn is overridable in tests.
var nowFn = time.Now

func styledStatus(status int) string {
	icon := statusIcon(status)
	switch status {
	case model.StatusCompleted:
		return statusDoneStyle.Render(icon)
	case model.StatusCancelled:
		return statusCancelledStyle.Render(icon)
	default:
		return statusOpenStyle.Render(icon)
	}
}

func styledProjectIcon(p model.Project) string {
	icon := projectIcon(p)
	if p.Status == model.StatusCancelled {
		return statusCancelledStyle.Render(icon)
	}
	if p.Status == model.StatusCompleted {
		return statusDoneStyle.Render(icon)
	}
	return projectStyle.Render(icon)
}

// styledDate formats and colours a date based on its proximity to today.
// The "deadline" flag toggles the "due:" prefix.
func styledDate(d *model.ThingsDate, deadline bool) string {
	if d == nil {
		return ""
	}
	text := d.String()
	if deadline {
		text = "due:" + text
	}

	today := startOfDay(nowFn())
	target := startOfDay(d.ToTime())
	switch {
	case target.Before(today):
		return dateOverdueStyle.Render(text)
	case target.Equal(today):
		return dateTodayStyle.Render(text)
	case target.Sub(today) <= 3*24*time.Hour:
		return dateSoonStyle.Render(text)
	default:
		return dimStyle.Render(text)
	}
}

func startOfDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

func styledTags(tags []string) string {
	if len(tags) == 0 {
		return ""
	}
	return tagStyle.Render("[" + strings.Join(tags, ", ") + "]")
}

// termWidth returns the terminal width, falling back to 120 for non-TTY
// (pipes, tests).
func termWidth() int {
	if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 0 {
		return w
	}
	return 120
}
