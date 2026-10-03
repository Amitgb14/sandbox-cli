package session

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/protocol"
	"github.com/Amitgb14/sandbox-cli/internal/runtime"
	"github.com/Amitgb14/sandbox-cli/internal/sandbox"
)

// A worktree with a live pane in it is not removed.
//
// The hazard is specific: the worktree directory is the container's bind-mount
// source, so removing it leaves an agent writing into a path that no longer has a
// name. `fleet clean` had always skipped a branch whose container was running;
// `worktree rm` and Studio's delete handler did not — which is why this is one
// function rather than a third copy of the rule.
// mounting builds a container that has dir bound at /workspace, which is what the
// guard actually looks for.
func mounting(name, repoID, branch, dir, state string, labels map[string]string) runtime.ContainerInfo {
	l := map[string]string{
		sandbox.LabelCLI: "1", sandbox.LabelRepo: repoID, sandbox.LabelBranch: branch,
	}
	for k, v := range labels {
		l[k] = v
	}
	return runtime.ContainerInfo{
		ID: "cid-" + name, Name: name, State: state, Labels: l,
		Mounts: []runtime.MountInfo{{Source: dir, Destination: "/workspace", ReadWrite: true}},
	}
}

func TestRefuseWorktreeInUse(t *testing.T) {
	const repoID, branch = "app-1234abcd", "feat"
	dir := shortTmp(t)

	live := mounting("sandbox-app-1234abcd-feat", repoID, branch, dir, "running",
		map[string]string{sandbox.LabelPane: "p_live"})
	err := RefuseWorktreeInUse(context.Background(), &fakeEngine{
		containers: []runtime.ContainerInfo{live},
	}, repoID, branch, dir)
	if err == nil {
		t.Fatal("a worktree with a running sandbox in it was cleared for removal")
	}
	for _, want := range []string{"p_live", "sandbox-app-1234abcd-feat", "sandbox-cli kill"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not name %q, so there is nothing to act on:\n%s", want, err)
		}
	}
	// Said explicitly, because --force overriding the *dirty* refusal is exactly
	// what invites trying it here.
	if !strings.Contains(err.Error(), "--force does not cover this") {
		t.Errorf("the refusal does not say --force will not help:\n%s", err)
	}
}

// An exited container on the branch is not a reason to refuse: nothing is writing
// to the directory, and tidiness is `clean`'s job.
func TestRefuseWorktreeInUseIgnoresAFinishedPane(t *testing.T) {
	const repoID, branch = "app-1234abcd", "feat"
	dir := shortTmp(t)
	for _, state := range []string{"exited", "dead", "created"} {
		done := mounting("sandbox-app-1234abcd-feat", repoID, branch, dir, state, nil)
		if err := RefuseWorktreeInUse(context.Background(), &fakeEngine{
			containers: []runtime.ContainerInfo{done},
		}, repoID, branch, dir); err != nil {
			t.Errorf("state %q blocked the removal: %v", state, err)
		}
	}
}

// The states that are *not* finished, and the reason this is not `!Running()`.
//
// A paused or restarting container is somebody's live run in an odd moment, and an
// unreadable state is not a licence — the rule `clean` already states. The first
// version of this guard asked `Running()`, so `docker pause` on an agent was enough
// to let its bind-mount source be deleted: the accident the guard exists to prevent,
// arriving through the guard itself.
func TestRefuseWorktreeInUseTreatsAnOddMomentAsLive(t *testing.T) {
	const repoID, branch = "app-1234abcd", "feat"
	dir := shortTmp(t)
	for _, state := range []string{"running", "paused", "restarting", "removing", ""} {
		c := mounting("sandbox-app-1234abcd-feat", repoID, branch, dir, state,
			map[string]string{sandbox.LabelPane: "p_x"})
		if err := RefuseWorktreeInUse(context.Background(), &fakeEngine{
			containers: []runtime.ContainerInfo{c},
		}, repoID, branch, dir); err == nil {
			t.Errorf("state %q was treated as finished; not knowing is not a licence", state)
		}
	}
}

