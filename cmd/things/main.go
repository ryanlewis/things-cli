package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/alecthomas/kong"
	"github.com/mattn/go-isatty"
	"github.com/willabides/kongplete"

	"github.com/ryanlewis/things-cli/internal/cache"
	"github.com/ryanlewis/things-cli/internal/config"
	"github.com/ryanlewis/things-cli/internal/db"
	"github.com/ryanlewis/things-cli/internal/model"
	"github.com/ryanlewis/things-cli/internal/output"
	"github.com/ryanlewis/things-cli/internal/skill"
	"github.com/ryanlewis/things-cli/internal/things"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

type CLI struct {
	JSON    bool             `help:"Output as JSON." short:"j" default:"false"`
	Color   string           `help:"Color mode (auto|always|never)." enum:"auto,always,never" default:"auto"`
	DB      string           `help:"Override database path." type:"path"`
	Config  string           `help:"Path to the TOML config file (default ~/.config/things-cli/config.toml)." placeholder:"PATH"`
	Version kong.VersionFlag `help:"Print version and exit." short:"v"`

	NoVerify bool `help:"Skip the read-back that confirms a complete/cancel, tag creation, or an import's status changes actually landed." name:"no-verify" default:"false"`

	Hints bool `help:"Print the hint line under a plain task listing. Use --no-hints to turn it off." negatable:"" default:"true"`

	List     ListCmd     `cmd:"" help:"List tasks (today,inbox,upcoming,anytime,someday,repeating,logbook,trash,deadlines). Use as: things today, things inbox, etc." default:"withargs"`
	Projects ProjectsCmd `cmd:"" help:"List projects."`
	Areas    AreasCmd    `cmd:"" help:"List areas."`
	Tags     TagsCmd     `cmd:"" help:"List tags."`
	Tag      TagCmd      `cmd:"" help:"Manage tags."`
	Show     ShowCmd     `cmd:"" help:"Show task detail."`
	Add      AddCmd      `cmd:"" help:"Create a new task."`
	Project  ProjectCmd  `cmd:"" help:"Manage projects."`
	Edit     EditCmd     `cmd:"" help:"Edit a task via the Things URL scheme."`
	Complete CompleteCmd `cmd:"" help:"Mark a task or project as completed."`
	Cancel   CancelCmd   `cmd:"" help:"Cancel a task or project."`
	Search   SearchCmd   `cmd:"" help:"Search tasks by title or notes."`
	Log      LogCmd      `cmd:"" help:"Move completed and cancelled items from Today to the Logbook (Items → Log Completed)."`
	Open     OpenCmd     `cmd:"" help:"Reveal a task, project, area, tag, or built-in list in Things3."`
	Import   ImportCmd   `cmd:"" help:"Batch create/update via the Things JSON URL scheme. Reads JSON from stdin or --file."`
	Skill    SkillCmd    `cmd:"" help:"Manage the bundled agent skill (Claude Code, etc.)."`
	Conf     ConfigCmd   `cmd:"" name:"config" help:"Inspect and create the config file that supplies flag defaults."`
	Ver      VersionCmd  `cmd:"" name:"version" help:"Print version and exit."`

	Completions CompletionsCmd `cmd:"" help:"Print a shell completion script (bash|zsh|fish)."`
}

// Deps carries cross-cutting state into each command's Run method. The DB is
// opened lazily so commands that don't touch it skip the FindDBPath/Open work,
// and tests can pre-populate DB with an in-memory SQLite handle.
type Deps struct {
	DB     *db.DB
	DBPath string
	JSON   bool
	Stdout io.Writer
	Stderr io.Writer

	// NoVerify skips the post-write read-back on complete/cancel.
	NoVerify bool

	// Hints allows the pointer line printed under a plain listing. Off means
	// the user has said they know the CLI; see printAgentHint for the other
	// conditions that suppress it.
	Hints bool

	// Config is the config file that seeded the flag defaults. `things config`
	// reports on it; every other command has already had its defaults applied
	// by the time it runs.
	Config *config.File
}

// config returns the loaded config file. main always supplies one, so a nil
// Config means a Deps built by hand in a test. The fallback deliberately does
// not resolve the real default path: a command that wrote to it would be
// writing to the developer's own config file rather than the temp HOME the
// test set up.
func (d *Deps) config() *config.File {
	if d.Config == nil {
		d.Config = &config.File{Source: config.SourceDefault}
	}
	return d.Config
}

// errOut is where warnings go. Tests leave Stderr nil and capture os.Stderr,
// or set it to a buffer to assert on the text.
func (d *Deps) errOut() io.Writer {
	if d.Stderr == nil {
		return os.Stderr
	}
	return d.Stderr
}

