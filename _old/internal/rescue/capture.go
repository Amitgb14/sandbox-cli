package rescue

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/config"
)

// ErrNothingToSnapshot is returned by Capture when the workspace tree is
// identical to the last snapshot's — there was nothing to record.
//
// It is an error rather than an empty success on purpose. The snapshot loop
// treats an unchanged tree as a non-event, because an idle agent should cost
// nothing; but somebody who asked for a checkpoint and got back an id pointing
// at no commit would believe they had one. Callers that want the loop's
// behaviour check for this and carry on.
var ErrNothingToSnapshot = errors.New("nothing to snapshot: the workspace has not changed")

// CaptureOptions describes one on-demand snapshot.
type CaptureOptions struct {
	// Agent is the agent this workspace belongs to, when there is one. Recorded
	// for the same reason a run records it: it is half of how a snapshot is
	// later matched to the work it came from.
	Agent string

	// Label is what to call it. A run is already named by its branch and its
	// agent; a checkpoint taken by hand is otherwise a hex id in a list.
	Label string

	// Source is SourceRun or SourceSDK. Empty is recorded as SourceRun, which is
	// what every session predating the field was.
	Source string

	// Retention overrides how long to keep this one, as a duration string.
	// Empty follows whatever default is in force when pruning runs — see
	// Session.Retention for why the rule is stored rather than an expiry.
	Retention string

	// S3 mirrors the snapshot to object storage once it is taken. Nil, or a
	// spec whose upload mode excludes manual captures, keeps it local.
	//
	// Passed in rather than read from a config here because this package does
	// not load configuration — the caller already resolved which layers apply,
	// and a second resolution is how the daemon and the CLI end up mirroring to
	// different buckets.
	S3 *config.S3Spec
}

// Capture takes a single snapshot of workspace and closes its session
// immediately, returning the snapshot it created.
//
// This is the explicit "snapshot now" that Once was left exposed for. It differs
// from Begin in three ways, and each is the same difference wearing different
// clothes — a caller asking in as many words is owed an answer, where the run
// path is owed silence:
//
//   - it reports why there is no snapshot rather than returning nil;
//   - an unchanged tree is ErrNothingToSnapshot, not a success;
//   - the session is closed on the spot with OutcomeManual, so it never reads as
//     a run that died.
//
// Nothing here writes HEAD, a branch, the index or the working tree: it is the
// same mechanism the loop uses, through the same private GIT_INDEX_FILE and the
// same githard-hardened git calls.
func Capture(workspace string, opts CaptureOptions) (Snapshot, error) {
	// The interval is never used — nothing calls Start on this Snapshotter — but
	// it must be positive to be a coherent value on the struct, and retention is
	// carried on the session rather than here, since prune reads it from the
	// manifest long after this call has returned.
	s, err := newSnapshotter(workspace, opts.Agent, snapshotTimeout, Retention{})
	if err != nil {
		return Snapshot{}, err
	}
	sess := s.sess
	sess.Label = opts.Label
	sess.Retention = opts.Retention
	sess.Source = opts.Source
	if sess.Source == "" {
		sess.Source = SourceRun
	}

	// Seed the comparison, so "nothing changed" means what a caller means by it.
	//
	// Without this the first snapshot of a session always commits, because a
	// fresh Snapshotter has no previous tree to compare against. What to compare
	// *to* has two answers and both are needed: the newest existing snapshot of
	// this workspace when there is one — taking two identical checkpoints back to
	// back is the case worth refusing, and it is far commoner than the other —
	// and HEAD's tree otherwise, since a snapshot identical to a commit already
	// on a branch is exactly what PruneSuperseded deletes as a duplicate.
	//
	// The first version compared only against HEAD, which meant a second capture
	// of an unchanged *dirty* tree quietly produced a duplicate: the tree
	// differed from HEAD, so there was "something to snapshot" by a definition
	// nobody holds.
	//
	// Deliberately not done in Begin. A run *wants* that first unconditional
	// snapshot: it is the before-image, and studioapi's baselineFor is built on
	// getting one back for a workspace that is usually clean.
	if tree := priorTree(workspace, s.repoRoot); tree != "" {
		s.mu.Lock()
		s.lastTree = tree
		s.mu.Unlock()
	}

	commit, err := s.Once()
	if err != nil {
		// The session recorded nothing and never will: leaving the manifest
		// behind would put a permanently empty entry in `recover list`.
		sess.remove()
		return Snapshot{}, err
	}
	if commit == "" {
		sess.remove()
		return Snapshot{}, ErrNothingToSnapshot
	}

	now := time.Now()
	sess.EndedAt = &now
	sess.Outcome = OutcomeManual
	if err := sess.Save(); err != nil {
		return Snapshot{}, fmt.Errorf("recording snapshot %s: %w", sess.ID, err)
	}

	// Mirrored before returning, and a failure is *returned* rather than
	// swallowed — the opposite of the loop's policy, and the same distinction
	// this whole file is built on. Somebody who asked for a checkpoint before a
	// risky step, with a bucket configured, has been told they have an off-machine
	// copy; discovering otherwise a fortnight later, from an empty bucket, is the
	// failure this feature exists to prevent.
	//
	// The snapshot itself is real either way and is returned alongside the error,
	// so a caller that would rather have a local checkpoint than none can say so.
	// The manifest records why it failed regardless, so the listing shows this one
	// as local-only instead of leaving it to whoever was watching the terminal.
	if opts.S3.UploadsManual() {
		// Bounded, because this call is reachable over HTTP: an endpoint that
		// hung for as long as a stalled TCP connection wanted to would hold a
		// handler open with no way for the caller to tell the difference between
		// slow and dead.
		ctx, cancel := context.WithTimeout(context.Background(), remoteTimeout)
		defer cancel()
		if err := Mirror(ctx, sess, opts.S3); err != nil {
			return Snapshot{Session: *sess, Commit: commit, Reachable: true},
				fmt.Errorf("snapshot %s was taken but not mirrored: %w", sess.ID, err)
		}
	}
	return Snapshot{Session: *sess, Commit: commit, Reachable: true}, nil
}

