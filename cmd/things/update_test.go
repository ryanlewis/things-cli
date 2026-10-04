package main

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"runtime/debug"
	"strings"
	"testing"
)

// fakeUpdate swaps every seam `things update` touches. ran collects the argv
// of each command it would have run; none of them runs.
type fakeUpdate struct {
	exe      string
	version  string
	info     *debug.BuildInfo
	latest   string
	fetchErr error
	writable bool
	runFails bool
	ran      [][]string
}

func (f *fakeUpdate) install(t *testing.T) {
	t.Helper()
	origExe, origInfo, origLatest, origCmd, origWritable, origVersion :=
		updateExecutable, updateBuildInfo, latestReleaseTag, updateCommand, dirWritable, version
	t.Cleanup(func() {
		updateExecutable, updateBuildInfo, latestReleaseTag, updateCommand, dirWritable, version =
			origExe, origInfo, origLatest, origCmd, origWritable, origVersion
	})
	updateExecutable = func() (string, error) { return f.exe, nil }
	updateBuildInfo = func() (*debug.BuildInfo, bool) { return f.info, f.info != nil }
	latestReleaseTag = func() (string, error) { return f.latest, f.fetchErr }
	updateCommand = func(name string, args ...string) *exec.Cmd {
		f.ran = append(f.ran, append([]string{name}, args...))
		if f.runFails {
			return exec.Command("false")
		}
		return exec.Command("true")
	}
	dirWritable = func(string) bool { return f.writable }
	version = f.version
}

func (f *fakeUpdate) run(t *testing.T, dryRun bool) (stdout, stderr string, err error) {
	t.Helper()
	f.install(t)
	var out, errBuf bytes.Buffer
	err = (&UpdateCmd{DryRun: dryRun}).Run(&Deps{Stdout: &out, Stderr: &errBuf})
	return out.String(), errBuf.String(), err
}

func moduleInfo(ver string, settings ...debug.BuildSetting) *debug.BuildInfo {
	return &debug.BuildInfo{Main: debug.Module{Path: "github.com/ryanlewis/things-cli", Version: ver}, Settings: settings}
}

var vcsRevision = debug.BuildSetting{Key: "vcs.revision", Value: "285642b"}

func TestUpdateDryRunPerMethod(t *testing.T) {
	cases := []struct {
		name string
		f    fakeUpdate
		want string
	}{
		{
			name: "homebrew cask",
			// A goreleaser build carries its module version too; the Caskroom
			// path has to win.
			f:    fakeUpdate{exe: "/opt/homebrew/Caskroom/things/0.9.0/things", version: "0.9.0", info: moduleInfo("v0.9.0", vcsRevision), latest: "v0.9.1"},
			want: "Would run: brew upgrade --cask ryanlewis/tap/things\n",
		},
		{
			name: "install.sh or prebuilt",
			f:    fakeUpdate{exe: "/usr/local/bin/things", version: "0.9.0", info: moduleInfo("v0.9.0", vcsRevision), writable: true, latest: "v0.9.1"},
			want: "Would run: curl -fsSL https://raw.githubusercontent.com/ryanlewis/things-cli/v0.9.1/install.sh | INSTALL_DIR=/usr/local/bin VERSION=v0.9.1 sh\n",
		},
		{
			name: "install.sh, release check failed",
			// No tag to pin to: the script on main finds the latest itself.
			f:    fakeUpdate{exe: "/usr/local/bin/things", version: "0.9.0", writable: true, fetchErr: errors.New("offline")},
			want: "Would run: curl -fsSL https://raw.githubusercontent.com/ryanlewis/things-cli/main/install.sh | INSTALL_DIR=/usr/local/bin sh\n",
		},
		{
			name: "install.sh, latest tag is not a release",
			// The tag lands in a shell command, so an odd one is not pinned.
			f:    fakeUpdate{exe: "/usr/local/bin/things", version: "0.9.0", writable: true, latest: "nightly;touch /tmp/x"},
			want: "Would run: curl -fsSL https://raw.githubusercontent.com/ryanlewis/things-cli/main/install.sh | INSTALL_DIR=/usr/local/bin sh\n",
		},
		{
			name: "go install",
			f:    fakeUpdate{exe: "/Users/me/go/bin/things", version: "dev", info: moduleInfo("v0.9.0"), latest: "v0.9.1"},
			want: "Would run: GOBIN=/Users/me/go/bin go install github.com/ryanlewis/things-cli/cmd/things@latest\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, _, err := tc.f.run(t, true)
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if !strings.HasPrefix(out, tc.f.exe+" was installed ") || !strings.HasSuffix(out, tc.want) {
				t.Errorf("stdout = %q, want the path and %q", out, tc.want)
			}
			if len(tc.f.ran) != 0 {
				t.Errorf("dry run ran %v", tc.f.ran)
			}
		})
	}
}

