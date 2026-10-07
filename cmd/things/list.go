package main

import (
	"fmt"
	"strings"

	"github.com/alecthomas/kong"

	"github.com/ryanlewis/things-cli/internal/db"
	"github.com/ryanlewis/things-cli/internal/model"
	"github.com/ryanlewis/things-cli/internal/output"
	"github.com/ryanlewis/things-cli/internal/things"
)

type ListCmd struct {
	Args    []string `arg:"" optional:"" help:"View or project name. Views: today,inbox,upcoming,anytime,someday,repeating,logbook,trash,deadlines."`
	Project string   `help:"Filter by project name or UUID." short:"p"`
	Area    string   `help:"Filter by area name or UUID." short:"a"`
	Tag     string   `help:"Filter by tag name." short:"t"`

	OpenOnly         bool   `help:"Leave out the closed items the inbox, today, anytime, upcoming and someday views, and a --project or --area listing, show by default: the ones Things hasn't logged out of the list yet, which under the app's default Daily logging means closed today. Not supported on logbook or trash. --open-only=false overrides open_only in the config file." xor:"closed"`
	IncludeCompleted bool   `hidden:"" help:"No effect beyond overriding open_only in the config file: the closed items Things still shows are listed by default since --open-only was added. Accepted so existing scripts keep working." xor:"closed"`
	On               string `help:"Only tasks scheduled on YYYY-MM-DD (or RFC3339). On 'deadlines', filters by deadline; on 'upcoming', an undated task is matched by its deadline. Mutually exclusive with --from/--to."`
	From             string `help:"Only tasks scheduled on or after YYYY-MM-DD (or RFC3339). On 'deadlines', filters by deadline; on 'upcoming', an undated task is matched by its deadline."`
	To               string `help:"Only tasks scheduled on or before YYYY-MM-DD (or RFC3339). On 'deadlines', filters by deadline; on 'upcoming', an undated task is matched by its deadline."`
}

func (c *ListCmd) Run(kctx *kong.Context, d *Deps) error {
	database, err := d.Database()
	if err != nil {
		return err
	}

	view := db.ViewToday
	explicitView := false
	project := c.Project
	args := c.Args

	if len(args) > 0 && db.ValidView(args[0]) {
		view = args[0]
		explicitView = true
		args = args[1:]
	}
	if project == "" && len(args) > 0 {
		project = strings.Join(args, " ")
	}

	// A filter names what to list, so on its own it covers every open task
	// (and, for a project or area, those closed today and not yet logged)
	// rather than the Today slice the bare `things` default would apply
	// (issue #140). An explicit view still wins: `things today --project X`
	// is today within X, and says so in the output.
	filtered := project != "" || c.Area != "" || c.Tag != ""
	if filtered && !explicitView {
		view = db.ViewProject
	}

	// The views the app keeps a just-closed item visible in — inbox, today,
	// anytime, upcoming and someday (issues #238, #293), and a named
	// project's or area's contents (issue #295) — list it by default, as the
	// app does. --include-completed used to be how to ask for it and is now a
	// no-op; it is still rejected where it never applied (a bare --tag sweep
	// included), as it was, so a script that ran before runs the same.
	if c.IncludeCompleted && !db.CompletableView(view, project != "", c.Area != "") {
		names := db.CompletableViewNames()
		return fmt.Errorf("--include-completed is only supported on the %s and %s views and on a --project or --area listing with no view, not %q; it has no effect now, since those list the closed items Things still shows by default, so drop it",
			strings.Join(names[:len(names)-1], ", "), names[len(names)-1], view)
	}
	// --open-only is a no-op on the views that list only open rows anyway,
	// so an agent can pass it everywhere. logbook and trash are the two that
	// list closed rows as their whole point, and dropping them would leave
	// nothing true to say, so those reject it. open_only in the config file is
	// a default for the lists it applies to, not a request, so there it is
	// dropped instead.
	if c.OpenOnly && (view == db.ViewLogbook || view == db.ViewTrash) {
		if !flagResolved(kctx, "open-only") {
			return fmt.Errorf("--open-only is not supported on the %q view", view)
		}
		c.OpenOnly = false
	}

	// someday lists only what has no parent project, so narrowing it to one
	// could never match a row (issue #211). Say so rather than print an empty
	// list, the same way an impossible date filter is rejected.
	if project != "" && !db.ProjectFilterableView(view) {
		return fmt.Errorf("--project is not supported on the %q view: it lists only items with no parent project; use `things --project %q` for a project's own tasks", view, project)
	}

	filter := db.TaskFilter{
		Project:  project,
		Area:     c.Area,
		Tag:      c.Tag,
		OpenOnly: c.OpenOnly,
	}
	if err := applyDateFilters(&filter, view, c.On, c.From, c.To); err != nil {
		return err
	}

	tasks, err := database.ListTasks(view, filter)
	if err != nil {
		return err
	}
	cacheTaskUUIDs(d, c.commandLine(d, view, project), tasks)

	// A filtered listing off a view is a slice of that view, not the whole
	// project/area/tag — label it so the group header can't be read as the
	// full set. The "project" view is that full set, so it needs no label.
	viewLabel := ""
	if filtered && view != db.ViewProject {
		viewLabel = view
	}
	if err := output.PrintTaskList(d.Stdout, tasks, d.JSON, viewLabel); err != nil {
		return err
	}
	noteEmptyRepeatingProject(d, database, view, project, len(tasks))
	return printAgentHint(d, len(tasks))
}

