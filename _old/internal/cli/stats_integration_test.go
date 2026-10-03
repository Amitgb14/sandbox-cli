//go:build docker_integration

package cli

import (
	"os/exec"
	"testing"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/sandbox"
)

// TestCollectSandboxStats starts a labelled container and asserts the stats
// collector reports it with a memory reading.
//
// The label is what makes it visible, and the second container is the point of
// the test rather than padding: `stats` selects on sandbox.cli, not on a
// `sandbox-` name prefix, so a container merely *named* like one of ours is
// somebody else's and must not be sampled. The name prefix was the old filter,
// and it would have reported a stranger's `sandbox-postgres` as a sandbox.
func TestCollectSandboxStats(t *testing.T) {
	if exec.Command("docker", "info").Run() != nil {
		t.Skip("docker daemon not available")
	}

	stamp := time.Now().Format("150405.000")
	ours := "sandbox-statstest-" + stamp
	if err := exec.Command("docker", "run", "-d", "--rm",
		"--name", ours, "--label", sandbox.LabelCLI+"=1",
		"alpine", "sleep", "30").Run(); err != nil {
		t.Fatalf("starting test container: %v", err)
	}
	defer exec.Command("docker", "rm", "-f", ours).Run()

	// Named like ours, labelled as nobody's.
	theirs := "sandbox-notours-" + stamp
	if err := exec.Command("docker", "run", "-d", "--rm",
		"--name", theirs, "alpine", "sleep", "30").Run(); err != nil {
		t.Fatalf("starting unlabelled container: %v", err)
	}
	defer exec.Command("docker", "rm", "-f", theirs).Run()

	// Give docker a moment to register the containers.
	time.Sleep(1 * time.Second)

	rows, err := collectSandboxStats("docker")
	if err != nil {
		t.Fatalf("collectSandboxStats: %v", err)
	}
	var found *statRow
	for i := range rows {
		if rows[i].Name == theirs {
			t.Errorf("a container that is merely named like a sandbox was sampled: %+v", rows[i])
		}
		if rows[i].Name == ours {
			found = &rows[i]
		}
	}
	if found == nil {
		t.Fatalf("container %q not found in stats rows: %+v", ours, rows)
	}
	if found.Mem == "" || found.CPU == "" {
		t.Errorf("empty stats for %q: %+v", ours, *found)
	}
	// The id column is the join with `list`, `attach`, `logs` and `kill`; an empty
	// one makes the table unusable as anything but a picture.
	if found.ID == "" {
		t.Errorf("no session id reported for %q: %+v", ours, *found)
	}
}
