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
type seccompOK struct{ runtime.Runtime }

func (seccompOK) SeccompUnavailable(context.Context) (bool, bool) { return false, true }

// TestProdActuallyLaunchesAContainer covers the half of prod that the author's
// own machine could not: a daemon that *does* apply seccomp, where
// enforceSeccomp passes and the container is genuinely started.
//
// The blocker this guards against was invisible precisely because the only
// daemon it was tried on refused first, so the run never got as far as docker.
func TestProdActuallyLaunchesAContainer(t *testing.T) {
	proj := t.TempDir()
	cfg := config.Default()
	cfg.Profile = config.ProfileProd
	cfg.Security.Seccomp = config.SeccompRequired
	cfg.Network.Baseline = new(bool) // false
	cfg.Network.Allow = []string{"api.anthropic.com"}

	sess := newTestSession(t, cfg)
	sess.Runtime = seccompOK{sess.Runtime}

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
