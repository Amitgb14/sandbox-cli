package cli

import (
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/fleet"
	"github.com/Amitgb14/sandbox-cli/internal/protocol"
	"github.com/Amitgb14/sandbox-cli/internal/runtime"
)

// `fleet status`'s STATE column says what the *agent* is doing when the session server
// has been watching, and what the container is doing when it has not.
//
// The detail comes from the catalog rather than from a correlation done here, which is
// why it can be in this table at all: phase 4 left agent state out of `fleet status`
// precisely because every branch of a fleet shares one repository, so a second
// correlation is a chance to show one branch's state on another's row. The daemon has
// already done it once, for the pane.
func TestFleetStateUsesThePaneStateWhenThereIsOne(t *testing.T) {
	cases := []struct {
		name      string
		paneState string
		container runtime.ContainerInfo
		want      string
	}{
		{"a daemon has been watching", string(protocol.StateBlocked),
			runtime.ContainerInfo{State: "running"}, "blocked"},
		{"working, which running cannot say", string(protocol.StateWorking),
			runtime.ContainerInfo{State: "running"}, "working"},
		{"no daemon, so the container's own word", "",
			runtime.ContainerInfo{State: "running"}, "running"},
		// `unknown` is not detail — it is the catalog saying it could not tell, and
		// showing it would be less informative than "running".
		{"the catalog could not tell", string(protocol.StateUnknown),
			runtime.ContainerInfo{State: "running"}, "running"},
		// A finished container's exit code outranks anything a conversation implies,
		// and the pane state is not consulted at all.
		{"exited, whatever the catalog last thought", string(protocol.StateWorking),
			runtime.ContainerInfo{State: "exited", ExitCode: 2}, "exited 2"},
	}
	for _, tc := range cases {
		c := tc.container
		got := fleetState(fleet.Status{Container: &c, PaneState: tc.paneState})
		if got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}

	// A branch with no container left to ask reads as unknown, which is what it is.
	if got := fleetState(fleet.Status{PaneState: string(protocol.StateBlocked)}); got != "—" {
		t.Errorf("no container: %q, want the dash — a pane state cannot outlive the thing it described", got)
	}
}
