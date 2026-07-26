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
		name         string
		in           []string
		wantFlags    []string
		wantGuest    []string
		wantExplicit bool
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
			name:      "--publish is consumed as a sandbox flag with its value",
			in:        []string{"--publish", "3000:3000", "--dangerously-skip-permissions"},
			wantFlags: []string{"--publish", "3000:3000"},
			wantGuest: []string{"--dangerously-skip-permissions"},
		},
		{
			// -P is a short flag, and short flags always end the sandbox portion
			// (that is what keeps agent flags from colliding). Inside a wrapper the
			// long form is the one that works; -P is for `sandbox-cli run`, or after
			// an explicit --.
			name:      "-P goes to the agent, like every other short flag",
			in:        []string{"-P", "3000:3000"},
			wantFlags: []string{},
			wantGuest: []string{"-P", "3000:3000"},
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
			name:         "explicit -- forces boundary and is dropped",
			in:           []string{"--project", "/x", "--", "--dangerously-skip-permissions", "--model", "opus"},
			wantFlags:    []string{"--project", "/x"},
			wantGuest:    []string{"--dangerously-skip-permissions", "--model", "opus"},
			wantExplicit: true,
		},
		{
			// The escape hatch for the agent-scoped `context` subcommand: with a
			// typed `--`, the token belongs to the agent no matter what it says.
			name:         "explicit -- marks the boundary as the user's own",
			in:           []string{"--", "context", "list"},
			wantFlags:    []string{},
			wantGuest:    []string{"context", "list"},
			wantExplicit: true,
		},
		{
			name:      "a positional ends the sandbox portion without marking it explicit",
			in:        []string{"context", "stores"},
			wantFlags: []string{},
			wantGuest: []string{"context", "stores"},
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
			gotFlags, gotGuest, gotExplicit := splitWrapperArgs(cmd, c.in)
			if !reflect.DeepEqual(gotFlags, c.wantFlags) {
				t.Errorf("flags = %#v, want %#v", gotFlags, c.wantFlags)
			}
			if !reflect.DeepEqual(gotGuest, c.wantGuest) {
				t.Errorf("guest = %#v, want %#v", gotGuest, c.wantGuest)
			}
			if gotExplicit != c.wantExplicit {
				t.Errorf("explicit = %v, want %v", gotExplicit, c.wantExplicit)
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
			for _, f := range []string{"project", "worktree", "dry-run", "detach", "no-persist-auth", "no-snapshot", "publish"} {
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
	// Run from a scratch directory outside any repository. Without this the CLI
	// discovers the developer's own .sandbox.yaml at this repo's root and folds it
	// into every rendered argv — so the assertions below depend on a file that is
	// not part of the test, and a machine whose config happens to set `image:` or
	// `mounts:` gets different results from CI.
	t.Chdir(t.TempDir())

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

// TestAnnounceBroadCredentials pins what gets said and what does not. Most
// wrapper allowlists carry a model-provider key, which is worth exactly the thing
// the agent is for; an AWS session or a GitHub token is the whole account. Those
// are still forwarded — it is how continue reaches Bedrock and how copilot
// authenticates — but silently was the wrong way to do it.
func TestAnnounceBroadCredentials(t *testing.T) {
	capture := func(envAllow []string) string {
		r, w, _ := os.Pipe()
		orig := os.Stderr
		os.Stderr = w
		announceBroadCredentials(envAllow)
		w.Close()
		os.Stderr = orig
		var b strings.Builder
		io.Copy(&b, r)
		r.Close()
		return b.String()
	}

	t.Setenv("AWS_ACCESS_KEY_ID", "set")
	t.Setenv("ANTHROPIC_API_KEY", "set")
	os.Unsetenv("AWS_SECRET_ACCESS_KEY")

	got := capture([]string{"ANTHROPIC_API_KEY", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY"})
	if !strings.Contains(got, "AWS_ACCESS_KEY_ID") {
		t.Errorf("a set account-wide credential must be announced, got %q", got)
	}
	// One line per scope, not per variable — three AWS names are one fact.
	if strings.Count(got, "your AWS account") != 1 {
		t.Errorf("expected the AWS scope named once, got %q", got)
	}
	// An allowlist entry that is not set forwards nothing, so warning about it
	// would train people to ignore the message.
	if strings.Contains(got, "AWS_SECRET_ACCESS_KEY") {
		t.Errorf("an unset variable was announced: %q", got)
	}
	// A model-provider key is scoped to the agent and is not account-wide.
	if strings.Contains(got, "ANTHROPIC_API_KEY") {
		t.Errorf("a model API key should not be announced as account-wide: %q", got)
	}
	if out := capture([]string{"ANTHROPIC_API_KEY"}); out != "" {
		t.Errorf("nothing account-wide forwarded, want silence, got %q", out)
	}
}

// TestBootstrapDoesNotShadowSystemBinaries pins the fix for a cross-project
// persistence path.
//
// $HOME/.local/bin is the persisted agent HOME: bind-mounted from the host, the
// SAME directory in every project, and writable by the agent. The bootstrap used
// to prepend it to PATH, which put it ahead of /usr/bin for every future session
// in every project — so an agent compromised in one repository could drop a file
// named `git`, `node` or `sh` there and shadow that command everywhere
// afterwards.
//
// Appending keeps the directory usable while system binaries win; the absolute
// exec is what still lets the agent self-update, which is why it lives there.
func TestBootstrapDoesNotShadowSystemBinaries(t *testing.T) {
	scripts := map[string]string{
		"claude":    claudeBootstrap,
		"npm agent": npmAgentBootstrap("gemini", "@google/gemini-cli")[2],
	}
	for name, script := range scripts {
		t.Run(name, func(t *testing.T) {
			if strings.Contains(script, `PATH="$HOME/.local/bin:$PATH"`) {
				t.Error("the persisted HOME is prepended to PATH, so anything written there " +
					"shadows every system binary in every future session")
			}
			if !strings.Contains(script, `PATH="$PATH:$HOME/.local/bin"`) {
				t.Errorf("expected the persisted bin directory to be appended:\n%s", script)
			}
			// The self-updating install must still be the one that runs, or the
			// baked copy silently wins and the agent stops updating itself.
			if !strings.Contains(script, `exec "$HOME/.local/bin/`) {
				t.Errorf("expected an absolute exec of the persisted binary:\n%s", script)
			}
		})
	}
}
