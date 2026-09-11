package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/protocol"
	"github.com/Amitgb14/sandbox-cli/internal/sandbox"
)

// The invariant phase 2 exists to protect: **a pane is the same container as the
// equivalent CLI run.**
//
// It matters because a session server is exactly the kind of thing that grows a
// second launcher. `runtime.BuildArgs` is the only function that turns policy into
// engine argv, and the way that stops being true is not a rewrite — it is a new
// caller that assembles "almost" the same spec. So the two paths are compared as
// strings, and the only difference allowed is the pane identity itself.
func TestPaneSpawnArgvMatchesTheRunPath(t *testing.T) {
	repo := testRepo(t)

	cases := []struct {
		name  string
		flags []string
		guest []string
	}{
		{"plain command", nil, []string{"npm", "test"}},
		{"no network", []string{"--network", "none"}, []string{"true"}},
		{"with a worktree", []string{"--worktree", "feat-argv"}, []string{"true"}},
		{"memory and cpus", []string{"--memory", "1g", "--cpus", "1.5"}, []string{"true"}},
		{"published port", []string{"--publish", "3000"}, []string{"true"}},
		{"shared directory", []string{"--share"}, []string{"true"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// One config root for **both** invocations, and this is load-bearing rather
			// than tidy. A fresh root per call gave each path its own
			// `~/.config/sandbox/shared`, so `--share` mounted two different
			// directories and the comparison failed on an environment difference
			// rather than a policy one — and `--worktree` failed outright, because the
			// second call tried to create a branch the first had already checked out
			// somewhere else. Two paths can only be compared under one environment.
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())

			args := func(head ...string) []string {
				return append(head, append(tc.flags, append([]string{"--"}, tc.guest...)...)...)
			}
			run := dryRunLine(t, repo, args("run", "--detach", "--dry-run"))
			pane := dryRunLine(t, repo, args("pane", "spawn", "--dry-run"))

			// The container name carries a timestamp for a run with no branch, so the
			// two differ in that one token by construction rather than by policy.
			run, pane = maskVolatile(run), maskVolatile(pane)
			if run != pane {
				t.Errorf("a pane and the equivalent run produce different argv.\n  run:  %s\n  pane: %s\n%s",
					run, pane, firstDifference(run, pane))
			}
		})
	}
}

// And the other half: a dry run starts nothing, so it stamps **no pane** — there
// is no pane to stamp, since the id is minted at launch.
//
// This is what keeps the comparison above meaningful. If `--dry-run` invented a
// pane id, the two lines would differ in it and the test would have to mask the
// very label it is checking for.
func TestDryRunStampsNoPane(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	repo := testRepo(t)
	for _, argv := range [][]string{
		{"run", "--detach", "--dry-run", "--", "true"},
		{"pane", "spawn", "--dry-run", "--", "true"},
	} {
		line := dryRunLine(t, repo, argv)
		for _, label := range []string{sandbox.LabelPane, sandbox.LabelPaneKind, sandbox.LabelPaneSession} {
			if strings.Contains(line, label+"=") {
				t.Errorf("%v stamped %s on a dry run, which starts nothing:\n%s", argv, label, line)
			}
		}
	}
}

// Every run records which kind of sandbox isolated it, because a catalog read next
// week should not have to ask what the machine has now.
func TestEveryRunStampsTheSandboxKind(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	line := dryRunLine(t, testRepo(t), []string{"run", "--dry-run", "--", "true"})
	if !strings.Contains(line, sandbox.LabelSandbox+"=docker") {
		t.Errorf("no %s label:\n%s", sandbox.LabelSandbox, line)
	}
}

