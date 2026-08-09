package doctor

import (
	"context"
	"strings"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/runtime"
)

// recordingHost captures what the firewall probe was asked about.
type recordingHost struct {
	gotRuntime string
	asked      bool
}

func (h *recordingHost) Available(context.Context) error                   { return nil }
func (h *recordingHost) SeccompUnavailable(context.Context) (bool, bool)   { return false, true }
func (h *recordingHost) Runtimes(context.Context) ([]string, error)        { return []string{"runc"}, nil }
func (h *recordingHost) ImagePresent(context.Context, string) (bool, bool) { return true, true }
func (h *recordingHost) StrongerRuntimeSupport(context.Context) runtime.RuntimeSupport {
	return runtime.RuntimeSupport{All: []string{"runc"}, Default: "runc", Complete: true, Known: true}
}

func (h *recordingHost) FirewallProgrammable(_ context.Context, _, runtimeName string) (runtime.FirewallProbe, string) {
	h.asked = true
	h.gotRuntime = runtimeName
	return runtime.FirewallOK, ""
}

// TestFirewallIsProbedUnderTheSelectedRuntime.
//
// Whether a container can program iptables is a property of the kernel it gets,
// and runtimes differ: gVisor serves only the legacy backend, and only when
// installed with --net-raw. A host running gVisor reported a healthy firewall
// from a runc probe while a runsc run could not program a single rule — the
// preflight passing and the launch failing, which is what ClassifyRuntimeGap was
// centralised to prevent.
func TestFirewallIsProbedUnderTheSelectedRuntime(t *testing.T) {
	prev := NewRuntime
	t.Cleanup(func() { NewRuntime = prev })

	for _, selected := range []string{"runsc", "kata-runtime", ""} {
		t.Run("runtime="+selected, func(t *testing.T) {
			host := &recordingHost{}
			NewRuntime = func(string) Runtime { return host }

			RunChecks(context.Background(), "dev", "docker", selected)

			if !host.asked {
				t.Fatal("the firewall was never probed")
			}
			if host.gotRuntime != selected {
				t.Errorf("probed runtime = %q, want %q: a probe on the wrong kernel answers a question nobody asked",
					host.gotRuntime, selected)
			}
		})
	}
}

// unregisteredHost stands in for an engine that does not have the runtime.
type unregisteredHost struct{ recordingHost }

func (h *unregisteredHost) FirewallProgrammable(_ context.Context, _, runtimeName string) (runtime.FirewallProbe, string) {
	h.asked = true
	h.gotRuntime = runtimeName
	return runtime.FirewallUnknown, "the runtime " + runtimeName + " is not registered, so nothing could be probed on it"
}

// TestUnregisteredRuntimeIsUnknownNotBlocked.
//
// A probe that could not start because the runtime does not exist has answered
// nothing about whether this host can filter. Reporting it as Weak told the
// operator their daemon was at fault and offered `--network default` — advice to
// switch the egress allowlist off in order to fix a misspelled flag — while
// checkRuntimes reported the real GapMissing one line below.
func TestUnregisteredRuntimeIsUnknownNotBlocked(t *testing.T) {
	prev := NewRuntime
	t.Cleanup(func() { NewRuntime = prev })
	host := &unregisteredHost{}
	NewRuntime = func(string) Runtime { return host }

	for _, c := range RunChecks(context.Background(), "prod", "docker", "runsx") {
		if c.Name != "egress firewall" {
			continue
		}
		if c.Status != StatusUnknown {
			t.Errorf("status = %v, want StatusUnknown: a probe that never ran is not a verdict on the host", c.Status)
		}
		if strings.Contains(c.Remedy, "--network default") {
			t.Errorf("a missing runtime must not be answered by dropping the allowlist: %q", c.Remedy)
		}
	}
}

// TestBlockedRemedyNamesTheRuntimeCause: once the probe runs under a selected
// runtime, "rootless or userns-remapped" is no longer the only explanation, and
// on gVisor it is not the explanation at all.
func TestBlockedRemedyNamesTheRuntimeCause(t *testing.T) {
	prev := NewRuntime
	t.Cleanup(func() { NewRuntime = prev })
	NewRuntime = func(string) Runtime { return &blockedHost{} }

	for _, c := range RunChecks(context.Background(), "dev", "docker", "runsc") {
		if c.Name != "egress firewall" {
			continue
		}
		if !strings.Contains(c.Remedy, "net-raw") {
			t.Errorf("the remedy should name the runtime-side cause, got %q", c.Remedy)
		}
	}
}

type blockedHost struct{ recordingHost }

func (h *blockedHost) FirewallProgrammable(context.Context, string, string) (runtime.FirewallProbe, string) {
	return runtime.FirewallBlocked, "Couldn't load match `conntrack'"
}
