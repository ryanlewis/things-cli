package main

import (
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
func loadConfig(args []string) (*config.File, error) {
	path, source, err := config.ResolvePath(configPathFromArgs(args))
	if err != nil {
		return nil, err
	}
	f, err := config.Load(path)
	if err != nil {
		return nil, err
	}
	f.Source = source
	return f, nil
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

type ConfigCmd struct {
	Path ConfigPathCmd `cmd:"" help:"Print the config file in use and whether it exists."`
	Show ConfigShowCmd `cmd:"" help:"Print the defaults the config file establishes."`
	Init ConfigInitCmd `cmd:"" help:"Write a commented config file template."`
}

type ConfigPathCmd struct{}

func (c *ConfigPathCmd) Run(d *Deps) error {
	cfg := d.config()
	if d.JSON {
		return output.Print(d.Stdout, struct {
			Path   string `json:"path"`
			Exists bool   `json:"exists"`
			Source string `json:"source"`
		}{cfg.Path, cfg.Exists, cfg.Source}, true)
	}
	state := "not found"
	if cfg.Exists {
		state = "exists"
	}
	fmt.Fprintf(d.Stdout, "%s (%s)\n", cfg.Path, state)
	return nil
}

type ConfigShowCmd struct{}

func (c *ConfigShowCmd) Run(d *Deps) error {
	cfg := d.config()
	settings := cfg.Settings()

	if d.JSON {
		return output.Print(d.Stdout, struct {
			Path     string           `json:"path"`
			Exists   bool             `json:"exists"`
			Source   string           `json:"source"`
			Settings []config.Setting `json:"settings"`
		}{cfg.Path, cfg.Exists, cfg.Source, settings}, true)
	}

	state := "not found"
	if cfg.Exists {
		state = "exists"
	}
	fmt.Fprintf(d.Stdout, "config: %s (%s)\n", cfg.Path, state)
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

type ConfigInitCmd struct {
	Force bool `help:"Overwrite an existing config file." short:"f"`
}

func (c *ConfigInitCmd) Run(d *Deps) error {
	cfg := d.config()
	if cfg.Exists && !c.Force {
		return fmt.Errorf("config file already exists: %s — pass --force to overwrite", cfg.Path)
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
