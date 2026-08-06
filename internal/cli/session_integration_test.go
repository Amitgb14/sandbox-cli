//go:build docker_integration

package cli

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/runtime"
	"github.com/Amitgb14/sandbox-cli/internal/sandbox"
)

// The session commands against a real daemon.
//
// Everything here was previously proved only against a fake backend, and three
// of the claims cannot be proved that way at all, because they are claims about
// what docker does rather than about what this package computes:
//
//   - `attach` cannot kill. A fake Attacher returns whatever it is told to;
//     whether ending the client leaves the guest running is a property of
//     `--sig-proxy=false` and of the daemon owning the container.
//   - `--force` is a different signal, not a louder word. Stop and Kill are two
//     methods on an interface until something observes 143 against 137.
//   - The listing's fields are parsed from `docker inspect`. OpenStdin, the exit
//     code and the state strings are all shapes a fake supplies by construction.
//
// The containers are plain `alpine`: this is a test of the supervision layer,
// not of the base image, and building one would make the suite slower for no
// added coverage.

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
	args := []string{"run", "-d", "--name", name}
	for k, v := range labels {
		args = append(args, "--label", k+"="+v)
	}
	args = append(args, "alpine")
	args = append(args, guest...)

	out, err := exec.Command("docker", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("starting %s: %v: %s", name, err, strings.TrimSpace(string(out)))
	}
	t.Cleanup(func() { exec.Command("docker", "rm", "-f", name).Run() })
	return strings.TrimSpace(string(out))
}

// waitForState polls until the container reaches want, or fails the test.
func waitForState(t *testing.T, name, want string, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	var last string
	for time.Now().Before(deadline) {
		out, err := exec.Command("docker", "inspect", "-f", "{{.State.Status}}", name).Output()
		if err == nil {
			if last = strings.TrimSpace(string(out)); last == want {
				return
			}
		}
		time.Sleep(150 * time.Millisecond)
	}
	t.Fatalf("%s never reached %q (last %q) within %s", name, want, last, within)
}

