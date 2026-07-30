package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/fleet"
	"github.com/Amitgb14/sandbox-cli/internal/runtime"
	"github.com/Amitgb14/sandbox-cli/internal/sandbox"
)

func fleetRow(branch, agent, id, state string) fleet.Status {
	return fleet.Status{
		Branch: branch,
		Agent:  agent,
		Container: &runtime.ContainerInfo{
			ID:        id,
			State:     state,
			StartedAt: time.Now().Add(-time.Minute),
			Labels:    map[string]string{sandbox.LabelAgent: agent},
		},
		Ahead: 2,
	}
}

// The branch table and the session table have to agree on how a container is
// named, or "the id column matches list's" is a claim rather than a fact — and
// the agent column is what makes a mixed fleet legible.
func TestFleetStatusTableCarriesTheSessionIDAndTheAgent(t *testing.T) {
	rows := []fleet.Status{
		fleetRow("feature-a", "claude", "a1b2c3d4e5f6a7b8", "running"),
		fleetRow("feature-b", "codex", "ffff0000ffff0000", "exited"),
	}
	var out bytes.Buffer
	if err := renderFleetStatus(&out, rows); err != nil {
		t.Fatalf("renderFleetStatus: %v", err)
	}
	got := out.String()
	for _, want := range []string{"ID", "AGENT", "a1b2c3d4e5f6", "claude", "codex"} {
		if !strings.Contains(got, want) {
			t.Errorf("status table is missing %q:\n%s", want, got)
		}
	}
	// The id is the 12-character form `list` prints and `kill` accepts, not the
	// full one.
	if strings.Contains(got, "a1b2c3d4e5f6a7b8") {
		t.Errorf("the full container id leaked into the table:\n%s", got)
	}
}

// Same rule as the session listing: a branch name is text from the repository
// and a tab-separated table must not be forgeable by one.
func TestFleetStatusCleansLabelText(t *testing.T) {
	row := fleetRow("main\nsandbox-fake\tforged", "claude\x1b]0;pwned\x07", "aaaa1111bbbb", "running")
	var out bytes.Buffer
	if err := renderFleetStatus(&out, []fleet.Status{row}); err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(out.String(), "\x1b\x07") {
		t.Error("a label put an escape sequence on the user's terminal")
	}
	if lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n"); len(lines) != 2 {
		t.Errorf("a branch name forged a row:\n%s", out.String())
	}
}

// A branch whose container has been reaped still has a line — that is the state
// you most need to see — but there is no session id left to print for it.
func TestFleetStatusWithoutAContainer(t *testing.T) {
	var out bytes.Buffer
	if err := renderFleetStatus(&out, []fleet.Status{{Branch: "orphan"}}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "orphan") {
		t.Errorf("a branch with no container should still be listed:\n%s", out.String())
	}
}
