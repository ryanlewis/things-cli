package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	updateCask   = "ryanlewis/tap/things"
	updateModule = "github.com/ryanlewis/things-cli/cmd/things"
	// updateScriptURL takes a git ref: a release tag, or main when there is
	// no tag to pin to.
	updateScriptURL = "https://raw.githubusercontent.com/ryanlewis/things-cli/%s/install.sh"
)

// The seams `things update` reaches the outside world through. Tests swap them
// so nothing touches the network or runs brew, go or sh.
var (
	// updateExecutable is the running binary with symlinks resolved, so a
	// Homebrew bin/things link points into the Caskroom.
	updateExecutable = func() (string, error) {
		p, err := os.Executable()
		if err != nil {
			return "", err
		}
		return filepath.EvalSymlinks(p)
	}
	updateBuildInfo  = debug.ReadBuildInfo
	latestReleaseURL = "https://api.github.com/repos/ryanlewis/things-cli/releases/latest"
	latestReleaseTag = fetchLatestReleaseTag
	updateCommand    = exec.Command
	dirWritable      = func(dir string) bool {
		const wOK = 0x2 // W_OK in <unistd.h>
		return syscall.Access(dir, wOK) == nil
	}
)

type UpdateCmd struct {
	DryRun bool `help:"Print the command that would run, and exit." name:"dry-run"`
}

// installMethod is how the running binary got onto the machine, which decides
// how it is updated.
type installMethod int

const (
	methodBrew installMethod = iota
	methodScript
	methodGo
)

// updatePlan is the command that updates the running binary.
type updatePlan struct {
	method installMethod
	how    string   // "with Homebrew", for the first line of output
	dir    string   // install directory, for the install.sh and go install methods
	args   []string // argv to run
	shown  string   // args as the user would type them
	// current is the installed version in the form the release tag uses once
	// the leading "v" is dropped.
	current string
}

// releaseVersion matches the version goreleaser stamps into a release binary:
// the tag without its leading "v". `make install` stamps `git describe`
// output, which keeps the "v" (or is a bare commit hash), so a local build
// never looks like a release.
var releaseVersion = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$`)

// planUpdate works out how the binary at exe was installed. The order matters:
// release binaries carry a module version in their build info too, so the
// Caskroom path and the goreleaser version are checked before build info.
func planUpdate(exe, ver string, info *debug.BuildInfo, haveInfo bool) (updatePlan, error) {
	if strings.Contains(exe, "/Caskroom/things/") {
		args := []string{"brew", "upgrade", "--cask", updateCask}
		return updatePlan{method: methodBrew, how: "with Homebrew", args: args, shown: strings.Join(args, " "), current: ver}, nil
	}
	if releaseVersion.MatchString(ver) {
		plan := updatePlan{method: methodScript, how: "from a release download", dir: filepath.Dir(exe), current: ver}
		plan.pinScript("")
		return plan, nil
	}
	// go install from the module proxy leaves main.version unset, stamps the
	// module version, and records no VCS revision — a build from a checkout
	// always records one.
	if ver == "dev" && haveInfo && isModuleRelease(info) {
		plan := updatePlan{method: methodGo, how: "with go install", dir: filepath.Dir(exe), current: strings.TrimPrefix(info.Main.Version, "v")}
		plan.pinGo("")
		return plan, nil
	}
	return updatePlan{}, fmt.Errorf("%s is a local build (version %s), made with make install or go build, so there is nothing to update it from. Rebuild it from your checkout, or install a release: https://things.rlew.io/install/", exe, ver)
}

// pinScript points the install.sh command at a release tag: the script comes
// from that tag and installs that version, so what runs is what the release
// check found. With no tag it falls back to the script on main, which finds
// the latest release itself; `things update` only prints that command for the
// user to run, never runs it.
func (p *updatePlan) pinScript(tag string) {
	ref, version := "main", ""
	if tag != "" {
		ref, version = tag, " VERSION="+shellQuote(tag)
	}
	p.shown = "curl -fsSL " + fmt.Sprintf(updateScriptURL, ref) + " | INSTALL_DIR=" + shellQuote(p.dir) + version + " sh"
	// Without pipefail a failed download feeds sh an empty script, which
	// exits 0 and reports an update that never happened.
	p.args = []string{"sh", "-c", "set -o pipefail; " + p.shown}
}

// pinGo points the go install command at a release tag, so what it installs
// is what the release check found. With no tag it uses @latest, which `things
// update` only prints for the user to run, never runs.
func (p *updatePlan) pinGo(tag string) {
	query := "latest"
	if tag != "" {
		query = tag
	}
	// GOBIN puts the new binary where the running one is; left to go env
	// it may land elsewhere and leave this one stale.
	p.args = []string{"env", "GOBIN=" + p.dir, "go", "install", updateModule + "@" + query}
	p.shown = "GOBIN=" + shellQuote(p.dir) + " go install " + updateModule + "@" + query
}

