package doctor

import (
	"context"
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