// The branch label records what the launcher asked for; an agent that runs
// `git checkout -b other` inside its worktree puts it out of sync with git — the
// desync CLAUDE.md documents for `land`. The guard therefore matches the **mount
// source**, which is what the container actually has open.
func TestRefuseWorktreeInUseMatchesTheMountNotTheLabel(t *testing.T) {
	const repoID = "app-1234abcd"
	dir := shortTmp(t)

	// Label says feat; the container is working in the directory now checked out as
	// `other`. Removing `other` would take this container's workspace away.
	stale := mounting("sandbox-app-1234abcd-feat", repoID, "feat", dir, "running",
		map[string]string{sandbox.LabelPane: "p_stale"})
	if err := RefuseWorktreeInUse(context.Background(), &fakeEngine{
		containers: []runtime.ContainerInfo{stale},
	}, repoID, "other", dir); err == nil {
		t.Error("a container whose branch label had gone stale was not found; the label is not the answer")
	}

	// And the other direction: a container with the right label that is *not*
	// mounting this directory does not block it.
	elsewhere := mounting("sandbox-app-1234abcd-other", repoID, "other", shortTmp(t), "running",
		map[string]string{sandbox.LabelPane: "p_else"})
	if err := RefuseWorktreeInUse(context.Background(), &fakeEngine{
		containers: []runtime.ContainerInfo{elsewhere},
	}, repoID, "other", dir); err != nil {
		t.Errorf("a container mounting a different directory blocked this removal: %v", err)
	}
}

// The scope of the question. A branch name is not unique across repositories, so
// the filter carries the repo id — otherwise another project's agent working on a
// branch of the same name would block this removal.
func TestPanesUsingIsScopedToTheRepository(t *testing.T) {
	eng := &fakeEngine{}
	if _, err := PanesUsing(context.Background(), eng, "app-1234abcd", shortTmp(t)); err != nil {
		t.Fatal(err)
	}
	if len(eng.asked) != 1 {
		t.Fatalf("asked the engine %d times", len(eng.asked))
	}
	q := eng.asked[0]
	for k, want := range map[string]string{
		sandbox.LabelCLI:  "1",
		sandbox.LabelRepo: "app-1234abcd",
	} {
		if q[k] != want {
			t.Errorf("filter[%s] = %q, want %q — a listing must be scoped to this repository", k, q[k], want)
		}
	}
	// Deliberately *not* filtered by branch: the label is what went stale, and the
	// match is on the mount source instead.
	if _, ok := q[sandbox.LabelBranch]; ok {
		t.Error("filtered by branch; the guard matches the mount source, because the label can be stale")
	}
}

// Unanswerable is not refused. An engine that cannot be reached is no evidence
// that a pane is running, and turning it into one would block a git operation for
// a reason that has nothing to do with the worktree — the dirty check is what
// protects the files, and it still stands.
func TestRefuseWorktreeInUseAllowsWhenTheEngineCannotBeAsked(t *testing.T) {
	dir := shortTmp(t)
	if err := RefuseWorktreeInUse(context.Background(),
		&fakeEngine{err: os.ErrPermission}, "app-1234abcd", "feat", dir); err != nil {
		t.Errorf("an unreachable engine blocked the removal: %v", err)
	}
	// And no engine at all, which is the `docker not on PATH` case.
	if err := RefuseWorktreeInUse(context.Background(), nil, "app-1234abcd", "feat", dir); err != nil {
		t.Errorf("no engine blocked the removal: %v", err)
	}
	// And no directory, which is the caller saying it found no worktree. The refusal
	// must not fire where there is nothing to protect: it used to claim a worktree was
	// in use about a `--detach` run in the main checkout, and advise killing a working
	// agent to permit an operation that was always a no-op.
	if err := RefuseWorktreeInUse(context.Background(),
		&fakeEngine{}, "app-1234abcd", "feat", ""); err != nil {
		t.Errorf("no worktree blocked the removal: %v", err)
	}
}