// interactive reports whether the process may prompt the user. --json means a
// machine is reading stdout, so a prompt would hang it — the flag implies
// non-interactive regardless of whether stdin is a terminal (issue #152).
func (d *Deps) interactive() bool {
	return !d.JSON && isInteractive()
}

// Database returns the lazily-opened DB. Subsequent calls return the same
// handle. Callers must call (*Deps).Close to release it.
func (d *Deps) Database() (*db.DB, error) {
	if d.DB != nil {
		return d.DB, nil
	}
	path := d.DBPath
	if path == "" {
		p, err := db.FindDBPath()
		if err != nil {
			return nil, err
		}
		path = p
	}
	// SQLite creates a missing file rather than refusing to open it, so a path
	// that is not there would otherwise surface as "no such table" much later.
	// The check lives here rather than on the --db flag so that a stale path in
	// the config file does not break the commands that never read the database
	// — `config path` and `config show` are how you find out it is stale.
	if _, err := os.Stat(path); err != nil {
		if cfg := d.config(); cfg.SetsDB(path) {
			return nil, &config.Error{Path: cfg.Path, Err: fmt.Errorf("db: %s", err)}
		}
		return nil, fmt.Errorf("cannot open the Things database: %s", err)
	}
	database, err := db.Open(path)
	if err != nil {
		return nil, err
	}
	d.DB = database
	return database, nil
}

func (d *Deps) Close() {
	if d.DB != nil {
		_ = d.DB.Close()
		d.DB = nil
	}
}

type VersionCmd struct{}

func (c *VersionCmd) Run(d *Deps) error {
	fmt.Fprintf(d.Stdout, "things %s (commit %s, built %s)\n", version, commit, date)
	return nil
}

