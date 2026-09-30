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
			f:    fakeUpdate{exe: "/opt/homebrew/Caskroom/things/0.9.0/things", version: "0.9.0", info: moduleInfo("v0.9.0", vcsRevision)},
			want: "Would run: brew upgrade --cask ryanlewis/tap/things\n",
		},
		{
			name: "install.sh or prebuilt",
			f:    fakeUpdate{exe: "/usr/local/bin/things", version: "0.9.0", info: moduleInfo("v0.9.0", vcsRevision), writable: true},
			want: "Would run: curl -fsSL https://raw.githubusercontent.com/ryanlewis/things-cli/main/install.sh | INSTALL_DIR=/usr/local/bin sh\n",
		},
		{
			name: "go install",
			f:    fakeUpdate{exe: "/Users/me/go/bin/things", version: "dev", info: moduleInfo("v0.9.0")},
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
	want := "sh -c set -o pipefail; curl -fsSL https://raw.githubusercontent.com/ryanlewis/things-cli/main/install.sh | INSTALL_DIR='/Users/me/my bin' sh"
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

func TestUpdateCarriesOnWhenTheCheckFails(t *testing.T) {
	f := fakeUpdate{exe: "/Users/me/go/bin/things", version: "dev", info: moduleInfo("v0.9.0"), fetchErr: errors.New("offline")}
	_, stderr, err := f.run(t, false)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(stderr, "Could not check the latest release (offline)") {
		t.Errorf("stderr = %q", stderr)
	}
	want := "env GOBIN=/Users/me/go/bin go install github.com/ryanlewis/things-cli/cmd/things@latest"
	if len(f.ran) != 1 || strings.Join(f.ran[0], " ") != want {
		t.Errorf("ran %v, want %q", f.ran, want)
	}
}

func TestUpdateUnwritableDirectory(t *testing.T) {
	f := fakeUpdate{exe: "/usr/local/bin/things", version: "0.8.0", latest: "v0.9.0"}
	out, _, err := f.run(t, false)
	if err == nil || !strings.Contains(err.Error(), "/usr/local/bin is not writable") ||
		!strings.Contains(err.Error(), "| INSTALL_DIR=/usr/local/bin sh") {
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
