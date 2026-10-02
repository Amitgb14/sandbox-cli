package fleet

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/Amitgb14/sandbox-cli/internal/workspace"
)

// Land merges a fleet task's work — refs/sandbox/fleet/<branch> — into the
// branch checked out in the repository. It is the only fleet operation that
// writes to a branch, so every step that could destroy or entangle work refuses
// rather than guesses. The reasoning is beta.15's (_old/internal/fleet/land.go);
// what changed is what gets merged: a ref brought back and verified on the way
// in, never a worktree an agent could still be writing.

// Refusals about one branch, as opposed to about the base. `land --all` skips a
// branch refused for one of these and carries on; anything wrong with the base
// stops the run, because it will be just as wrong for the next branch.
var (
	ErrAgentRunning  = errors.New("agent still running")
	ErrNotVerified   = errors.New("not verified")
	ErrNothingToLand = errors.New("nothing to land")
	ErrUnknownBranch = errors.New("not in the last fleet run")
)

type branchRefusal struct {
	sentinel error
	msg      string
}

func (b branchRefusal) Error() string { return b.msg }
func (b branchRefusal) Unwrap() error { return b.sentinel }

func refuse(sentinel error, format string, a ...any) error {
	return branchRefusal{sentinel, fmt.Sprintf(format, a...)}
}

// LandOptions override refusals, deliberately and by name.
type LandOptions struct {
	// Unverified lands work whose task failed or whose verify rejected it.
	Unverified bool
	// Onto lands into a branch other than the one the fleet started from. It
	// does not choose where the merge goes — git merges into HEAD — it says the
	// difference is intended.
	Onto string
}

// Land merges one task's work into HEAD with --no-ff, so it stays a visible,
// revertible unit. A conflict is left in place for the person to resolve; land
// never resolves.
func Land(st *State, branch string, opts LandOptions) error {
	ts, ok := st.Tasks[branch]
	if !ok {
		return refuse(ErrUnknownBranch, "%q was not a task of the last fleet run", branch)
	}
	switch ts.State {
	case TaskRunning:
		return refuse(ErrAgentRunning, "%q is still running; its commits are not final", branch)
	case TaskVerified:
	default:
		if !opts.Unverified {
			return refuse(ErrNotVerified, "%q finished as %s (exit %d); --unverified lands it anyway", branch, ts.State, ts.ExitCode)
		}
	}
	if ts.Ref == "" {
		return refuse(ErrNothingToLand, "%q brought back no commits; nothing to land", branch)
	}

	repo := st.Repo
	head, err := workspace.Git(repo, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		return errors.New("the repository is not on a branch (detached HEAD); check out the branch to land into")
	}
	target := st.BaseBranch
	if opts.Onto != "" {
		target = opts.Onto
	}
	if head != target {
		return fmt.Errorf("the repository is on %q, not %q, which this fleet started from; check it out, or say --onto %s", head, target, head)
	}
	if dirty, _ := workspace.Git(repo, "status", "--porcelain"); strings.TrimSpace(dirty) != "" {
		return fmt.Errorf("%s has uncommitted changes; commit or stash them before landing", head)
	}
	if n, _ := workspace.Git(repo, "rev-list", "--count", "HEAD.."+ts.Ref); strings.TrimSpace(n) == "0" {
		return refuse(ErrNothingToLand, "%q has nothing that %s does not already have", branch, head)
	}
	msg := fmt.Sprintf("Land %s from the fleet (%s, %s)", branch, ts.Agent, ts.State)
	if _, err := workspace.Git(repo, "merge", "--no-ff", "--no-edit", "-m", msg, ts.Ref); err != nil {
		return fmt.Errorf("merging %s into %s stopped; resolve it there, or `git merge --abort`:\n%w", branch, head, err)
	}
	return nil
}

// Skipped is one branch `land --all` passed over, and why.
type Skipped struct {
	Branch string
	Reason string
}

// LandAll lands every branch it can, in name order. A branch refusal skips the
// branch; anything else stops the run there.
func LandAll(st *State, opts LandOptions) (landed []string, skipped []Skipped, err error) {
	branches := make([]string, 0, len(st.Tasks))
	for b := range st.Tasks {
		branches = append(branches, b)
	}
	sort.Strings(branches)
	for _, b := range branches {
		err := Land(st, b, opts)
		var br branchRefusal
		switch {
		case err == nil:
			landed = append(landed, b)
		case errors.As(err, &br):
			skipped = append(skipped, Skipped{b, err.Error()})
		default:
			return landed, skipped, err
		}
	}
	return landed, skipped, nil
}
