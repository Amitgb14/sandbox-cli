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
	support runtime.RuntimeSupport
}

func (r runtimeStub) Available(context.Context) error                 { return nil }
func (r runtimeStub) SeccompUnavailable(context.Context) (bool, bool) { return false, true }
func (r runtimeStub) Runtimes(context.Context) ([]string, error)      { return r.support.All, nil }
func (r runtimeStub) FirewallProgrammable(context.Context, string) (runtime.FirewallProbe, string) {
	return runtime.FirewallUnknown, ""
}
func (r runtimeStub) ImagePresent(context.Context, string) (bool, bool) { return true, true }

func (r runtimeStub) StrongerRuntimeSupport(context.Context) runtime.RuntimeSupport {
	return r.support
}

// dockerHost is a complete registered set with a named default, as docker
// reports. podmanHost names only the runtime it is using.
func dockerHost(def string, all ...string) runtime.RuntimeSupport {
	var strong []string
	for _, n := range all {
		if runtime.StrongerRuntime(n) {
			strong = append(strong, n)
		}
	}
	return runtime.RuntimeSupport{All: all, Registered: strong, Default: def, Complete: true, Known: true}
}

func TestRuntimeCheckUnderProd(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name     string
		support  runtime.RuntimeSupport
		selected string
		fatal    bool
		wants    []string
	}{
		{
			name:     "selected and registered",
			support:  dockerHost("runc", "runc", "runsc"),
			selected: "runsc",
			wants:    []string{"runsc"},
		},
		{
			// The host that had already done the work. Refusing it was the
			// sharpest of the wrong refusals.
			name:    "the engine's default is strong",
			support: dockerHost("runsc", "runc", "runsc"),
			wants:   []string{"runsc", "default"},
		},
		{
			// The boundary was there and unused: the one provable gap.
			name:    "registered and unused",
			support: dockerHost("runc", "runc", "kata-runtime"),
			fatal:   true,
			wants:   []string{"kata-runtime"},
		},
		{
			// A launch that cannot work is not a matter of degree.
			name:     "selected but this engine has not registered it",
			support:  dockerHost("runc", "runc"),
			selected: "kata-runtime",
			fatal:    true,
			wants:    []string{"kata-runtime", "not registered"},
		},
		{
			// Deliberate and unrecognised: permitted, and not vouched for.
			name:     "an unrecognised name",
			support:  dockerHost("runc", "runc", "sysbox-runc"),
			selected: "sysbox-runc",
			wants:    []string{"sysbox-runc", "not a runtime this tool recognises"},
		},
		{
			// The names are the point: an operator told only "install gVisor"
			// might already have it under a name no list knows.
			name:    "nothing stronger reported",
			support: dockerHost("runc", "runc", "runsc-hostnet"),
			wants:   []string{"share the host kernel", "runsc-hostnet"},
		},
		{
			// Same verdict the run path reaches, which it did not before.
			name:    "the engine could not be asked",
			support: runtime.RuntimeSupport{},
			fatal:   true,
			wants:   []string{"could not be asked"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := checkRuntimes(ctx, runtimeStub{support: tc.support}, config.ProfileProd, tc.selected)
			if _, fatal := Verdict(c.Status, true); fatal != tc.fatal {
				t.Errorf("prod fatal = %v, want %v: %+v", fatal, tc.fatal, c)
			}
			for _, want := range tc.wants {
				if !strings.Contains(c.Detail+" "+c.Remedy, want) {
					t.Errorf("the finding does not mention %q: %+v", want, c)
				}
			}
			if strings.Contains(c.Detail, "\n") {
				t.Errorf("detail contains a newline: %q", c.Detail)
			}
		})
	}
}

// dev reports the same facts and fails on nothing it can merely warn about. The
// two exceptions are configurations that cannot work at all: a runtime the
// engine does not have, and an engine that could not be asked.
func TestRuntimeCheckUnderDev(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name     string
		support  runtime.RuntimeSupport
		selected string
		fatal    bool
	}{
		{name: "nothing stronger reported", support: dockerHost("runc", "runc")},
		{name: "registered and unused", support: dockerHost("runc", "runc", "runsc")},
		{name: "default is strong", support: dockerHost("runsc", "runc", "runsc")},
		{
			name:     "selected but absent",
			support:  dockerHost("runc", "runc"),
			selected: "runsc",
			fatal:    false, // reported, and dev only warns
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := checkRuntimes(ctx, runtimeStub{support: tc.support}, config.ProfileDev, tc.selected)
			if _, fatal := Verdict(c.Status, false); fatal != tc.fatal {
				t.Errorf("dev fatal = %v, want %v: %+v", fatal, tc.fatal, c)
			}
		})
	}
}

// The fallback for a backend without the interface upgrade reports unverified
// rather than assembling a passing verdict out of what it can see.
func TestRuntimeSupportFallbackIsNotPermissive(t *testing.T) {
	c := checkRuntimes(context.Background(), bareStub{}, config.ProfileProd, "")
	if _, fatal := Verdict(c.Status, true); !fatal {
		t.Errorf("a backend that cannot be asked produced a passing verdict: %+v", c)
	}
}

// bareStub answers the interface without the StrongerRuntimeSupport upgrade.
type bareStub struct{}

func (bareStub) Available(context.Context) error                 { return nil }
func (bareStub) SeccompUnavailable(context.Context) (bool, bool) { return false, true }
func (bareStub) Runtimes(context.Context) ([]string, error)      { return nil, errors.New("nope") }
func (bareStub) FirewallProgrammable(context.Context, string) (runtime.FirewallProbe, string) {
	return runtime.FirewallUnknown, ""
}
func (bareStub) ImagePresent(context.Context, string) (bool, bool) { return true, true }
