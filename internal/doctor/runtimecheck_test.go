package doctor

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/config"
	"github.com/Amitgb14/sandbox-cli/internal/runtime"
)

// The isolation-runtime check, in both worlds.
//
// Which world a machine is in is the one input that differs per machine, so it
// is pinned here rather than left to whoever runs the suite: on Linux a stronger
// runtime can be installed and prod says so, on Docker Desktop it cannot be
// registered at all and prod accepts the VM the engine already puts every
// container in. A test that asserted only the local answer would pass on one
// platform and fail on the other while the code was right on both.

type runtimeStub struct {
	names []string
	err   error
}

func (r runtimeStub) Available(context.Context) error                 { return nil }
func (r runtimeStub) SeccompUnavailable(context.Context) (bool, bool) { return false, true }
func (r runtimeStub) Runtimes(context.Context) ([]string, error)      { return r.names, r.err }
func (r runtimeStub) FirewallProgrammable(context.Context, string) (runtime.FirewallProbe, string) {
	return runtime.FirewallUnknown, ""
}
func (r runtimeStub) ImagePresent(context.Context, string) (bool, bool) { return true, true }

func pinHost(t *testing.T, can bool) {
	t.Helper()
	old := hostCanRegisterStrongerRuntime
	hostCanRegisterStrongerRuntime = func() bool { return can }
	t.Cleanup(func() { hostCanRegisterStrongerRuntime = old })
}

func TestRuntimeCheckAsksForAKernelOnlyWhereOneCanBeInstalled(t *testing.T) {
	ctx := context.Background()
	onlyRunc := runtimeStub{names: []string{"runc"}}

	t.Run("linux, nothing registered: prod fails and says how", func(t *testing.T) {
		pinHost(t, true)
		c := checkRuntimes(ctx, onlyRunc, config.ProfileProd, "")
		if _, fatal := Verdict(c.Status, true); !fatal {
			t.Errorf("prod accepted a shared kernel on a host that could have one of its own: %+v", c)
		}
		if c.Remedy == "" {
			t.Error("prod says nothing about how to close the gap")
		}
		// A newline inside a tabwriter cell ends the column block, so a check
		// added after this one would silently misalign.
		if strings.Contains(c.Detail, "\n") {
			t.Errorf("detail contains a newline: %q", c.Detail)
		}
	})

	t.Run("desktop, nothing registrable: prod reports the VM it already has", func(t *testing.T) {
		pinHost(t, false)
		c := checkRuntimes(ctx, onlyRunc, config.ProfileProd, "")
		if _, fatal := Verdict(c.Status, true); fatal {
			t.Errorf("prod refused a platform that cannot register a runtime at all: %+v", c)
		}
		if !strings.Contains(c.Detail, "VM") {
			t.Errorf("the report does not say what boundary is there instead: %+v", c)
		}
	})

	t.Run("dev never asks, on either", func(t *testing.T) {
		for _, can := range []bool{true, false} {
			pinHost(t, can)
			c := checkRuntimes(ctx, onlyRunc, config.ProfileDev, "")
			if _, fatal := Verdict(c.Status, false); fatal {
				t.Errorf("dev failed over a runtime it only ever reported (canRegister=%v): %+v", can, c)
			}
		}
	})
}

func TestRuntimeCheckReadsTheDaemonBeforeThePlatform(t *testing.T) {
	ctx := context.Background()
	// Registered but unused: the boundary prod promises was available, and
	// nothing asked for it. Fails on *either* platform, because this is the
	// daemon's own answer rather than an assumption about the host.
	for _, can := range []bool{true, false} {
		pinHost(t, can)
		c := checkRuntimes(ctx, runtimeStub{names: []string{"runc", "kata-runtime"}}, config.ProfileProd, "")
		if _, fatal := Verdict(c.Status, true); !fatal {
			t.Errorf("prod accepted a shared kernel where kata was registered (canRegister=%v): %+v", can, c)
		}
		if !strings.Contains(c.Remedy, "kata-runtime") {
			t.Errorf("the remedy does not name what this host actually has: %+v", c)
		}
	}
}

func TestRuntimeCheckOnASelectedRuntime(t *testing.T) {
	ctx := context.Background()
	pinHost(t, true)

	// Selected and present: the check says which boundary the runs get.
	c := checkRuntimes(ctx, runtimeStub{names: []string{"runc", "runsc"}}, config.ProfileProd, "runsc")
	if _, fatal := Verdict(c.Status, true); fatal {
		t.Errorf("prod refused a host that selected runsc: %+v", c)
	}
	if !strings.Contains(c.Detail, "runsc") {
		t.Errorf("the check does not say what the runs are getting: %+v", c)
	}

	// Selected and absent: the launch would fail anyway, and this is the earlier,
	// cheaper place to find out.
	c = checkRuntimes(ctx, runtimeStub{names: []string{"runc"}}, config.ProfileProd, "kata-runtime")
	if _, fatal := Verdict(c.Status, true); !fatal {
		t.Errorf("a runtime this daemon has not registered was accepted: %+v", c)
	}
	if !strings.Contains(c.Detail, "kata-runtime") {
		t.Errorf("the finding does not name the runtime that is missing: %+v", c)
	}
}

// A question that could not be asked is a failure under prod and silence under
// dev — unchanged by this work, and worth keeping pinned beside the rest.
func TestRuntimeCheckCannotAskTheDaemon(t *testing.T) {
	pinHost(t, true)
	c := checkRuntimes(context.Background(), runtimeStub{err: errors.New("nope")}, config.ProfileProd, "")
	if c.Status != StatusUnknown {
		t.Errorf("an unanswerable question was not reported as unknown: %+v", c)
	}
	if _, fatal := Verdict(c.Status, true); !fatal {
		t.Error("prod assumed the answer it would prefer")
	}
}
