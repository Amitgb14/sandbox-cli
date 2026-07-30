package fleet

import (
	"context"
	"sort"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/runtime"
	"github.com/Amitgb14/sandbox-cli/internal/sandbox"
	"github.com/Amitgb14/sandbox-cli/internal/worktree"
)

// dirtyLimit caps how many modified paths are collected per branch. The count is
// all the status table shows, and a branch with thousands of changed files
// should not cost thousands of strings to say "dirty".
const dirtyLimit = 50

// Status is one branch's line in the supervisor table: is the agent still
// working, how did it end, and did it leave anything behind.
type Status struct {
	Branch string

	// Agent is which agent worked this branch, read back from the container's
	// label rather than from the fleet file — the file can be edited between the
	// launch and the question, and a fleet may put a different agent on each
	// branch. Empty when there is no container left to ask.
	Agent string

	// Container is the most recent container for this branch, or nil if the
	// branch has a worktree but was never run (or its container was reaped).
	Container *runtime.ContainerInfo

	// Elapsed is how long the agent has been running, or how long it ran before
	// exiting. Zero when there is no container.
	Elapsed time.Duration

	// Dirty counts uncommitted (modified or untracked) files in the worktree —
	// work that exists nowhere else and would be lost by removing it.
	Dirty int

	// Ahead counts commits on this branch not yet in the base branch: what there
	// is to land.
	Ahead int

	// WorktreePath is the checkout the agent worked in, empty if it is gone.
	WorktreePath string
}

// Running reports whether an agent is still working this branch.
func (s Status) Running() bool { return s.Container != nil && s.Container.Running() }

// Status joins what docker knows (which agents are alive, how they ended) with
// what git knows (what they produced), for every branch this repository has a
// sandbox worktree or container for.
//
// base is the branch that work would land into; commits are counted against it.
// Any per-branch git lookup that fails contributes zero rather than an error:
// this is a status table, and one unreadable worktree must not blank the rest.
func (r *Runner) Status(ctx context.Context, base string) ([]Status, error) {
	infos, err := r.Inspector.Containers(ctx, map[string]string{
		sandbox.LabelCLI:   "1",
		sandbox.LabelFleet: "1",
		sandbox.LabelRepo:  r.RepoID,
	})
	if err != nil {
		return nil, err
	}

	// Containers arrive newest-first, so the first one seen for a branch is the
	// current one; older runs of the same branch are kept out of the table.
	latest := map[string]*runtime.ContainerInfo{}
	for i := range infos {
		b := infos[i].Labels[sandbox.LabelBranch]
		if b == "" {
			continue // a sandbox container from a non-git run; not a fleet branch
		}
		if _, seen := latest[b]; !seen {
			latest[b] = &infos[i]
		}
	}

	// Every branch with a worktree gets a line even if it never ran, and every
	// branch with a container gets one even if its worktree is gone — otherwise
	// the state you most need to see is the state that disappears.
	paths := map[string]string{}
	branches := map[string]bool{}
	if wts, err := worktree.List(r.Repo); err == nil {
		for _, wt := range wts {
			branches[wt.Branch] = true
			paths[wt.Branch] = wt.Path
		}
	}
	for b := range latest {
		branches[b] = true
	}

	now := time.Now()
	out := make([]Status, 0, len(branches))
	for b := range branches {
		s := Status{
			Branch:       b,
			Container:    latest[b],
			WorktreePath: paths[b],
			Dirty:        len(worktree.Dirty(r.Repo, b, dirtyLimit)),
			Ahead:        worktree.Ahead(r.Repo, b, base),
		}
		if s.Container != nil {
			s.Agent = s.Container.Labels[sandbox.LabelAgent]
		}
		s.Elapsed = elapsed(s.Container, now)
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Branch < out[j].Branch })
	return out, nil
}

// elapsed reports how long the container has run, or ran for. A container that
// was created but never started has no meaningful duration.
func elapsed(c *runtime.ContainerInfo, now time.Time) time.Duration {
	if c == nil || c.StartedAt.IsZero() {
		return 0
	}
	end := c.FinishedAt
	if c.Running() || end.IsZero() || end.Before(c.StartedAt) {
		end = now
	}
	return end.Sub(c.StartedAt)
}
