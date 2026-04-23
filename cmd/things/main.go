package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/alecthomas/kong"
	"github.com/mattn/go-isatty"
	"github.com/ryanlewis/things-cli/internal/cache"
	"github.com/ryanlewis/things-cli/internal/db"
	"github.com/ryanlewis/things-cli/internal/model"
	"github.com/ryanlewis/things-cli/internal/output"
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
	Complete CompleteCmd `cmd:"" help:"Mark a task as completed."`
	Cancel   CancelCmd   `cmd:"" help:"Cancel a task."`
	Search   SearchCmd   `cmd:"" help:"Search tasks by title or notes."`
	Log      LogCmd      `cmd:"" help:"Move completed and cancelled items from Today to the Logbook (Items → Log Completed)."`
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

func main() {
	var cli CLI
	ctx := kong.Parse(&cli,
		kong.Name("things"),
		kong.Description("CLI for Things3"),
		kong.UsageOnError(),
		kong.Vars{"version": fmt.Sprintf("things %s (commit %s, built %s)", version, commit, date)},
	)

	if ctx.Command() == "version" {
		fmt.Printf("things %s (commit %s, built %s)\n", version, commit, date)
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
	case "complete <task>":
		return runComplete(cli, database)
	case "cancel <task>":
		return runCancel(cli, database)
	case "search <query>":
		return runSearch(cli, database)
	case "log":
		return things.LogCompleted()
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
		Checklist: cli.Add.Checklist,
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
		Todos:    cli.Project.Add.Todos,
	})
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

func runSearch(cli *CLI, database *db.DB) error {
	tasks, err := database.SearchTasks(cli.Search.Query)
	if err != nil {
		return err
	}
	cacheTaskUUIDs(tasks)
	return output.Print(os.Stdout, tasks, cli.JSON)
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