// commandLine renders the listing as the user could type it again, for the
// cache to record alongside the rows (issue #265). It is built from the
// resolved view and project rather than the raw arguments, so `things "Some
// Project"` and `things --project "Some Project"` both come back as the
// spelling that always works. --db comes along when the flag supplied it, so
// the re-run reads the database this listing came from rather than the default.
func (c *ListCmd) commandLine(d *Deps, view, project string) string {
	parts := append([]string{"things"}, globalFlags(d)...)
	// "project" is not a view name a user can type; it is what a bare filter
	// resolves to, and the filter flags below say the same thing.
	if view != db.ViewProject {
		parts = append(parts, view)
	}
	for _, f := range []struct{ flag, value string }{
		{"--project", project},
		{"--area", c.Area},
		{"--tag", c.Tag},
		{"--on", c.On},
		{"--from", c.From},
		{"--to", c.To},
	} {
		if f.value != "" {
			parts = append(parts, f.flag, shellQuote(f.value))
		}
	}
	// The re-run reads the same config file, so a --open-only=false that
	// overrode it has to come along or the re-run would list fewer rows.
	if c.OpenOnly {
		parts = append(parts, "--open-only")
	} else if configOpenOnly(d) {
		parts = append(parts, "--open-only=false")
	}
	return strings.Join(parts, " ")
}

// flagResolved reports whether the named flag took its value from a resolver,
// which here means the config file, rather than from the command line.
func flagResolved(kctx *kong.Context, name string) bool {
	if kctx == nil {
		return false
	}
	for _, p := range kctx.Path {
		if p.Resolved && p.Flag != nil && p.Flag.Name == name {
			return true
		}
	}
	return false
}

// configOpenOnly reports whether the config file sets open_only.
func configOpenOnly(d *Deps) bool {
	for _, s := range d.config().Settings() {
		if s.Key == "open_only" {
			return s.Value == true
		}
	}
	return false
}

// noteEmptyRepeatingProject explains an empty listing whose --project named a
// repeating project template. Since issue #171 the to-dos inside one are kept
// out of every open view, so the listing is empty by design and said nothing
// about why (issue #174).
//
// The note goes to the warning stream in both modes, so a --json consumer
// still reads a well-formed array on stdout.
//
// It is held back on the views that keep templates — trash, logbook and
// repeating — where a template's to-dos do list once they are trashed or
// closed. An empty logbook there means none has closed yet, not that the view
// hides them, and saying otherwise would send the reader away from the one
// view that would have shown the row.
//
// A failure of the extra lookup is swallowed rather than returned: the listing
// itself already succeeded, and a note that could not be worked out is not a
// reason to fail a read that did.
func noteEmptyRepeatingProject(d *Deps, database *db.DB, view, project string, listed int) {
	if project == "" || listed > 0 || !db.HidesTemplateContentsView(view) {
		return
	}
	repeating, err := database.NamesRepeatingProject(project)
	if err != nil || !repeating {
		return
	}
	fmt.Fprintf(d.errOut(), "note: %q is a repeating project template, so its to-dos are not listed; `things repeating` lists the templates themselves\n", project)
}

