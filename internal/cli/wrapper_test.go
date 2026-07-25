package cli

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestSplitWrapperArgs(t *testing.T) {
	cases := []struct {
		name      string
		in        []string
		wantFlags []string
		wantGuest []string
	}{
		{
			name:      "bare agent flag passes through (the reported bug)",
			in:        []string{"--dangerously-skip-permissions"},
			wantFlags: []string{},
			wantGuest: []string{"--dangerously-skip-permissions"},
		},
		{
			name:      "no args",
			in:        []string{},
			wantFlags: []string{},
			wantGuest: []string{},
		},
		{
			name:      "colliding short flag goes to agent, not sandbox",
			in:        []string{"-p", "do the thing"},
			wantFlags: []string{},
			wantGuest: []string{"-p", "do the thing"},
		},
		{
			name:      "leading sandbox long-flags consumed, rest to agent (no -- needed)",
			in:        []string{"--no-persist-auth", "--dangerously-skip-permissions"},
			wantFlags: []string{"--no-persist-auth"},
			wantGuest: []string{"--dangerously-skip-permissions"},
		},
		{
			name:      "sandbox value flag consumes its value",
			in:        []string{"--project", "/x", "--dangerously-skip-permissions"},
			wantFlags: []string{"--project", "/x"},
			wantGuest: []string{"--dangerously-skip-permissions"},
		},
		{
			name:      "dry-run alone (natural, no separator)",
			in:        []string{"--dry-run"},
			wantFlags: []string{"--dry-run"},
			wantGuest: []string{},
		},
		{
			name:      "explicit -- forces boundary and is dropped",
			in:        []string{"--project", "/x", "--", "--dangerously-skip-permissions", "--model", "opus"},
			wantFlags: []string{"--project", "/x"},
			wantGuest: []string{"--dangerously-skip-permissions", "--model", "opus"},
		},
		{
			name:      "unknown long flag (agent's) ends sandbox portion",
			in:        []string{"--model", "opus"},
			wantFlags: []string{},
			wantGuest: []string{"--model", "opus"},
		},
		{
			name:      "--flag=value form",
			in:        []string{"--project=/x", "--dangerously-skip-permissions"},
			wantFlags: []string{"--project=/x"},
			wantGuest: []string{"--dangerously-skip-permissions"},
		},
	}
	cmd := newClaudeCmd() // real command so Flags() knows sandbox's flag set
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotFlags, gotGuest := splitWrapperArgs(cmd, c.in)
			if !reflect.DeepEqual(gotFlags, c.wantFlags) {
				t.Errorf("flags = %#v, want %#v", gotFlags, c.wantFlags)
			}
			if !reflect.DeepEqual(gotGuest, c.wantGuest) {
				t.Errorf("guest = %#v, want %#v", gotGuest, c.wantGuest)
			}
		})
	}
}

// TestClaudeWrapperForwardsUnknownFlags exercises the full command so a
// regression in DisableFlagParsing would be caught: the wrapper must not reject
// an unknown agent flag at parse time.
func TestClaudeWrapperParsesWithoutError(t *testing.T) {
	cmd := newClaudeCmd()
	// DisableFlagParsing must be set, otherwise cobra rejects --dangerously-skip-permissions.
	if !cmd.DisableFlagParsing {
		t.Fatal("claude wrapper must set DisableFlagParsing to forward agent flags")
	}
}

// TestAgentWrappersShareTheContract pins the properties every agent adapter must
// have, so a new one added by copying an existing file can't quietly drop them:
// unknown agent flags are forwarded rather than rejected, the shared sandbox
// flag set is present, the crash safety net reaches every agent (not just the
// ones someone remembered), and the login persists in a sandbox-owned host dir
// of its own with an opt-out. Distinct persist names matter most — two adapters
// sharing one would cross their logins into a single directory.
func TestAgentWrappersShareTheContract(t *testing.T) {
	agents := map[string]bool{}
	for _, cmd := range agentCmds() {
		name := strings.Fields(cmd.Use)[0]
		t.Run(name, func(t *testing.T) {
			if !cmd.DisableFlagParsing {
				t.Error("must set DisableFlagParsing to forward agent flags")
			}
			for _, f := range []string{"project", "worktree", "dry-run", "no-persist-auth", "no-snapshot"} {
				if cmd.Flags().Lookup(f) == nil {
					t.Errorf("missing sandbox flag --%s", f)
				}
			}
			// Set by finishAgentCmd from the same string it assigns to
			// rf.persistName, which newSession turns into the persisted HOME.
			agent := cmd.Annotations[agentAnnotation]
			if agent == "" {
				t.Fatal("no agent annotation: the login would not persist across runs")
			}
			if agents[agent] {
				t.Errorf("agent name %q is used by more than one wrapper", agent)
			}
			agents[agent] = true
		})
	}
}

// TestNpmAgentBootstrap checks the shape the guest argv relies on: a shell
// script whose argv[0] is the agent, so runWrapper's forwarded args arrive as
// "$@" and the agent is exec'd (not left as a child of sh, which would swallow
// signals and the exit code).
func TestNpmAgentBootstrap(t *testing.T) {
	got := npmAgentBootstrap("gemini", "@google/gemini-cli")
	if len(got) != 4 || got[0] != "sh" || got[1] != "-c" || got[3] != "gemini" {
		t.Fatalf("bootstrap argv = %#v, want [sh -c <script> gemini]", got)
	}
	for _, want := range []string{`exec gemini "$@"`, "@google/gemini-cli", `$HOME/.local`} {
		if !strings.Contains(got[2], want) {
			t.Errorf("script does not contain %q:\n%s", want, got[2])
		}
	}
}