// SetRetention changes how long one snapshot is kept, by session id or
// unambiguous prefix. An empty duration clears the override and returns the
// snapshot to the default.
//
// Validated here rather than at the caller because the manifest is this
// package's format: an unparseable string written into it would not surface
// until pruning ran, which is the one moment nobody is watching.
func SetRetention(dir, id, retention string) (Snapshot, error) {
	if retention != "" {
		if _, err := time.ParseDuration(retention); err != nil {
			return Snapshot{}, fmt.Errorf("retention %q is not a duration (try %q, %q): %w", retention, "24h", "168h", err)
		}
	}
	repoRoot, err := MainRepoRoot(dir)
	if err != nil {
		return Snapshot{}, err
	}
	sess, err := findSession(repoRoot, id)
	if err != nil {
		return Snapshot{}, err
	}
	sess.Retention = retention
	if err := sess.Save(); err != nil {
		return Snapshot{}, err
	}
	return resolve([]Session{sess})[0], nil
}

// DefaultManualRetention is how long a snapshot somebody asked for is kept when
// nothing says otherwise.
//
// Shorter than the crash net's fourteen days on purpose. A crash snapshot is
// insurance against something nobody saw coming, so it has to still be there
// when they finally look; a checkpoint is taken *before* a known risk, by
// somebody who is right there and will either use it within the hour or not at
// all.
const DefaultManualRetention = 7 * 24 * time.Hour

// Retention is how long each kind of snapshot is kept when the snapshot itself
// does not say.
//
// A struct rather than two arguments because these travel together everywhere
// and are the same type: `Prune(root, a, b)` is a call nobody can read, and
// swapping the two is a mistake that shows up as missing snapshots a fortnight
// later.
type Retention struct {
	Run    time.Duration // sessions recorded by a sandbox run
	Manual time.Duration // sessions recorded by Capture
}

// For is how long to keep one session: what it says, else the default for its
// kind. A zero result means "keep it", which is what an unset Run retention has
// always meant to the snapshot loop.
func (r Retention) For(s Session) time.Duration {
	if s.Retention != "" {
		if d, err := time.ParseDuration(s.Retention); err == nil && d > 0 {
			return d
		}
		// An unparseable value is not a licence to delete somebody's work. It can
		// only get here by hand-editing the manifest, and keeping it is the
		// failure that costs disk rather than the one that costs the snapshot.
		return 0
	}
	if s.Outcome == OutcomeManual {
		if r.Manual > 0 {
			return r.Manual
		}
		return DefaultManualRetention
	}
	return r.Run
}

// priorTree is the tree a capture of workspace would be a duplicate of: the
// newest snapshot already recorded for it, else the commit HEAD points at.
//
// Empty when neither can be read — an unborn HEAD, a workspace whose manifests
// are unreadable — and empty means "compare against nothing", which captures.
// That is the right way to fail: a duplicate snapshot costs some disk, while
// refusing one that was not a duplicate costs the checkpoint somebody asked for.
func priorTree(workspace, repoRoot string) string {
	ctx := context.Background()
	sessions, err := Sessions(repoRoot)
	if err == nil {
		// Sessions come back newest-activity first, so the first match is the
		// one to compare against.
		for _, sess := range sessions {
			if sess.Workspace != workspace || sess.LastSnapshot == "" {
				continue
			}
			if tree, terr := run(ctx, sess.Repo, nil, "rev-parse", "--verify", "--quiet", sess.LastSnapshot+"^{tree}"); terr == nil && tree != "" {
				return tree
			}
			// The newest one's objects are gone (its ref was deleted and git
			// collected them). Keep looking rather than falling straight to
			// HEAD: an older snapshot is still a truthful comparison.
		}
	}
	if tree, terr := run(ctx, workspace, nil, "rev-parse", "--verify", "--quiet", "HEAD^{tree}"); terr == nil {
		return tree
	}
	return ""
}
