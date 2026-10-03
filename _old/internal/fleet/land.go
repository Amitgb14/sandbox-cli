package fleet

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/Amitgb14/sandbox-cli/internal/sandbox"
	"github.com/Amitgb14/sandbox-cli/internal/worktree"
)

// The refusals Land can make that are about *one branch* rather than about the
// repository it would land into. Sentinels because `land --all` has to tell the
// two apart, and the rule it applies is exactly this distinction: a branch that
// cannot be landed is skipped and the fleet carries on, while anything wrong
// with the base — a checkout that moved, uncommitted work in it, a conflict —
// stops the whole run, because it will be just as wrong for the next branch and
// landing more on top of it makes it harder to undo.
//
// Their text is never printed; each one is attached to a message written for the
// person reading it (see branchRefusal). Classifying an error and explaining it
// are different jobs, and making one string do both is what produces the
// "not verified: feature-a did not pass its verify" shape.
var (
	ErrAgentRunning  = errors.New("agent still running")
	ErrNotVerified   = errors.New("not verified")
	ErrNoWorktree    = errors.New("no worktree")
	ErrNothingToLand = errors.New("nothing to land")
)

// branchRefusal pairs a message with the sentinel that classifies it.
func branchRefusal(kind error, format string, args ...any) error {
	return refusal{kind: kind, msg: fmt.Sprintf(format, args...)}
}

type refusal struct {
	kind error
	msg  string
}

func (r refusal) Error() string { return r.msg }
func (r refusal) Unwrap() error { return r.kind }

// LandOptions are the caller's choices for one landing. A struct rather than
// three more positional arguments, because each one exists to override a refusal
// and they are easy to transpose at a call site.
type LandOptions struct {
	// Message is the commit message used if the agent left work uncommitted.
	Message string
	// Onto names the target branch deliberately, overriding the branch recorded at
	// launch. It must still be the branch checked out.
	Onto string
	// Force lands work whose run did not pass its own verify. Deliberately not a
	// blanket override: it defeats this refusal and no other.
	Force bool
}

// LandResult reports what landing a branch did.
type LandResult struct {
	Branch    string
	Base      string // the branch it was merged into
	Committed bool   // land made a commit from an otherwise-uncommitted worktree
	Merged    bool

	// Verified is true when the run declared a verify command and passed it.
	// False covers three different things — no verify declared, no container left
	// to ask, or a pass forced past a failure — so the caller must say "unverified"
	// rather than "failed". A task that only *ran* is never reported as passed.
	Verified bool
	// Forced records that Force overrode a run that did not pass, so the CLI can
	// say so rather than printing an ordinary success.
	Forced bool
	// ForcedUnchecked narrows Forced: the run never reached its verify at all
	// (killed, out of memory) rather than running it and failing. Reported apart
	// because "the tests said no" and "nothing ever asked the tests" are different
	// things to have decided to land.
	ForcedUnchecked bool
}

// defaultLandMessage is used when land has to commit a worktree the agent left
// dirty and the caller supplied no message of their own.
func defaultLandMessage(branch string) string {
	return fmt.Sprintf("sandbox-cli: land work from %s", branch)
}

