package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/alecthomas/kong"

	"github.com/ryanlewis/things-cli/internal/config"
	"github.com/ryanlewis/things-cli/internal/output"
	"github.com/ryanlewis/things-cli/internal/skill"
	"github.com/ryanlewis/things-cli/internal/things"
)

// parserOptions is the one place the kong parser is configured. main and the
// test harness both build from it, so an option cannot be added to one and
// forgotten in the other. A nil cfg leaves kong's built-in defaults alone.
func parserOptions(cfg *config.File) []kong.Option {
	opts := []kong.Option{
		kong.Name("things"),
		kong.Description("CLI for Things3"),
		kong.UsageOnError(),
		kong.Vars{
			"version":       fmt.Sprintf("things %s (commit %s, built %s)", version, commit, date),
			"builtin_lists": strings.Join(things.BuiltinLists, ", "),
			"skill_agents":  skill.AgentNames(),
		},
	}
	if cfg != nil {
		opts = append(opts, kong.Resolvers(cfg.Resolver()))
	}
	return opts
}

// loadConfig resolves which config file applies to this invocation and reads
// it. The file has to be found before kong parses, because it supplies the
// defaults kong parses against, so --config is read straight off the argv.
//
// The returned File is never nil, even alongside an error: it names the file
// that failed, which is what the `config` commands report. Returning nil here
// would send them to (*Deps).config's fallback and have them describe — and,
// for `config init`, write — the default path instead of the one asked for.
func loadConfig(args []string) (*config.File, error) {
	path, source, err := config.ResolvePath(configPathFromArgs(args))
	if err != nil {
		return &config.File{Source: config.SourceDefault, Err: err}, err
	}
	f, loadErr := config.Load(path)
	f.Source = source
	return f, loadErr
}

// configPathFromArgs returns the value of a --config flag in args, or "".
// It reads the flag the same way kong would: both spellings, and nothing after
// the -- terminator.
func configPathFromArgs(args []string) string {
	for i, a := range args {
		switch {
		case a == "--":
			return ""
		case a == "--config":
			if i+1 < len(args) {
				return args[i+1]
			}
			return ""
		case strings.HasPrefix(a, "--config="):
			return strings.TrimPrefix(a, "--config=")
		}
	}
	return ""
}

// diagnosesConfig reports whether the selected command is one that exists to
// tell the user about the config file. Those have to run even when the file
// could not be read — refusing would leave no way to find out why.
func diagnosesConfig(ctx *kong.Context) bool {
	if ctx == nil || ctx.Selected() == nil {
		return false
	}
	target := ctx.Selected().Target
	if !target.CanAddr() {
		return false
	}
	_, ok := target.Addr().Interface().(configDiagnostic)
	return ok
}

// configDiagnostic marks a command as one of those. It is a marker rather than
// a name match so the set cannot drift from the commands that implement it.
type configDiagnostic interface{ diagnosesConfig() }

type ConfigCmd struct {
	Path ConfigPathCmd `cmd:"" help:"Print the config file in use and whether it exists."`
	Show ConfigShowCmd `cmd:"" help:"Print the defaults the config file establishes."`
	Init ConfigInitCmd `cmd:"" help:"Write a commented config file template."`
}

type ConfigPathCmd struct{}

func (*ConfigPathCmd) diagnosesConfig() {}

func (c *ConfigPathCmd) Run(d *Deps) error {
	cfg := d.config()
	if d.JSON {
		problem := ""
		if cfg.Err != nil {
			problem = cfg.Err.Error()
		}
		return output.Print(d.Stdout, struct {
			Path   string `json:"path"`
			Exists bool   `json:"exists"`
			Source string `json:"source"`
			Error  string `json:"error,omitempty"`
		}{cfg.Path, cfg.Exists, cfg.Source, problem}, true)
	}
	fmt.Fprintf(d.Stdout, "%s (%s)\n", cfg.Path, existence(cfg))
	// Which file is in use is a fact about the path, so it is still worth
	// printing when the contents are unusable — but say so, or the caller
	// walks away thinking the file is fine.
	if cfg.Err != nil {
		fmt.Fprintf(d.errOut(), "warning: this file cannot be used: %v\n", cfg.Err)
	}
	return nil
}

