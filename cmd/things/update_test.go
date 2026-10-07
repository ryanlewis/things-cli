package main

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"runtime/debug"
	"strconv"
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
	latestReleaseTag = func(io.Writer) (string, error) { return f.latest, f.fetchErr }
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
			// No tag to pin to: the command shown uses the script on main,
			// which a real run prints but does not run.
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
			want: "Would run: GOBIN=/Users/me/go/bin go install github.com/ryanlewis/things-cli/cmd/things@v0.9.1\n",
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
	want := "/bin/bash -c set -o pipefail; curl -fsSL https://raw.githubusercontent.com/ryanlewis/things-cli/v0.9.0/install.sh | INSTALL_DIR='/Users/me/my bin' VERSION=v0.9.0 sh"
	if len(f.ran) != 1 || strings.Join(f.ran[0], " ") != want {
		t.Errorf("ran %v, want %q", f.ran, want)
	}
}

func TestUpdateGoInstallPinsTheCheckedTag(t *testing.T) {
	f := fakeUpdate{exe: "/Users/me/go/bin/things", version: "dev", info: moduleInfo("v0.9.0"), latest: "v0.9.1"}
	if _, _, err := f.run(t, false); err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := "env GOBIN=/Users/me/go/bin go install github.com/ryanlewis/things-cli/cmd/things@v0.9.1"
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
	// version that does not parse counts as a failed check, which Homebrew
	// updates through (brew upgrade compares for itself).
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
			if !strings.Contains(out, "Running: brew upgrade") || len(f.ran) != 1 {
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

func TestUpdateStopsWhenTheLatestTagCannotBeCompared(t *testing.T) {
	f := fakeUpdate{exe: "/usr/local/bin/things", version: "0.9.98", writable: true, latest: "nightly"}
	_, _, err := f.run(t, false)
	if err == nil || !strings.Contains(err.Error(), `cannot compare things 0.9.98 with the latest release tag "nightly"`) {
		t.Fatalf("err = %v, want a refusal naming the tag", err)
	}
	if len(f.ran) != 0 {
		t.Errorf("ran %v, want nothing", f.ran)
	}
}

func TestUpdateStopsWhenTheLatestTagCannotBePinned(t *testing.T) {
	// A tag that compares fine but is not a plain vX.Y.Z release is not put
	// into a URL or shell command, and without it the script would run from
	// main and go install would take @latest.
	for _, tag := range []string{"v0.10.0+build.1", "0.10.0"} {
		cases := map[string]fakeUpdate{
			"script": {exe: "/usr/local/bin/things", version: "0.9.0", writable: true, latest: tag},
			"go":     {exe: "/Users/me/go/bin/things", version: "dev", info: moduleInfo("v0.9.0"), latest: tag},
		}
		for name, f := range cases {
			t.Run(name+" "+tag, func(t *testing.T) {
				_, _, err := f.run(t, false)
				if err == nil || !strings.Contains(err.Error(), "cannot pin the latest release tag "+strconv.Quote(tag)) {
					t.Fatalf("err = %v, want a refusal naming the tag", err)
				}
				if len(f.ran) != 0 {
					t.Errorf("ran %v, want nothing", f.ran)
				}
				g := f
				g.ran = nil
				_, stderr, err := g.run(t, true)
				if err != nil {
					t.Fatalf("dry run: %v", err)
				}
				if !strings.Contains(stderr, "cannot pin the latest release tag") || !strings.Contains(stderr, "`things update` would stop here") {
					t.Errorf("dry run stderr = %q, want it to say it would stop", stderr)
				}
				if len(g.ran) != 0 {
					t.Errorf("dry run ran %v", g.ran)
				}
			})
		}
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

// fakeGitHub answers the release check's requests in-process, keyed by host,
// and records each one. Nothing reaches the network.
type fakeGitHub struct {
	api, page http.HandlerFunc
	reqs      []*http.Request
	t         *testing.T
}

func (g *fakeGitHub) RoundTrip(req *http.Request) (*http.Response, error) {
	g.reqs = append(g.reqs, req)
	h := g.page
	if req.URL.Host == "api.github.com" {
		h = g.api
	}
	rec := httptest.NewRecorder()
	if h == nil {
		g.t.Errorf("unexpected request to %s", req.URL)
		rec.WriteHeader(http.StatusInternalServerError)
	} else {
		h(rec, req)
	}
	return rec.Result(), nil
}

func (g *fakeGitHub) install(t *testing.T) {
	t.Helper()
	g.t = t
	orig := updateTransport
	updateTransport = g
	t.Cleanup(func() { updateTransport = orig })
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITHUB_TOKEN", "")
}

func respond(status int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}
}

func pageRedirectsTo(loc string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", loc)
		w.WriteHeader(http.StatusFound)
	}
}

func TestFetchLatestReleaseTag(t *testing.T) {
	g := &fakeGitHub{api: respond(http.StatusOK, `{"tag_name":"v0.9.0"}`)}
	g.install(t)
	var note bytes.Buffer
	if tag, err := fetchLatestReleaseTag(&note); err != nil || tag != "v0.9.0" {
		t.Errorf("got %q, %v; want v0.9.0", tag, err)
	}
	if len(g.reqs) != 1 || g.reqs[0].Header.Get("Authorization") != "" {
		t.Errorf("requests %v, want one to the API with no token", g.reqs)
	}
	if note.Len() != 0 {
		t.Errorf("noted %q, want nothing", note.String())
	}
}

func TestFetchLatestReleaseTagSendsTheToken(t *testing.T) {
	cases := []struct {
		name, gh, github, want string
	}{
		{"GITHUB_TOKEN", "", "ghp_github", "Bearer ghp_github"},
		{"GH_TOKEN", "ghp_gh", "", "Bearer ghp_gh"},
		{"GH_TOKEN first, as gh does", "ghp_gh", "ghp_github", "Bearer ghp_gh"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := &fakeGitHub{api: respond(http.StatusOK, `{"tag_name":"v0.9.0"}`)}
			g.install(t)
			t.Setenv("GH_TOKEN", tc.gh)
			t.Setenv("GITHUB_TOKEN", tc.github)
			if _, err := fetchLatestReleaseTag(io.Discard); err != nil {
				t.Fatal(err)
			}
			if got := g.reqs[0].Header.Get("Authorization"); got != tc.want {
				t.Errorf("Authorization = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFetchLatestReleaseTagKeepsTheTokenOnAPIGitHub(t *testing.T) {
	// The token goes to api.github.com only: not to another API URL, not to
	// the releases page, and not along a redirect.
	g := &fakeGitHub{
		api:  pageRedirectsTo("https://elsewhere.example/releases/latest"),
		page: pageRedirectsTo("https://github.com/ryanlewis/things-cli/releases/tag/v0.9.0"),
	}
	g.install(t)
	t.Setenv("GITHUB_TOKEN", "ghp_secret")
	if tag, err := fetchLatestReleaseTag(io.Discard); err != nil || tag != "v0.9.0" {
		t.Fatalf("got %q, %v; want v0.9.0 from the page", tag, err)
	}
	orig := latestReleaseURL
	latestReleaseURL = "https://elsewhere.example/repos/ryanlewis/things-cli/releases/latest"
	t.Cleanup(func() { latestReleaseURL = orig })
	_, _ = fetchLatestReleaseTag(io.Discard)
	for _, r := range g.reqs {
		if r.URL.Host != "api.github.com" && r.Header.Get("Authorization") != "" {
			t.Errorf("token sent to %s", r.URL)
		}
	}
	if len(g.reqs) != 4 {
		t.Errorf("made %d requests, want 4 (no redirect followed)", len(g.reqs))
	}
}

func TestFetchLatestReleaseTagFollowsAPIRedirectsWithTheToken(t *testing.T) {
	// A renamed repository's API URL answers 301 to /repositories/ID/...,
	// which stays on api.github.com and so keeps the token.
	g := &fakeGitHub{api: func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repositories/42/releases/latest" {
			respond(http.StatusOK, `{"tag_name":"v0.9.2"}`)(w, r)
			return
		}
		w.Header().Set("Location", "https://api.github.com/repositories/42/releases/latest")
		w.WriteHeader(http.StatusMovedPermanently)
	}}
	g.install(t)
	t.Setenv("GITHUB_TOKEN", "ghp_secret")
	if tag, err := fetchLatestReleaseTag(io.Discard); err != nil || tag != "v0.9.2" {
		t.Fatalf("got %q, %v; want v0.9.2", tag, err)
	}
	if len(g.reqs) != 2 || g.reqs[1].Header.Get("Authorization") != "Bearer ghp_secret" {
		t.Errorf("requests %v, want the redirect followed with the token", g.reqs)
	}
}

func TestFetchLatestReleaseTagAcceptsAnyRedirectStatus(t *testing.T) {
	for _, status := range []int{http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther, http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			g := &fakeGitHub{
				api: respond(http.StatusForbidden, ""),
				page: func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Location", "https://github.com/ryanlewis/things-cli/releases/tag/v0.9.1")
					w.WriteHeader(status)
				},
			}
			g.install(t)
			if tag, err := fetchLatestReleaseTag(io.Discard); err != nil || tag != "v0.9.1" {
				t.Errorf("got %q, %v; want v0.9.1", tag, err)
			}
		})
	}
}

func TestFetchLatestReleaseTagFallsBackToTheRedirect(t *testing.T) {
	// GitHub matches owner and repo ignoring case, so the Location may not
	// match the page URL's case.
	for _, loc := range []string{
		"https://github.com/ryanlewis/things-cli/releases/tag/v0.9.1",
		"https://github.com/RyanLewis/Things-CLI/releases/tag/v0.9.1",
	} {
		t.Run(loc, func(t *testing.T) {
			g := &fakeGitHub{
				api:  respond(http.StatusUnauthorized, `{"message":"Bad credentials"}`),
				page: pageRedirectsTo(loc),
			}
			g.install(t)
			t.Setenv("GITHUB_TOKEN", "ghp_expired")
			var note bytes.Buffer
			if tag, err := fetchLatestReleaseTag(&note); err != nil || tag != "v0.9.1" {
				t.Errorf("got %q, %v; want v0.9.1", tag, err)
			}
			if len(g.reqs) != 2 || g.reqs[1].URL.String() != latestReleasePage {
				t.Errorf("requests %v, want the API then %s", g.reqs, latestReleasePage)
			}
			// A bad token is reported, not retried without.
			if want := "note: GitHub API check failed (GitHub API returned 401 Unauthorized: Bad credentials); used the releases/latest redirect\n"; note.String() != want {
				t.Errorf("noted %q, want %q", note.String(), want)
			}
		})
	}
}

func TestFetchLatestReleaseTagRedirectMustNameATag(t *testing.T) {
	cases := map[string]http.HandlerFunc{
		"no releases yet": pageRedirectsTo("https://github.com/ryanlewis/things-cli/releases"),
		"other host":      pageRedirectsTo("https://elsewhere.example/ryanlewis/things-cli/releases/tag/v0.9.1"),
		"plain http":      pageRedirectsTo("http://github.com/ryanlewis/things-cli/releases/tag/v0.9.1"),
		"other repo":      pageRedirectsTo("https://github.com/someone/else/releases/tag/v0.9.1"),
		"deeper path":     pageRedirectsTo("https://github.com/ryanlewis/things-cli/releases/tag/v0.9.1/extra"),
		"no redirect":     respond(http.StatusOK, "<html>"),
		"not found":       respond(http.StatusNotFound, ""),
	}
	for name, page := range cases {
		t.Run(name, func(t *testing.T) {
			g := &fakeGitHub{api: respond(http.StatusForbidden, ""), page: page}
			g.install(t)
			_, err := fetchLatestReleaseTag(io.Discard)
			if err == nil || !strings.Contains(err.Error(), "GitHub API returned 403 Forbidden; ") ||
				!strings.Contains(err.Error(), latestReleasePage) {
				t.Errorf("err = %v, want both failures", err)
			}
		})
	}
}

func TestKongUpdate(t *testing.T) {
	cli, ctx := parse(t, "update", "--dry-run")
	if ctx.Command() != "update" || !cli.Update.DryRun {
		t.Errorf("command %q, dry-run %v", ctx.Command(), cli.Update.DryRun)
	}
}