// Land merges a fleet branch into the branch currently checked out in the main
// repository. It is the only fleet operation that writes to the base branch, so
// every step that could destroy or entangle work refuses rather than guesses.
//
// In order: refuse while the agent is still running (its commits are not final);
// refuse work that failed its own verify; refuse if the checkout has moved off the
// branch this work was launched for; refuse while another agent is working in the
// merge target itself; require the base checkout to be clean (a merge must not
// braid itself into in-progress work); commit whatever the agent left uncommitted;
// and only then merge --no-ff. A merge conflict stops with git's own message and
// leaves the merge in progress for the user to finish by hand — land never
// resolves.
//
// opts.Onto is the user naming the target branch explicitly. It does not change
// *where* the merge goes — git merges into HEAD and nothing else — it says that
// landing somewhere other than the recorded base is deliberate, and is still
// checked against what is actually checked out.
func (r *Runner) Land(ctx context.Context, branch string, opts LandOptions) (LandResult, error) {
	res := LandResult{Branch: branch}

	// 1. A running agent's work is not final: its next action could change or
	//    revert what we are about to merge.
	if busy, err := r.runningFor(ctx, branch); err != nil {
		return res, err
	} else if busy != nil {
		return res, branchRefusal(ErrAgentRunning,
			"agent is still running on %q (%s); stop it first with `sandbox-cli fleet stop %s`",
			branch, busy.Name, branch)
	}

	// 2. The work must have passed its own definition of done. This is the check
	//    the whole verify field exists for: everything else here protects the
	//    repository from a badly-timed merge, and this one protects it from the
	//    merge being wrong.
	verdict, err := r.checkVerified(ctx, branch, opts.Force)
	if err != nil {
		return res, err
	}
	res.Verified = verdict == verifyPassed
	res.Forced = verdict == verifyForced || verdict == verifyForcedUnchecked
	res.ForcedUnchecked = verdict == verifyForcedUnchecked

	// 3. There must be a worktree to land from.
	if _, exists, err := worktree.Path(r.Repo, branch); err != nil {
		return res, err
	} else if !exists {
		return res, branchRefusal(ErrNoWorktree, "no worktree for branch %q; nothing to land", branch)
	}

	// 4. The merge target is whatever the main checkout is on — git offers no
	//    other option — and step 5 checks it against what this work was launched
	//    for. A detached HEAD has no branch to land into, and landing a branch into
	//    itself is meaningless.
	base := worktree.HeadBranch(r.Repo)
	if base == "" {
		return res, fmt.Errorf("the main checkout is not on a branch (detached HEAD); check out the branch you want to land into")
	}
	if base == branch {
		return res, fmt.Errorf("%q is checked out in the main repository; check out the branch you want to land into first", branch)
	}
	res.Base = base

	// 5. The checkout must still be the branch this work was launched for.
	//
	//    Two things claim to answer "what is the base branch": the sandbox.base
	//    label, stamped when the agent started, and whatever the main checkout is
	//    on right now. They are not competing answers to one question — the label
	//    is the *intent* and HEAD is the *target*, and git can only ever merge into
	//    HEAD. So the fix for their disagreement is not to prefer one, it is to
	//    refuse: a checkout that moved between launch and landing means the merge
	//    would go somewhere nobody chose, and an unwanted merge commit on the wrong
	//    branch is recoverable only by rewriting it.
	if err := r.checkBase(ctx, branch, base, opts.Onto); err != nil {
		return res, err
	}

	// 6. Nothing may be working in the merge target itself. Step 1 refused a live
	//    agent on the branch being landed; this is the other side of the same
	//    problem — a merge rewrites files in the main checkout, and an agent
	//    working there would have the ground move under it mid-edit, with its
	//    uncommitted work swept into the merge or lost to a conflict it cannot
	//    see.
	//
	//    Identifying it takes no new label: git already refuses to check out one
	//    branch in two worktrees, so a running container labelled with the base
	//    branch is, necessarily, working the main checkout.
	//
	//    The label records the branch as it was at *launch*, though, so the check
	//    misses an agent started in this checkout before it moved — or while it was
	//    on a detached HEAD, which labels no branch at all. Closing that exactly
	//    would mean labelling the workspace path and comparing resolved paths;
	//    worth doing if it ever bites, but the common case is an agent launched in
	//    the checkout it is still working, and that is caught.
	if busy, err := r.runningFor(ctx, base); err != nil {
		return res, err
	} else if busy != nil {
		return res, fmt.Errorf("an agent is working in the %s checkout itself (container %s); "+
			"merging would rewrite files under it. Stop it with `sandbox-cli fleet stop %s`, or land after it finishes",
			base, busy.Name, base)
	}

	// 7. Refuse to merge into a dirty checkout: the merge commit would sweep up
	//    the user's unrelated in-progress work.
	clean, err := worktree.IsClean(r.Repo)
	if err != nil {
		return res, err
	}
	if !clean {
		return res, fmt.Errorf("the %s checkout has uncommitted changes; commit or stash them before landing", base)
	}

	// 8. Commit anything the agent left uncommitted, so it is part of the merge
	//    rather than stranded in the worktree.
	message := opts.Message
	if message == "" {
		message = defaultLandMessage(branch)
	}
	committed, err := worktree.CommitAll(r.Repo, branch, message)
	if err != nil {
		return res, err
	}
	res.Committed = committed

	// 9. Nothing to land is a no-op worth stating, not a merge of zero commits.
	if worktree.Ahead(r.Repo, branch, base) == 0 {
		return res, branchRefusal(ErrNothingToLand, "%q has no commits beyond %s; nothing to land", branch, base)
	}

	// 10. The merge itself. --no-ff keeps the branch a revertible unit.
	if err := worktree.Merge(r.Repo, branch); err != nil {
		return res, fmt.Errorf("merging %s into %s failed; the conflict is left in place to resolve in %s:\n%w",
			branch, base, r.Repo, err)
	}
	res.Merged = true
	return res, nil
}

