package session

import (
	"context"
	"testing"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/protocol"
	"github.com/Amitgb14/sandbox-cli/internal/runtime"
	"github.com/Amitgb14/sandbox-cli/internal/sandbox"
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

	out := mergeCatalogs(onDisk, ours, true /* ours came from an Adopt */)

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

// The freshness signal, which the first merge lacked.
//
// "Take the grouping from ours" rests on ours being a fresh derivation — true for a
// process that runs `Adopt`, and false for `internal/studioapi`, which is a writer
// that deliberately runs no adoption loop. Its grouping is frozen at daemon start and
// is the stalest copy in the system, so asserting it would write a removed worktree's
// *pathed* record back over the pathless one `serve` had just derived — and that field
// is what `WorktreeDir` returns and the snapshot keeper writes into.
func TestANonAdoptingWriterDoesNotAssertTheGrouping(t *testing.T) {
	// What `serve` derived: the worktree is gone, so its record has no path. That is
	// the deliberate "never invent a path for a worktree git has not heard of".
	onDisk := protocol.Session{
		Workspaces: []protocol.Workspace{{ID: "ws", Worktrees: []protocol.Worktree{
			{ID: "wt_feat", Branch: "feat"},
		}}},
	}
	// What a long-lived writer still believes, from when it started.
	stale := protocol.Session{
		ID: "s_1",
		Workspaces: []protocol.Workspace{{ID: "ws", Worktrees: []protocol.Worktree{
			{ID: "wt_feat", Branch: "feat", Path: "/gone/feat"},
		}}},
	}

	out := mergeCatalogs(onDisk, stale, false /* never adopted */)
	for _, wt := range out.Workspaces[0].Worktrees {
		if wt.ID == "wt_feat" && wt.Path != "" {
			t.Errorf("a non-adopting writer asserted a path for a removed worktree: %q\n"+
				"  that is the field WorktreeDir returns and the keeper writes into", wt.Path)
		}
	}
	// Its own panes still survive, which is the whole reason it writes at all.
	stale.Panes = []protocol.Pane{{ID: "p_new", UpdatedAt: time.Now()}}
	out = mergeCatalogs(onDisk, stale, false)
	if len(out.Panes) != 1 || out.Panes[0].ID != "p_new" {
		t.Errorf("the writer's own pane was dropped: %+v", out.Panes)
	}

	// And a writer that *has* adopted still wins, because then it is the fresher one.
	fresh := stale
	out = mergeCatalogs(onDisk, fresh, true)
	var pathed bool
	for _, wt := range out.Workspaces[0].Worktrees {
		if wt.ID == "wt_feat" && wt.Path != "" {
			pathed = true
		}
	}
	if !pathed {
		t.Error("an adopting writer's grouping was discarded; it is the fresher derivation")
	}
}

// The regression the merge introduced, and the reason `Save` holds both locks for the
// whole read-modify-write.
//
// The first version snapshotted `s.sess` *before* taking the flock and wrote the merged
// result back afterwards, so anything that changed the catalog while it waited for the
// lock was silently reverted. `serve` does exactly that from several goroutines — the
// keeper's sweep runs `Adopt`, and every accepted connection can run `refresh` — so a
// handler's in-flight `Save` could undo a sweep's discovery that a pane had exited. The
// sweep's next `Snapshot` would then hand the keeper a pane list in which that pane was
// still running, so its rescue session never closed, never took the final snapshot, and
// kept committing into its worktree.
//
// Driven with a contended flock, because that is the window: a second process holding
// the lock is what gives `Adopt` time to run in between.
func TestSaveDoesNotRevertAConcurrentAdopt(t *testing.T) {
	dir, repoID := repo(t)
	t.Setenv("XDG_CONFIG_HOME", shortTmp(t))

	srv, err := Open(dir, "dev", "docker")
	if err != nil {
		t.Fatal(err)
	}

	// One running pane, catalogued.
	c := running("sandbox-x-feat", repoID, "feat", map[string]string{sandbox.LabelPane: "p_x"})
	eng := &fakeEngine{containers: []runtime.ContainerInfo{c}}
	if err := srv.Adopt(context.Background(), eng); err != nil {
		t.Fatal(err)
	}
	if err := srv.Save("first"); err != nil {
		t.Fatal(err)
	}

	// Hold the flock from "another process", so the Save below has to wait.
	release, err := flock(lockPath(srv.Dir()))
	if err != nil {
		t.Fatal(err)
	}

	saved := make(chan error, 1)
	go func() { saved <- srv.Save("contended") }()

	// While that Save is blocked on the lock, the container exits and an Adopt
	// notices — which is the sweep's job in the real daemon.
	time.Sleep(50 * time.Millisecond)
	exited := c
	exited.State = "exited"
	exited.ExitCode = 7
	if err := srv.Adopt(context.Background(), &fakeEngine{
		containers: []runtime.ContainerInfo{exited},
	}); err != nil {
		t.Fatal(err)
	}

	release()
	if err := <-saved; err != nil {
		t.Fatal(err)
	}

	// The pane must still be finished. If Save reverted the Adopt, the keeper would be
	// handed a pane that looks alive and would go on snapshotting a run that is over.
	panes := srv.Snapshot().Panes
	if len(panes) != 1 {
		t.Fatalf("panes = %d", len(panes))
	}
	if panes[0].Running() {
		t.Errorf("state = %q: an in-flight Save reverted the Adopt that found this pane finished,\n"+
			"  so the keeper would keep committing snapshots into a finished run's worktree", panes[0].State)
	}
	if panes[0].ExitCode == nil || *panes[0].ExitCode != 7 {
		t.Errorf("exit code = %v, want 7 — the Adopt's finding was lost", panes[0].ExitCode)
	}
}