func TestUpdateRefusesLocalBuilds(t *testing.T) {
	cases := map[string]fakeUpdate{
		"make install":           {exe: "/Users/me/go/bin/things", version: "v0.9.0-5-g285642b-dirty", info: moduleInfo("v0.9.1-0.20260930-285642b+dirty", vcsRevision)},
		"make install on a tag":  {exe: "/Users/me/go/bin/things", version: "v0.9.0", info: moduleInfo("v0.9.0", vcsRevision)},
		"go build in a checkout": {exe: "/Users/me/dev/things-cli/things", version: "dev", info: moduleInfo("v0.9.1-0.20260930-285642b", vcsRevision)},
		"go build, no vcs":       {exe: "/tmp/things", version: "dev", info: moduleInfo("(devel)")},
		"no build info":          {exe: "/tmp/things", version: "dev"},
	}
	for name, f := range cases {
		t.Run(name, func(t *testing.T) {
			out, _, err := f.run(t, false)
			if err == nil || !strings.Contains(err.Error(), f.exe+" is a local build") {
				t.Fatalf("err = %v, want a local-build refusal naming %s", err, f.exe)
			}
			if out != "" || len(f.ran) != 0 {
				t.Errorf("printed %q and ran %v, want nothing", out, f.ran)
			}
		})
	}
}

func TestUpdateRunsTheCommand(t *testing.T) {
	f := fakeUpdate{exe: "/opt/homebrew/Caskroom/things/0.8.0/things", version: "0.8.0", latest: "v0.9.0"}
	out, _, err := f.run(t, false)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if want := [][]string{{"brew", "upgrade", "--cask", "ryanlewis/tap/things"}}; len(f.ran) != 1 || strings.Join(f.ran[0], " ") != strings.Join(want[0], " ") {
		t.Errorf("ran %v, want %v", f.ran, want)
	}
	for _, want := range []string{"Updating things 0.8.0 to 0.9.0.", "Running: brew upgrade --cask ryanlewis/tap/things"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout missing %q:\n%s", want, out)
		}
	}
}

func TestUpdateScriptTargetsTheBinarysDirectory(t *testing.T) {
	f := fakeUpdate{exe: "/Users/me/my bin/things", version: "0.8.0", latest: "v0.9.0", writable: true}
	if _, _, err := f.run(t, false); err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := "sh -c set -o pipefail; curl -fsSL https://raw.githubusercontent.com/ryanlewis/things-cli/v0.9.0/install.sh | INSTALL_DIR='/Users/me/my bin' VERSION=v0.9.0 sh"
	if len(f.ran) != 1 || strings.Join(f.ran[0], " ") != want {
		t.Errorf("ran %v, want %q", f.ran, want)
	}
}

func TestUpdateGoInstallComparesTheModuleVersion(t *testing.T) {
	f := fakeUpdate{exe: "/Users/me/go/bin/things", version: "dev", info: moduleInfo("v0.9.0"), latest: "v0.9.0"}
	out, _, err := f.run(t, false)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out, "things 0.9.0 is the latest release.") || len(f.ran) != 0 {
		t.Errorf("stdout %q, ran %v; want up to date and nothing run", out, f.ran)
	}
}