// The phase's shape: two worktrees, two panes, one snapshot — grouped the way
// testdata/session.example.json is, with each pane under the worktree it is
// working in.
func TestSnapshotGroupsTwoWorktreesWithTheirPanes(t *testing.T) {
	dir, repoID := repo(t)
	s := open(t, dir)

	one := running("sandbox-x-one", repoID, "one", map[string]string{
		sandbox.LabelPane: "p_one", sandbox.LabelAgent: "claude",
	})
	two := running("sandbox-x-two", repoID, "two", map[string]string{
		sandbox.LabelPane: "p_two", sandbox.LabelAgent: "codex",
	})
	if err := s.Adopt(context.Background(), &fakeEngine{
		containers: []runtime.ContainerInfo{one, two},
	}); err != nil {
		t.Fatal(err)
	}

	snap := s.Snapshot()
	if len(snap.Workspaces) != 1 {
		t.Fatalf("workspaces = %d, want one per repository", len(snap.Workspaces))
	}
	ws := snap.Workspaces[0]

	// Every pane hangs off a worktree the catalog actually holds. A dangling
	// worktree_id is a pane that renders under nothing, which is the failure a flat
	// list plus ids can have and a nested structure cannot.
	ids := map[string]bool{}
	for _, wt := range ws.Worktrees {
		ids[wt.ID] = true
	}
	byWorktree := map[string][]string{}
	for _, p := range snap.Panes {
		if !ids[p.WorktreeID] {
			t.Errorf("pane %s names worktree %q, which is not in the catalog", p.ID, p.WorktreeID)
		}
		byWorktree[p.WorktreeID] = append(byWorktree[p.WorktreeID], p.ID)
	}
	if len(byWorktree) != 2 {
		t.Errorf("two panes on two branches landed in %d worktrees: %v", len(byWorktree), byWorktree)
	}
	for wt, panes := range byWorktree {
		if len(panes) != 1 {
			t.Errorf("worktree %s holds %v, want one pane", wt, panes)
		}
	}

	// And the user's own checkout is there, exactly once, flagged as the one
	// `worktree rm` must never touch.
	var mains int
	for _, wt := range ws.Worktrees {
		if wt.Main {
			mains++
			if wt.Path != dir {
				t.Errorf("the worktree flagged Main is %q, not the repository %q", wt.Path, dir)
			}
		}
	}
	if mains != 1 {
		t.Errorf("worktrees flagged Main = %d, want exactly one", mains)
	}

	// The shape the phase names: the same fields the worked example carries.
	if ws.RepoHash != repoID || ws.RepoPath != dir || ws.ID == "" || ws.Label == "" {
		t.Errorf("workspace is missing identity the example has: %+v", ws)
	}
	for _, p := range snap.Panes {
		if p.Kind != protocol.PaneAgent || p.Agent == "" || p.ContainerName == "" {
			t.Errorf("pane is missing what the example carries: %+v", p)
		}
	}
}

