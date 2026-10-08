package output

import (
	"fmt"
	"io"
	"math"
	"os"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"golang.org/x/term"

	"github.com/ryanlewis/things-cli/internal/clock"
	"github.com/ryanlewis/things-cli/internal/model"
)

// colorProfile controls how the ANSI emitted by lipgloss renders is downsampled
// when written out. Lipgloss v2 styles always render full-fidelity ANSI; the
// stripping/downsampling that v1 did inside Render now happens at write time via
// a colorprofile.Writer (see newWriter). It is set by SetColorMode and defaults
// to auto-detection from stdout (honouring NO_COLOR, CLICOLOR_FORCE, etc.).
var colorProfile = detectProfile()

// detectProfile auto-detects the color profile from stdout and the environment
// (honouring NO_COLOR, CLICOLOR_FORCE, etc.). Used for both the package default
// and the "auto" mode so the two can't drift.
func detectProfile() colorprofile.Profile {
	return colorprofile.Detect(os.Stdout, os.Environ())
}

// SetColorMode reconfigures color output based on the user's --color flag.
// "auto" detects from stdout/env, "always" forces TrueColor, "never" strips all
// ANSI.
func SetColorMode(mode string) error {
	switch mode {
	case "", "auto":
		colorProfile = detectProfile()
	case "always":
		colorProfile = colorprofile.TrueColor
	case "never":
		colorProfile = colorprofile.NoTTY
	default:
		return fmt.Errorf("invalid --color mode %q (want auto|always|never)", mode)
	}
	return nil
}

// newWriter wraps w so that the ANSI emitted by lipgloss renders is downsampled
// (or stripped entirely) according to the active color profile on the way out.
func newWriter(w io.Writer) *colorprofile.Writer {
	return &colorprofile.Writer{Forward: w, Profile: colorProfile}
}

// Palette. Styles render full-fidelity ANSI unconditionally; the
// colorprofile.Writer applied in Print strips it for non-TTY / --color=never
// output, so substring-based tests over Print remain valid.
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

func styledStatus(status model.Status) string {
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

// styledChecklist renders a task's checklist progress as done/total, marked
// with a tick so it cannot be read as a date, or nothing when the task has no
// checklist.
func styledChecklist(c *model.ChecklistProgress) string {
	if c == nil {
		return ""
	}
	return dimStyle.Render(fmt.Sprintf("✓ %d/%d", c.Done(), c.Total))
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
	return styleDate(d.String(), deadline, daysFromToday(d, clock.Now()))
}

// styledCompactDate is styledDate's short form for a narrow terminal, relative
// to today the way Things shows dates: "today", "tomorrow" and "yesterday",
// a weekday for the rest of the coming week, "3d ago" for the past week, and
// otherwise the day and month, with the year only when it is not this one.
// A deadline keeps its "due:" prefix, so it still reads as one without
// colour.
func styledCompactDate(d *model.ThingsDate, deadline bool) string {
	if d == nil {
		return ""
	}
	now := clock.Now()
	days := daysFromToday(d, now)
	target := d.ToTime()
	var text string
	switch {
	case days == 0:
		text = "today"
	case days == 1:
		text = "tomorrow"
	case days == -1:
		text = "yesterday"
	case days > 1 && days < 7:
		text = target.Format("Mon")
	case days < -1 && days > -7:
		text = fmt.Sprintf("%dd ago", -days)
	case target.Year() == now.Year():
		text = target.Format("2 Jan")
	default:
		text = target.Format("2 Jan 06")
	}
	return styleDate(text, deadline, days)
}

// daysFromToday counts the calendar days from now's day to d in local time,
// negative for a date in the past. It rounds rather than truncates, so a
// daylight-saving change, which puts two midnights 23 or 25 hours apart, does
// not shift a day.
func daysFromToday(d *model.ThingsDate, now time.Time) int {
	today := clock.DayStart(now)
	// Noon on d's date, read from the encoding rather than through ToTime,
	// whose midnight can be skipped and land on the day before.
	noon := time.Date(int(*d>>16), time.Month((int(*d)>>12)&0xF), (int(*d)>>7)&0x1F, 12, 0, 0, 0, time.Local)
	target := clock.DayStart(noon)
	return int(math.Round(target.Sub(today).Hours() / 24))
}

// styleDate prefixes a deadline's text and colours it by how many days the
// date is from today.
func styleDate(text string, deadline bool, days int) string {
	if deadline {
		text = "due:" + text
	}
	switch {
	case days < 0:
		return dateOverdueStyle.Render(text)
	case days == 0:
		return dateTodayStyle.Render(text)
	case days <= 3:
		return dateSoonStyle.Render(text)
	default:
		return dimStyle.Render(text)
	}
}

func styledTags(tags []string) string {
	if len(tags) == 0 {
		return ""
	}
	return tagStyle.Render("[" + oneLine(strings.Join(tags, ", ")) + "]")
}

// compactTagWidth caps the first tag in styledCompactTags: most tags are a
// word or two, and a longer one still reads by its start.
const compactTagWidth = 15

// styledCompactTags is styledTags' short form for a narrow terminal: the first
// tag, cut to compactTagWidth, and a count of the rest, "[waiting-on-pos… +2]".
// A single tag is only cut, "[waiting-on-pos…]", and one that already fits
// compactTagWidth has no short form and gets "", as does no tag.
func styledCompactTags(tags []string) string {
	if len(tags) == 0 {
		return ""
	}
	first := oneLine(tags[0])
	if len(tags) == 1 {
		if lipgloss.Width(first) <= compactTagWidth {
			return ""
		}
		return tagStyle.Render("[" + ansi.Truncate(first, compactTagWidth, "…") + "]")
	}
	first = ansi.Truncate(first, compactTagWidth, "…")
	return tagStyle.Render(fmt.Sprintf("[%s +%d]", first, len(tags)-1))
}

// termWidth returns the terminal width, falling back to 120 for non-TTY
// (pipes, tests). It is a var so a test can pin a width instead
// of depending on how the test binary's stdout happens to be attached.
var termWidth = func() int {
	if w, _, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 0 {
		return w
	}
	return 120
}

// stdoutIsTerminal reports whether stdout is a terminal. Titles are cut short
// only there: piped output falls back to termWidth's 120 columns, and cutting
// a title there would hide text from grep and from agents reading the rows.
// A var, like termWidth, so a test can pin it.
var stdoutIsTerminal = func() bool {
	return term.IsTerminal(int(os.Stdout.Fd()))
}

// layout is what a printer fits its output to: the width termWidth reports
// and whether stdout is a terminal at all. Piped, width is termWidth's
// 120-column fallback, which only a task listing uses (see printTasks); every
// other surface fits fitWidth, which is 0 there.
type layout struct {
	width int
	tty   bool
}

// currentLayout reads the layout from stdout.
func currentLayout() layout {
	return layout{width: termWidth(), tty: stdoutIsTerminal()}
}

// fitWidth is the width output has to fit: the terminal's on a terminal, and
// 0 otherwise, which leaves text unwrapped and table columns whole. Piped
// output would fall back to termWidth's 120 columns, and fitting that adds
// line breaks and cuts the text does not have.
func (l layout) fitWidth() int {
	if l.tty {
		return l.width
	}
	return 0
}

// FitWidth is fitWidth for the command layer, so a hint printed under a
// listing is decided on and fitted with the same answer the listing used: a
// terminal's width, or 0 when stdout is not a terminal.
func FitWidth() int {
	return currentLayout().fitWidth()
}