func exitCodeOf(t *testing.T, name string) int {
	t.Helper()
	out, err := exec.Command("docker", "inspect", "-f", "{{.State.ExitCode}}", name).Output()
	if err != nil {
		t.Fatalf("inspecting %s: %v", name, err)
	}
	var code int
	if _, err := fmt.Sscanf(strings.TrimSpace(string(out)), "%d", &code); err != nil {
		t.Fatalf("reading exit code of %s: %v", name, err)
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

func has(t *testing.T, infos []runtime.ContainerInfo, id string) bool {
	t.Helper()
	for _, c := range infos {
		if c.ID == id {
			return true
		}
	}
	return false
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
	ctx := context.Background()
	stamp := time.Now().Format("150405.000")

	running := "sandbox-sesstest-run-" + stamp
	runningID := startSession(t, running, sessionLabels("feature-a"), "sleep", "300")

	finished := "sandbox-sesstest-done-" + stamp
	finishedID := startSession(t, finished, sessionLabels("docs"), "sh", "-c", "exit 0")
	waitForState(t, finished, "exited", 20*time.Second)

	// Named like ours, labelled as nobody's.
	stranger := "sandbox-sesstest-theirs-" + stamp
	startSession(t, stranger, nil, "sleep", "300")

	live, err := sandboxSessions(ctx, rt, "docker", false)
	if err != nil {
		t.Fatalf("sandboxSessions: %v", err)
	}
	if !has(t, live, runningID) {
		t.Errorf("the running session is missing from the default listing")
	}
	if has(t, live, finishedID) {
		t.Errorf("a finished session appears without --all")
	}
	for _, c := range live {
		if c.Name == stranger {
			t.Errorf("a container merely named like a sandbox was listed: %s", c.Name)
		}
	}

	all, err := sandboxSessions(ctx, rt, "docker", true)
	if err != nil {
		t.Fatalf("sandboxSessions --all: %v", err)
	}
	done := find(t, all, finishedID, "the finished session")
	if got := sessionStatus(done); got != "exited (0)" {
		t.Errorf("sessionStatus of a finished session = %q, want %q", got, "exited (0)")
	}
	if done.FinishedAt.IsZero() || done.StartedAt.IsZero() {
		t.Errorf("timestamps not parsed from docker inspect: started %v finished %v",
			done.StartedAt, done.FinishedAt)
	}

	// The four ways somebody has a session in front of them, all resolving to the
	// same container: the id from the listing, the whole id, the container name,
	// and the branch — which is how a detached or fleet run is addressed.
	for _, ref := range []string{shortID(runningID), runningID, running, "feature-a"} {
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
	for _, want := range []string{"KIND", "interactive", shortID(runningID), "feature-a", "exited (0)"} {
		if !strings.Contains(table.String(), want) {
			t.Errorf("listing does not mention %q:\n%s", want, table.String())
		}
	}
}

// TestLogsKeepsTheStreamsApart is the claim that a fake writer cannot make:
// docker returns stdout and stderr separately for a container started without a
// tty, and `logs` keeps them that way so piping a fleet agent's output into a
// file does not swallow its diagnostics.
func TestLogsKeepsTheStreamsApart(t *testing.T) {
	if !dockerAvailable() {
		t.Skip("docker daemon not available")
	}
	rt := runtime.NewEngine("docker")
	ctx := context.Background()
	name := "sandbox-sesstest-logs-" + time.Now().Format("150405.000")

	id := startSession(t, name, sessionLabels("logs"),
		"sh", "-c", "echo to-stdout; echo to-stderr 1>&2")
	waitForState(t, name, "exited", 20*time.Second)

	var out, errb bytes.Buffer
	if err := rt.Logs(ctx, id, false, &out, &errb); err != nil {
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

	// And a finished session's logs are readable at all, which is the whole
	// reason detached and fleet containers are not removed when they exit.
	infos, err := sandboxSessions(ctx, rt, "docker", true)
	if err != nil {
		t.Fatalf("sandboxSessions: %v", err)
	}
	if _, err := resolveSession(infos, name); err != nil {
		t.Errorf("a finished session is unreachable by name: %v", err)
	}
}

// TestAttachDetachesWithoutStoppingTheAgent is the one that could never be
// written against a fake.
//
// Ending the attach — what Ctrl-C does, and what a cancelled context does here —
// must leave the guest working. `--sig-proxy=false` is the load-bearing flag,
// and the daemon owning the container is what makes the guest survive the client
// dying. Both are properties of docker, so only docker can prove it.
func TestAttachDetachesWithoutStoppingTheAgent(t *testing.T) {
	if !dockerAvailable() {
		t.Skip("docker daemon not available")
	}
	rt := runtime.NewEngine("docker")
	name := "sandbox-sesstest-attach-" + time.Now().Format("150405.000")

	id := startSession(t, name, sessionLabels("attach"),
		"sh", "-c", "while true; do echo tick; sleep 1; done")

	infos, err := sandboxSessions(context.Background(), rt, "docker", true)
	if err != nil {
		t.Fatalf("sandboxSessions: %v", err)
	}
	info := find(t, infos, id, "the attach target")

	// A detached run has no stdin, and `attach` says so before handing the
	// terminal over. That the field is false here is docker's answer, not ours.
	if info.OpenStdin {
		t.Errorf("a container started without -i reports OpenStdin=true")
	}
	notes := strings.Join(attachNotes(info), "\n")
	if !strings.Contains(notes, "no keyboard") {
		t.Errorf("attach does not warn that a detached session cannot be typed at:\n%s", notes)
	}

	ctx, cancel := context.WithCancel(context.Background())
	var seen syncBuffer
	done := make(chan error, 1)
	go func() { done <- rt.Attach(ctx, id, strings.NewReader(""), &seen, &seen) }()

	// Wait for the guest's output to actually reach us: cancelling before the
	// client is attached would prove nothing about detaching from one.
	deadline := time.Now().Add(30 * time.Second)
	for !strings.Contains(seen.String(), "tick") {
		if time.Now().After(deadline) {
			cancel()
			t.Fatalf("no output from the attached session within 30s: %q", seen.String())
		}
		time.Sleep(200 * time.Millisecond)
	}

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

	// The point of the test.
	out, err := exec.Command("docker", "inspect", "-f", "{{.State.Status}}", name).Output()
	if err != nil {
		t.Fatalf("inspecting after detach: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != "running" {
		t.Fatalf("detaching stopped the agent: container is %q, want running", got)
	}
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
func TestKillIsGracefulAndForceIsNot(t *testing.T) {
	if !dockerAvailable() {
		t.Skip("docker daemon not available")
	}
	rt := runtime.NewEngine("docker")
	ctx := context.Background()
	stamp := time.Now().Format("150405.000")

	polite := "sandbox-sesstest-polite-" + stamp
	politeID := startSession(t, polite, sessionLabels("graceful"),
		"sh", "-c", `trap 'echo caught-term; exit 42' TERM; while true; do sleep 0.2; done`)

	stubborn := "sandbox-sesstest-stubborn-" + stamp
	startSession(t, stubborn, sessionLabels("stubborn"),
		"sh", "-c", `trap "" TERM; while true; do sleep 1; done`)

	stranger := "sandbox-sesstest-nobodys-" + stamp
	startSession(t, stranger, nil, "sleep", "300")

	infos, err := sandboxSessions(ctx, rt, "docker", true)
	if err != nil {
		t.Fatalf("sandboxSessions: %v", err)
	}

	// A reference that names a container we did not start resolves to nothing,
	// and the whole command is refused rather than half-run.
	if _, err := killTargets(infos, []string{"graceful", stranger}, false); err == nil {
		t.Errorf("killTargets accepted a container sandbox-cli did not start")
	}

	// Graceful: SIGTERM, addressed by branch.
	targets, err := killTargets(infos, []string{"graceful"}, false)
	if err != nil {
		t.Fatalf("killTargets: %v", err)
	}
	var out bytes.Buffer
	graceStarted := time.Now()
	if err := stopSessions(ctx, rt, targets, false, &out); err != nil {
		t.Fatalf("stopSessions: %v", err)
	}
	if !strings.Contains(out.String(), "stopped "+polite) {
		t.Errorf("stop did not report what it stopped: %q", out.String())
	}
	if !strings.Contains(out.String(), "logs and exit codes are kept") {
		t.Errorf("stop did not say the container is kept: %q", out.String())
	}
	waitForState(t, polite, "exited", 30*time.Second)
	// 42 is the trap's own exit, so the guest chose how to end. A 137 here would
	// mean SIGTERM was never handled and docker's grace period did the stopping.
	if code := exitCodeOf(t, polite); code != 42 {
		t.Errorf("graceful stop exited %d, want 42 (the guest's own SIGTERM handler)", code)
	}
	if elapsed := time.Since(graceStarted); elapsed > 8*time.Second {
		t.Errorf("graceful stop took %s, i.e. it waited out the grace period", elapsed)
	}
	// The evidence that "an agent gets to finish writing" is not a figure of
	// speech: the guest ran an instruction after the signal, and `logs` can read
	// it back off a session that has already finished.
	var politeOut, politeErr bytes.Buffer
	if err := rt.Logs(ctx, politeID, false, &politeOut, &politeErr); err != nil {
		t.Fatalf("reading the stopped session's logs: %v", err)
	}
	if !strings.Contains(politeOut.String(), "caught-term") {
		t.Errorf("the guest never ran its SIGTERM handler: %q", politeOut.String())
	}

	// Forced: SIGKILL, on a guest that ignores SIGTERM entirely.
	infos, err = sandboxSessions(ctx, rt, "docker", true)
	if err != nil {
		t.Fatalf("sandboxSessions: %v", err)
	}
	targets, err = killTargets(infos, []string{"stubborn"}, false)
	if err != nil {
		t.Fatalf("killTargets: %v", err)
	}
	out.Reset()
	started := time.Now()
	if err := stopSessions(ctx, rt, targets, true, &out); err != nil {
		t.Fatalf("stopSessions --force: %v", err)
	}
	if !strings.Contains(out.String(), "killed "+stubborn) {
		t.Errorf("--force did not report a kill: %q", out.String())
	}
	waitForState(t, stubborn, "exited", 30*time.Second)
	if code := exitCodeOf(t, stubborn); code != 137 {
		t.Errorf("forced kill exited %d, want 137 (128+SIGKILL)", code)
	}
	// docker's default grace period is 10s. A forced kill that took that long
	// would be SIGTERM-then-SIGKILL wearing the right exit code.
	if elapsed := time.Since(started); elapsed > 8*time.Second {
		t.Errorf("--force waited %s, which is a grace period rather than a kill", elapsed)
	}

	// Asking a finished session to stop is not an error: it is already in the
	// state the caller asked for, and says so.
	infos, err = sandboxSessions(ctx, rt, "docker", true)
	if err != nil {
		t.Fatalf("sandboxSessions: %v", err)
	}
	done := find(t, infos, findIDByName(t, infos, polite), "the stopped session")
	out.Reset()
	if err := stopSessions(ctx, rt, []runtime.ContainerInfo{done}, false, &out); err != nil {
		t.Errorf("stopping an already-stopped session reported an error: %v", err)
	}
	if !strings.Contains(out.String(), "had already exited") {
		t.Errorf("no word about a session that had already finished: %q", out.String())
	}
}

func findIDByName(t *testing.T, infos []runtime.ContainerInfo, name string) string {
	t.Helper()
	for _, c := range infos {
		if c.Name == name {
			return c.ID
		}
	}
	t.Fatalf("no listed session named %q", name)
	return ""
}

// syncBuffer is a bytes.Buffer the attach goroutine writes to while the test
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
