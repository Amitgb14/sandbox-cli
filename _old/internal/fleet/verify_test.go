package fleet

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
)

// The script is a contract with /bin/sh, so it is executed rather than only read.
// No container needed: the wrapper is plain POSIX shell, and running it here is
// what proves the exit codes land back the way land expects them.
func TestVerifyScriptExitCodes(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh available")
	}
	cases := []struct {
		name          string
		agent, verify string
		want          int
	}{
		{"agent and verify both pass", "exit 0", "true", 0},
		{"verify fails", "exit 0", "false", VerifyFailedExit},
		// Verify has the last word on purpose: an agent that exits non-zero having
		// left a tree that builds and tests clean has done the job.
		{"agent fails but verify passes", "exit 3", "true", 0},
		{"both fail", "exit 3", "false", VerifyFailedExit},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			argv := withVerify([]string{"sh", "-c", tc.agent}, tc.verify)
			err := exec.Command(argv[0], argv[1:]...).Run()
			got := 0
			var ee *exec.ExitError
			if errors.As(err, &ee) {
				got = ee.ExitCode()
			} else if err != nil {
				t.Fatalf("running the wrapper: %v", err)
			}
			if got != tc.want {
				t.Errorf("exit code %d, want %d", got, tc.want)
			}
		})
	}
}

// A task with no verify gets the agent's argv untouched: the wrapper must not
// become something every run pays for.
func TestWithVerifyLeavesArgvAloneWhenUndeclared(t *testing.T) {
	argv := []string{"claude", "-p", "do the thing"}
	for _, empty := range []string{"", "   ", "\n"} {
		got := withVerify(argv, empty)
		if len(got) != len(argv) || got[0] != "claude" {
			t.Errorf("withVerify(%q) wrapped an undeclared verify: %v", empty, got)
		}
	}
}

// The agent's argv must reach the shell through "$@", never pasted into the
// script: it carries the prompt, which is free text and would otherwise be able
// to rewrite the script that judges it.
func TestWithVerifyPassesAgentArgvThroughDollarAt(t *testing.T) {
	argv := []string{"claude", "-p", `"; rm -rf /; echo "`, "--dangerously-skip-permissions"}
	got := withVerify(argv, "go test ./...")

	if len(got) < 4 || got[0] != "sh" || got[1] != "-c" || got[3] != "sh" {
		t.Fatalf("expected an sh -c wrapper with $0, got %v", got)
	}
	script := got[2]
	if !strings.Contains(script, `"$@"`) {
		t.Error("the script does not run the agent through \"$@\"")
	}
	// The prompt appears only as a later argv element, never inside the script.
	if strings.Contains(script, "rm -rf") {
		t.Errorf("the prompt was interpolated into the script:\n%s", script)
	}
	if want := argv[2]; got[6] != want {
		t.Errorf("prompt not forwarded verbatim: got %q, want %q", got[6], want)
	}
}

// The exit code is the contract land reads back off a container that may have
// exited days ago, so the script has to carry the constant rather than a literal
// that could drift from it.
func TestWithVerifyEncodesTheFailureExitCode(t *testing.T) {
	got := withVerify([]string{"codex", "exec", "x"}, "make check")
	script := got[2]

	if !strings.Contains(script, "make check") {
		t.Error("the verify command is not in the script")
	}
	if !strings.Contains(script, "exit 90") {
		t.Errorf("the script does not exit %d on failure:\n%s", VerifyFailedExit, script)
	}
	if VerifyFailedExit != 90 {
		t.Errorf("VerifyFailedExit changed to %d; it is a contract with containers that already exist", VerifyFailedExit)
	}
}

// A verify that calls `exit` must still be reported as a failed verify, not as a
// run that never reached one.
//
// The two are different states everywhere downstream: `fleet status` prints
// `failed` against `unchecked`, and `land` refuses with "did not pass its
// verify" against "exited N without reaching its verify; the work is unchecked".
// Interpolating the verify into the wrapper's own shell collapsed them, and it
// collapsed them for the *most* careful verify scripts — the ones that guard
// with `test -d x || { echo; exit 1; }` or open with `set -e`.
func TestVerifyRunsInASubshellSoExitCannotBypassTheContract(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	dir := t.TempDir()

	cases := []struct {
		name     string
		verify   string
		agentRC  string
		wantCode int
	}{
		{
			name:     "guard clause exits 1",
			verify:   "test -d built || { echo 'nothing built' >&2; exit 1; }",
			agentRC:  "0",
			wantCode: VerifyFailedExit,
		},
		{
			name:     "set -e on a failing command",
			verify:   "set -e\nfalse\necho unreachable",
			agentRC:  "0",
			wantCode: VerifyFailedExit,
		},
		{
			name:     "explicit exit 0 still passes",
			verify:   "set -e\nexit 0",
			agentRC:  "1", // the agent's own code must not decide this
			wantCode: 0,
		},
		{
			name:     "plain non-zero command",
			verify:   "false",
			agentRC:  "0",
			wantCode: VerifyFailedExit,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			argv := withVerify([]string{"sh", "-c", "exit " + tc.agentRC}, tc.verify)
			cmd := exec.Command(argv[0], argv[1:]...)
			cmd.Dir = dir
			err := cmd.Run()

			code := 0
			if err != nil {
				var ee *exec.ExitError
				if !errors.As(err, &ee) {
					t.Fatalf("running the wrapper: %v", err)
				}
				code = ee.ExitCode()
			}
			if code != tc.wantCode {
				t.Errorf("container exit = %d, want %d", code, tc.wantCode)
			}
		})
	}
}
