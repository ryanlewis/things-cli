package skill

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// allAgents is the set of registered agent names exercised by the table-driven
// tests below. Add a new agent here when registering one in skill/<agent>.go.
var allAgents = []string{"claude", "codex", "pi"}

func TestBodyNonEmpty(t *testing.T) {
	if strings.TrimSpace(Body()) == "" {
		t.Fatal("Body() is empty")
	}
}

func TestSkillMDIsSelfContained(t *testing.T) {
	out := SkillMD()
	if !strings.HasPrefix(out, "---\nname: "+Name+"\n") {
		t.Errorf("SkillMD missing shared frontmatter prefix:\n%s", out[:min(len(out), 80)])
	}
	if !strings.Contains(out, "description: "+Description) {
		t.Error("SkillMD missing shared description")
	}
	if !strings.Contains(out, Body()) {
		t.Error("SkillMD missing body")
	}
}

func TestAgentSkillMDFrontmatterAndCommands(t *testing.T) {
	for _, name := range allAgents {
		t.Run(name, func(t *testing.T) {
			a, err := Lookup(name)
			if err != nil {
				t.Fatalf("Lookup(%s): %v", name, err)
			}
			files := a.Files()
			raw, ok := files["SKILL.md"]
			if !ok {
				t.Fatalf("%s files missing SKILL.md: %v", name, keys(files))
			}
			content := string(raw)

			if !strings.HasPrefix(content, "---\n") {
				t.Fatalf("SKILL.md must start with YAML frontmatter, got: %q", content[:min(len(content), 40)])
			}
			end := strings.Index(content[4:], "\n---\n")
			if end < 0 {
				t.Fatal("SKILL.md frontmatter not closed")
			}
			frontmatter := content[:4+end]
			for _, need := range []string{"name: things-cli", "description:"} {
				if !strings.Contains(frontmatter, need) {
					t.Errorf("frontmatter missing %q:\n%s", need, frontmatter)
				}
			}
			for _, cmd := range []string{"things list", "things show", "things add", "things complete", "things cancel"} {
				if !strings.Contains(content, cmd) {
					t.Errorf("SKILL.md missing reference to %q", cmd)
				}
			}
		})
	}
}

func TestLookupUnknown(t *testing.T) {
	_, err := Lookup("bogus-agent")
	if err == nil {
		t.Fatal("expected error for unknown agent")
	}
	if !strings.Contains(err.Error(), "supported") {
		t.Errorf("error should list supported agents: %v", err)
	}
}

func TestAgentsSorted(t *testing.T) {
	agents := Agents()
	if len(agents) == 0 {
		t.Fatal("no agents registered")
	}
	for i := 1; i < len(agents); i++ {
		if agents[i-1].Name() > agents[i].Name() {
			t.Errorf("agents not sorted: %s > %s", agents[i-1].Name(), agents[i].Name())
		}
	}
}

func TestAgentDefaultDir(t *testing.T) {
	t.Setenv("HOME", "/tmp/fake-home")
	for _, tc := range []struct {
		agent, want string
	}{
		{"claude", filepath.Join("/tmp/fake-home", ".claude", "skills", "things-cli")},
		{"codex", filepath.Join("/tmp/fake-home", ".codex", "skills", "things-cli")},
		{"pi", filepath.Join("/tmp/fake-home", ".pi", "agent", "skills", "things-cli")},
	} {
		t.Run(tc.agent, func(t *testing.T) {
			a, err := Lookup(tc.agent)
			if err != nil {
				t.Fatalf("Lookup(%s): %v", tc.agent, err)
			}
			dir, err := a.DefaultDir()
			if err != nil {
				t.Fatalf("DefaultDir: %v", err)
			}
			if dir != tc.want {
				t.Errorf("DefaultDir = %q, want %q", dir, tc.want)
			}
		})
	}
}

func TestInstallExistsUninstall(t *testing.T) {
	for _, name := range allAgents {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			a, _ := Lookup(name)

			if Exists(a, dir) {
				t.Error("fresh dir should not report Exists")
			}
			if got := InstalledFiles(a, dir); len(got) != 0 {
				t.Errorf("InstalledFiles on empty dir = %v", got)
			}

			if err := Install(a, dir); err != nil {
				t.Fatalf("Install: %v", err)
			}

			if !Exists(a, dir) {
				t.Error("after Install, Exists should be true")
			}
			got := InstalledFiles(a, dir)
			if len(got) != 1 || got[0] != "SKILL.md" {
				t.Errorf("InstalledFiles = %v, want [SKILL.md]", got)
			}

			// Install is idempotent (overwrites)
			if err := Install(a, dir); err != nil {
				t.Fatalf("re-Install: %v", err)
			}

			if err := Uninstall(a, dir); err != nil {
				t.Fatalf("Uninstall: %v", err)
			}
			if Exists(a, dir) {
				t.Error("after Uninstall, Exists should be false")
			}
			// Directory should be removed when empty.
			if _, err := os.Stat(dir); !os.IsNotExist(err) {
				t.Errorf("expected dir removed, stat err = %v", err)
			}
		})
	}
}

func TestUninstallLeavesUnrelatedFiles(t *testing.T) {
	for _, name := range allAgents {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			a, _ := Lookup(name)
			if err := Install(a, dir); err != nil {
				t.Fatalf("Install: %v", err)
			}
			extra := filepath.Join(dir, "user-notes.md")
			if err := os.WriteFile(extra, []byte("keep me"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := Uninstall(a, dir); err != nil {
				t.Fatalf("Uninstall: %v", err)
			}
			if _, err := os.Stat(extra); err != nil {
				t.Errorf("unrelated file removed: %v", err)
			}
			if _, err := os.Stat(dir); err != nil {
				t.Errorf("dir removed despite unrelated file: %v", err)
			}
		})
	}
}

func keys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
