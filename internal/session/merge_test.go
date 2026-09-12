package session

import (
	"testing"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/protocol"
)

// Two writers must not lose each other's panes.
//
// `Save` used to write the whole catalog from a copy taken at `Open`, so two processes
// each doing Open → Spawn → Save clobbered one another: the second's view predates the
// first's write. The lock serialises the writes and does nothing about the lost update.
//
// It was narrow while every writer was short-lived, and self-healing for *running*
// panes — the pane id is a label, so the next `Adopt` recovers the row from the engine.
// What it lost permanently was a **stopped** pane, which is the one thing the catalog
// holds that the engine cannot give back. Studio becoming a writer makes the window
// hours wide.
func TestTwoWritersDoNotLoseAPane(t *testing.T) {
	dir, _ := repo(t)
	t.Setenv("XDG_CONFIG_HOME", shortTmp(t))

	a, err := Open(dir, "dev", "docker")
	if err != nil {
		t.Fatal(err)
	}
	b, err := Open(dir, "dev", "docker")
	if err != nil {
		t.Fatal(err)
	}
	if a.Dir() != b.Dir() {
		t.Fatalf("two opens of one repository got different directories")
	}

	// Each appends a pane to its own view, the way Spawn does — and the one that is
	// *stopped* is the case that used to be unrecoverable.
	stopped := protocol.Pane{
		ID: "p_a", Kind: protocol.PaneAgent, State: protocol.StateStopped,
		ContainerName: "sandbox-x-a", UpdatedAt: time.Now().UTC(),
	}
	live := protocol.Pane{
		ID: "p_b", Kind: protocol.PaneAgent, State: protocol.StateUnknown,
		ContainerName: "sandbox-x-b", UpdatedAt: time.Now().UTC(),
	}
	a.mu.Lock()
	a.sess.Panes = append(a.sess.Panes, stopped)
	a.mu.Unlock()
	b.mu.Lock()
	b.sess.Panes = append(b.sess.Panes, live)
	b.mu.Unlock()

	if err := a.Save("a"); err != nil {
		t.Fatal(err)
	}
	// b's in-memory copy predates a's write — the whole point.
	if err := b.Save("b"); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(dir, "dev", "docker")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, p := range reopened.Snapshot().Panes {
		got[p.ID] = true
	}
	if !got["p_a"] {
		t.Error("the first writer's stopped pane was clobbered; that row is the one thing the engine cannot give back")
	}
	if !got["p_b"] {
		t.Error("the second writer's pane is missing")
	}
}

// The merge rule, stated where it can be checked: panes are the union keyed by id,
// the newer UpdatedAt wins, and the grouping comes from the fresher derivation rather
// than from a merge of two.
func TestMergeKeepsTheNewerPaneAndTheFresherGrouping(t *testing.T) {
	older := time.Date(2026, 9, 12, 10, 0, 0, 0, time.UTC)
	newer := older.Add(time.Minute)

	onDisk := protocol.Session{
		Panes: []protocol.Pane{
			{ID: "p_1", State: protocol.StateUnknown, UpdatedAt: older},
			{ID: "p_only_on_disk", State: protocol.StateStopped, UpdatedAt: older},
		},
		Workspaces: []protocol.Workspace{{ID: "ws", Worktrees: []protocol.Worktree{
			{ID: "wt_gone", Branch: "gone"},
		}}},
	}
	ours := protocol.Session{
		ID: "s_1", Root: "/repo",
		Panes: []protocol.Pane{
			{ID: "p_1", State: protocol.StateDone, UpdatedAt: newer},
			{ID: "p_only_ours", State: protocol.StateUnknown, UpdatedAt: newer},
		},
		Workspaces: []protocol.Workspace{{ID: "ws", Worktrees: []protocol.Worktree{
			{ID: "wt_live", Branch: "live"},
		}}},
	}

	out := mergeCatalogs(onDisk, ours)

	byID := map[string]protocol.Pane{}
	for _, p := range out.Panes {
		byID[p.ID] = p
	}
	if len(byID) != 3 {
		t.Errorf("panes = %d, want the union of 3: %v", len(byID), byID)
	}
	if byID["p_1"].State != protocol.StateDone {
		t.Errorf("p_1 = %q, want the newer view", byID["p_1"].State)
	}
	if _, ok := byID["p_only_on_disk"]; !ok {
		t.Error("a pane only the other writer knew about was dropped")
	}

	// Identity is ours: two writers for one repository agree on it by construction.
	if out.ID != "s_1" || out.Root != "/repo" {
		t.Errorf("identity came from the disk copy: %+v", out)
	}
	// The worktree a kept pane might hang off is carried over, because a pane that
	// renders under nothing is the failure the ids exist to prevent.
	var live, gone bool
	for _, wt := range out.Workspaces[0].Worktrees {
		switch wt.ID {
		case "wt_live":
			live = true
		case "wt_gone":
			gone = true
		}
	}
	if !live {
		t.Error("the fresher grouping was lost")
	}
	if !gone {
		t.Error("a worktree only the disk copy knew about was dropped, so its panes have nothing to hang off")
	}
}
