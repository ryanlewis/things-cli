package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/alecthomas/kong"
	"github.com/mattn/go-isatty"
	"github.com/willabides/kongplete"

	"github.com/ryanlewis/things-cli/internal/config"
	"github.com/ryanlewis/things-cli/internal/db"
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
	info, err := os.Stat(path)
	if err == nil && info.IsDir() {
		// SQLite would take a directory too and fail much later with an opaque
		// "unable to open database file"; kong's existingfile check used to
		// catch this before the check moved here.
		err = fmt.Errorf("%s is a directory, not a database file", path)
	}
	if err != nil {
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

type LogCmd struct{}

func (c *LogCmd) Run(_ *Deps) error {
	return things.LogCompleted()
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
			// Bad content in the config file exits 2 wherever it was caught —
			// here, or before the command ran. Failures around the file rather
			// than in it (nowhere to look for one, a refusal to overwrite) are
			// ordinary command errors and exit 1.
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