// TestAgentBootstrapScriptRuns executes the generated script for real, which is
// the only way to catch a quoting bug in it — the Go tests above only inspect
// text. A missing agent must produce the diagnostic and exit 127 rather than
// sh's bare "not found", and a present one must be exec'd with its args intact.
func TestAgentBootstrapScriptRuns(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh available")
	}
	home := t.TempDir()

	run := func(argv []string, extra ...string) (string, int) {
		t.Helper()
		c := exec.Command(argv[0], append(argv[1:], extra...)...)
		c.Env = append(os.Environ(), "HOME="+home)
		out, err := c.CombinedOutput()
		code := 0
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitCode()
		}
		return string(out), code
	}

	// An agent that cannot be installed: the bootstrap must say so and exit 127,
	// so the failure is legible instead of looking like the agent crashed.
	out, code := run(agentBootstrap("definitely-not-a-real-agent", "false"))
	if code != 127 {
		t.Errorf("missing agent: exit = %d, want 127\n%s", code, out)
	}
	if !strings.Contains(out, "is not installed") || !strings.Contains(out, "--allow") {
		t.Errorf("missing agent: unhelpful diagnostic:\n%s", out)
	}

	// An agent already on PATH must be exec'd, with the install skipped and the
	// guest args passed through untouched — including one containing spaces.
	out, code = run(agentBootstrap("echo", "exit 1"), "hello", "two words")
	if code != 0 {
		t.Errorf("present agent: exit = %d, want 0\n%s", code, out)
	}
	if strings.TrimSpace(out) != "hello two words" {
		t.Errorf("present agent: args mangled, got %q", strings.TrimSpace(out))
	}
	if strings.Contains(out, "installing") {
		t.Errorf("present agent: should not have tried to install:\n%s", out)
	}
}

// TestGooseForcesKeyringOff proves the one thing standing between the goose
// wrapper and a broken promise. Goose stores secrets in the OS keyring, a
// container has none, so without GOOSE_DISABLE_KEYRING reaching the container
// the login does not survive — "log in once" would simply be false.
//
// The --env case is the regression that motivated the afterParse callback:
// pflag's string array replaces its initial contents on the first --env, so
// setting the variable before parsing would silently drop it for exactly the
// users who pass an env var of their own.
func TestGooseForcesKeyringOff(t *testing.T) {
	for _, extra := range [][]string{nil, {"--env", "FOO=bar"}} {
		line := renderDryRun(t, newGooseCmd(), extra)
		if !strings.Contains(line, gooseDisableKeyring) {
			t.Errorf("goose %v: docker argv is missing %s:\n%s", extra, gooseDisableKeyring, line)
		}
		if len(extra) > 0 && !strings.Contains(line, "FOO=bar") {
			t.Errorf("goose %v: the user's own --env was dropped:\n%s", extra, line)
		}
	}
}

// TestEveryAgentRendersADryRun runs every adapter end to end through config
// load, spec build and argv render. It is the cheapest guard against a new
// adapter that compiles and satisfies the contract test but blows up the moment
// anyone runs it — and with more than a dozen of them, nobody is going to try
// each by hand.
//
// It also pins the isolation invariants per agent, since an adapter is the one
// place a mount or a HOME could be wired in by mistake: every agent must get the
// fake HOME, its own persisted agent home, and a disposable container.
func TestEveryAgentRendersADryRun(t *testing.T) {
	for _, cmd := range agentCmds() {
		agent := cmd.Annotations[agentAnnotation]
		t.Run(agent, func(t *testing.T) {
			line := renderDryRun(t, cmd, nil)
			for _, want := range []string{
				"--rm",
				"-e HOME=/sandbox/home",
				"target=/sandbox/home",
				"agents/" + agent,
			} {
				if !strings.Contains(line, want) {
					t.Errorf("docker argv missing %q:\n%s", want, line)
				}
			}
			// No agent may reach the host home. The persisted agent dir lives
			// under it, so check for a bare mount of the home itself.
			if strings.Contains(line, "source="+os.Getenv("HOME")+",") {
				t.Errorf("agent mounts the host home:\n%s", line)
			}
		})
	}
}

// renderDryRun runs a wrapper with --dry-run and returns the docker command line
// it printed — the real argv, not a reconstruction. HOME points at a temp dir so
// the run neither reads nor creates anything in the real one.
func renderDryRun(t *testing.T, cmd *cobra.Command, extra []string) string {
	t.Helper()
	t.Setenv("HOME", t.TempDir())

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	orig := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = orig }()

	done := make(chan string, 1)
	go func() {
		var b strings.Builder
		io.Copy(&b, r)
		done <- b.String()
	}()

	cmd.SetArgs(append([]string{"--dry-run"}, extra...))
	execErr := cmd.Execute()
	w.Close()
	out := <-done
	r.Close()
	if execErr != nil {
		t.Fatalf("dry run failed: %v", execErr)
	}
	return out
}

func TestClaudeProjectBucket(t *testing.T) {
	cases := map[string]string{
		"/Users/amitghadge/project/sandbox-cli": "-Users-amitghadge-project-sandbox-cli",
		"/workspace":                            "-workspace",
		"/Users/x/.agent/ai":                    "-Users-x--agent-ai", // '/.' -> '--'
	}
	for in, want := range cases {
		if got := claudeProjectBucket(in); got != want {
			t.Errorf("claudeProjectBucket(%q) = %q, want %q", in, got, want)
		}
	}
}
