//go:build docker_integration

package cli

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/runtime"
	"github.com/Amitgb14/sandbox-cli/internal/sandbox"
)

// The session commands against a real daemon.
//
// Everything here was previously proved only against a fake backend, and several
// of the claims cannot be proved that way at all, because they are claims about
// what docker does rather than about what this package computes: that ending an
// attach leaves the guest running, that --force is a different signal rather
// than a louder word, and that the listing's fields are what `docker inspect`
// actually returns.
//
// Two conventions hold the file together, and both were learned from failures:
//
//   - **Everything is addressed by the id `startSession` returns, and every
//     label is stamped per run.** These tests share a daemon with whatever the
//     developer is doing, and `sandboxSessions` lists every container on the
//     host carrying sandbox.cli=1. A branch label of `feature-a` would collide
//     with somebody's own detached agent on a branch of that name, and
//     resolveSession would rightly refuse the ambiguity — a red suite reporting
//     a bug in the resolver when the resolver was correct.
//   - **No guest outlives the test by more than a few minutes.** A cleanup does
//     not run when the binary is killed by `go test -timeout` or Ctrl-C, so a
//     guest that ignores SIGTERM and loops forever would have to be found and
//     removed by hand. The loops are bounded.
//
// The containers are plain `alpine`: this is a test of the supervision layer,
// not of the base image, and building one would make the suite slower for no
// added coverage.

// stampedBranch is a branch label no other container on this machine can be
// carrying. See the note above: the listing is machine-wide.
func stampedBranch(t *testing.T, name string) string {
	t.Helper()
	return fmt.Sprintf("%s-%s", name, time.Now().Format("150405.000000"))
}

// sessionLabels is what a detached sandbox run leaves stamped on its container.
func sessionLabels(branch string) map[string]string {
	return map[string]string{
		sandbox.LabelCLI:    "1",
		sandbox.LabelBranch: branch,
		sandbox.LabelAgent:  "claude",
	}
}

// startSession starts a container shaped like a detached run: no stdin, no tty,
// and *not* --rm, because a finished session's logs and exit code surviving is
// the behaviour under test rather than an accident.
func startSession(t *testing.T, name string, labels map[string]string, guest ...string) string {
	t.Helper()
	return startSessionWith(t, name, labels, nil, guest...)
}

// startSessionWith is startSession plus extra docker flags, for the one case
// that needs a different container shape (a console session, started with -i).
//
// The id comes from Output() rather than CombinedOutput(): on a machine without
// the alpine image cached, docker writes its pull progress to *stderr* and the
// id to stdout, and merging the two returns a multi-line string that matches no
// container. That failure only ever appears on a cold machine — the first CI
// run — and it points at the supervision layer rather than at this helper.
func startSessionWith(t *testing.T, name string, labels map[string]string, flags []string, guest ...string) string {
	t.Helper()
	// Registered before the container exists, deliberately: `docker run` can fail
	// *after* creating it, and a cleanup registered on the next line would never
	// be reached. Removing a name that was never created is a no-op we ignore.
	t.Cleanup(func() { exec.Command("docker", "rm", "-f", name).Run() })

	args := append([]string{"run", "-d", "--name", name}, flags...)
	for k, v := range labels {
		args = append(args, "--label", k+"="+v)
	}
	args = append(args, "alpine")
	args = append(args, guest...)

	var stderr bytes.Buffer
	cmd := exec.Command("docker", args...)
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("starting %s: %v: %s", name, err, strings.TrimSpace(stderr.String()))
	}
	id := strings.TrimSpace(string(out))
	if len(id) < 12 {
		t.Fatalf("docker returned no container id for %s: %q", name, id)
	}
	return id
}

// waitForState polls until the container reaches want, or fails the test.
func waitForState(t *testing.T, id, want string, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	var last string
	for time.Now().Before(deadline) {
		if out, err := exec.Command("docker", "inspect", "-f", "{{.State.Status}}", id).Output(); err == nil {
			if last = strings.TrimSpace(string(out)); last == want {
				return
			}
		}
		time.Sleep(150 * time.Millisecond)
	}
	t.Fatalf("%s never reached %q (last %q) within %s", shortID(id), want, last, within)
}