func applyDateFilters(filter *db.TaskFilter, view, on, from, to string) error {
	if on == "" && from == "" && to == "" {
		return nil
	}
	if !db.DateFilterableView(view) {
		return fmt.Errorf("--on/--from/--to are not supported on the %q view", view)
	}
	if on != "" && (from != "" || to != "") {
		return fmt.Errorf("--on cannot be combined with --from/--to")
	}

	parse := func(flag, raw string) (*model.ThingsDate, error) {
		if raw == "" {
			return nil, nil
		}
		t, err := things.ParseListDate(flag, raw)
		if err != nil {
			return nil, err
		}
		d := model.ThingsDateFromTime(t)
		return &d, nil
	}

	var err error
	if filter.On, err = parse("on", on); err != nil {
		return err
	}
	if filter.From, err = parse("from", from); err != nil {
		return err
	}
	if filter.To, err = parse("to", to); err != nil {
		return err
	}
	if filter.From != nil && filter.To != nil && *filter.From > *filter.To {
		return fmt.Errorf("--from %s is after --to %s", filter.From, filter.To)
	}
	return nil
}

type ProjectsCmd struct {
	Area      string `help:"Filter by area name or UUID." short:"a"`
	Completed bool   `help:"Include completed projects." default:"false"`
}

func (c *ProjectsCmd) Run(d *Deps) error {
	database, err := d.Database()
	if err != nil {
		return err
	}
	projects, err := database.ListProjects(c.Area, c.Completed)
	if err != nil {
		return err
	}
	return output.PrintProjects(d.Stdout, projects, d.JSON)
}

type AreasCmd struct{}

func (c *AreasCmd) Run(d *Deps) error {
	database, err := d.Database()
	if err != nil {
		return err
	}
	areas, err := database.ListAreas()
	if err != nil {
		return err
	}
	return output.PrintAreas(d.Stdout, areas, d.JSON)
}

type TagsCmd struct{}

func (c *TagsCmd) Run(d *Deps) error {
	database, err := d.Database()
	if err != nil {
		return err
	}
	tags, err := database.ListTags()
	if err != nil {
		return err
	}
	return output.PrintTags(d.Stdout, tags, d.JSON)
}

type ShowCmd struct {
	Task  string `arg:"" required:"" help:"Task title, UUID, or numeric index from last list."`
	Agent bool   `help:"Print a self-contained Markdown brief for handing the item to an agent. Not combinable with --json."`
}

func (c *ShowCmd) Run(d *Deps) error {
	// Before the lookup: an output format the CLI cannot serve is a mistake in
	// the invocation, not something to report after the work is done.
	if c.Agent {
		if err := checkAgentFormat(d); err != nil {
			return err
		}
	}
	database, err := d.Database()
	if err != nil {
		return err
	}
	task, err := resolveTask(d, c.Task, database)
	if err != nil {
		return err
	}
	if c.Agent {
		items, err := database.GetChecklistItems(task.UUID)
		if err != nil {
			return err
		}
		return showAgentBrief(d, database, task, items)
	}
	return printItem(d, database, task)
}

// printItem prints task with its checklist, as `things show` does. A
// confirmed edit prints through it too, so the two outputs cannot drift.
func printItem(d *Deps, database *db.DB, task *model.Task) error {
	items, err := database.GetChecklistItems(task.UUID)
	if err != nil {
		return err
	}
	return output.PrintTaskWithChecklist(d.Stdout, task, items, d.JSON)
}

type SearchCmd struct {
	Query string `arg:"" required:"" help:"Search query."`
}

func (c *SearchCmd) Run(d *Deps) error {
	database, err := d.Database()
	if err != nil {
		return err
	}
	tasks, err := database.SearchTasks(c.Query)
	if err != nil {
		return err
	}
	command := append([]string{"things"}, globalFlags(d)...)
	command = append(command, "search", shellQuote(c.Query))
	cacheTaskUUIDs(d, strings.Join(command, " "), tasks)
	// Search results are a listing like `list`, backed by the same cache, so
	// they share PrintTaskList's path (and hint) rather than the bare Print
	// ListCmd used to diverge to; an empty view label prints identically to
	// the old output.Print(tasks) call did.
	if err := output.PrintTaskList(d.Stdout, tasks, d.JSON, ""); err != nil {
		return err
	}
	return printAgentHint(d, len(tasks))
}
