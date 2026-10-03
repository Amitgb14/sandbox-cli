package session

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/config"
	"github.com/Amitgb14/sandbox-cli/internal/protocol"
	"github.com/Amitgb14/sandbox-cli/internal/rescue"
)

// Keeper is the crash safety net for panes, which is to say for **detached runs**
// — the two paths that never had one.
//
// A foreground `sandbox-cli claude` snapshots every two minutes and again on the
// way out, including on Ctrl-C. A `--detach` run gets nothing: `run.go` returns
// through `startDetached` before the snapshotter is built. A Studio run gets one
// *baseline*, the workspace as it was before the agent started, and then the session
// closes. So the net people picture has always existed only on the first of three
// paths, and Studio launches everything detached — which cost somebody a file they
// had watched an agent write (issue #163).
//
// The reason it was not simply built is recorded in
// `docs/security/open-items.md`, and both objections were real: a long-lived daemon
// writing into the user's repository on a timer is a different proposition from a
// foreground command doing it for one run, and a supervisor whose watch set is in
// memory would silently stop protecting runs across a restart — worse than no net,
// because the gap is invisible.
//
// The session server answers both, and that is why this lives here rather than in
// `studioapi/supervisor.go`:
//
//   - **One repository.** A session's id derives from `worktree.RepoID`, so a
//     daemon snapshots the repository it was started in and no other. Not
//     "repositories nobody is looking at, possibly several at once" — the one whose
//     directory you typed `serve` in.
//   - **A restart resumes.** The catalog is on disk and `Adopt` rebinds live
//     containers from their labels, so a restarted `serve` finds the panes that are
//     still going and protects them again. What it does not do is *continue* their
//     old rescue session — `rescue.Begin` mints one — so a pane that outlives a
//     restart ends up with two, both listed by `recover list`. That is two manifests
//     where one would be tidier, and it is deliberately not hidden: the alternative
//     is a protection gap nobody can see, which is the thing the open item objected
//     to.
//   - **Every detached run, not just Studio's.** It snapshots *panes*, and since
//     phase 2 every detached run is one. The two unprotected paths become one
//     protected one rather than one of them being fixed.
type Keeper struct {
	mu sync.Mutex
	// by is the live snapshotter per pane id. A pane leaves this map when its
	// container stops, which is also when its session is closed with an outcome.
	by map[string]*keeperEntry

	interval  time.Duration
	retention rescue.Retention
	mirror    *config.S3Spec
	enabled   bool
}

type keeperEntry struct {
	snap *rescue.Snapshotter
	// dir is the workspace being protected, kept so a pane whose worktree changed
	// underneath it is noticed rather than snapshotted into the wrong repository.
	dir string
}

// NewKeeper builds the net from the user's configuration.
//
// The config is the **user's own**, never a project `.sandbox.yaml`: `snapshot` is on
// `trust.go`'s refused list precisely because `enabled: false` silently removes crash
// protection and `interval: 1ms` is a host busy-loop. A daemon reading it needs that
// care more than a foreground run does, not less, because nobody is watching.
func NewKeeper(cfg config.Config) *Keeper {
	k := &Keeper{
		by:        map[string]*keeperEntry{},
		enabled:   cfg.Snapshot.IsEnabled(),
		interval:  cfg.Snapshot.EveryDuration(),
		retention: rescue.Retention{Run: cfg.Snapshot.RetentionDuration(), Manual: cfg.Snapshot.ManualRetentionDuration()},
	}
	// A floor, which the foreground path does not need and this does: that one runs
	// for as long as somebody sits there, and this one runs for as long as the
	// machine is on. `interval` comes from a file, and the file is trusted — but a
	// trusted file with a typo in it is still a timer the daemon honours forever.
	if k.interval > 0 && k.interval < minKeeperInterval {
		k.interval = minKeeperInterval
	}
	if cfg.Snapshot.S3.UploadsRun() {
		k.mirror = cfg.Snapshot.S3
	}
	return k
}

// minKeeperInterval is the fastest this loop will snapshot, whatever it is told.
//
// Thirty seconds. `git add -A` against a private index is cheap and not free, and
// the cost is paid per *pane*: a repository with six agents in it is six index walks
// per tick. The foreground path has no floor because its interval is bounded by
// somebody's patience.
const minKeeperInterval = 30 * time.Second

// Interval is how often Sweep should be called, or zero when snapshots are off.
func (k *Keeper) Interval() time.Duration {
	if k == nil || !k.enabled {
		return 0
	}
	return k.interval
}