func TestUpdateAlreadyLatest(t *testing.T) {
	// Up to date wins over an unwritable directory: there is nothing to do.
	f := fakeUpdate{exe: "/usr/local/bin/things", version: "0.9.0", latest: "v0.9.0"}
	out, _, err := f.run(t, false)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out, "things 0.9.0 is the latest release.") || len(f.ran) != 0 {
		t.Errorf("stdout %q, ran %v; want up to date and nothing run", out, f.ran)
	}
}

func TestUpdateNeverDowngrades(t *testing.T) {
	cases := map[string]fakeUpdate{
		"release binary":  {exe: "/usr/local/bin/things", version: "0.9.98", latest: "v0.9.0", writable: true},
		"go install":      {exe: "/Users/me/go/bin/things", version: "dev", info: moduleInfo("v0.10.0"), latest: "v0.9.0"},
		"prerelease core": {exe: "/opt/homebrew/Caskroom/things/1.0.0-rc1/things", version: "1.0.0-rc1", latest: "v0.9.0"},
	}
	for name, f := range cases {
		t.Run(name, func(t *testing.T) {
			for _, dryRun := range []bool{false, true} {
				f.ran = nil
				out, _, err := f.run(t, dryRun)
				if err != nil {
					t.Fatalf("Run(dryRun=%v): %v", dryRun, err)
				}
				if !strings.Contains(out, "is newer than the latest release 0.9.0; nothing to do.") || len(f.ran) != 0 || strings.Contains(out, "Would run") {
					t.Errorf("dryRun=%v: stdout %q, ran %v; want newer and nothing run", dryRun, out, f.ran)
				}
			}
		})
	}
}

func TestUpdateProceedsWhenNotNewer(t *testing.T) {
	// A prerelease orders as its core, so 1.0.0-rc1 moves on to 1.0.0. A
	// version that does not parse keeps the old behaviour and updates.
	cases := map[string]fakeUpdate{
		"prerelease to its release": {exe: "/opt/homebrew/Caskroom/things/1.0.0-rc1/things", version: "1.0.0-rc1", latest: "v1.0.0"},
		"latest does not parse":     {exe: "/opt/homebrew/Caskroom/things/0.9.0/things", version: "0.9.0", latest: "nightly"},
	}
	for name, f := range cases {
		t.Run(name, func(t *testing.T) {
			out, _, err := f.run(t, false)
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if !strings.Contains(out, "Updating things") || len(f.ran) != 1 {
				t.Errorf("stdout %q, ran %v; want an update", out, f.ran)
			}
		})
	}
}

func TestNewerThan(t *testing.T) {
	cases := []struct {
		a, b      string
		newer, ok bool
	}{
		{"0.9.98", "0.9.0", true, true},
		{"0.10.0", "v0.9.9", true, true},
		{"1.0.0", "0.99.99", true, true},
		{"0.9.0", "0.9.0", false, true},
		{"0.8.9", "0.9.0", false, true},
		{"1.0.0-rc1", "1.0.0", false, true},
		{"1.0.0", "1.0.0-rc1", false, true},
		{"0.9.1-0.20260930000000-abcdef", "0.9.0", true, true},
		{"0.9", "0.9.0", false, false},
		{"0.9.0", "nightly", false, false},
	}
	for _, c := range cases {
		newer, ok := newerThan(c.a, c.b)
		if newer != c.newer || ok != c.ok {
			t.Errorf("newerThan(%q, %q) = %v, %v; want %v, %v", c.a, c.b, newer, ok, c.newer, c.ok)
		}
	}
}

