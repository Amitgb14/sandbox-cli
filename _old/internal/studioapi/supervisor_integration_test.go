//go:build docker_integration

package studioapi

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/config"
	"github.com/Amitgb14/sandbox-cli/internal/runtime"
	"github.com/Amitgb14/sandbox-cli/internal/sandbox"
)

// The supervisor against a real daemon.
//
// Every other test of it drives a fake engine, which is right for the decisions
// — the workspace gate, the chain, what a retry is built with — because those
// are things this package computes. Two of its claims are not that: they are
// claims about what **docker** does, and a fake that agrees with them proves
// only that the fake was written by the same person as the code.
//
//   - A finished container's exit code and finish time come back through
//     `listRuns` in the shape `settle` reads them.
//   - `docker rename` really frees the old name, immediately, so the retry can
//     take it — the whole design rests on that, since a detached container's
//     name is what enforces one-agent-per-branch.
//
// What is deliberately *not* here is a full failover: that needs an agent image
// and a login, so it would test a vendor's CLI rather than this loop, and it is
// covered end to end by hand (see the CHANGELOG entry). The containers are plain
// `alpine`, the same choice internal/cli's session tests make and for the same
// reason.

// stamped keeps these tests from colliding with whatever else is on the machine:
// the listing is host-wide, filtered only by sandbox.cli.
func stamped(t *testing.T, name string) string {
	t.Helper()
	return fmt.Sprintf("sandbox-sv-%s-%s", name, time.Now().Format("150405.000000"))
}

func startContainer(t *testing.T, name, branch string, guest ...string) string {
	t.Helper()
	t.Cleanup(func() { exec.Command("docker", "rm", "-f", name).Run() })

	args := []string{"run", "-d", "--name", name,
		"--label", sandbox.LabelCLI + "=1",
		"--label", sandbox.LabelBranch + "=" + branch,
		"--label", sandbox.LabelAgent + "=claude",
		"alpine",
	}
	args = append(args, guest...)

	var stderr bytes.Buffer
	cmd := exec.Command("docker", args...)
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("starting %s: %v: %s", name, err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(string(out))
}

func realServer(t *testing.T) *Server {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cfg := config.Default()
	cfg.Profile = config.ProfileDev
	s, err := New(cfg, t.TempDir())
	if err != nil {
		t.Fatalf("building a server on the real engine: %v", err)
	}
	if err := s.RT.Available(context.Background()); err != nil {
		t.Skipf("docker is not available: %v", err)
	}
	return s
}

func waitUntilExited(t *testing.T, id string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		out, err := exec.Command("docker", "inspect", "-f", "{{.State.Status}}", id).Output()
		if err == nil && strings.TrimSpace(string(out)) == "exited" {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("%s never exited", id)
}

// What the loop reads off a container that has actually finished.
func TestSupervisorReadsARealExit(t *testing.T) {
	s := realServer(t)
	branch := stamped(t, "exit")
	id := startContainer(t, branch, branch, "sh", "-c", "exit 3")
	waitUntilExited(t, id)

	sv := s.sv()
	// No workspace to compare, which is the honest state for an alpine container
	// in a temp directory — and it is also the case the gate must refuse to
	// retry, so a chain is registered to prove it settles rather than fires.
	sv.treeOf = func(string) (string, error) { return "", fmt.Errorf("not a repository") }
	sv.supervise(&watch{
		container: id,
		name:      branch,
		agent:     "claude",
		remaining: []string{"codex"},
		routeID:   "int-test",
		attempt:   1,
	})

	// The engine's own answer, through the same listing the Runs screen uses.
	var found runtime.ContainerInfo
	infos, err := s.listRuns(context.Background(), true, nil)
	if err != nil {
		t.Fatalf("listing: %v", err)
	}
	for _, c := range infos {
		if c.ID == id {
			found = c
		}
	}
	if found.ID == "" {
		t.Fatalf("a real sandbox-labelled container is missing from listRuns")
	}
	if found.ExitCode != 3 {
		t.Errorf("ExitCode = %d, want 3 — settle reads this to decide", found.ExitCode)
	}
	if found.FinishedAt.IsZero() {
		t.Error("FinishedAt is zero on an exited container; tick uses it to know the run is over")
	}
	if found.Running() {
		t.Error("an exited container reports itself as running")
	}

	sv.tick(context.Background())

	// Unmeasurable workspace counts as work done, so this settles rather than
	// starting anything.
	sv.mu.Lock()
	defer sv.mu.Unlock()
	if _, still := sv.watched[id]; still {
		t.Error("the run is still watched after it finished")
	}
}

// The invariant the whole design rests on: renaming the dead attempt frees its
// name *now*, so the retry can be created under it. If docker held the name for
// any window, one-agent-per-branch would be enforced by nothing during it.
func TestRenamingTheDeadAttemptFreesItsName(t *testing.T) {
	s := realServer(t)
	branch := stamped(t, "handover")
	// The name a detached run on this branch would be given, asked of the same
	// function the launch asks, so this exercises handOverName's real decision
	// rather than a name chosen to make it fire.
	opts := sandbox.Options{Detach: true, RepoID: "svtest", Branch: branch}
	name := sandbox.ContainerName(opts)

	first := startContainer(t, name, branch, "sh", "-c", "exit 1")
	waitUntilExited(t, first)

	sv := s.sv()
	restore := sv.handOverName(context.Background(), &watch{
		container: first, name: name, attempt: 1,
	}, opts)
	t.Cleanup(func() { exec.Command("docker", "rm", "-f", name+"-attempt1").Run() })

	// Free the moment the rename returns — if docker held it for any window,
	// one-agent-per-branch would be enforced by nothing during it.
	second := startContainer(t, name, branch, "sh", "-c", "sleep 5")
	if second == "" {
		t.Fatal("the retry could not take the name the failed attempt gave up")
	}

	// And the dead attempt is still there with its exit code, which is why it is
	// renamed rather than removed: its logs are the evidence for the failover.
	out, err := exec.Command("docker", "inspect", "-f", "{{.State.ExitCode}}", name+"-attempt1").Output()
	if err != nil {
		t.Fatalf("the failed attempt is gone after the handover: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != "1" {
		t.Errorf("the failed attempt's exit code is %q, want 1", got)
	}

	// The undo works against a real daemon too. It runs when a launch never
	// happens, and a container left under a name saying it was superseded — when
	// nothing superseded it — is a false record that outlives the incident.
	exec.Command("docker", "rm", "-f", name).Run()
	restore()
	if out, err := exec.Command("docker", "inspect", "-f", "{{.Name}}", first).Output(); err != nil {
		t.Fatalf("inspecting the restored container: %v", err)
	} else if got := strings.TrimPrefix(strings.TrimSpace(string(out)), "/"); got != name {
		t.Errorf("after restore the container is named %q, want %q", got, name)
	}
}