// Sweep brings the net into line with the catalog: protect what is running, and
// close what has stopped.
//
// Driven by the caller's ticker rather than owning a goroutine per pane, because the
// set of panes changes and a goroutine per pane is a goroutine to stop per pane —
// and the daemon already has a loop. Each snapshot is taken synchronously here, so a
// repository that is slow to walk slows the sweep rather than piling up.
func (k *Keeper) Sweep(ctx context.Context, panes []protocol.Pane, worktreeDir func(protocol.Pane) string) {
	if k == nil || !k.enabled {
		return
	}

	live := map[string]bool{}
	for _, p := range panes {
		if p.Running() {
			live[p.ID] = true
		}
	}

	// Close first, so a pane that stopped gets its final snapshot before the sweep
	// spends time on the others — the last write an agent made is the one most
	// likely to be wanted, and it is also the one a reap could take away.
	k.mu.Lock()
	var closing []struct {
		id    string
		entry *keeperEntry
	}
	for id, e := range k.by {
		if !live[id] {
			closing = append(closing, struct {
				id    string
				entry *keeperEntry
			}{id, e})
			delete(k.by, id)
		}
	}
	k.mu.Unlock()

	for _, c := range closing {
		outcome, code := outcomeOf(panes, c.id)
		// Stop takes a final snapshot of its own, which is the point: whatever the
		// agent wrote between the last tick and its exit is in it.
		c.entry.snap.Stop(outcome, code)
	}

	for _, p := range panes {
		if !p.Running() {
			continue
		}
		dir := worktreeDir(p)
		if dir == "" {
			// No directory to protect. A pane whose worktree the catalog cannot name
			// is one whose worktree may have been removed since it started, and
			// snapshotting a guess would write into the wrong repository.
			continue
		}
		k.snapshot(ctx, p, dir)
	}
}

// snapshot protects one pane, starting a session for it if this is the first sight.
func (k *Keeper) snapshot(ctx context.Context, p protocol.Pane, dir string) {
	k.mu.Lock()
	e, known := k.by[p.ID]
	if known && e.dir != dir {
		// The pane's workspace moved. Close the old session rather than carrying on
		// writing snapshots of one repository under a manifest that names another.
		delete(k.by, p.ID)
		k.mu.Unlock()
		e.snap.Stop(rescue.OutcomeSignalled, nil)
		k.mu.Lock()
		known = false
	}
	if !known {
		// Begin returns nil for the reasons a workspace has nothing to protect — no
		// git, not a repository, a bare one — and says so itself for anything worse.
		// A nil is cached as a nil would be rechecked every tick otherwise, which for
		// a non-repository pane is a stat storm for an answer that will not change.
		snap := rescue.Begin(dir, p.Agent, k.interval, k.retention)
		if snap == nil {
			k.by[p.ID] = &keeperEntry{dir: dir}
			k.mu.Unlock()
			return
		}
		if k.mirror != nil {
			snap.MirrorTo(k.mirror)
		}
		e = &keeperEntry{snap: snap, dir: dir}
		k.by[p.ID] = e
	}
	k.mu.Unlock()

	if e.snap == nil {
		return // known-unsnapshottable, cached above
	}
	// Once rather than Start: the snapshotter's own loop would be a second timer per
	// pane, and this one is already ticking.
	if _, err := e.snap.Once(); err != nil {
		// Reported once and not fatal. A net that quietly is not there is worse than
		// none — the rule `rescue.Begin` already states — and a failing repository
		// must not stop the daemon answering about the others.
		fmt.Fprintf(os.Stderr, "sandbox-cli: snapshot of pane %s failed: %v\n", p.ID, err)
	}
}

// Close stops every session, for a daemon shutting down.
//
// Each gets a final snapshot and the `signalled` outcome, which is what a foreground
// run's Ctrl-C produces — and it is the honest word: the run did not end, the thing
// watching it did. The containers keep running, so a later `serve` picks them up
// again in a new session.
func (k *Keeper) Close() {
	if k == nil {
		return
	}
	k.mu.Lock()
	entries := make([]*keeperEntry, 0, len(k.by))
	for id, e := range k.by {
		entries = append(entries, e)
		delete(k.by, id)
	}
	k.mu.Unlock()
	for _, e := range entries {
		e.snap.Stop(rescue.OutcomeSignalled, nil)
	}
}

// outcomeOf translates a stopped pane's state into the word a rescue session
// records, so `recover list` describes a daemon-protected run the way it describes
// a foreground one.
func outcomeOf(panes []protocol.Pane, id string) (string, *int) {
	for _, p := range panes {
		if p.ID != id {
			continue
		}
		switch p.State {
		case protocol.StateDone:
			zero := 0
			if p.ExitCode != nil {
				return rescue.OutcomeClean, p.ExitCode
			}
			return rescue.OutcomeClean, &zero
		case protocol.StateFailed:
			return rescue.OutcomeFailed, p.ExitCode
		}
		// Stopped: the container is gone and took its exit code with it. Recorded as
		// signalled rather than clean, because "we do not know how it ended" must not
		// read as "it ended well".
		return rescue.OutcomeSignalled, nil
	}
	return rescue.OutcomeSignalled, nil
}