func containerState(t *testing.T, id string) string {
	t.Helper()
	out, err := exec.Command("docker", "inspect", "-f", "{{.State.Status}}", id).Output()
	if err != nil {
		t.Fatalf("inspecting %s: %v", shortID(id), err)
	}
	return strings.TrimSpace(string(out))
}

func exitCodeOf(t *testing.T, id string) int {
	t.Helper()
	out, err := exec.Command("docker", "inspect", "-f", "{{.State.ExitCode}}", id).Output()
	if err != nil {
		t.Fatalf("inspecting %s: %v", shortID(id), err)
	}
	code, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatalf("reading exit code of %s: %v", shortID(id), err)
	}
	return code
}

// find returns the listed session with the given id, or fails.
func find(t *testing.T, infos []runtime.ContainerInfo, id, what string) runtime.ContainerInfo {
	t.Helper()
	for _, c := range infos {
		if c.ID == id {
			return c
		}
	}
	t.Fatalf("%s (%s) is not in the listing of %d sessions", what, shortID(id), len(infos))
	return runtime.ContainerInfo{}
}

func has(infos []runtime.ContainerInfo, id string) bool {
	for _, c := range infos {
		if c.ID == id {
			return true
		}
	}
	return false
}

// listSessions is sandboxSessions with the plumbing the tests do not care about.
func listSessions(t *testing.T, rt sessionRuntime, all bool) []runtime.ContainerInfo {
	t.Helper()
	infos, err := sandboxSessions(context.Background(), rt, "docker", all)
	if err != nil {
		t.Fatalf("sandboxSessions(all=%v): %v", all, err)
	}
	return infos
}

// TestSessionListingAndResolutionAgainstDocker covers `list` and the resolver
// every one of the four commands shares.
//
// The unlabelled container is the point rather than padding, the same way it is
// in the stats test: it is *named* like one of ours, so anything resolving by
// name against the engine instead of against our own listing would find it —
// and `sandbox-cli kill postgres` reaching somebody's database is the failure
// this design exists to prevent.
func TestSessionListingAndResolutionAgainstDocker(t *testing.T) {
	if !dockerAvailable() {
		t.Skip("docker daemon not available")
	}
	rt := runtime.NewEngine("docker")
	stamp := time.Now().Format("150405.000")
	branch := stampedBranch(t, "feature-a")

	running := "sandbox-sesstest-run-" + stamp
	runningID := startSession(t, running, sessionLabels(branch), "sleep", "300")

	finished := "sandbox-sesstest-done-" + stamp
	finishedID := startSession(t, finished, sessionLabels(stampedBranch(t, "docs")), "sh", "-c", "exit 0")
	waitForState(t, finishedID, "exited", 30*time.Second)

	// Named like ours, labelled as nobody's.
	stranger := "sandbox-sesstest-theirs-" + stamp
	startSession(t, stranger, nil, "sleep", "300")

	live := listSessions(t, rt, false)
	if !has(live, runningID) {
		t.Errorf("the running session is missing from the default listing")
	}
	if has(live, finishedID) {
		t.Errorf("a finished session appears without --all")
	}
	for _, c := range live {
		if c.Name == stranger {
			t.Errorf("a container merely named like a sandbox was listed: %s", c.Name)
		}
	}

	all := listSessions(t, rt, true)
	done := find(t, all, finishedID, "the finished session")
	if got := sessionStatus(done); got != "exited (0)" {
		t.Errorf("sessionStatus of a finished session = %q, want %q", got, "exited (0)")
	}
	if done.FinishedAt.IsZero() || done.StartedAt.IsZero() {
		t.Errorf("timestamps not parsed from docker inspect: started %v finished %v",
			done.StartedAt, done.FinishedAt)
	}

	// The runtime is read back from the engine rather than remembered from the
	// launch, so it needs a daemon to be tested at all. On any host these tests
	// can run on it is the shared-kernel default — which is the assertion worth
	// making twice over: the field decodes, and it does not overclaim.
	live0 := find(t, all, runningID, "the running session")
	if live0.Runtime == "" {
		t.Errorf("no runtime reported for a live container — HostConfig.Runtime did not decode")
	}
	if live0.StrongerIsolation() {
		t.Errorf("a plain docker container claims a kernel of its own: runtime %q", live0.Runtime)
	}

	// The four ways somebody has a session in front of them, all resolving to the
	// same container: the id from the listing, the whole id, the container name,
	// and the branch — which is how a detached or fleet run is addressed.
	for _, ref := range []string{shortID(runningID), runningID, running, branch} {
		got, err := resolveSession(all, ref)
		if err != nil {
			t.Errorf("resolveSession(%q): %v", ref, err)
			continue
		}
		if got.ID != runningID {
			t.Errorf("resolveSession(%q) = %s, want %s", ref, shortID(got.ID), shortID(runningID))
		}
	}

	// The refusal that matters, against a container the daemon really does have.
	if _, err := resolveSession(all, stranger); err == nil {
		t.Errorf("resolveSession(%q) found a container sandbox-cli did not start", stranger)
	} else if !strings.Contains(err.Error(), "no sandbox session matches") {
		t.Errorf("unexpected refusal for a foreign container: %v", err)
	}

	var table bytes.Buffer
	if err := renderSessions(&table, all, true, time.Now()); err != nil {
		t.Fatalf("renderSessions: %v", err)
	}
	for _, want := range []string{"KIND", "interactive", shortID(runningID), branch, "exited (0)"} {
		if !strings.Contains(table.String(), want) {
			t.Errorf("listing does not mention %q:\n%s", want, table.String())
		}
	}
}

