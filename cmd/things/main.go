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
	"github.com/ryanlewis/things-cli/internal/cache"
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
	DB      string           `help:"Override database path." type:"existingfile"`
	Version kong.VersionFlag `help:"Print version and exit." short:"v"`

	List     ListCmd     `cmd:"" help:"List tasks (today,inbox,upcoming,anytime,someday,logbook,trash,deadlines). Use as: things today, things inbox, etc." default:"withargs"`
	Projects ProjectsCmd `cmd:"" help:"List projects."`
	Areas    AreasCmd    `cmd:"" help:"List areas."`
	Tags     TagsCmd     `cmd:"" help:"List tags."`
	Show     ShowCmd     `cmd:"" help:"Show task detail."`
	Add      AddCmd      `cmd:"" help:"Create a new task."`
	Project  ProjectCmd  `cmd:"" help:"Manage projects."`
	Edit     EditCmd     `cmd:"" help:"Edit a task via the Things URL scheme."`
	Complete CompleteCmd `cmd:"" help:"Mark a task as completed."`
	Cancel   CancelCmd   `cmd:"" help:"Cancel a task."`
	Search   SearchCmd   `cmd:"" help:"Search tasks by title or notes."`
	Log      LogCmd      `cmd:"" help:"Move completed and cancelled items from Today to the Logbook (Items → Log Completed)."`
	Open     OpenCmd     `cmd:"" help:"Reveal a task, project, area, tag, or built-in list in Things3."`
	Import   ImportCmd   `cmd:"" help:"Batch create/update via the Things JSON URL scheme. Reads JSON from stdin or --file."`
	Skill    SkillCmd    `cmd:"" help:"Manage the bundled agent skill (Claude Code, etc.)."`
	Ver      VersionCmd  `cmd:"" name:"version" help:"Print version and exit."`
}

type VersionCmd struct{}

type ListCmd struct {
	Args    []string `arg:"" optional:"" help:"View or project name. Views: today,inbox,upcoming,anytime,someday,logbook,trash,deadlines."`
	Project string   `help:"Filter by project name or UUID." short:"p"`
	Area    string   `help:"Filter by area name or UUID." short:"a"`
	Tag     string   `help:"Filter by tag name." short:"t"`
}

type ProjectsCmd struct {
	Area      string `help:"Filter by area name or UUID."`
	Completed bool   `help:"Include completed projects." default:"false"`
}

type AreasCmd struct{}

type TagsCmd struct{}

type ShowCmd struct {
	Task string `arg:"" required:"" help:"Task title or UUID."`
}

type AddCmd struct {
	Title     string `arg:"" required:"" help:"Task title."`
	Notes     string `help:"Notes for the task."`
	When      string `help:"When to schedule (date, today, tomorrow, evening, etc.)."`
	Deadline  string `help:"Deadline date."`
	Tags      string `help:"Comma-separated tags."`
	Checklist string `help:"Newline-separated checklist items."`
	Project   string `help:"Project name or UUID."`
	Heading   string `help:"Heading within project."`
	List      string `help:"List (project or area) name."`
}

type ProjectCmd struct {
	Add ProjectAddCmd `cmd:"" help:"Create a new project."`
}

type ProjectAddCmd struct {
	Title    string `arg:"" required:"" help:"Project title."`
	Notes    string `help:"Notes for the project."`
	When     string `help:"When to schedule (date, today, tomorrow, evening, etc.)."`
	Deadline string `help:"Deadline date."`
	Tags     string `help:"Comma-separated tags."`
	Area     string `help:"Area name or UUID."`
	Todos    string `help:"Newline-separated initial to-dos."`
}

type EditCmd struct {
	Task string `arg:"" required:"" help:"Task title, UUID, or numeric index from last list."`

	Title *string `help:"Replace title."`

	Notes        *string `help:"Replace notes."`
	PrependNotes *string `help:"Prepend text to notes." name:"prepend-notes"`
	AppendNotes  *string `help:"Append text to notes." name:"append-notes"`

	When     *string `help:"When to schedule (date, today, tomorrow, evening, someday, anytime, or an ISO date)."`
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

	Complete  bool `help:"Mark the task as completed."`
	Cancel    bool `help:"Mark the task as canceled."`
	Duplicate bool `help:"Duplicate the task before applying edits."`
	Reveal    bool `help:"Reveal the task in Things after editing."`
}

type CompleteCmd struct {
	Task string `arg:"" required:"" help:"Task title or UUID."`
}

type CancelCmd struct {
	Task string `arg:"" required:"" help:"Task title or UUID."`
}

type SearchCmd struct {
	Query string `arg:"" required:"" help:"Search query."`
}

