package things

import "os/exec"

// SetExecCommandForTest is a cross-package test seam for stubbing `open` /
// `osascript` invocations; production code MUST NOT call it. Lives in a
// non-`_test.go` file because Go does not export `_test.go` symbols to
// other packages.
func SetExecCommandForTest(f func(string, ...string) *exec.Cmd) func(string, ...string) *exec.Cmd {
	prev := execCommand
	execCommand = f
	return prev
}