// TestLogsAgainstDocker covers both halves of `logs`: the plain read, and the
// --follow path that has a bargain of its own with docker.
func TestLogsAgainstDocker(t *testing.T) {
	if !dockerAvailable() {
		t.Skip("docker daemon not available")
	}
	rt := runtime.NewEngine("docker")
	stamp := time.Now().Format("150405.000")

	// The claim a fake writer cannot make: docker returns stdout and stderr
	// separately for a container started without a tty, and `logs` keeps them
	// that way, so piping a fleet agent's output into a file does not swallow
	// its diagnostics.
	t.Run("the streams stay apart", func(t *testing.T) {
		name := "sandbox-sesstest-logs-" + stamp
		id := startSession(t, name, sessionLabels(stampedBranch(t, "logs")),
			"sh", "-c", "echo to-stdout; echo to-stderr 1>&2")
		waitForState(t, id, "exited", 30*time.Second)

		var out, errb bytes.Buffer
		if err := rt.Logs(context.Background(), id, false, &out, &errb); err != nil {
			t.Fatalf("Logs: %v", err)
		}
		if !strings.Contains(out.String(), "to-stdout") {
			t.Errorf("stdout missing from the stdout stream: %q", out.String())
		}
		if strings.Contains(out.String(), "to-stderr") {
			t.Errorf("stderr leaked into the stdout stream: %q", out.String())
		}
		if !strings.Contains(errb.String(), "to-stderr") {
			t.Errorf("stderr missing from the stderr stream: %q", errb.String())
		}

		// And a finished session is still addressable, which is the whole reason
		// detached and fleet containers are not removed when they exit.
		if _, err := resolveSession(listSessions(t, rt, true), name); err != nil {
			t.Errorf("a finished session is unreachable by name: %v", err)
		}
	})

	// --follow is the usage the listing's own hint advertises, and it blocks
	// until the guest is done rather than printing the backlog and leaving.
	t.Run("follow blocks until the guest exits", func(t *testing.T) {
		name := "sandbox-sesstest-follow-" + stamp
		id := startSession(t, name, sessionLabels(stampedBranch(t, "follow")),
			"sh", "-c", "echo first; sleep 2; echo second")

		var out syncBuffer
		done := make(chan error, 1)
		go func() { done <- rt.Logs(context.Background(), id, true, &out, &out) }()

		// If --follow were dropped, this returns with only "first" in hand — long
		// before the guest has printed "second" two seconds later.
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("Logs --follow: %v", err)
			}
		case <-time.After(60 * time.Second):
			t.Fatal("Logs --follow never returned after the guest exited")
		}
		for _, want := range []string{"first", "second"} {
			if !strings.Contains(out.String(), want) {
				t.Errorf("--follow did not stream %q: %q", want, out.String())
			}
		}
	})

	// The bargain in Logs: a read the caller ended is how following is *meant* to
	// stop, so a context-cancelled failure is reported as success. Without it,
	// the Ctrl-C the help text tells you to press exits non-zero.
	t.Run("follow ends quietly when the reader stops", func(t *testing.T) {
		name := "sandbox-sesstest-followstop-" + stamp
		id := startSession(t, name, sessionLabels(stampedBranch(t, "followstop")),
			"sh", "-c", "i=0; while [ $i -lt 120 ]; do echo tick; sleep 1; i=$((i+1)); done")

		ctx, cancel := context.WithCancel(context.Background())
		var out syncBuffer
		done := make(chan error, 1)
		go func() { done <- rt.Logs(ctx, id, true, &out, &out) }()

		waitForOutput(t, &out, "tick", 30*time.Second, cancel)
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("a cancelled --follow reported an error: %v", err)
			}
		case <-time.After(15 * time.Second):
			t.Fatal("Logs --follow did not return after its context was cancelled")
		}
		if got := containerState(t, id); got != "running" {
			t.Errorf("reading a session's logs stopped it: container is %q", got)
		}
	})
}