// Skipped is one branch `land --all` passed over, and why. Reported rather than
// dropped: a fleet of five that lands three is a useful outcome, and an
// unexplained two is not.
type Skipped struct {
	Branch string
	Reason string
}

// LandAllResult is what one `fleet land --all` did.
type LandAllResult struct {
	Landed  []LandResult
	Skipped []Skipped
}

// LandAll lands every branch of this fleet that can be landed, oldest first,
// stopping at the first refusal that is about the repository rather than about a
// branch.
//
// Landing five branches one command at a time is the common case, and doing it
// by hand has a failure mode this does not: after the second merge the checkout
// has moved on, so the third `land` is being run against a base that is no
// longer what the fleet was launched against. Here the base is re-checked before
// every merge, because Land re-reads it — the loop deliberately does not cache
// it.
//
// Oldest first is the launch order, which is the closest thing to the order the
// fleet file wrote: `land` takes no fleet file, so the containers are the only
// record of what ran, and reversing docker's newest-first listing is what turns
// that into the sequence a person would expect.
//
// Nothing here is atomic and it is not trying to be. Each land is its own merge
// commit; if the fourth conflicts, the first three are landed and the conflict
// is left in the working tree exactly as a single `land` leaves it.
func (r *Runner) LandAll(ctx context.Context, opts LandOptions) (LandAllResult, error) {
	var res LandAllResult

	branches, err := r.landableBranches(ctx)
	if err != nil {
		return res, err
	}
	if len(branches) == 0 {
		return res, fmt.Errorf("no fleet branches to land in %s; `sandbox-cli fleet status` shows what there is", r.Repo)
	}

	for _, b := range branches {
		one, err := r.Land(ctx, b, opts)
		if err == nil {
			res.Landed = append(res.Landed, one)
			continue
		}
		if skippable(err) {
			res.Skipped = append(res.Skipped, Skipped{Branch: b, Reason: err.Error()})
			continue
		}
		// A refusal about the base branch. Return what was landed so far along with
		// it — the caller has to print both, since some of these merges have
		// already happened and saying only "failed" would be false.
		return res, fmt.Errorf("stopped at %s: %w", b, err)
	}
	return res, nil
}

// skippable reports whether a Land refusal is about the branch alone, in which
// case the next branch may still be landable.
func skippable(err error) bool {
	for _, s := range []error{ErrAgentRunning, ErrNotVerified, ErrNoWorktree, ErrNothingToLand} {
		if errors.Is(err, s) {
			return true
		}
	}
	return false
}

// landableBranches lists this fleet's branches in launch order.
//
// Taken from the containers rather than from `worktree list`, for the same
// reason `fleet clean --worktrees` intersects the two: a worktree an ordinary
// `--worktree` run made is not this fleet's to land. A branch whose container
// has been reaped is therefore not landed by --all; it is still landable by
// name, which is the trade `clean` already makes and this keeps consistent.
func (r *Runner) landableBranches(ctx context.Context) ([]string, error) {
	infos, err := r.containers(ctx, "")
	if err != nil {
		return nil, err
	}
	type entry struct {
		branch string
		order  int
	}
	seen := map[string]bool{}
	var entries []entry
	for i, c := range infos {
		b := c.Labels[sandbox.LabelBranch]
		if b == "" || seen[b] {
			continue
		}
		seen[b] = true
		entries = append(entries, entry{branch: b, order: i})
	}
	// Containers arrive newest first; reverse to launch order.
	sort.Slice(entries, func(i, j int) bool { return entries[i].order > entries[j].order })
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.branch)
	}
	return out, nil
}

// verifyVerdict is what land could establish about a branch's definition of done.
// Three outcomes rather than a bool because "passed" and "nobody ever asked" must
// never print as the same thing.
type verifyVerdict int

const (
	verifyNone            verifyVerdict = iota // no verify was declared, or no record survives
	verifyPassed                               // declared, and the run exited 0
	verifyForced                               // declared, failed its verify, caller overrode
	verifyForcedUnchecked                      // declared, never reached its verify, caller overrode
)