// A worktree's branch is the name git has, not a reconstruction of its id.
//
// `worktreeIDFor` runs the branch through `sanitizeID`, which maps several
// characters onto "_", so the id cannot be turned back: `live-one` and `live_one`
// produce the same one. Reading the branch off the container's own label is exact,
// and the first version of the grouping reported `live_one` for a branch actually
// called `live-one`.
func TestWorktreeBranchComesFromTheLabelNotTheID(t *testing.T) {
	dir, repoID := repo(t)
	s := open(t, dir)

	// A branch with a hyphen, on a worktree git does not know about — so the only
	// source for its name is the container.
	c := running("sandbox-x-hy", repoID, "live-one", map[string]string{sandbox.LabelPane: "p_hy"})
	if err := s.Adopt(context.Background(), &fakeEngine{
		containers: []runtime.ContainerInfo{c},
	}); err != nil {
		t.Fatal(err)
	}

	snap := s.Snapshot()
	var found *protocol.Worktree
	for i, wt := range snap.Workspaces[0].Worktrees {
		if wt.ID == worktreeIDFor("live-one") {
			found = &snap.Workspaces[0].Worktrees[i]
		}
	}
	if found == nil {
		t.Fatalf("no worktree record for the branch: %+v", snap.Workspaces[0].Worktrees)
	}
	if found.Branch != "live-one" {
		t.Errorf("branch = %q, want %q — the id is sanitised and cannot be reversed", found.Branch, "live-one")
	}
	// The id is derived and stable — asked twice, the same answer — which is what
	// makes it usable as a key. It is *not* the bare sanitised form: a branch whose
	// sanitised form lost information carries a hash of the branch, so two branches
	// that sanitise alike cannot share one record.
	if found.ID != worktreeIDFor("live-one") {
		t.Errorf("id = %q, want the derived form %q", found.ID, worktreeIDFor("live-one"))
	}
	if worktreeIDFor("live-one") == worktreeIDFor("live_one") {
		t.Error("live-one and live_one share an id; the hash suffix is not doing its job")
	}
}

// Branches that sanitise to the same string get **different** worktree ids, so each
// keeps its own record and its own name.
//
// `sanitizeID` maps non-alphanumerics onto "_" and lowercases, so `live-one` and
// `live_one` produced one id — two real branches sharing one worktree record, whose
// reported name was whichever container the engine happened to list last: a wrong
// name that looked authoritative, and one that changed between two refreshes of
// unchanged state. Fixing the id scheme removes the question rather than answering
// it, which is why there is no collision handling to test.
func TestBranchesThatSanitiseAlikeGetDistinctWorktrees(t *testing.T) {
	for _, pair := range [][2]string{
		{"live-one", "live_one"},
		{"Feat", "feat"},
		{"a/b", "a-b"},
	} {
		if worktreeIDFor(pair[0]) == worktreeIDFor(pair[1]) {
			t.Errorf("%q and %q share worktree id %q", pair[0], pair[1], worktreeIDFor(pair[0]))
		}
	}
	// Derived, so the same branch always lands in the same record — which is what
	// lets adoption join a container to a worktree without storing the id anywhere.
	for _, b := range []string{"feat", "live-one", "Feat", "release/2.0"} {
		if worktreeIDFor(b) != worktreeIDFor(b) {
			t.Errorf("worktreeIDFor(%q) is not stable", b)
		}
	}
	// The common case stays readable: a branch that is already safe gets no suffix.
	if got := worktreeIDFor("feat"); got != "wt_feat" {
		t.Errorf("worktreeIDFor(\"feat\") = %q, want wt_feat — a plain branch should read plainly", got)
	}

	dir, repoID := repo(t)
	a := running("sandbox-x-a", repoID, "live-one", map[string]string{sandbox.LabelPane: "p_a"})
	b := running("sandbox-x-b", repoID, "live_one", map[string]string{sandbox.LabelPane: "p_b"})

	// Both orders, because the bug was order-dependent: whichever came last won.
	for _, order := range [][]runtime.ContainerInfo{{a, b}, {b, a}} {
		srv := open(t, dir)
		if err := srv.Adopt(context.Background(), &fakeEngine{containers: order}); err != nil {
			t.Fatal(err)
		}
		got := map[string]string{}
		for _, wt := range srv.Snapshot().Workspaces[0].Worktrees {
			got[wt.ID] = wt.Branch
		}
		for _, want := range []string{"live-one", "live_one"} {
			if got[worktreeIDFor(want)] != want {
				t.Errorf("worktree for %q reported branch %q", want, got[worktreeIDFor(want)])
			}
		}
	}
}
