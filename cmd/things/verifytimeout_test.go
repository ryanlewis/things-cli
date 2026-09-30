package main

import (
	"strings"
	"testing"
	"time"

	"github.com/alecthomas/kong"
)

// parseVerifyTimeout builds the parser the way main does — config file and
// environment first — and returns the --verify-timeout value a run would use.
func parseVerifyTimeout(t *testing.T, args ...string) (time.Duration, error) {
	t.Helper()
	isolateHome(t)
	cfg, err := loadConfig(args)
	if err != nil {
		return 0, err
	}
	var cli CLI
	parser, err := kong.New(&cli, parserOptions(cfg)...)
	if err != nil {
		t.Fatalf("kong.New: %v", err)
	}
	if _, err := parser.Parse(args); err != nil {
		return 0, err
	}
	return cli.VerifyTimeout, nil
}

func TestVerifyTimeoutPrecedence(t *testing.T) {
	cases := []struct {
		name string
		file string // verify_timeout in the config file, "" for none
		env  string // $THINGS_CLI_VERIFY_TIMEOUT, "" for unset
		flag string // --verify-timeout, "" for none
		want time.Duration
	}{
		{name: "built-in default", want: 5 * time.Second},
		{name: "config beats default", file: "3s", want: 3 * time.Second},
		{name: "env beats default", env: "2s", want: 2 * time.Second},
		{name: "env beats config", file: "3s", env: "2s", want: 2 * time.Second},
		{name: "flag beats config", file: "3s", flag: "1500ms", want: 1500 * time.Millisecond},
		{name: "flag beats env", env: "2s", flag: "1500ms", want: 1500 * time.Millisecond},
		{name: "flag beats env and config", file: "3s", env: "2s", flag: "1500ms", want: 1500 * time.Millisecond},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			isolateHome(t)
			t.Setenv(verifyTimeoutEnv, tc.env)
			var args []string
			if tc.file != "" {
				args = append(args, "--config", writeConfig(t, `verify_timeout = "`+tc.file+`"`+"\n"))
			}
			if tc.flag != "" {
				args = append(args, "--verify-timeout", tc.flag)
			}
			args = append(args, "today")

			got, err := parseVerifyTimeout(t, args...)
			if err != nil {
				t.Fatalf("parse %v: %v", args, err)
			}
			if got != tc.want {
				t.Errorf("verify timeout = %s, want %s", got, tc.want)
			}
		})
	}
}

// Each source names itself in the error and points at --no-verify (or its
// config key) for "don't wait at all".
func TestVerifyTimeoutRejectsBadValues(t *testing.T) {
	for _, bad := range []string{"0s", "0", "-1s", "soon"} {
		t.Run("flag "+bad, func(t *testing.T) {
			_, err := parseVerifyTimeout(t, "--verify-timeout="+bad, "today")
			assertTimeoutError(t, err, "--verify-timeout", `got "`+bad+`"`, "--no-verify")
		})
		t.Run("env "+bad, func(t *testing.T) {
			isolateHome(t)
			t.Setenv(verifyTimeoutEnv, bad)
			_, err := parseVerifyTimeout(t, "today")
			assertTimeoutError(t, err, "$"+verifyTimeoutEnv, `got "`+bad+`"`, "--no-verify")
		})
		t.Run("config "+bad, func(t *testing.T) {
			path := writeConfig(t, `verify_timeout = "`+bad+`"`+"\n")
			_, err := parseVerifyTimeout(t, "--config", path, "today")
			assertTimeoutError(t, err, path, `key "verify_timeout"`, `got "`+bad+`"`, "no_verify = true")
		})
	}
}

func assertTimeoutError(t *testing.T, err error, wants ...string) {
	t.Helper()
	if err == nil {
		t.Fatal("want an error, got nil")
	}
	for _, want := range append(wants, "must be a positive duration") {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err, want)
		}
	}
}

