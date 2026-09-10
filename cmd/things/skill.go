package main

import (
	"fmt"
	"os"

	"github.com/ryanlewis/things-cli/internal/skill"
)

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

func resolveSkillDir(agent skill.Agent, override string) (string, error) {
	if override != "" {
		return override, nil
	}
	return agent.DefaultDir()
}
