package main

import (
	"fmt"
	"strings"

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

	IncludeCompleted bool   `help:"On the today and anytime views, also show items closed today that Things hasn't logged out of the list yet (UI-parity). Not supported on other views."`
	On               string `help:"Only tasks scheduled on YYYY-MM-DD (or RFC3339). On 'deadlines', filters by deadline. Mutually exclusive with --from/--to."`
	From             string `help:"Only tasks scheduled on or after YYYY-MM-DD (or RFC3339). On 'deadlines', filters by deadline."`
	To               string `help:"Only tasks scheduled on or before YYYY-MM-DD (or RFC3339). On 'deadlines', filters by deadline."`
}

func (c *ListCmd) Run(d *Deps) error {
	database, err := d.Database()
	if err != nil {
		return err
	}

	view := "today"
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
	// rather than the Today slice the bare `things` default would apply
	// (issue #140). An explicit view still wins: `things today --project X`
	// is today within X, and says so in the output.
	filtered := project != "" || c.Area != "" || c.Tag != ""
	if filtered && !explicitView {
		view = "project"
	}

	// --include-completed only changes the views the app keeps a just-closed
	// item visible in — today and anytime (issue #238). Reject it elsewhere
	// (including when a filter defaults today → project) rather than silently
	// ignoring it, matching how --on/--from/--to reject views.
	if c.IncludeCompleted && !db.CompletableView(view) {
		return fmt.Errorf("--include-completed is only supported on the %s views, not %q; name the view explicitly, e.g. `things today --project NAME`",
			strings.Join(db.CompletableViewNames(), " and "), view)
	}

	// someday lists only what has no parent project, so narrowing it to one
	// could never match a row (issue #211). Say so rather than print an empty
	// list, the same way an impossible date filter is rejected.
	if project != "" && !db.ProjectFilterableView(view) {
		return fmt.Errorf("--project is not supported on the %q view: it lists only items with no parent project; use `things --project %q` for a project's own tasks", view, project)
	}

	filter := db.TaskFilter{
		Project:          project,
		Area:             c.Area,
		Tag:              c.Tag,
		IncludeCompleted: c.IncludeCompleted,
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
	if filtered && view != "project" {
		viewLabel = view
	}
	if err := output.PrintTaskList(d.Stdout, tasks, d.JSON, viewLabel); err != nil {
		return err
	}
	noteEmptyRepeatingProject(d, database, project, len(tasks))
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
	if view != "project" {
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
			parts = append(parts, f.flag, quoteArg(f.value))
		}
	}
	if c.IncludeCompleted {
		parts = append(parts, "--include-completed")
	}
	return strings.Join(parts, " ")
}

// noteEmptyRepeatingProject explains an empty listing whose --project named a
// repeating project template. Since issue #171 the to-dos inside one are kept
// out of every open view, so the listing is empty by design and said nothing
// about why (issue #174).
//
// The note goes to the warning stream in both modes, so a --json consumer
// still reads a well-formed array on stdout.
//
// A failure of the extra lookup is swallowed rather than returned: the listing
// itself already succeeded, and a note that could not be worked out is not a
// reason to fail a read that did.
func noteEmptyRepeatingProject(d *Deps, database *db.DB, project string, listed int) {
	if project == "" || listed > 0 {
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
	return output.Print(d.Stdout, projects, d.JSON)
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
	return output.Print(d.Stdout, areas, d.JSON)
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
	return output.Print(d.Stdout, tags, d.JSON)
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
	items, err := database.GetChecklistItems(task.UUID)
	if err != nil {
		return err
	}
	if c.Agent {
		return showAgentBrief(d, database, task, items)
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
	command = append(command, "search", quoteArg(c.Query))
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