// checkVerified reads back whether this branch's run passed the verify command it
// declared, and refuses to land it if it did not.
//
// The record is the container: the label says a verify was declared, and the exit
// code says how it went. Nothing here trusts a file in the workspace, because the
// workspace is what the agent was editing.
//
// The honest limit, worth knowing before relying on this: the verify command is
// fixed, but its *meaning* is not. `go test ./...` runs agent-written tests over an
// agent-written tree, so an agent that deletes the failing test passes. This makes
// forging a pass require editing the tests rather than merely claiming success —
// which is a real improvement over an exit code alone, and is not the same as
// being unforgeable.
//
// No record means no refusal. The label is gone once `fleet clean` reaps the
// container, and a branch landed by hand never had one; refusing there would turn
// reaping a container into a way of breaking landing. The caller is told instead,
// and says "unverified" rather than "passed".
func (r *Runner) checkVerified(ctx context.Context, branch string, force bool) (verifyVerdict, error) {
	c, err := r.latestFor(ctx, branch)
	if err != nil {
		return verifyNone, err
	}
	if c == nil || c.Labels[sandbox.LabelVerify] == "" {
		return verifyNone, nil
	}
	if c.ExitCode == 0 {
		return verifyPassed, nil
	}
	declared := c.Labels[sandbox.LabelVerify]
	if force {
		// Two different things to have overridden, kept apart all the way to the
		// message: a verify that ran and said no, and a run that died before its
		// verify ever ran. Collapsing them would have --force report "failed its
		// verify" for a container the OOM killer took.
		if c.ExitCode == VerifyFailedExit {
			return verifyForced, nil
		}
		return verifyForcedUnchecked, nil
	}
	if c.ExitCode == VerifyFailedExit {
		return verifyNone, branchRefusal(ErrNotVerified, "%q did not pass its verify (%s); "+
			"read the output with `sandbox-cli fleet logs %s`, fix it and run again, or land it anyway with --force",
			branch, declared, branch)
	}
	// Any other code means the run never reached its verify — killed, stopped, or
	// out of memory — so nothing has judged this work at all.
	return verifyNone, branchRefusal(ErrNotVerified, "%q exited %d without reaching its verify (%s); "+
		"the work is unchecked. See `sandbox-cli fleet logs %s`, or land it anyway with --force",
		branch, c.ExitCode, declared, branch)
}

// checkBase compares where this branch was launched to land with what is checked
// out now, and refuses when they differ.
//
// An explicit onto replaces the recorded expectation rather than adding to it —
// the user is standing in the repository saying "land it here" — but it is still
// checked against HEAD, because naming a branch that is not checked out is a
// mistake git cannot carry out and should not be silently reinterpreted as "land
// it wherever I am".
//
// A branch with no recorded base passes. That is not a hole: the label is missing
// when the container has been reaped, or when the run predates the label, and
// there is then no expectation to violate — only a user who named a branch while
// standing on another one. Refusing there would make `fleet clean` quietly break
// landing, which is a worse failure than the one being prevented.
func (r *Runner) checkBase(ctx context.Context, branch, head, onto string) error {
	if onto != "" {
		if onto != head {
			return fmt.Errorf("cannot land %q onto %q: %q is checked out in %s. "+
				"git merges into the current branch and nothing else — check out %q first",
				branch, onto, head, r.Repo, onto)
		}
		return nil
	}
	want := r.recordedBase(ctx, branch)
	if want == "" || want == head {
		return nil
	}
	return fmt.Errorf("%q was launched to land on %q, but %q is checked out in %s. "+
		"check out %q and land again, or pass --onto %s to land it here deliberately",
		branch, want, head, r.Repo, want, head)
}

// recordedBase reports the branch this agent was launched to land on, from the
// sandbox.base label its container carries, or "" when there is no container left
// to ask or it was started without one.
//
// Deliberately read from the *container* rather than from the fleet file: the file
// can be edited between launch and landing, and what matters is what was true when
// the agent started. Docker is the state store.
func (r *Runner) recordedBase(ctx context.Context, branch string) string {
	c, err := r.latestFor(ctx, branch)
	if err != nil || c == nil {
		return ""
	}
	return c.Labels[sandbox.LabelBase]
}