type ListCmd struct {
	Args    []string `arg:"" optional:"" help:"View or project name. Views: today,inbox,upcoming,anytime,someday,repeating,logbook,trash,deadlines."`
	Project string   `help:"Filter by project name or UUID." short:"p"`
	Area    string   `help:"Filter by area name or UUID." short:"a"`
	Tag     string   `help:"Filter by tag name." short:"t"`

	IncludeCompleted bool   `help:"On the today view, also show completed/cancelled items Things hasn't logged out of Today yet (UI-parity). Not supported on other views."`
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

	// --include-completed only changes the today view. Reject it elsewhere
	// (including when a filter defaults today → project) rather than silently
	// ignoring it, matching how --on/--from/--to reject views.
	if c.IncludeCompleted && view != "today" {
		return fmt.Errorf("--include-completed is only supported on the %q view, not %q; name the view explicitly, e.g. `things today --project NAME`", "today", view)
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
	cacheTaskUUIDs(tasks)

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
	return printAgentHint(d, len(tasks))
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

type AddCmd struct {
	Title     string `arg:"" required:"" help:"Task title."`
	Notes     string `help:"Notes for the task."`
	When      string `help:"Schedule: today|tomorrow|evening|anytime|someday, YYYY-MM-DD, HH:MM, YYYY-MM-DD@HH:MM, or RFC3339."`
	Deadline  string `help:"Deadline date (YYYY-MM-DD)."`
	Tags      string `help:"Comma-separated tags."`
	Checklist string `help:"Newline-separated checklist items."`
	Project   string `help:"Project name or UUID."`
	Heading   string `help:"Heading within project."`
	List      string `help:"List (project or area) name."`

	TagFlags
}

func (c *AddCmd) Run(d *Deps) error {
	if err := verifyTagStrings(d, c.TagFlags, &c.Tags); err != nil {
		return err
	}
	list := c.List
	if list == "" {
		list = c.Project
	}
	return things.AddTask(things.AddParams{
		Title:     c.Title,
		Notes:     c.Notes,
		When:      c.When,
		Deadline:  c.Deadline,
		Tags:      c.Tags,
		Checklist: expandNewlines(c.Checklist),
		Heading:   c.Heading,
		List:      list,
	})
}

type ProjectCmd struct {
	Add  ProjectAddCmd  `cmd:"" help:"Create a new project."`
	Edit ProjectEditCmd `cmd:"" help:"Edit a project via the Things URL scheme."`
}

type ProjectAddCmd struct {
	Title    string `arg:"" required:"" help:"Project title."`
	Notes    string `help:"Notes for the project."`
	When     string `help:"Schedule: today|tomorrow|evening|anytime|someday, YYYY-MM-DD, HH:MM, YYYY-MM-DD@HH:MM, or RFC3339."`
	Deadline string `help:"Deadline date (YYYY-MM-DD)."`
	Tags     string `help:"Comma-separated tags."`
	Area     string `help:"Area name or UUID."`
	Todos    string `help:"Newline-separated initial to-dos."`

	TagFlags
}

func (c *ProjectAddCmd) Run(d *Deps) error {
	if err := verifyTagStrings(d, c.TagFlags, &c.Tags); err != nil {
		return err
	}
	return things.AddProject(things.AddProjectParams{
		Title:    c.Title,
		Notes:    c.Notes,
		When:     c.When,
		Deadline: c.Deadline,
		Tags:     c.Tags,
		Area:     c.Area,
		Todos:    expandNewlines(c.Todos),
	})
}

type ProjectEditCmd struct {
	Project string `arg:"" required:"" help:"Project title, UUID, or numeric index from last list."`

	Title *string `help:"Replace title."`

	Notes        *string `help:"Replace notes."`
	PrependNotes *string `help:"Prepend text to notes." name:"prepend-notes"`
	AppendNotes  *string `help:"Append text to notes." name:"append-notes"`

	When     *string `help:"Schedule: today|tomorrow|evening|anytime|someday, YYYY-MM-DD, HH:MM, YYYY-MM-DD@HH:MM, RFC3339, or empty to clear."`
	Deadline *string `help:"Deadline date (YYYY-MM-DD) or empty to clear."`

	Tags    *string `help:"Replace all tags (comma-separated)."`
	AddTags *string `help:"Add tags (comma-separated)." name:"add-tags"`

	Area   *string `help:"Move to area by name."`
	AreaID *string `help:"Move to area by UUID." name:"area-id"`

	Complete  bool `help:"Mark the project as completed." xor:"status"`
	Cancel    bool `help:"Mark the project as canceled." xor:"status"`
	Duplicate bool `help:"Duplicate the project before applying edits."`
	Reveal    bool `help:"Reveal the project in Things after editing."`

	TagFlags
}

func (c *ProjectEditCmd) Run(d *Deps) error {
	database, err := d.Database()
	if err != nil {
		return err
	}
	project, err := resolveTask(d, c.Project, database)
	if err != nil {
		return err
	}
	if project.Type != model.TypeProject {
		return fmt.Errorf("not a project: %s", project.Title)
	}
	if err := checkRepeating(project, restrictedEdits(c.When, c.Deadline, c.Complete, c.Cancel, c.Duplicate)); err != nil {
		return err
	}
	// After checkRepeating: no point warning about tags on an edit Things
	// is going to refuse anyway.
	if err := verifyTagStrings(d, c.TagFlags, c.Tags, c.AddTags); err != nil {
		return err
	}

	token, _ := database.GetAuthToken()
	update := func() error {
		return things.UpdateProject(things.UpdateProjectParams{
			ID:           project.UUID,
			AuthToken:    token,
			Title:        c.Title,
			Notes:        c.Notes,
			PrependNotes: c.PrependNotes,
			AppendNotes:  c.AppendNotes,
			When:         c.When,
			Deadline:     c.Deadline,
			Tags:         c.Tags,
			AddTags:      c.AddTags,
			Area:         c.Area,
			AreaID:       c.AreaID,
			Completed:    c.Complete,
			Canceled:     c.Cancel,
			Duplicate:    c.Duplicate,
			Reveal:       c.Reveal,
		})
	}
	return applyEditStatusWrite(d, database, project, c.Complete, c.Cancel, c.Duplicate, update)
}

type EditCmd struct {
	Task string `arg:"" required:"" help:"Task title, UUID, or numeric index from last list."`

	Title *string `help:"Replace title."`

	Notes        *string `help:"Replace notes."`
	PrependNotes *string `help:"Prepend text to notes." name:"prepend-notes"`
	AppendNotes  *string `help:"Append text to notes." name:"append-notes"`

	When     *string `help:"Schedule: today|tomorrow|evening|anytime|someday, YYYY-MM-DD, HH:MM, YYYY-MM-DD@HH:MM, RFC3339, or empty to clear."`
	Deadline *string `help:"Deadline date (YYYY-MM-DD) or empty to clear."`

	Tags    *string `help:"Replace all tags (comma-separated)."`
	AddTags *string `help:"Add tags (comma-separated)." name:"add-tags"`

	Checklist        *string `help:"Replace checklist items (newline-separated)."`
	PrependChecklist *string `help:"Prepend checklist items (newline-separated)." name:"prepend-checklist"`
	AppendChecklist  *string `help:"Append checklist items (newline-separated)." name:"append-checklist"`

	List      *string `help:"Move to list/project by name."`
	ListID    *string `help:"Move to list/project by UUID." name:"list-id"`
	Heading   *string `help:"Set heading within project by name."`
	HeadingID *string `help:"Set heading by UUID." name:"heading-id"`

	Complete  bool `help:"Mark the task as completed." xor:"status"`
	Cancel    bool `help:"Mark the task as canceled." xor:"status"`
	Duplicate bool `help:"Duplicate the task before applying edits."`
	Reveal    bool `help:"Reveal the task in Things after editing."`

	TagFlags
}

func (c *EditCmd) Run(d *Deps) error {
	database, err := d.Database()
	if err != nil {
		return err
	}
	task, err := resolveTask(d, c.Task, database)
	if err != nil {
		return err
	}
	if err := checkRepeating(task, restrictedEdits(c.When, c.Deadline, c.Complete, c.Cancel, c.Duplicate)); err != nil {
		return err
	}
	// After checkRepeating: no point warning about tags on an edit Things
	// is going to refuse anyway.
	if err := verifyTagStrings(d, c.TagFlags, c.Tags, c.AddTags); err != nil {
		return err
	}

	token, _ := database.GetAuthToken()
	update := func() error {
		return things.UpdateTask(things.UpdateParams{
			ID:               task.UUID,
			AuthToken:        token,
			Title:            c.Title,
			Notes:            c.Notes,
			PrependNotes:     c.PrependNotes,
			AppendNotes:      c.AppendNotes,
			When:             c.When,
			Deadline:         c.Deadline,
			Tags:             c.Tags,
			AddTags:          c.AddTags,
			Checklist:        expandNewlinesPtr(c.Checklist),
			PrependChecklist: expandNewlinesPtr(c.PrependChecklist),
			AppendChecklist:  expandNewlinesPtr(c.AppendChecklist),
			List:             c.List,
			ListID:           c.ListID,
			Heading:          c.Heading,
			HeadingID:        c.HeadingID,
			Completed:        c.Complete,
			Canceled:         c.Cancel,
			Duplicate:        c.Duplicate,
			Reveal:           c.Reveal,
		})
	}
	return applyEditStatusWrite(d, database, task, c.Complete, c.Cancel, c.Duplicate, update)
}

type CompleteCmd struct {
	Task string `arg:"" required:"" help:"Task title, UUID, or numeric index from last list."`
	ConfirmFlags
}

func (c *CompleteCmd) Run(d *Deps) error {
	database, err := d.Database()
	if err != nil {
		return err
	}
	task, err := resolveTask(d, c.Task, database)
	if err != nil {
		return err
	}
	if err := checkRepeating(task, []string{"completed"}); err != nil {
		return err
	}
	write := func() error { return things.CompleteTask(task.UUID) }
	if task.Type == model.TypeProject {
		if err := confirmProjectStatusChange(d, c.Yes, "Complete", task.Title); err != nil {
			return err
		}
		write = func() error { return things.CompleteProject(task.UUID) }
	}
	return applyStatusWrite(d, database, task, model.StatusCompleted, write)
}

type CancelCmd struct {
	Task string `arg:"" required:"" help:"Task title, UUID, or numeric index from last list."`
	ConfirmFlags
}

func (c *CancelCmd) Run(d *Deps) error {
	database, err := d.Database()
	if err != nil {
		return err
	}
	task, err := resolveTask(d, c.Task, database)
	if err != nil {
		return err
	}
	if err := checkRepeating(task, []string{"canceled"}); err != nil {
		return err
	}
	write := func() error { return things.CancelTask(task.UUID) }
	if task.Type == model.TypeProject {
		if err := confirmProjectStatusChange(d, c.Yes, "Cancel", task.Title); err != nil {
			return err
		}
		write = func() error { return things.CancelProject(task.UUID) }
	}
	return applyStatusWrite(d, database, task, model.StatusCancelled, write)
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
	cacheTaskUUIDs(tasks)
	if err := output.Print(d.Stdout, tasks, d.JSON); err != nil {
		return err
	}
	// Search results are a numbered listing backed by the same cache as `list`,
	// so the hint applies here too.
	return printAgentHint(d, len(tasks))
}

type LogCmd struct{}

func (c *LogCmd) Run(_ *Deps) error {
	return things.LogCompleted()
}

type SkillCmd struct {
	Install   SkillInstallCmd   `cmd:"" help:"Install the bundled skill for an AI coding agent."`
	Uninstall SkillUninstallCmd `cmd:"" help:"Remove the bundled skill for an AI coding agent."`
	Show      SkillShowCmd      `cmd:"" help:"Print the skill source (neutral, or rendered for an agent)."`
	List      SkillListCmd      `cmd:"" help:"List supported agents."`
}

type SkillInstallCmd struct {
	Agent string `arg:"" required:"" help:"Target agent (${skill_agents})."`
	Path  string `help:"Override destination directory."`
	Yes   bool   `help:"Assume yes — overwrite without prompting." short:"y"`
}

func (c *SkillInstallCmd) Run(d *Deps) error {
	agent, err := skill.Lookup(c.Agent)
	if err != nil {
		return err
	}
	dir, err := resolveSkillDir(agent, c.Path)
	if err != nil {
		return err
	}
	if skill.Exists(agent, dir) && !c.Yes {
		if !d.interactive() {
			return fmt.Errorf("skill already installed at %s — pass -y to overwrite", dir)
		}
		if !confirmAction(d, fmt.Sprintf("Skill already installed at %s. Overwrite?", dir)) {
			return fmt.Errorf("cancelled")
		}
	}
	if err := skill.Install(agent, dir); err != nil {
		return err
	}
	fmt.Fprintf(d.Stdout, "Installed %s skill to %s\n", agent.Name(), dir)
	return nil
}

type SkillUninstallCmd struct {
	Agent string `arg:"" required:"" help:"Target agent (${skill_agents})."`
	Path  string `help:"Override directory to uninstall from."`
	Yes   bool   `help:"Assume yes — uninstall without prompting." short:"y"`
}

func (c *SkillUninstallCmd) Run(d *Deps) error {
	agent, err := skill.Lookup(c.Agent)
	if err != nil {
		return err
	}
	dir, err := resolveSkillDir(agent, c.Path)
	if err != nil {
		return err
	}
	present := skill.InstalledFiles(agent, dir)
	if len(present) == 0 {
		return fmt.Errorf("no %s skill installed at %s", agent.Name(), dir)
	}
	fmt.Fprintf(os.Stderr, "Will remove %d file(s) from %s:\n", len(present), dir)
	for _, f := range present {
		fmt.Fprintf(os.Stderr, "  - %s\n", f)
	}
	if !c.Yes {
		if !d.interactive() {
			return fmt.Errorf("refusing to uninstall non-interactively — pass -y to confirm")
		}
		if !confirmAction(d, fmt.Sprintf("Remove %s skill at %s?", agent.Name(), dir)) {
			return fmt.Errorf("cancelled")
		}
	}
	if err := skill.Uninstall(agent, dir); err != nil {
		return err
	}
	fmt.Fprintf(d.Stdout, "Removed %s skill from %s\n", agent.Name(), dir)
	return nil
}

type SkillShowCmd struct {
	Agent string `arg:"" optional:"" help:"Render for a specific agent (${skill_agents}); default is the neutral source."`
}

func (c *SkillShowCmd) Run(d *Deps) error {
	if c.Agent == "" {
		fmt.Fprint(d.Stdout, skill.SkillMD())
		return nil
	}
	agent, err := skill.Lookup(c.Agent)
	if err != nil {
		return err
	}
	for fname, content := range agent.Files() {
		fmt.Fprintf(d.Stdout, "# %s\n%s", fname, content)
	}
	return nil
}

type SkillListCmd struct{}

func (c *SkillListCmd) Run(d *Deps) error {
	for _, a := range skill.Agents() {
		dir, err := a.DefaultDir()
		if err != nil {
			dir = "(unknown)"
		}
		status := "not installed"
		if skill.Exists(a, dir) {
			status = "installed"
		}
		fmt.Fprintf(d.Stdout, "%-10s %s  (%s)\n", a.Name(), dir, status)
	}
	fmt.Fprintf(d.Stdout, "\nUse `things skill install <agent>` (agents: %s)\n", skill.AgentNames())
	return nil
}

type ImportCmd struct {
	File   string `help:"Read JSON payload from this file instead of stdin." short:"f" type:"existingfile"`
	Reveal bool   `help:"Reveal the first created/updated item in Things after import."`

	TagFlags
}

func (c *ImportCmd) Run(d *Deps) error {
	database, err := d.Database()
	if err != nil {
		return err
	}
	var data []byte
	if c.File != "" {
		data, err = os.ReadFile(c.File)
		if err != nil {
			return fmt.Errorf("reading %s: %w", c.File, err)
		}
	} else {
		if isInteractive() {
			return fmt.Errorf("no JSON on stdin and no --file given")
		}
		data, err = io.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("reading stdin: %w", err)
		}
	}
	if err := validateImportJSON(data); err != nil {
		return err
	}
	if err := verifyTags(d, c.TagFlags, importTags(data)); err != nil {
		return err
	}
	// Refuse before anything is sent if any `operation: update` item would
	// change an attribute Things drops silently on a repeating item.
	plan, err := prepareImport(d, database, data)
	if err != nil {
		return err
	}
	token, err := database.GetAuthToken()
	if err != nil {
		// Don't fail the import — the payload may be create-only and not need
		// the token at all — but surface the read error so users debugging an
		// `operation: update` failure aren't left guessing.
		fmt.Fprintf(d.errOut(), "warning: could not read Things auth token: %v\n", err)
	}
	if err := things.ImportJSON(string(data), token, c.Reveal); err != nil {
		return err
	}
	return verifyImportStatuses(d, database, plan)
}

type OpenCmd struct {
	Ref        string `arg:"" optional:"" help:"Task/project UUID, numeric list index, title, or built-in list name (${builtin_lists})."`
	Project    string `help:"Open project by name or UUID." short:"p"`
	Area       string `help:"Open area by name or UUID." short:"a"`
	Tag        string `help:"Open tag by name or UUID." short:"t"`
	Query      string `help:"App-side quick find." short:"q"`
	Filter     string `help:"Tag filter on the shown list (comma-separated)."`
	Background bool   `help:"Don't bring Things to the foreground."`
}

func (c *OpenCmd) Run(d *Deps) error {
	database, err := d.Database()
	if err != nil {
		return err
	}

	flags := 0
	for _, s := range []string{c.Ref, c.Project, c.Area, c.Tag, c.Query} {
		if s != "" {
			flags++
		}
	}
	if flags == 0 {
		return fmt.Errorf("open: pass a reference, --project, --area, --tag, or --query")
	}
	if flags > 1 {
		return fmt.Errorf("open: pass only one of <ref>, --project, --area, --tag, --query")
	}

	params := things.ShowParams{Filter: c.Filter, Background: c.Background}

	resolveUUID := func(kind, name string, find func(string) (string, error)) (string, error) {
		uuid, err := find(name)
		if err != nil {
			return "", err
		}
		if uuid == "" {
			return "", &notFoundError{Kind: kind, Query: name}
		}
		return uuid, nil
	}

	switch {
	case c.Query != "":
		params.Query = c.Query
	case c.Area != "":
		uuid, err := resolveUUID("area", c.Area, database.FindAreaUUID)
		if err != nil {
			return err
		}
		params.ID = uuid
	case c.Tag != "":
		uuid, err := resolveUUID("tag", c.Tag, database.FindTagUUID)
		if err != nil {
			return err
		}
		params.ID = uuid
	case c.Project != "":
		task, err := resolveTask(d, c.Project, database)
		if err != nil {
			return err
		}
		params.ID = task.UUID
	case things.IsBuiltinList(c.Ref):
		params.ID = c.Ref
	default:
		task, err := resolveTask(d, c.Ref, database)
		if err != nil {
			return err
		}
		params.ID = task.UUID
	}

	return things.Show(params)
}

func main() {
	var cli CLI

	// The config file supplies the defaults kong parses against, so it has to
	// be read before the parser is built. A file that could not be read does
	// not stop us here: it supplies no defaults, parsing carries on with the
	// built-in ones, and the failure is reported after we know which command
	// was asked for — because `things config` is how you find out what is
	// wrong with the file.
	cfg, cfgErr := loadConfig(os.Args[1:])

	parser := kong.Must(&cli, parserOptions(cfg)...)

	// Answer shell completion requests. When the shell invokes us with COMP_LINE
	// set — via the script emitted by `things completions <shell>` — this
	// computes candidates from the command tree and exits. For normal
	// invocations COMP_LINE is unset and this is a no-op.
	kongplete.Complete(parser)

	ctx, err := parser.Parse(os.Args[1:])
	if err != nil {
		asJSON := jsonRequested(cfg.JSON(), os.Args[1:])
		// kong's UsageOnError writes the usage block to stdout, which under
		// --json would leave a consumer parsing help text instead of the JSON
		// object it was promised. Render the failure as JSON instead — the
		// flag has to come from argv because parsing is what just failed.
		if asJSON {
			renderError(os.Stdout, os.Stderr, true, err)
			os.Exit(parseExitCode(err))
		}
		parser.FatalIfErrorf(err)
	}

	// Every command but the ones that report on the config file needs the file
	// to have loaded. --help and --version never get here: kong answers them
	// during Parse.
	if cfgErr != nil && !diagnosesConfig(ctx) {
		renderError(os.Stdout, os.Stderr, cli.JSON, cfgErr)
		os.Exit(2)
	}

	if err := output.SetColorMode(cli.Color); err != nil {
		renderError(os.Stdout, os.Stderr, cli.JSON, err)
		os.Exit(2)
	}

	deps := &Deps{DBPath: cli.DB, JSON: cli.JSON, Stdout: os.Stdout, Stderr: os.Stderr, NoVerify: cli.NoVerify, Hints: cli.Hints, Config: cfg}
	defer deps.Close()

	if err := ctx.Run(deps); err != nil {
		renderError(os.Stdout, os.Stderr, cli.JSON, err)
		var runCfgErr *config.Error
		if errors.As(err, &runCfgErr) {
			// A broken config file always exits 2, whether it was caught
			// before, during, or after parsing.
			os.Exit(2)
		}
		os.Exit(1)
	}
}

// parseExitCode is the status kong would have exited with for a parse
// failure: 80 for a usage error, 1 for one of the errors it raises without a
// code of its own (a resolver or a hook). The --json branch above reports the
// same failure kong would, so it has to exit the same way.
func parseExitCode(err error) int {
	var coder kong.ExitCoder
	if errors.As(err, &coder) {
		return coder.ExitCode()
	}
	return 1
}

// boolShorts are the single-letter flags that take no value. Only these can be
// followed by more flags in a cluster: kong lets the first short that takes a
// value swallow the rest of the cluster as that value. "f" is deliberately
// absent — it is boolean on `config init` but a value flag on `import`, and
// treating an ambiguous letter as value-taking only costs a fallback to kong's
// plain-text usage, while the reverse would misread a filename.
//
// TestBoolShortsMatchesGrammar keeps this in step with the CLI struct.
var boolShorts = map[byte]bool{'j': true, 'v': true, 'y': true}

// jsonRequested reports whether argv asks for --json, starting from def — the
// default the config file establishes. main needs the answer before kong has
// parsed anything, because a parse error still has to be rendered in the mode
// the caller asked for.
func jsonRequested(def bool, args []string) bool {
	asJSON := def
	for _, a := range args {
		if a == "--" {
			break
		}
		flag, value, hasValue := strings.Cut(a, "=")
		asks, takesValue := flagAsksJSON(flag)
		if !asks {
			continue
		}
		// --json=1 and --json=yes mean the same as --json. A value kong
		// would not accept is kong's to complain about — leave the mode as
		// it was rather than guess at what was meant.
		on := true
		if hasValue && takesValue {
			b, ok := kongBool(value)
			if !ok {
				continue
			}
			on = b
		}
		asJSON = on
	}
	return asJSON
}

// kongBool parses an explicit flag value the way kong's bool mapper does:
// true/1/yes and false/0/no, case-insensitively. strconv.ParseBool is a
// different set — it takes "t" and "T" and rejects "yes" — so using it here
// would disagree with the parser about what the caller asked for.
func kongBool(v string) (value, ok bool) {
	switch strings.ToLower(v) {
	case "true", "1", "yes":
		return true, true
	case "false", "0", "no":
		return false, true
	}
	return false, false
}

// flagAsksJSON reports whether one argv token names --json, and whether an
// "=value" on it would belong to --json rather than to another flag in the
// same cluster. Only a long flag really carries a value that way — kong does
// not split a short on "=" at all, so `-j=false` is a parse error either way
// — but answering for the cluster keeps a token like -vj=false from being
// read as plain -j.
//
// kong clusters boolean shorts, so -vj is two flags and asks for JSON just as
// -j does. Scanning stops at the first short that is not boolean, because that
// one takes the rest of the cluster as its value: -pj is --project=j.
func flagAsksJSON(flag string) (asks, takesValue bool) {
	if flag == "--json" {
		return true, true
	}
	if len(flag) < 2 || flag[0] != '-' || flag[1] == '-' {
		return false, false
	}
	for i := 1; i < len(flag); i++ {
		if flag[i] == 'j' {
			return true, i == len(flag)-1
		}
		if !boolShorts[flag[i]] {
			return false, false
		}
	}
	return false, false
}

// isInteractive reports whether stdin is a terminal. It is a var so tests can
// stub the terminal check — see (*Deps).interactive, which is what callers
// should use.
var isInteractive = func() bool {
	fd := os.Stdin.Fd()
	return isatty.IsTerminal(fd) || isatty.IsCygwinTerminal(fd)
}

// expandNewlines converts the literal two-character sequence `\n` into real
// newlines so users can pass multi-line values in a single shell-quoted flag
// (e.g. --todos "Draft\nShip"). Actual newlines in the input are preserved.
func expandNewlines(s string) string {
	return strings.ReplaceAll(s, `\n`, "\n")
}

func expandNewlinesPtr(p *string) *string {
	if p == nil {
		return nil
	}
	v := expandNewlines(*p)
	return &v
}

func resolveTask(d *Deps, ref string, database *db.DB) (*model.Task, error) {
	// Try numeric index from last list
	if n, err := strconv.Atoi(ref); err == nil && n >= 1 {
		uuids, cacheErr := cache.ReadLastList()
		if cacheErr == nil && n <= len(uuids) {
			t, err := database.GetTaskByUUID(uuids[n-1])
			if err != nil {
				return nil, err
			}
			if t != nil {
				return t, nil
			}
			return nil, &notFoundError{
				Kind:  "task",
				Query: ref,
				msg:   fmt.Sprintf("task #%d no longer exists (stale list cache — re-run list)", n),
			}
		}
	}

	task, err := database.GetTask(ref)
	if err == nil {
		return task, nil
	}

	var ambig *db.AmbiguousTaskError
	if !errors.As(err, &ambig) {
		return nil, err
	}

	if !d.interactive() {
		var b strings.Builder
		fmt.Fprintf(&b, "ambiguous task %q — matches %d tasks:\n", ambig.Query, len(ambig.Matches))
		for i, m := range ambig.Matches {
			fmt.Fprintf(&b, "  %d. %s  (%s)\n", i+1, m.Title, m.UUID)
		}
		fmt.Fprint(&b, "Re-run with a UUID or more specific string.")
		// Wrap rather than replace: the plain-text message stays exactly as
		// it was, while --json still sees the candidates (issue #152).
		return nil, &ambiguousRefError{msg: b.String(), inner: ambig}
	}

	// Interactive: prompt user to pick
	fmt.Fprintf(os.Stderr, "Multiple tasks match %q:\n", ambig.Query)
	for i, m := range ambig.Matches {
		project := ""
		if m.ProjectTitle != "" {
			project = "  (" + m.ProjectTitle + ")"
		}
		fmt.Fprintf(os.Stderr, "  %d. %s%s\n", i+1, m.Title, project)
	}
	fmt.Fprintf(os.Stderr, "Pick [1-%d]: ", len(ambig.Matches))

	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return nil, fmt.Errorf("cancelled")
	}
	choice, err := strconv.Atoi(strings.TrimSpace(scanner.Text()))
	if err != nil || choice < 1 || choice > len(ambig.Matches) {
		return nil, fmt.Errorf("invalid choice")
	}
	return &ambig.Matches[choice-1], nil
}