func TestUpdateStopsWhenTheCheckFails(t *testing.T) {
	// install.sh and go install pick the latest stable release themselves, so
	// without the check a binary ahead of it would be swapped for an older one.
	cases := map[string]fakeUpdate{
		"release binary ahead of the latest": {exe: "/usr/local/bin/things", version: "0.9.98", writable: true, fetchErr: errors.New("GitHub API returned 403 Forbidden")},
		"go install ahead of the latest":     {exe: "/Users/me/go/bin/things", version: "dev", info: moduleInfo("v0.10.0"), fetchErr: errors.New("GitHub API returned 403 Forbidden")},
	}
	for name, f := range cases {
		t.Run(name, func(t *testing.T) {
			out, _, err := f.run(t, false)
			if err == nil || !strings.Contains(err.Error(), "could not check the latest release (GitHub API returned 403 Forbidden)") ||
				!strings.Contains(err.Error(), "older release") {
				t.Fatalf("err = %v, want a refusal naming the failed check", err)
			}
			if len(f.ran) != 0 || strings.Contains(out, "Running:") {
				t.Errorf("stdout %q, ran %v; want nothing run", out, f.ran)
			}
		})
	}
}

func TestUpdateHomebrewCarriesOnWhenTheCheckFails(t *testing.T) {
	f := fakeUpdate{exe: "/opt/homebrew/Caskroom/things/0.9.0/things", version: "0.9.0", fetchErr: errors.New("offline")}
	_, stderr, err := f.run(t, false)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(stderr, "Could not check the latest release (offline); updating anyway") {
		t.Errorf("stderr = %q", stderr)
	}
	want := "brew upgrade --cask ryanlewis/tap/things"
	if len(f.ran) != 1 || strings.Join(f.ran[0], " ") != want {
		t.Errorf("ran %v, want %q", f.ran, want)
	}
}

func TestUpdateDryRunSaysWhenTheCheckFails(t *testing.T) {
	f := fakeUpdate{exe: "/usr/local/bin/things", version: "0.9.0", writable: true, fetchErr: errors.New("offline")}
	_, stderr, err := f.run(t, true)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, want := range []string{"Could not check the latest release (offline).", "`things update` would stop here"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr missing %q: %q", want, stderr)
		}
	}
	if len(f.ran) != 0 {
		t.Errorf("dry run ran %v", f.ran)
	}
}

func TestUpdateUnwritableDirectory(t *testing.T) {
	f := fakeUpdate{exe: "/usr/local/bin/things", version: "0.8.0", latest: "v0.9.0"}
	out, _, err := f.run(t, false)
	if err == nil || !strings.Contains(err.Error(), "/usr/local/bin is not writable") ||
		!strings.Contains(err.Error(), "| INSTALL_DIR=/usr/local/bin VERSION=v0.9.0 sh") {
		t.Fatalf("err = %v, want the directory and the command to run", err)
	}
	if strings.Contains(out, "Updating") {
		t.Errorf("stdout %q announces an update that is not going to happen", out)
	}
	if len(f.ran) != 0 {
		t.Errorf("ran %v, want nothing", f.ran)
	}
}

func TestUpdateReportsAFailedCommand(t *testing.T) {
	f := fakeUpdate{exe: "/opt/homebrew/Caskroom/things/0.8.0/things", version: "0.8.0", latest: "v0.9.0", runFails: true}
	_, _, err := f.run(t, false)
	if err == nil || !strings.Contains(err.Error(), "brew upgrade --cask ryanlewis/tap/things failed") {
		t.Fatalf("err = %v", err)
	}
}

func TestFetchLatestReleaseTag(t *testing.T) {
	status, body := http.StatusOK, `{"tag_name":"v0.9.0"}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	orig := latestReleaseURL
	latestReleaseURL = srv.URL
	t.Cleanup(func() { latestReleaseURL = orig })

	if tag, err := fetchLatestReleaseTag(); err != nil || tag != "v0.9.0" {
		t.Errorf("got %q, %v; want v0.9.0", tag, err)
	}
	status, body = http.StatusForbidden, `{"message":"rate limited"}`
	if _, err := fetchLatestReleaseTag(); err == nil || !strings.Contains(err.Error(), "403") {
		t.Errorf("err = %v, want the 403", err)
	}
}

func TestKongUpdate(t *testing.T) {
	cli, ctx := parse(t, "update", "--dry-run")
	if ctx.Command() != "update" || !cli.Update.DryRun {
		t.Errorf("command %q, dry-run %v", ctx.Command(), cli.Update.DryRun)
	}
}
