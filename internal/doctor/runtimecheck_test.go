package doctor

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/config"
	"github.com/Amitgb14/sandbox-cli/internal/runtime"
)

// The isolation-runtime check, driven entirely by what the daemon says.
//
// Nothing here pins a platform, and that is the point: the client's own
// operating system is not evidence about the engine, which may be on another
// machine. Every case below is a different answer from the daemon, and the
// verdict follows the answer.

type runtimeStub struct {
	names   []string
	err     error
	support *runtime.RuntimeSupport
}

func (r runtimeStub) Available(context.Context) error                 { return nil }
func (r runtimeStub) SeccompUnavailable(context.Context) (bool, bool) { return false, true }
func (r runtimeStub) Runtimes(context.Context) ([]string, error)      { return r.names, r.err }
func (r runtimeStub) FirewallProgrammable(context.Context, string) (runtime.FirewallProbe, string) {
	return runtime.FirewallUnknown, ""
}
func (r runtimeStub) ImagePresent(context.Context, string) (bool, bool) { return true, true }

// StrongerRuntimeSupport is the interface upgrade checkRuntimes looks for. A
// stub without one exercises the fallback.
func (r runtimeStub) StrongerRuntimeSupport(context.Context) runtime.RuntimeSupport {
	if r.support != nil {
		return *r.support
	}
	var strong []string
	for _, n := range r.names {
		if runtime.StrongerRuntime(n) {
			strong = append(strong, n)
		}
	}
	return runtime.RuntimeSupport{Registered: strong, Known: r.err == nil}
}

func support(registered []string, known bool) *runtime.RuntimeSupport {
	return &runtime.RuntimeSupport{Registered: registered, Known: known}
}

func TestRuntimeCheckUnderProd(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name     string
		stub     runtimeStub
		selected string
		fatal    bool // under prod
		wants    []string
	}{
		{
			name:     "registered and selected",
			stub:     runtimeStub{names: []string{"runc", "runsc"}, support: support([]string{"runsc"}, true)},
			selected: "runsc",
			wants:    []string{"runsc"},
		},
		{
			// The sharpest case: the boundary was available and unused.
			name:  "registered, nothing selected",
			stub:  runtimeStub{names: []string{"runc", "kata-runtime"}, support: support([]string{"kata-runtime"}, true)},
			fatal: true,
			wants: []string{"kata-runtime"},
		},
		{
			// Selected but not among what the engine reported. Not a refusal:
			// podman reports only its active runtime, so an absent name is not
			// proof of absence — and the launch settles it either way. The check
			// says what the engine did report.
			name:     "a different stronger runtime is reported",
			stub:     runtimeStub{names: []string{"runc", "kata-runtime"}, support: support([]string{"kata-runtime"}, true)},
			selected: "runsc",
			wants:    []string{"runsc", "kata-runtime"},
		},
		{
			// The name no list knows. gVisor's installer produces it, and
			// refusing it would be a refusal its operator cannot clear.
			name:     "an unrecognised non-default name",
			stub:     runtimeStub{names: []string{"runc"}, support: support(nil, true)},
			selected: "runsc-hostnet",
			wants:    []string{"runsc-hostnet"},
		},
		{
			// Nothing reported. Said plainly and not failed: the tool cannot tell
			// a host that could install Kata from a VM image its user does not
			// compose, and refusing on that guess broke the second kind.
			name:  "nothing reported",
			stub:  runtimeStub{names: []string{"runc"}, support: support(nil, true)},
			wants: []string{"share the host kernel"},
		},
		{
			name:  "the engine could not be asked",
			stub:  runtimeStub{names: []string{"runc"}, support: support(nil, false)},
			wants: []string{"share the host kernel"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := checkRuntimes(ctx, tc.stub, config.ProfileProd, tc.selected)
			if _, fatal := Verdict(c.Status, true); fatal != tc.fatal {
				t.Errorf("prod fatal = %v, want %v: %+v", fatal, tc.fatal, c)
			}
			for _, want := range tc.wants {
				if !strings.Contains(c.Detail+" "+c.Remedy, want) {
					t.Errorf("the finding does not mention %q: %+v", want, c)
				}
			}
			// A newline inside a tabwriter cell ends the column block, so a check
			// added after this one would silently misalign.
			if strings.Contains(c.Detail, "\n") {
				t.Errorf("detail contains a newline: %q", c.Detail)
			}
		})
	}
}

// dev reports every one of those facts and fails on none of them. That is the
// asymmetry the profiles exist for: a developer is here to read a warning, and
// even a runtime the engine has never heard of is a warning rather than a
// refusal, because the launch that follows will say so plainly.
func TestRuntimeCheckUnderDevWarnsAndNeverFails(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name     string
		stub     runtimeStub
		selected string
		warns    bool
	}{
		{name: "nothing registered", stub: runtimeStub{names: []string{"runc"}, support: support(nil, true)}},
		{name: "registered, unused", stub: runtimeStub{names: []string{"runc", "runsc"}, support: support([]string{"runsc"}, true)}},
		{
			name:     "selected but not reported",
			stub:     runtimeStub{names: []string{"runc"}, support: support(nil, true)},
			selected: "runsc",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := checkRuntimes(ctx, tc.stub, config.ProfileDev, tc.selected)
			if _, fatal := Verdict(c.Status, false); fatal {
				t.Errorf("dev failed over a runtime it should only report: %+v", c)
			}
			if warned := c.Status != StatusOK; warned != tc.warns {
				t.Errorf("dev warned = %v, want %v: %+v", warned, tc.warns, c)
			}
		})
	}
}

func TestRuntimeCheckCannotListTheRuntimes(t *testing.T) {
	c := checkRuntimes(context.Background(), runtimeStub{err: errors.New("nope")}, config.ProfileProd, "")
	if c.Status != StatusUnknown {
		t.Errorf("an unanswerable question was not reported as unknown: %+v", c)
	}
	if _, fatal := Verdict(c.Status, true); !fatal {
		t.Error("prod assumed the answer it would prefer")
	}
}