type LogCmd struct{}

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

type SkillUninstallCmd struct {
	Agent string `arg:"" required:"" help:"Target agent (${skill_agents})."`
	Path  string `help:"Override directory to uninstall from."`
	Yes   bool   `help:"Assume yes — uninstall without prompting." short:"y"`
}

type SkillShowCmd struct {
	Agent string `arg:"" optional:"" help:"Render for a specific agent (${skill_agents}); default is the neutral source."`
}

type SkillListCmd struct{}

type ImportCmd struct {
	File   string `help:"Read JSON payload from this file instead of stdin." short:"f" type:"existingfile"`
	Reveal bool   `help:"Reveal the first created/updated item in Things after import."`
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

func main() {
	var cli CLI
	ctx := kong.Parse(&cli,
		kong.Name("things"),
		kong.Description("CLI for Things3"),
		kong.UsageOnError(),
		kong.Vars{
			"version":       fmt.Sprintf("things %s (commit %s, built %s)", version, commit, date),
			"builtin_lists": strings.Join(things.BuiltinLists, ", "),
			"skill_agents":  skill.AgentNames(),
		},
	)

	if ctx.Command() == "version" {
		fmt.Printf("things %s (commit %s, built %s)\n", version, commit, date)
		return
	}

	if strings.HasPrefix(ctx.Command(), "skill ") {
		if err := runSkill(ctx, &cli); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		return
	}

	dbPath := cli.DB
	if dbPath == "" {
		var err error
		dbPath, err = db.FindDBPath()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	}

	database, err := db.Open(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	defer database.Close()

	err = run(ctx, &cli, database)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run(ctx *kong.Context, cli *CLI, database *db.DB) error {
	switch ctx.Command() {
	case "list", "list <args>":
		return runList(cli, database)
	case "projects":
		return runProjects(cli, database)
	case "areas":
		return runAreas(cli, database)
	case "tags":
		return runTags(cli, database)
	case "show <task>":
		return runShow(cli, database)
	case "add <title>":
		return runAdd(cli, database)
	case "project add <title>":
		return runProjectAdd(cli)
	case "edit <task>":
		return runEdit(cli, database)
	case "complete <task>":
		return runComplete(cli, database)
	case "cancel <task>":
		return runCancel(cli, database)
	case "search <query>":
		return runSearch(cli, database)
	case "log":
		return things.LogCompleted()
	case "open", "open <ref>":
		return runOpen(cli, database)
	case "import":
		return runImport(cli, database)
	case "version":
		return nil
	default:
		return fmt.Errorf("unknown command: %s", ctx.Command())
	}
}

func isInteractive() bool {
	fd := os.Stdin.Fd()
	return isatty.IsTerminal(fd) || isatty.IsCygwinTerminal(fd)
}

func runList(cli *CLI, database *db.DB) error {
	view := "today"
	project := cli.List.Project
	args := cli.List.Args

	if len(args) > 0 && db.ValidView(args[0]) {
		view = args[0]
		args = args[1:]
	}
	if project == "" && len(args) > 0 {
		project = strings.Join(args, " ")
		if view == "today" {
			view = "project"
		}
	}

	tasks, err := database.ListTasks(view, db.TaskFilter{
		Project: project,
		Area:    cli.List.Area,
		Tag:     cli.List.Tag,
	})
	if err != nil {
		return err
	}
	cacheTaskUUIDs(tasks)
	return output.Print(os.Stdout, tasks, cli.JSON)
}

func runProjects(cli *CLI, database *db.DB) error {
	projects, err := database.ListProjects(cli.Projects.Area, cli.Projects.Completed)
	if err != nil {
		return err
	}
	return output.Print(os.Stdout, projects, cli.JSON)
}

func runAreas(cli *CLI, database *db.DB) error {
	areas, err := database.ListAreas()
	if err != nil {
		return err
	}
	return output.Print(os.Stdout, areas, cli.JSON)
}

func runTags(cli *CLI, database *db.DB) error {
	tags, err := database.ListTags()
	if err != nil {
		return err
	}
	return output.Print(os.Stdout, tags, cli.JSON)
}

func runShow(cli *CLI, database *db.DB) error {
	task, err := resolveTask(cli.Show.Task, database)
	if err != nil {
		return err
	}
	items, err := database.GetChecklistItems(task.UUID)
	if err != nil {
		return err
	}
	return output.PrintTaskWithChecklist(os.Stdout, task, items, cli.JSON)
}

func runAdd(cli *CLI, database *db.DB) error {
	list := cli.Add.List
	if list == "" {
		list = cli.Add.Project
	}
	token, _ := database.GetAuthToken()
	return things.AddTask(things.AddParams{
		Title:     cli.Add.Title,
		Notes:     cli.Add.Notes,
		When:      cli.Add.When,
		Deadline:  cli.Add.Deadline,
		Tags:      cli.Add.Tags,
		Checklist: expandNewlines(cli.Add.Checklist),
		Heading:   cli.Add.Heading,
		List:      list,
		AuthToken: token,
	})
}

func runProjectAdd(cli *CLI) error {
	return things.AddProject(things.AddProjectParams{
		Title:    cli.Project.Add.Title,
		Notes:    cli.Project.Add.Notes,
		When:     cli.Project.Add.When,
		Deadline: cli.Project.Add.Deadline,
		Tags:     cli.Project.Add.Tags,
		Area:     cli.Project.Add.Area,
		Todos:    expandNewlines(cli.Project.Add.Todos),
	})
}

// expandNewlines converts the literal two-character sequence `\n` into real
// newlines so users can pass multi-line values in a single shell-quoted flag
// (e.g. --todos "Draft\nShip"). Actual newlines in the input are preserved.
func expandNewlines(s string) string {
	return strings.ReplaceAll(s, `\n`, "\n")
}

func runEdit(cli *CLI, database *db.DB) error {
	task, err := resolveTask(cli.Edit.Task, database)
	if err != nil {
		return err
	}

	token, _ := database.GetAuthToken()
	return things.UpdateTask(things.UpdateParams{
		ID:               task.UUID,
		AuthToken:        token,
		Title:            cli.Edit.Title,
		Notes:            cli.Edit.Notes,
		PrependNotes:     cli.Edit.PrependNotes,
		AppendNotes:      cli.Edit.AppendNotes,
		When:             cli.Edit.When,
		Deadline:         cli.Edit.Deadline,
		Tags:             cli.Edit.Tags,
		AddTags:          cli.Edit.AddTags,
		Checklist:        expandNewlinesPtr(cli.Edit.Checklist),
		PrependChecklist: expandNewlinesPtr(cli.Edit.PrependChecklist),
		AppendChecklist:  expandNewlinesPtr(cli.Edit.AppendChecklist),
		List:             cli.Edit.List,
		ListID:           cli.Edit.ListID,
		Heading:          cli.Edit.Heading,
		HeadingID:        cli.Edit.HeadingID,
		Completed:        cli.Edit.Complete,
		Canceled:         cli.Edit.Cancel,
		Duplicate:        cli.Edit.Duplicate,
		Reveal:           cli.Edit.Reveal,
	})
}

func expandNewlinesPtr(p *string) *string {
	if p == nil {
		return nil
	}
	v := expandNewlines(*p)
	return &v
}

func runComplete(cli *CLI, database *db.DB) error {
	task, err := resolveTask(cli.Complete.Task, database)
	if err != nil {
		return err
	}
	if task.Type == model.TypeProject {
		if !confirmAction(fmt.Sprintf("Complete project %q? This will also complete all its tasks.", task.Title)) {
			return fmt.Errorf("cancelled")
		}
		return things.CompleteProject(task.UUID)
	}
	return things.CompleteTask(task.UUID)
}

func runCancel(cli *CLI, database *db.DB) error {
	task, err := resolveTask(cli.Cancel.Task, database)
	if err != nil {
		return err
	}
	return things.CancelTask(task.UUID)
}

func resolveTask(ref string, database *db.DB) (*model.Task, error) {
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
			return nil, fmt.Errorf("task #%d no longer exists (stale list cache — re-run list)", n)
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

	if !isInteractive() {
		var b strings.Builder
		fmt.Fprintf(&b, "ambiguous task %q — matches %d tasks:\n", ambig.Query, len(ambig.Matches))
		for i, m := range ambig.Matches {
			fmt.Fprintf(&b, "  %d. %s  (%s)\n", i+1, m.Title, m.UUID)
		}
		fmt.Fprint(&b, "Re-run with a UUID or more specific string.")
		return nil, fmt.Errorf("%s", b.String())
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

func confirmAction(msg string) bool {
	if !isInteractive() {
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

func runOpen(cli *CLI, database *db.DB) error {
	cmd := &cli.Open

	flags := 0
	for _, s := range []string{cmd.Ref, cmd.Project, cmd.Area, cmd.Tag, cmd.Query} {
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

	params := things.ShowParams{Filter: cmd.Filter, Background: cmd.Background}

	switch {
	case cmd.Query != "":
		params.Query = cmd.Query
	case cmd.Area != "":
		uuid, err := database.FindAreaUUID(cmd.Area)
		if err != nil {
			return err
		}
		if uuid == "" {
			return fmt.Errorf("area not found: %s", cmd.Area)
		}
		params.ID = uuid
	case cmd.Tag != "":
		uuid, err := database.FindTagUUID(cmd.Tag)
		if err != nil {
			return err
		}
		if uuid == "" {
			return fmt.Errorf("tag not found: %s", cmd.Tag)
		}
		params.ID = uuid
	case cmd.Project != "":
		task, err := resolveTask(cmd.Project, database)
		if err != nil {
			return err
		}
		params.ID = task.UUID
	case things.IsBuiltinList(cmd.Ref):
		params.ID = cmd.Ref
	default:
		task, err := resolveTask(cmd.Ref, database)
		if err != nil {
			return err
		}
		params.ID = task.UUID
	}

	return things.Show(params)
}

func runImport(cli *CLI, database *db.DB) error {
	var data []byte
	var err error
	if cli.Import.File != "" {
		data, err = os.ReadFile(cli.Import.File)
		if err != nil {
			return fmt.Errorf("reading %s: %w", cli.Import.File, err)
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
	token, err := database.GetAuthToken()
	if err != nil {
		// Don't fail the import — the payload may be create-only and not need
		// the token at all — but surface the read error so users debugging an
		// `operation: update` failure aren't left guessing.
		fmt.Fprintf(os.Stderr, "warning: could not read Things auth token: %v\n", err)
	}
	return things.ImportJSON(string(data), token, cli.Import.Reveal)
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

func runSearch(cli *CLI, database *db.DB) error {
	tasks, err := database.SearchTasks(cli.Search.Query)
	if err != nil {
		return err
	}
	cacheTaskUUIDs(tasks)
	return output.Print(os.Stdout, tasks, cli.JSON)
}

func resolveSkillDir(agent skill.Agent, override string) (string, error) {
	if override != "" {
		return override, nil
	}
	return agent.DefaultDir()
}

func runSkill(ctx *kong.Context, cli *CLI) error {
	switch ctx.Command() {
	case "skill install <agent>":
		return runSkillInstall(cli)
	case "skill uninstall <agent>":
		return runSkillUninstall(cli)
	case "skill show", "skill show <agent>":
		return runSkillShow(cli)
	case "skill list":
		return runSkillList()
	default:
		return fmt.Errorf("unknown skill command: %s", ctx.Command())
	}
}

func runSkillInstall(cli *CLI) error {
	agent, err := skill.Lookup(cli.Skill.Install.Agent)
	if err != nil {
		return err
	}
	dir, err := resolveSkillDir(agent, cli.Skill.Install.Path)
	if err != nil {
		return err
	}
	if skill.Exists(agent, dir) && !cli.Skill.Install.Yes {
		if !isInteractive() {
			return fmt.Errorf("skill already installed at %s — pass -y to overwrite", dir)
		}
		if !confirmAction(fmt.Sprintf("Skill already installed at %s. Overwrite?", dir)) {
			return fmt.Errorf("cancelled")
		}
	}
	if err := skill.Install(agent, dir); err != nil {
		return err
	}
	fmt.Printf("Installed %s skill to %s\n", agent.Name(), dir)
	return nil
}

func runSkillUninstall(cli *CLI) error {
	agent, err := skill.Lookup(cli.Skill.Uninstall.Agent)
	if err != nil {
		return err
	}
	dir, err := resolveSkillDir(agent, cli.Skill.Uninstall.Path)
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
	if !cli.Skill.Uninstall.Yes {
		if !isInteractive() {
			return fmt.Errorf("refusing to uninstall non-interactively — pass -y to confirm")
		}
		if !confirmAction(fmt.Sprintf("Remove %s skill at %s?", agent.Name(), dir)) {
			return fmt.Errorf("cancelled")
		}
	}
	if err := skill.Uninstall(agent, dir); err != nil {
		return err
	}
	fmt.Printf("Removed %s skill from %s\n", agent.Name(), dir)
	return nil
}

func runSkillShow(cli *CLI) error {
	name := cli.Skill.Show.Agent
	if name == "" {
		fmt.Print(skill.Body())
		return nil
	}
	agent, err := skill.Lookup(name)
	if err != nil {
		return err
	}
	for fname, content := range agent.Files() {
		fmt.Printf("# %s\n%s", fname, content)
	}
	return nil
}

func runSkillList() error {
	for _, a := range skill.Agents() {
		dir, err := a.DefaultDir()
		if err != nil {
			dir = "(unknown)"
		}
		status := "not installed"
		if skill.Exists(a, dir) {
			status = "installed"
		}
		fmt.Printf("%-10s %s  (%s)\n", a.Name(), dir, status)
	}
	return nil
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