type ConfigShowCmd struct{}

func (*ConfigShowCmd) diagnosesConfig() {}

func (c *ConfigShowCmd) Run(d *Deps) error {
	cfg := d.config()
	// Nothing can be shown for a file that would not load, but naming it is
	// the point of running this at all, so report which file failed and why.
	// Under --json the error alone carries the path: a plain header on stdout
	// would sit in front of the JSON object and break the consumer parsing it.
	if cfg.Err != nil {
		if !d.JSON {
			fmt.Fprintf(d.Stdout, "config: %s (%s)\n", cfg.Path, existence(cfg))
		}
		return cfg.Err
	}
	settings := cfg.Settings()

	if d.JSON {
		return output.Print(d.Stdout, struct {
			Path     string           `json:"path"`
			Exists   bool             `json:"exists"`
			Source   string           `json:"source"`
			Settings []config.Setting `json:"settings"`
		}{cfg.Path, cfg.Exists, cfg.Source, settings}, true)
	}

	fmt.Fprintf(d.Stdout, "config: %s (%s)\n", cfg.Path, existence(cfg))
	fmt.Fprintf(d.Stdout, "These apply when no flag overrides them.\n\n")

	keyW := 0
	for _, s := range settings {
		if len(s.Key) > keyW {
			keyW = len(s.Key)
		}
	}
	valW := 0
	rendered := make([]string, len(settings))
	for i, s := range settings {
		rendered[i] = showValue(s.Value)
		if len(rendered[i]) > valW {
			valW = len(rendered[i])
		}
	}
	for i, s := range settings {
		fmt.Fprintf(d.Stdout, "  %-*s  %-*s  %s\n", keyW, s.Key, valW, rendered[i], s.Source)
	}
	return nil
}

// existence is the parenthetical the config commands print after the path.
func existence(cfg *config.File) string {
	if cfg.Exists {
		return "exists"
	}
	return "not found"
}

// showValue renders a setting for the plain listing. An empty string is a key
// with nothing set, which reads better as a placeholder than as blank space.
func showValue(v any) string {
	if s, ok := v.(string); ok {
		if s == "" {
			return "(unset)"
		}
		return s
	}
	return fmt.Sprintf("%v", v)
}

// configCause strips the "config file <path>: " prefix from a config error,
// for messages that already name the file.
func configCause(err error) error {
	var cfgErr *config.Error
	if errors.As(err, &cfgErr) {
		return cfgErr.Err
	}
	return err
}

type ConfigInitCmd struct {
	Force bool `help:"Overwrite an existing config file." short:"f"`
}

func (*ConfigInitCmd) diagnosesConfig() {}

func (c *ConfigInitCmd) Run(d *Deps) error {
	cfg := d.config()
	if cfg.Path == "" {
		// Only reachable if the config was never resolved. Refuse rather than
		// fall back to a path nobody asked for.
		return fmt.Errorf("no config file path was resolved for this run")
	}
	if cfg.Exists && !c.Force {
		hint := ""
		if cfg.Err != nil {
			// The path is already in this sentence, so quote only the cause
			// rather than the whole "config file <path>: ..." error.
			hint = fmt.Sprintf(" (unusable as it stands: %v)", configCause(cfg.Err))
		}
		return fmt.Errorf("config file already exists: %s%s — pass --force to overwrite", cfg.Path, hint)
	}
	if dir := filepath.Dir(cfg.Path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	if err := os.WriteFile(cfg.Path, []byte(config.Template()), 0o644); err != nil {
		return err
	}
	fmt.Fprintf(d.Stdout, "Wrote config template to %s\n", cfg.Path)
	return nil
}
