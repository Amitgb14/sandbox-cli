package cli

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/runtime"
	"github.com/Amitgb14/sandbox-cli/internal/sandbox"
)

// reaperStub is a sessionRuntime that also reaps, which is the whole point of
// the test: before `runtime.NetworkReaper` existed, `clean` reached the reaper
// through an unchecked inline type assertion, so a fake took the "not supported"
// path silently and no test here could reach the wiring at all.
type reaperStub struct {
	sessionRuntime
	gotFilter string
	out       []runtime.NetworkReap
}

func (r *reaperStub) ReapPerRunNetworks(_ context.Context, ownerFilter string) []runtime.NetworkReap {
	r.gotFilter = ownerFilter
	return r.out
}

func TestCleanReportsWhatItKept(t *testing.T) {
	stub := &reaperStub{out: []runtime.NetworkReap{
		{Name: "sandbox-cli-gone", Removed: true},
		{Name: "sandbox-cli-held", Reason: "a sandbox is still on it: agent-7"},
	}}

	out := captureStdout(t, func() { reapPerRunNetworks(context.Background(), stub) })

	// A removed network and a kept one are different facts and both are printed:
	// the kept one has a host-wide cost (`podman network reload --all` fails for
	// every network) that nobody would attribute to sandbox-cli, and silence
	// about it is what let the leak survive a `clean` that reported success.
	if !strings.Contains(out, "removed network sandbox-cli-gone") {
		t.Errorf("output does not report the removal: %q", out)
	}
	if !strings.Contains(out, "kept network sandbox-cli-held (a sandbox is still on it: agent-7)") {
		t.Errorf("output does not say what was kept, or why: %q", out)
	}

	// The filter carries the *value*, not just the key: a bare `label=sandbox.cli`
	// matches any value, and this decides which containers `network rm -f` may
	// delete along with the network.
	if want := sandbox.LabelCLI + "=1"; stub.gotFilter != want {
		t.Errorf("ownership filter = %q, want %q", stub.gotFilter, want)
	}
}

// TestCleanSurvivesAnEngineThatCannotReap: docker shares one network and does not
// implement the capability. `clean` must still work rather than panic on a failed
// assertion.
func TestCleanSurvivesAnEngineThatCannotReap(t *testing.T) {
	out := captureStdout(t, func() { reapPerRunNetworks(context.Background(), nil) })
	if out != "" {
		t.Errorf("an engine with no per-run networks printed %q, want nothing", out)
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	orig := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = orig }()

	done := make(chan string, 1)
	go func() {
		var b strings.Builder
		io.Copy(&b, r)
		done <- b.String()
	}()
	fn()
	w.Close()
	return <-done
}