// A bad environment value stops ordinary commands, as a bad config file does,
// but `config show` still runs and says what is wrong.
func TestBadVerifyTimeoutEnvStopsCommandsButNotConfigShow(t *testing.T) {
	isolateHome(t)
	t.Setenv(verifyTimeoutEnv, "soon")

	if err := runWith(t, seedFullDB(t), "today"); err == nil || !strings.Contains(err.Error(), "$"+verifyTimeoutEnv) {
		t.Errorf("today: err = %v, want the environment variable named", err)
	}
	out, err := runOut(t, nil, "config", "show")
	if err == nil || !strings.Contains(err.Error(), "$"+verifyTimeoutEnv) {
		t.Errorf("config show: err = %v, want the environment variable named", err)
	}
	if !strings.Contains(out, "config: ") {
		t.Errorf("config show did not name the file first; stdout = %q", out)
	}
}

func TestConfigShowReportsVerifyTimeoutSource(t *testing.T) {
	isolateHome(t)
	path := writeConfig(t, `verify_timeout = "3s"`+"\n")

	out, err := runOut(t, nil, "--config", path, "config", "show")
	if err != nil {
		t.Fatalf("config show: %v", err)
	}
	if !lineHas(out, "verify_timeout", "3s", "config") {
		t.Errorf("config show without the env var:\n%s", out)
	}

	t.Setenv(verifyTimeoutEnv, "750ms")
	out, err = runOut(t, nil, "--config", path, "config", "show")
	if err != nil {
		t.Fatalf("config show: %v", err)
	}
	if !lineHas(out, "verify_timeout", "750ms", "env") {
		t.Errorf("config show with the env var:\n%s", out)
	}
}

// lineHas reports whether one line of out has every field, in order.
func lineHas(out string, fields ...string) bool {
	for line := range strings.SplitSeq(out, "\n") {
		if strings.Join(strings.Fields(line), " ") == strings.Join(fields, " ") {
			return true
		}
	}
	return false
}

// The configured value is the one every read-back waits for, and the one its
// error reports. fastVerify's own 20ms comes through the environment, so a
// flag beating it shows the flag reached the wait.
func TestReadBackUsesConfiguredTimeout(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"complete", []string{"complete", "one-1"}, "still open after 35ms"},
		{"cancel", []string{"cancel", "one-1"}, "still open after 35ms"},
		{"edit", []string{"edit", "one-1", "--title", "Post the letter"}, "not modified within 35ms"},
		{"edit --complete", []string{"edit", "one-1", "--complete"}, "still open after 35ms"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fastVerify(t)
			database, _ := seedWritable(t)
			stubExecDropping(t)

			start := time.Now()
			err := runWith(t, database, append([]string{"--verify-timeout", "35ms"}, tc.args...)...)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.want)
			}
			if elapsed := time.Since(start); elapsed < 35*time.Millisecond {
				t.Errorf("gave up after %s, before the 35ms it was given", elapsed)
			}
		})
	}

	t.Run("tag add", func(t *testing.T) {
		fastVerify(t)
		database, _ := seedTagDB(t)
		stubExecCreatingTags(t, nil)

		_, err := runOut(t, database, "--verify-timeout", "35ms", "tag", "add", "focus")
		if err == nil || !strings.Contains(err.Error(), "after 35ms") {
			t.Fatalf("err = %v, want the 35ms budget reported", err)
		}
	})

	t.Run("import", func(t *testing.T) {
		fastVerify(t)
		database, sqlDB := seedWritable(t)
		stubExecApplyingAll(t, sqlDB, nil)

		payload := `[{"type":"to-do","operation":"update","id":"one-1","attributes":{"completed":true}}]`
		_, err := runImport(t, database, payload, "--verify-timeout", "35ms")
		if err == nil || !strings.Contains(err.Error(), "still open after 35ms") {
			t.Fatalf("err = %v, want the 35ms budget reported", err)
		}
	})

	t.Run("from the environment", func(t *testing.T) {
		fastVerify(t)
		t.Setenv(verifyTimeoutEnv, "45ms")
		database, _ := seedWritable(t)
		stubExecDropping(t)

		err := runWith(t, database, "complete", "one-1")
		if err == nil || !strings.Contains(err.Error(), "after 45ms") {
			t.Fatalf("err = %v, want the 45ms from the environment reported", err)
		}
	})
}