// newerThan reports whether version a is ahead of b. Only MAJOR.MINOR.PATCH
// is compared, so a prerelease suffix is ignored: 1.0.0-rc1 orders as 1.0.0.
// ok is false when either side does not start with three numbers.
func newerThan(a, b string) (newer, ok bool) {
	ca, oka := versionCore(a)
	cb, okb := versionCore(b)
	if !oka || !okb {
		return false, false
	}
	for i := range ca {
		if ca[i] != cb[i] {
			return ca[i] > cb[i], true
		}
	}
	return false, true
}

func versionCore(v string) (core [3]int, ok bool) {
	v, _, _ = strings.Cut(strings.TrimPrefix(v, "v"), "-")
	v, _, _ = strings.Cut(v, "+")
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return core, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return core, false
		}
		core[i] = n
	}
	return core, true
}

func isModuleRelease(info *debug.BuildInfo) bool {
	if info.Main.Version == "" || info.Main.Version == "(devel)" {
		return false
	}
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" {
			return false
		}
	}
	return true
}

func (c *UpdateCmd) Run(d *Deps) error {
	exe, err := updateExecutable()
	if err != nil {
		return fmt.Errorf("cannot find the running binary: %w", err)
	}
	info, haveInfo := updateBuildInfo()
	plan, err := planUpdate(exe, version, info, haveInfo)
	if err != nil {
		return err
	}
	fmt.Fprintf(d.Stdout, "%s was installed %s.\n", exe, plan.how)

	tag, checkErr := latestReleaseTag()
	latest := strings.TrimPrefix(tag, "v")
	if checkErr == nil {
		if latest == plan.current {
			fmt.Fprintf(d.Stdout, "things %s is the latest release.\n", plan.current)
			return nil
		}
		newer, ok := newerThan(plan.current, latest)
		switch {
		case !ok:
			// A tag that cannot be compared is no better than no tag: the
			// update might still be a downgrade.
			checkErr = fmt.Errorf("cannot compare things %s with the latest release tag %q", plan.current, tag)
		case newer:
			fmt.Fprintf(d.Stdout, "things %s is newer than the latest release %s; nothing to do.\n", plan.current, latest)
			return nil
		case tag == "v"+latest && releaseVersion.MatchString(latest):
			// The tag goes into a URL and a shell command, so only a
			// well-formed release tag is pinned.
			switch plan.method {
			case methodScript:
				plan.pinScript(tag)
			case methodGo:
				plan.pinGo(tag)
			}
		}
	}

	// install.sh and go install each pick the latest release themselves and
	// never compare it with what is installed, so without the check they could
	// replace a newer binary with an older one. brew upgrade makes its own
	// comparison, so Homebrew carries on.
	blind := checkErr != nil && plan.method != methodBrew

	if c.DryRun {
		if checkErr != nil {
			fmt.Fprintf(d.errOut(), "Could not check the latest release (%v).\n", checkErr)
		}
		fmt.Fprintf(d.Stdout, "Would run: %s\n", plan.shown)
		if blind {
			fmt.Fprintf(d.errOut(), "Without the latest release to compare with, `things update` would stop here; run the command above yourself if you want it.\n")
		} else if plan.method == methodScript && !dirWritable(plan.dir) {
			fmt.Fprintf(d.errOut(), "%s is not writable, so `things update` would stop here; run the command above yourself.\n", plan.dir)
		}
		return nil
	}

	if blind {
		return fmt.Errorf("could not check the latest release (%v), so things will not update itself: it cannot tell whether the update would replace things %s with an older release. Try again later, or run this yourself:\n  %s", checkErr, plan.current, plan.shown)
	}

	// install.sh reaches for sudo when it cannot write the directory. Leave
	// that decision to the user rather than prompting for a password here.
	if plan.method == methodScript && !dirWritable(plan.dir) {
		return fmt.Errorf("%s is not writable, so things will not update itself. Run this yourself (install.sh asks for sudo when it needs it):\n  %s", plan.dir, plan.shown)
	}

	if checkErr != nil {
		fmt.Fprintf(d.errOut(), "Could not check the latest release (%v); updating anyway, as brew upgrade checks for itself.\n", checkErr)
	} else {
		fmt.Fprintf(d.Stdout, "Updating things %s to %s.\n", plan.current, latest)
	}
	fmt.Fprintf(d.Stdout, "Running: %s\n", plan.shown)
	cmd := updateCommand(plan.args[0], plan.args[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = d.Stdout
	cmd.Stderr = d.errOut()
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s failed: %w", plan.shown, err)
	}
	return nil
}

// fetchLatestReleaseTag asks the GitHub API for the newest release's tag.
func fetchLatestReleaseTag() (string, error) {
	req, err := http.NewRequest(http.MethodGet, latestReleaseURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "things-cli")
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub API returned %s", resp.Status)
	}
	var release struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", fmt.Errorf("reading GitHub API response: %w", err)
	}
	if release.TagName == "" {
		return "", errors.New("GitHub API response has no tag_name")
	}
	return release.TagName, nil
}