// TestAttachAgainstDocker covers the three things attaching has to get right,
// and the middle one is the reason this file exists.
func TestAttachAgainstDocker(t *testing.T) {
	if !dockerAvailable() {
		t.Skip("docker daemon not available")
	}
	rt := runtime.NewEngine("docker")
	stamp := time.Now().Format("150405.000")
	const chatty = `i=0; while [ $i -lt 240 ]; do echo tick; sleep 1; i=$((i+1)); done`

	t.Run("detaching leaves the agent running", func(t *testing.T) {
		name := "sandbox-sesstest-attach-" + stamp
		id := startSession(t, name, sessionLabels(stampedBranch(t, "attach")), "sh", "-c", chatty)

		info := find(t, listSessions(t, rt, true), id, "the attach target")
		if info.OpenStdin {
			t.Errorf("a container started without -i reports OpenStdin=true")
		}
		notes := strings.Join(attachNotes(info), "\n")
		if !strings.Contains(notes, "no keyboard") {
			t.Errorf("attach does not warn that a detached session cannot be typed at:\n%s", notes)
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		var seen syncBuffer
		done := make(chan error, 1)
		go func() { done <- rt.Attach(ctx, id, strings.NewReader(""), &seen, &seen) }()

		// Waiting for the guest's output first: cancelling before the client is
		// attached would prove nothing about detaching from one.
		waitForOutputOrError(t, &seen, "tick", done, 30*time.Second, cancel)
		cancel()
		select {
		case err := <-done:
			// A cancelled attach is how detaching ends; it is not a failure.
			if err != nil {
				t.Errorf("Attach reported an error on detach: %v", err)
			}
		case <-time.After(15 * time.Second):
			t.Fatal("Attach did not return after its context was cancelled")
		}
		if got := containerState(t, id); got != "running" {
			t.Fatalf("detaching stopped the agent: container is %q, want running", got)
		}
	})

	// The invariant the flag exists for, tested with the signal it governs, and
	// tested differentially: the same guest and the same SIGINT, with only
	// --sig-proxy changed between the two halves.
	//
	// Cancelling a context is not this test. exec.CommandContext SIGKILLs the
	// client, which no signal-forwarding setting can intercept, so the guest
	// survives with or without the flag — which is why the subtest above proves
	// something weaker than it looks. Ctrl-C sends **SIGINT to the client**, and
	// that is the case docker forwards by default.
	//
	// That our own Attach renders the flag is pinned separately, by
	// TestAttachRendersSigProxyFalse in internal/runtime: the two are halves of
	// one claim, and neither half catches a deletion on its own.
	t.Run("ctrl-c reaches the guest only with sig-proxy on", func(t *testing.T) {
		// A guest that would die of SIGINT if one ever arrived. The trap is not
		// optional: PID 1 gets no default dispositions, so an untrapped guest
		// ignores SIGINT and both halves would pass for the wrong reason.
		const dies = `trap 'echo caught-int; exit 7' INT; i=0; while [ $i -lt 240 ]; do echo tick; sleep 1; i=$((i+1)); done`

		protected := startSession(t, "sandbox-sesstest-sigoff-"+stamp,
			sessionLabels(stampedBranch(t, "sigoff")), "sh", "-c", dies)
		interruptAttachedClient(t, protected, false)

		// Settle: if the signal were being forwarded, the trap fires within a
		// second, so this is long enough for the wrong answer to show up.
		time.Sleep(3 * time.Second)
		if got := containerState(t, protected); got != "running" {
			t.Errorf("Ctrl-C on an attached session stopped the agent: container is %q", got)
		}
		var out, errb bytes.Buffer
		if err := rt.Logs(context.Background(), protected, false, &out, &errb); err != nil {
			t.Fatalf("reading the attached session's logs: %v", err)
		}
		if strings.Contains(out.String(), "caught-int") {
			t.Errorf("SIGINT reached the guest despite --sig-proxy=false")
		}

		// The control. Without it the assertions above hold for any guest that
		// simply never receives a signal, and the test would keep passing after
		// the flag was deleted.
		exposed := startSession(t, "sandbox-sesstest-sigon-"+stamp,
			sessionLabels(stampedBranch(t, "sigon")), "sh", "-c", dies)
		interruptAttachedClient(t, exposed, true)

		waitForState(t, exposed, "exited", 30*time.Second)
		if code := exitCodeOf(t, exposed); code != 7 {
			t.Errorf("with sig-proxy on the guest exited %d, want 7 (its own SIGINT handler)", code)
		}
	})

	// The other shape a session comes in: a console run (Studio starts every run
	// detached, so `-dit` is how an agent can still be answered). OpenStdin has
	// to come back *true* from docker for the note to be correct — asserting only
	// the false case would pass just as well if the field stopped decoding.
	t.Run("a console session reports a keyboard", func(t *testing.T) {
		name := "sandbox-sesstest-console-" + stamp
		id := startSessionWith(t, name, sessionLabels(stampedBranch(t, "console")),
			[]string{"-i", "-t"}, "sh", "-c", chatty)

		info := find(t, listSessions(t, rt, true), id, "the console session")
		if !info.OpenStdin {
			t.Errorf("a container started with -i reports OpenStdin=false")
		}
		if !info.TTY {
			t.Errorf("a container started with -t reports TTY=false")
		}
		notes := strings.Join(attachNotes(info), "\n")
		if strings.Contains(notes, "no keyboard") {
			t.Errorf("attach tells a console session it cannot be typed at:\n%s", notes)
		}
	})
}

// TestKillIsGracefulAndForceIsNot proves the two paths are different signals
// rather than a different word: the polite guest catches SIGTERM and gets to run
// one more instruction, the stubborn one ignores it and only SIGKILL ends it.
//
// Both guests install a trap deliberately, and the polite one is the reason:
// **PID 1 does not get default signal dispositions.** A container running plain
// `sleep 300` ignores SIGTERM entirely, so `docker stop` falls through its 10s
// grace period and SIGKILLs — which looks like a graceful stop that "worked"
// (the container did stop) while proving the opposite of what it claims. The
// echo is the evidence that matters: an agent finishing the file it was writing
// is exactly a guest running code after the signal arrives.
//
// Subtests rather than one body, because a regression in the graceful path is
// the failure this test exists to catch and it must not also delete the
// --force coverage on its way out.
func TestKillIsGracefulAndForceIsNot(t *testing.T) {
	if !dockerAvailable() {
		t.Skip("docker daemon not available")
	}
	rt := runtime.NewEngine("docker")
	ctx := context.Background()
	stamp := time.Now().Format("150405.000")

	polite := "sandbox-sesstest-polite-" + stamp
	politeID := startSession(t, polite, sessionLabels(stampedBranch(t, "graceful")),
		"sh", "-c", `trap 'echo caught-term; exit 42' TERM; i=0; while [ $i -lt 240 ]; do sleep 1; i=$((i+1)); done`)

	stubborn := "sandbox-sesstest-stubborn-" + stamp
	// Bounded, unlike the guest it imitates: a leaked container that ignores
	// SIGTERM and loops forever has to be removed by hand, and a cleanup does not
	// run when the test binary is killed by `go test -timeout`.
	stubbornID := startSession(t, stubborn, sessionLabels(stampedBranch(t, "stubborn")),
		"sh", "-c", `trap "" TERM; i=0; while [ $i -lt 240 ]; do sleep 1; i=$((i+1)); done`)

	stranger := "sandbox-sesstest-nobodys-" + stamp
	startSession(t, stranger, nil, "sleep", "300")

	t.Run("a foreign reference refuses the whole command", func(t *testing.T) {
		if _, err := killTargets(listSessions(t, rt, true), []string{politeID, stranger}, false); err == nil {
			t.Errorf("killTargets accepted a container sandbox-cli did not start")
		}
	})

	t.Run("graceful stop lets the guest finish", func(t *testing.T) {
		targets, err := killTargets(listSessions(t, rt, true), []string{politeID}, false)
		if err != nil {
			t.Fatalf("killTargets: %v", err)
		}
		var out bytes.Buffer
		if err := stopSessions(ctx, rt, targets, false, &out); err != nil {
			t.Fatalf("stopSessions: %v", err)
		}
		if !strings.Contains(out.String(), "stopped "+polite) {
			t.Errorf("stop did not report what it stopped: %q", out.String())
		}
		if !strings.Contains(out.String(), "logs and exit codes are kept") {
			t.Errorf("stop did not say the container is kept: %q", out.String())
		}
		waitForState(t, politeID, "exited", 30*time.Second)

		// 42 is the trap's own exit, so the guest chose how to end. A 137 here
		// would mean SIGTERM was never handled and docker's grace period did the
		// stopping — which is also why no wall-clock bound is asserted: the exit
		// code says which path ran, and a loaded daemon says nothing.
		if code := exitCodeOf(t, politeID); code != 42 {
			t.Errorf("graceful stop exited %d, want 42 (the guest's own SIGTERM handler)", code)
		}
		// "An agent gets to finish writing" is not a figure of speech: the guest
		// ran an instruction after the signal, and `logs` reads it back off a
		// session that has already finished.
		var politeOut, politeErr bytes.Buffer
		if err := rt.Logs(ctx, politeID, false, &politeOut, &politeErr); err != nil {
			t.Fatalf("reading the stopped session's logs: %v", err)
		}
		if !strings.Contains(politeOut.String(), "caught-term") {
			t.Errorf("the guest never ran its SIGTERM handler: %q", politeOut.String())
		}
	})

	t.Run("force kills a guest that ignores SIGTERM", func(t *testing.T) {
		targets, err := killTargets(listSessions(t, rt, true), []string{stubbornID}, false)
		if err != nil {
			t.Fatalf("killTargets: %v", err)
		}
		var out bytes.Buffer
		if err := stopSessions(ctx, rt, targets, true, &out); err != nil {
			t.Fatalf("stopSessions --force: %v", err)
		}
		if !strings.Contains(out.String(), "killed "+stubborn) {
			t.Errorf("--force did not report a kill: %q", out.String())
		}
		waitForState(t, stubbornID, "exited", 30*time.Second)
		// 137 is 128+SIGKILL, and this guest ignores SIGTERM — so nothing but the
		// forced path could have ended it.
		if code := exitCodeOf(t, stubbornID); code != 137 {
			t.Errorf("forced kill exited %d, want 137 (128+SIGKILL)", code)
		}
	})

	t.Run("a finished session is not an error to stop", func(t *testing.T) {
		done := find(t, listSessions(t, rt, true), politeID, "the stopped session")
		var out bytes.Buffer
		if err := stopSessions(ctx, rt, []runtime.ContainerInfo{done}, false, &out); err != nil {
			t.Errorf("stopping an already-stopped session reported an error: %v", err)
		}
		if !strings.Contains(out.String(), "had already exited") {
			t.Errorf("no word about a session that had already finished: %q", out.String())
		}
	})
}

// interruptAttachedClient attaches to a container the way a terminal does,
// waits until output is actually arriving, and sends the client the SIGINT that
// Ctrl-C would.
//
// It does not require the client to *exit*: measured against Docker 28, an
// attach with --sig-proxy=false swallows SIGINT and keeps reading, so requiring
// an exit would fail on the very configuration the flag is there to produce.
// The claim under test is about the guest, not about the client, so the client
// is simply killed on the way out.
func interruptAttachedClient(t *testing.T, id string, sigProxy bool) {
	t.Helper()
	var seen syncBuffer
	client := exec.Command("docker", "attach", fmt.Sprintf("--sig-proxy=%t", sigProxy), id)
	client.Stdout = &seen
	client.Stderr = &seen
	if err := client.Start(); err != nil {
		t.Fatalf("starting docker attach: %v", err)
	}
	t.Cleanup(func() {
		if client.Process != nil {
			client.Process.Kill()
			client.Wait()
		}
	})

	// Signalling before the client has attached would prove nothing: the guest
	// would be unsignalled either way.
	waitForOutput(t, &seen, "tick", 30*time.Second, nil)
	if err := client.Process.Signal(os.Interrupt); err != nil {
		t.Fatalf("interrupting the attach client: %v", err)
	}
}

// waitForOutput blocks until want appears in buf, failing the test if it never
// does. onTimeout runs before the failure, for callers holding something that
// has to be released (a context that would otherwise leak a goroutine).
func waitForOutput(t *testing.T, buf *syncBuffer, want string, within time.Duration, onTimeout func()) {
	t.Helper()
	waitForOutputOrError(t, buf, want, nil, within, onTimeout)
}

// waitForOutputOrError is waitForOutput that also watches a command's result
// channel. A read that failed immediately — a daemon that refused, a container
// that had already exited — otherwise spends the whole timeout looking at an
// empty buffer and then blames the guest, discarding the error that said why.
func waitForOutputOrError(t *testing.T, buf *syncBuffer, want string, done <-chan error, within time.Duration, onTimeout func()) {
	t.Helper()
	deadline := time.After(within)
	tick := time.NewTicker(150 * time.Millisecond)
	defer tick.Stop()
	for {
		if strings.Contains(buf.String(), want) {
			return
		}
		select {
		case err := <-done:
			t.Fatalf("the command ended before %q appeared (%v): %q", want, err, buf.String())
		case <-deadline:
			if onTimeout != nil {
				onTimeout()
			}
			t.Fatalf("no %q from the session within %s: %q", want, within, buf.String())
		case <-tick.C:
		}
	}
}

// syncBuffer is a bytes.Buffer the reading goroutine writes to while the test
// reads it. Without the lock this is a data race the race detector will and
// should fail on.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