// confirmProjectStatusChange gates a project-wide complete/cancel behind a
// confirmation. When the run cannot prompt — piped stdin, or --json, which
// never prompts — say so instead of returning a bare "cancelled" the caller
// has no way to interpret. assumeYes is the caller's --yes: it answers the
// question up front, which is the only way a machine caller can get through
// this at all.
func confirmProjectStatusChange(d *Deps, assumeYes bool, verb, title string) error {
	if assumeYes {
		return nil
	}
	action := strings.ToLower(verb)
	if !d.interactive() {
		reason := "pass --yes, or re-run in a terminal"
		if d.JSON {
			reason = "pass --yes, or re-run without --json"
		}
		return fmt.Errorf("cancelled: %s project %q needs confirmation, and this run cannot prompt — %s", action, title, reason)
	}
	if !confirmAction(d, fmt.Sprintf("%s project %q? This will also %s all its tasks.", verb, title, action)) {
		return fmt.Errorf("cancelled")
	}
	return nil
}

func confirmAction(d *Deps, msg string) bool {
	if !d.interactive() {
		return false
	}
	fmt.Fprintf(os.Stderr, "%s [y/N]: ", msg)
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return false
	}
	answer := strings.TrimSpace(strings.ToLower(scanner.Text()))
	return answer == "y" || answer == "yes"
}

