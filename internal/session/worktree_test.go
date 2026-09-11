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
func TestRefuseWorktreeInUse(t *testing.T) {
	const repoID, branch = "app-1234abcd", "feat"

	live := runtime.ContainerInfo{
		ID: "cid", Name: "sandbox-app-1234abcd-feat", State: "running",
		Labels: map[string]string{
			sandbox.LabelCLI: "1", sandbox.LabelRepo: repoID,
			sandbox.LabelBranch: branch, sandbox.LabelPane: "p_live",
		},
	}
	err := RefuseWorktreeInUse(context.Background(), &fakeEngine{
		containers: []runtime.ContainerInfo{live},
	}, repoID, branch)
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
	done := runtime.ContainerInfo{
		ID: "cid", Name: "sandbox-app-1234abcd-feat", State: "exited", ExitCode: 0,
		Labels: map[string]string{
			sandbox.LabelCLI: "1", sandbox.LabelRepo: repoID, sandbox.LabelBranch: branch,
		},
	}
	if err := RefuseWorktreeInUse(context.Background(), &fakeEngine{
		containers: []runtime.ContainerInfo{done},
	}, repoID, branch); err != nil {
		t.Errorf("a finished container blocked the removal: %v", err)
	}
}

// The scope of the question. A branch name is not unique across repositories, so
// the filter carries the repo id — otherwise another project's agent working on a
// branch of the same name would block this removal.
func TestRunningPanesOnIsScopedToTheRepository(t *testing.T) {
	eng := &fakeEngine{}
	if _, err := RunningPanesOn(context.Background(), eng, "app-1234abcd", "feat"); err != nil {
		t.Fatal(err)
	}
	if len(eng.asked) != 1 {
		t.Fatalf("asked the engine %d times", len(eng.asked))
	}
	q := eng.asked[0]
	for k, want := range map[string]string{
		sandbox.LabelCLI:    "1",
		sandbox.LabelRepo:   "app-1234abcd",
		sandbox.LabelBranch: "feat",
	} {
		if q[k] != want {
			t.Errorf("filter[%s] = %q, want %q — a branch name alone matches other repositories", k, q[k], want)
		}
	}
}

// Unanswerable is not refused. An engine that cannot be reached is no evidence
// that a pane is running, and turning it into one would block a git operation for
// a reason that has nothing to do with the worktree — the dirty check is what
// protects the files, and it still stands.
func TestRefuseWorktreeInUseAllowsWhenTheEngineCannotBeAsked(t *testing.T) {
	if err := RefuseWorktreeInUse(context.Background(),
		&fakeEngine{err: os.ErrPermission}, "app-1234abcd", "feat"); err != nil {
		t.Errorf("an unreachable engine blocked the removal: %v", err)
	}
	// And no engine at all, which is the `docker not on PATH` case.
	if err := RefuseWorktreeInUse(context.Background(), nil, "app-1234abcd", "feat"); err != nil {
		t.Errorf("no engine blocked the removal: %v", err)
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
	// And the id itself is still the sanitised form, because that is what makes it a
	// stable key.
	if found.ID != "wt_live_one" {
		t.Errorf("id = %q, want the sanitised form", found.ID)
	}
}
