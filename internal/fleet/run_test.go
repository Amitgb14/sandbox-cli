package fleet

import (
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/agents"
	"github.com/Amitgb14/sandbox-cli/internal/config"
	"github.com/Amitgb14/sandbox-cli/internal/sandbox"
)

func testOptions(t *testing.T, cfg config.Config, task Task) sandbox.Options {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir()) // never touch the real persisted HOME
	agent, ok := agents.Lookup("claude")
	if !ok {
		t.Fatal("no claude descriptor")
	}
	r := &Runner{Session: sandbox.New(cfg), Repo: "/repo", RepoID: testRepoID}
	opts, err := r.options(Spec{Agent: "claude"}, agent, task, t.TempDir(), "main")
	if err != nil {
		t.Fatalf("options: %v", err)
	}
	return opts
}

// The credential leak this guards is the substantive one prod exists to prevent:
// the default auth path is an OAuth refresh token in the persisted HOME, readable
// by the agent, and prod's answer is not to mount it at all. BuildSpec mounts
// AuthPersistDir whenever it is non-empty and does not re-consult the config, so
// every path that builds Options has to apply the gate itself — and ValidateProfile
// cannot stand in for this one, because it validates the resolved Config while the
// leak would be in the Options.
func TestOptionsDoNotPersistAuthWhenTheProfileForbidsIt(t *testing.T) {
	cfg := config.Default()
	cfg.PersistAuth = new(bool) // *false — what profileBase sets under prod

	if got := testOptions(t, cfg, Task{Branch: "feature-a", Prompt: "x"}).AuthPersistDir; got != "" {
		t.Errorf("AuthPersistDir = %q with persist_auth off; the refresh token must not be mounted", got)
	}
}

// The positive twin: with persistence on, a detached agent must get its login or
// it starts, finds itself logged out, and dies with nobody watching.
func TestOptionsPersistAuthByDefault(t *testing.T) {
	if got := testOptions(t, config.Default(), Task{Branch: "feature-a", Prompt: "x"}).AuthPersistDir; got == "" {
		t.Error("AuthPersistDir is empty by default; a detached agent would start logged out")
	}
}

// A fleet container must be findable as a fleet container. Without this label
// `fleet stop --all` reaches an interactive detached session, `fleet clean` reaps
// it, and max_parallel counts it — one open `sandbox-cli claude --detach` would
// then block a max_parallel: 1 fleet forever on a slot that never frees.
func TestOptionsMarkTheContainerAsFleetOwned(t *testing.T) {
	if !testOptions(t, config.Default(), Task{Branch: "feature-a", Prompt: "x"}).Fleet {
		t.Error("fleet containers are not marked as fleet-owned")
	}
}