// validateImportJSON checks the payload is a non-empty JSON array — the shape
// the Things JSON URL scheme requires — without allocating on the happy path.
// On syntax errors it falls back to a full decode purely to extract the byte
// offset, which it converts to line/column so the user can jump to the bad
// byte in their editor.
func validateImportJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return fmt.Errorf("empty payload")
	}
	if !json.Valid(data) {
		// Re-decode to get an offset for the error message; this is the slow
		// path (only on invalid input) so the allocation doesn't matter.
		var v any
		err := json.Unmarshal(data, &v)
		var syn *json.SyntaxError
		if errors.As(err, &syn) {
			line, col := offsetToLineCol(data, syn.Offset)
			return fmt.Errorf("invalid JSON at line %d, column %d: %s", line, col, syn.Error())
		}
		return fmt.Errorf("invalid JSON: %w", err)
	}
	if trimmed[0] != '[' {
		return fmt.Errorf("payload must be a JSON array of items")
	}
	// Valid JSON starting with `[` is at minimum `[]`, so len >= 2.
	if len(bytes.TrimSpace(trimmed[1:len(trimmed)-1])) == 0 {
		return fmt.Errorf("payload array is empty")
	}
	return nil
}

func offsetToLineCol(data []byte, offset int64) (int, int) {
	if offset < 0 {
		offset = 0
	}
	if int(offset) > len(data) {
		offset = int64(len(data))
	}
	prefix := data[:offset]
	line := 1 + bytes.Count(prefix, []byte{'\n'})
	col := int(offset) - bytes.LastIndexByte(prefix, '\n')
	return line, col
}

func resolveSkillDir(agent skill.Agent, override string) (string, error) {
	if override != "" {
		return override, nil
	}
	return agent.DefaultDir()
}

func cacheTaskUUIDs(tasks []model.Task) {
	uuids := make([]string, len(tasks))
	for i, t := range tasks {
		uuids[i] = t.UUID
	}
	if err := cache.WriteLastList(uuids); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to cache task list: %v\n", err)
	}
}
