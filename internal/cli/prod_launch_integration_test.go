//go:build docker_integration

package cli

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/config"
	"github.com/Amitgb14/sandbox-cli/internal/runtime"
	"github.com/Amitgb14/sandbox-cli/internal/sandbox"
)

// seccompOK is a runtime that reports the daemon applies a syscall filter,
// standing in for the branch a daemon reporting profile=unconfined can never
// reach. Everything else is the real docker backend, so the container is really
// started with the argv prod produces.
// seccompOK is a daemon that applies a syscall filter and reports one runtime
// with a kernel of its own, already selected.
//
// Both halves matter, and the second was missing: embedding the Runtime
// *interface* promotes only its own methods, so a wrapper that answers
// SeccompUnavailable silently fails the StrongerRuntimeSupport type assertion in
// enforceKernelBoundary — the gate skipped itself, and the only test that
// launches a prod container never exercised it. Answering both keeps this a test
// of a prod run rather than of a prod run with one control switched off.
type seccompOK struct{ runtime.Runtime }

func (seccompOK) SeccompUnavailable(context.Context) (bool, bool) { return false, true }

// StrongerRuntimeSupport reports what a CI machine actually has: no runtime with
// a kernel of its own. That is the unverified case, which prod warns about and
// permits — so the gate is exercised, takes its real decision, and the container
// still launches under the host default. Naming one instead would satisfy the
// gate and then fail at `docker run`, since no machine here has Kata installed;
// the refusing path is covered against a real Session in
// internal/sandbox/kernelboundary_test.go.
func (seccompOK) StrongerRuntimeSupport(context.Context) runtime.RuntimeSupport {
	return runtime.RuntimeSupport{Known: true}
}

// TestProdActuallyLaunchesAContainer covers the half of prod that the author's
// own machine could not: a daemon that *does* apply seccomp, where
// enforceSeccomp passes and the container is genuinely started.
//
// The blocker this guards against was invisible precisely because the only
// daemon it was tried on refused first, so the run never got as far as docker.
func TestProdActuallyLaunchesAContainer(t *testing.T) {
	proj := testWorkspace(t)
	cfg := config.Default()
	cfg.Profile = config.ProfileProd
	cfg.Security.Seccomp = config.SeccompRequired
	cfg.Network.Baseline = new(bool) // false
	cfg.Network.Allow = []string{"api.anthropic.com"}

	sess := newTestSession(t, cfg)
	sess.Runtime = seccompOK{sess.Runtime}

	// What this test is for is the launch, which no unit test can reach. The
	// boundary gate is exercised rather than skipped — seccompOK answers the
	// question it asks, and answers it the way a CI machine really would. A prod
	// run on a host that does have Kata is the manual check task 3 §1 records.

	name, err := sess.Start(context.Background(), sandbox.Options{
		Project: proj,
		Command: []string{"sleep", "10"},
	}, false)
	if err != nil {
		t.Fatalf("a prod run could not start a container: %v", err)
	}
	defer exec.Command("docker", "rm", "-f", name).Run()

	out, _ := exec.Command("docker", "inspect", name, "--format", "{{.State.Status}}").Output()
	if got := strings.TrimSpace(string(out)); got != "running" {
		t.Errorf("prod container status = %q, want running", got)
	}
}