// What a detached run *is*, derived from the options rather than threaded through
// every wrapper as a parameter one of them would eventually pass wrongly.
func TestPaneKindFollowsTheOptions(t *testing.T) {
	cases := []struct {
		opts sandbox.Options
		want protocol.PaneKind
	}{
		{sandbox.Options{}, protocol.PaneCommand},
		{sandbox.Options{Command: []string{"npm", "test"}}, protocol.PaneCommand},
		{sandbox.Options{Agent: "claude"}, protocol.PaneAgent},
		{sandbox.Options{Agent: "claude", Console: true}, protocol.PaneConsole},
		// Verify outranks both: its exit code is the run's, so the pane's purpose is
		// the judging rather than the working.
		{sandbox.Options{Agent: "claude", Verify: "go test ./..."}, protocol.PaneVerify},
		{sandbox.Options{Agent: "claude", Console: true, Verify: "make test"}, protocol.PaneVerify},
	}
	for _, tc := range cases {
		if got := paneKindFor(tc.opts); got != tc.want {
			t.Errorf("agent=%q console=%v verify=%q: kind = %q, want %q",
				tc.opts.Agent, tc.opts.Console, tc.opts.Verify, got, tc.want)
		}
	}
}

// testRepo is a real git repository, because the worktree case needs git and the
// repo id is a hash of the path.
func testRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "t@example.com"},
		{"config", "user.name", "T"},
		{"commit", "-q", "--allow-empty", "-m", "first"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git is needed for this test: %v: %s", err, out)
		}
	}
	return dir
}

// dryRunLine runs the CLI in-process and returns the engine command it printed.
//
// It deliberately does **not** set up the environment: a caller comparing two
// invocations has to give them the same one, and a helper that quietly made a
// fresh config root per call is what made the first version of this test compare
// two different machines.
func dryRunLine(t *testing.T, repo string, argv []string) string {
	t.Helper()
	if os.Getenv("XDG_CONFIG_HOME") == "" {
		t.Fatal("set XDG_CONFIG_HOME before calling dryRunLine; two invocations must share one")
	}

	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(repo); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(wd)

	// --dry-run prints to stdout through fmt.Println, so the pipe is the only way
	// to read it. Restored before any assertion runs.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := os.Stdout
	os.Stdout = w

	root := NewRootCmd()
	root.SetArgs(argv)
	root.SetOut(w)
	root.SetErr(w)
	runErr := root.Execute()

	os.Stdout = saved
	w.Close()
	out, _ := readAll(r)
	r.Close()

	if runErr != nil {
		t.Fatalf("%v: %v\n%s", argv, runErr, out)
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "docker ") || strings.HasPrefix(line, "podman ") {
			return strings.TrimSpace(line)
		}
	}
	t.Fatalf("%v printed no engine command:\n%s", argv, out)
	return ""
}

func readAll(r *os.File) (string, error) {
	var b strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		b.Write(buf[:n])
		if err != nil {
			return b.String(), nil
		}
	}
}

// maskVolatile removes the one token that differs between two identical launches.
//
// A detached run with no branch gets a timestamped container name, deliberately,
// so repeated runs of one project do not collide. That is a fact about *when* the
// command ran rather than about what it asked for, so comparing it would make this
// test fail for the wrong reason — and masking anything else would make it pass
// for the wrong reason, which is why the mask is one name and not a pattern.
func maskVolatile(line string) string {
	fields := strings.Fields(line)
	for i, f := range fields {
		if f != "--name" || i+1 >= len(fields) {
			continue
		}
		// Only the timestamped form, which is one segment after the prefix. A
		// deterministic name is `sandbox-<repo>-<branch>` and has more — and that one
		// is the one-agent-per-branch lock, so masking it would hide the difference
		// most worth catching.
		name := strings.TrimPrefix(fields[i+1], "sandbox-")
		if name != fields[i+1] && !strings.Contains(name, "-") {
			fields[i+1] = "<timestamped-name>"
		}
	}
	return strings.Join(fields, " ")
}

// firstDifference points at the token that differs, because two 40-token docker
// lines printed one after the other are not something anybody can diff by eye.
func firstDifference(a, b string) string {
	fa, fb := strings.Fields(a), strings.Fields(b)
	for i := 0; i < len(fa) && i < len(fb); i++ {
		if fa[i] != fb[i] {
			return "  first difference at token " + itoa(i) + ": " + fa[i] + " vs " + fb[i]
		}
	}
	if len(fa) != len(fb) {
		return "  same prefix, different lengths: " + itoa(len(fa)) + " vs " + itoa(len(fb)) + " tokens"
	}
	return ""
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var d []byte
	for n > 0 {
		d = append([]byte{byte('0' + n%10)}, d...)
		n /= 10
	}
	return string(d)
}
