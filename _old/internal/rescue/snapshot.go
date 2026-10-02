package rescue

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/worktree"

	"github.com/Amitgb14/sandbox-cli/internal/termsafe"
)

// snapshotTimeout bounds one snapshot. A pathological workspace (a huge
// untracked tree that .gitignore does not cover) must slow nothing down and
// must never wedge the exit path; it just loses that snapshot.
const snapshotTimeout = 30 * time.Second

// finalSnapshotTimeout is the shorter budget for the snapshot taken on the way
// out, where the user is waiting and, if we were signalled, may well press the
// key again.
const finalSnapshotTimeout = 8 * time.Second

// maxConsecutiveFailures disables snapshotting for the rest of a run. Something
// is structurally wrong with the repository; repeating it every interval would
// only spam the agent's UI.
const maxConsecutiveFailures = 3

// Snapshotter commits the workspace into refs/sandbox/snapshots/<session> while
// a sandbox runs.
//
// Every method is safe on a nil receiver: Begin returns nil when there is
// nothing to protect (no git, not a repository, snapshots switched off), so the
// run path can call Start/Stop unconditionally and stay readable.
type Snapshotter struct {
	workspace string
	repoRoot  string
	indexFile string
	interval  time.Duration
	retention time.Duration
	sess      *Session

	mu         sync.Mutex
	lastTree   string
	lastCommit string
	failures   int
	disabled   bool
	skipped    map[string]bool // oversized paths already reported, so we say it once

	// contentDir is a scratch git directory used only for reading the workspace,
	// so the repository's own config is never consulted (see contentEnv).
	// objectDir is the real repository's object store, where those reads still
	// write. Both empty when the scratch directory could not be built.
	contentDir string
	objectDir  string

	stop     chan struct{}
	stopOnce sync.Once
	wg       sync.WaitGroup
}

// Begin records a session for this run and returns the Snapshotter that will
// protect it, or nil when there is nothing to protect. It never fails a run:
// anything that goes wrong here means no safety net, not no sandbox.
//
// The expected reasons for nil — no git, not a repository, switched off — are
// silent, because they are not problems. An unwritable rescue directory is, so
// that one says so: a safety net that quietly isn't there is worse than none.
func Begin(workspace, agent string, interval, retention time.Duration) *Snapshotter {
	if interval <= 0 || !Available() {
		return nil
	}
	repoRoot, err := MainRepoRoot(workspace)
	if err != nil {
		return nil // not a git repository; nothing to snapshot into
	}
	// A bare repository has no working tree to snapshot. Without this the first
	// `add -A` would fail, and fail again, until the failure policy gave up
	// noisily in the middle of the agent's UI.
	if bare, err := run(context.Background(), workspace, nil, "rev-parse", "--is-bare-repository"); err == nil && bare == "true" {
		return nil
	}
	now := time.Now()
	sess := &Session{
		ID:          newSessionID(now),
		Repo:        repoRoot,
		Workspace:   workspace,
		Branch:      worktree.Branch(workspace),
		Agent:       agent,
		HeadAtStart: headSHA(context.Background(), workspace),
		StartedAt:   now,
	}
	sess.Ref = RefPrefix + sess.ID
	if err := sess.Save(); err != nil {
		warnNoSafetyNet(err)
		return nil
	}
	s := &Snapshotter{
		workspace: workspace,
		repoRoot:  repoRoot,
		indexFile: filepath.Join(indexDir(repoRoot, sess.ID), "index"),
		interval:  interval,
		retention: retention,
		sess:      sess,
		stop:      make(chan struct{}),
	}
	if err := os.MkdirAll(filepath.Dir(s.indexFile), 0o700); err != nil {
		warnNoSafetyNet(err)
		return nil
	}
	// Built once, here, rather than per snapshot: it costs a `git init` and the
	// answer never changes for a session. A failure is not fatal — snapshotting
	// falls back to reading through the repository's own git directory, which is
	// weaker but is still a safety net.
	if !s.prepareContentDir(context.Background()) {
		fmt.Fprintln(os.Stderr, "sandbox-cli: snapshots will read through the repository's own git config "+
			"(scratch git dir unavailable); a hostile repo could run a clean filter on this host")
	}
	return s
}

// warnNoSafetyNet reports that this run has no crash protection, once, on the
// way in — while the user can still do something about it.
func warnNoSafetyNet(err error) {
	fmt.Fprintf(os.Stderr, "sandbox-cli: crash snapshots unavailable for this run: %v\n", err)
}

// Session exposes the manifest for callers that need to report on it.
func (s *Snapshotter) Session() *Session {
	if s == nil {
		return nil
	}
	return s.sess
}

// Start takes an immediate snapshot and then keeps taking them on the interval,
// so the loss window on a hard kill is bounded by the interval rather than by
// the length of the run.
func (s *Snapshotter) Start() {
	if s == nil {
		return
	}
	s.wg.Add(1)
	go s.loop()
}

func (s *Snapshotter) loop() {
	defer s.wg.Done()
	// Housekeeping first: a snapshot ref keeps its objects alive forever, so old
	// sessions have to age out somewhere, and the start of a run in the same
	// repository is the natural place. Sessions whose work has since been
	// committed for real go at the same time — that, rather than the 14-day
	// timeout, is how most snapshots should end.
	prune(s.repoRoot, s.retention)
	_, _ = PruneSuperseded(s.repoRoot)

	s.take(snapshotTimeout)
	t := time.NewTicker(s.interval)
	defer t.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-t.C:
			s.take(snapshotTimeout)
		}
	}
}

// Stop halts the loop, takes one final snapshot, and closes the manifest. It is
// the last chance to capture work, so it runs even when the loop already
// disabled itself for repeated failures — a final failure costs nothing.
//
// Idempotent, because the run path and the signal handler both race to be the
// one that ends the run, and a panic from the crash safety net would be a poor
// way to lose someone's work.
func (s *Snapshotter) Stop(outcome string, exitCode *int) {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() {
		close(s.stop)
		s.wg.Wait()

		s.mu.Lock()
		s.failures, s.disabled = 0, false
		s.mu.Unlock()
		s.take(finalSnapshotTimeout)

		now := time.Now()
		s.sess.EndedAt = &now
		s.sess.Outcome = outcome
		s.sess.ExitCode = exitCode
		_ = s.sess.Save()
	})
}

// Once takes a single snapshot and reports the commit it created ("" when
// nothing had changed). Exposed for tests and for a future explicit
// "snapshot now" command.
func (s *Snapshotter) Once() (string, error) {
	if s == nil {
		return "", nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), snapshotTimeout)
	defer cancel()
	return s.snapshot(ctx)
}

// take is the fire-and-forget wrapper the loop uses: it applies the failure
// policy and never propagates an error, because a failed snapshot must not
// change what the run does.
func (s *Snapshotter) take(timeout time.Duration) {
	s.mu.Lock()
	if s.disabled {
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	_, err := s.snapshot(ctx)

	s.mu.Lock()
	defer s.mu.Unlock()
	if err == nil {
		s.failures = 0
		return
	}
	s.failures++
	if s.failures >= maxConsecutiveFailures {
		s.disabled = true
		// Say it once. The user is looking at an agent's full-screen UI; the
		// point is that they can find out later why `recover list` is empty.
		fmt.Fprintf(os.Stderr, "\nsandbox-cli: crash snapshots disabled for this run after %d failures: %v\n",
			s.failures, err)
	}
}

// snapshot is the whole mechanism, and its shape is the safety argument:
//
//	add -A       against a private index — the repository's own index is untouched
//	write-tree   -> if the tree is unchanged, stop; an idle agent costs nothing
//	commit-tree  parented on the previous snapshot *and* on HEAD, so commits the
//	             agent made stay reachable even if it later resets the branch away
//	update-ref   compare-and-swap under refs/sandbox/, the only ref we ever write
//
// Nothing here writes HEAD, a branch, the index, or the working tree — that part
// of the argument is carried by the private GIT_INDEX_FILE and holds.
//
// What did NOT hold, until the githard hardening: "no hook runs, these are all
// plumbing commands". Plumbing is not the same as inert. `add -A` runs
// filter.<x>.clean and core.fsmonitor, and `update-ref` runs the
// reference-transaction hook — all named by files inside the repository, which
// the agent has read-write access to for the whole life of the run. Both were
// reproduced executing on the host during a plain `sandbox-cli run`. Every git
// invocation in this package now goes through githard, which is what makes the
// sentence above true rather than merely plausible; see
// hostile_repo_test.go for the regression, and do not add a git call here that
// bypasses run/runRaw.
func (s *Snapshotter) snapshot(ctx context.Context) (string, error) {
	env := append([]string{"GIT_INDEX_FILE=" + s.indexFile}, snapshotIdentity...)

	// Reading the workspace's *content* is done against a scratch git directory,
	// not the user's. See contentEnv: it is what stops a filter driver being
	// defined at all.
	readEnv := append(append([]string{}, env...), s.contentEnv()...)

	addArgs := []string{"add", "-A"}
	if specFile := s.oversizedExcludes(ctx, readEnv); specFile != "" {
		addArgs = append(addArgs, "--pathspec-from-file="+specFile, "--pathspec-file-nul")
	}
	if _, err := run(ctx, s.workspace, readEnv, addArgs...); err != nil {
		return "", err
	}
	tree, err := run(ctx, s.workspace, readEnv, "write-tree")
	if err != nil {
		return "", err
	}

	s.mu.Lock()
	lastTree, lastCommit := s.lastTree, s.lastCommit
	s.mu.Unlock()
	if tree == lastTree {
		return "", nil
	}

	head := headSHA(ctx, s.workspace)
	args := []string{"commit-tree", tree}
	if lastCommit != "" {
		args = append(args, "-p", lastCommit)
	}
	// HEAD joins as a parent whenever it is not already reachable — on the first
	// snapshot, and again every time the agent commits. That is what makes the
	// agent's own commits survive a `git reset --hard` inside the container.
	if head != "" && (lastCommit == "" || !isAncestor(ctx, s.workspace, head, lastCommit)) {
		args = append(args, "-p", head)
	}
	branch := s.sess.Branch
	if branch == "" {
		branch = "(detached)"
	}
	msg := fmt.Sprintf("sandbox snapshot: %s @ %s\n\nsession: %s\nworkspace: %s\n",
		branch, time.Now().Format(time.RFC3339), s.sess.ID, s.workspace)
	if s.sess.Agent != "" {
		msg += "agent: " + s.sess.Agent + "\n"
	}
	args = append(args, "-m", msg)

	commit, err := run(ctx, s.workspace, env, args...)
	if err != nil {
		return "", err
	}

	// Compare-and-swap against what we last wrote: an empty old value means "this
	// ref must not exist yet", so two sandboxes can never quietly overwrite each
	// other's history even if their session ids somehow collided.
	if _, err := run(ctx, s.workspace, env, "update-ref", "--create-reflog", s.sess.Ref, commit, lastCommit); err != nil {
		return "", err
	}

	now := time.Now()
	s.mu.Lock()
	s.lastTree, s.lastCommit = tree, commit
	s.sess.Snapshots++
	s.sess.LastSnapshot = commit
	s.sess.LastSnapshotAt = &now
	err = s.sess.Save()
	s.mu.Unlock()
	if err != nil {
		// The snapshot itself is safe in the object store; only the manifest that
		// makes it findable failed to update. Worth reporting as a failure so the
		// failure policy notices a broken rescue directory.
		return commit, fmt.Errorf("recording session: %w", err)
	}
	return commit, nil
}

// liveSessionGrace is how long a session with no end record has to stay quiet
// before pruning will touch it. A running sandbox snapshots every couple of
// minutes and closes its manifest on the way out, so an hour of silence with no
// end record means the run died — not that it is still working. Deleting a live
// session's ref would make its next compare-and-swap fail.
const liveSessionGrace = time.Hour

// settled reports whether a session is definitely over.
func settled(s Session) bool {
	return s.EndedAt != nil || time.Since(s.Activity()) > liveSessionGrace
}

// prune drops sessions whose last activity is older than the retention window,
// deleting the snapshot ref (which is what was pinning the objects) along with
// the manifest. Best-effort throughout: retention housekeeping must never be the
// reason a run misbehaves.
func prune(repoRoot string, olderThan time.Duration) {
	if olderThan <= 0 {
		return
	}
	_, _ = Prune(repoRoot, olderThan)
}

// Prune removes snapshot sessions for a repository that are older than
// olderThan, and reports how many went. Exposed for `sandbox-cli recover prune`.
func Prune(repoRoot string, olderThan time.Duration) (int, error) {
	sessions, err := Sessions(repoRoot)
	if err != nil {
		return 0, err
	}
	cutoff := time.Now().Add(-olderThan)
	n := 0
	for _, sess := range sessions {
		if !sess.Activity().Before(cutoff) || !settled(sess) {
			continue
		}
		drop(sess)
		n++
	}
	return n, nil
}

// PruneSuperseded removes sessions whose snapshot content is already in the
// repository's real history — the normal end of a snapshot's life. Once you have
// committed the work the agent left behind, the snapshot holds a tree that some
// commit on a branch already holds, and its ref is doing nothing but pinning a
// duplicate.
//
// "Superseded" means an exact tree match, never "the branch moved on". Commit
// half of what the agent left and the trees differ, so the snapshot stays — it
// is still the only copy of the other half.
func PruneSuperseded(repoRoot string) (int, error) {
	sessions, err := Sessions(repoRoot)
	if err != nil {
		return 0, err
	}
	ctx := context.Background()
	n := 0
	for _, sess := range sessions {
		if !settled(sess) {
			continue
		}
		snap := resolve([]Session{sess})[0]
		// Nothing captured, or the objects are already gone: not this function's
		// business, retention will collect the record in its own time.
		if snap.Commit == "" || !snap.Reachable {
			continue
		}
		if _, ok := supersededBy(ctx, sess.Repo, snap.Commit, sess.HeadAtStart); !ok {
			continue
		}
		drop(sess)
		n++
	}
	return n, nil
}

// supersededBy looks for a commit on a branch whose tree is identical to the
// snapshot's, and returns it. Identical trees mean identical content, so such a
// commit makes the snapshot redundant.
//
// The search is bounded by the commit the run started from: anything that
// captures this snapshot's content has to have been committed since then. If
// that commit is unknown the search falls back to a bounded window rather than
// walking all of history — failing to notice a superseded snapshot only costs
// some disk, while a slow scan would be paid at the start of every run.
func supersededBy(ctx context.Context, repo, snapCommit, headAtStart string) (string, bool) {
	tree, err := run(ctx, repo, nil, "rev-parse", "--verify", "--quiet", snapCommit+"^{tree}")
	if err != nil || tree == "" {
		return "", false
	}
	// The starting commit counts too: a run that changed nothing (or changed
	// something and reverted it) snapshots a tree that was already in history.
	if headAtStart != "" {
		if t, err := run(ctx, repo, nil, "rev-parse", "--verify", "--quiet", headAtStart+"^{tree}"); err == nil && t == tree {
			return headAtStart, true
		}
	}
	args := []string{"log", "--branches", "--format=%H %T"}
	if headAtStart != "" && objectExists(ctx, repo, headAtStart) {
		args = append(args, "--not", headAtStart)
	} else {
		args = append(args, "--max-count=1000")
	}
	out, err := run(ctx, repo, nil, args...)
	if err != nil {
		return "", false
	}
	for _, line := range strings.Split(out, "\n") {
		commit, t, ok := strings.Cut(strings.TrimSpace(line), " ")
		if ok && t == tree {
			return commit, true
		}
	}
	return "", false
}

// drop deletes a session's snapshot ref and its record. The ref goes first: it
// is the thing pinning objects, and a manifest without a ref is merely stale
// while a ref without a manifest is unfindable.
func drop(sess Session) {
	if sess.Ref != "" {
		_, _ = run(context.Background(), sess.Repo, nil, "update-ref", "-d", sess.Ref)
	}
	sess.remove()
}

// maxSnapshotFileBytes is the largest single file a snapshot will copy into the
// user's object store.
//
// A snapshot writes into the *user's* repository and pins what it wrote behind
// refs/sandbox for the retention window, so anything captured survives the agent
// deleting it — 60 MB of junk written once stayed 60 MB for fourteen days, and
// PruneSuperseded only drops a session whose tree is committed exactly, so it
// never self-cleaned. The safety net is for source code someone would be upset to
// lose; a build artifact, a core dump or a model checkpoint is neither at risk
// nor worth that.
//
// Deliberately a constant rather than config. As a `snapshot.*` key it would be
// settable from a project .sandbox.yaml, which is untrusted — and "raise the cap"
// is exactly the knob a hostile repository would reach for.
const maxSnapshotFileBytes = 10 << 20 // 10 MiB

// maxOversizedExcludes bounds how many paths one snapshot will exclude. Past
// that the repository is pathological, and an unbounded list becomes its own
// problem — an argv (or pathspec file) that grows without limit, and a report the
// user cannot read.
const maxOversizedExcludes = 500

// oversizedExcludes writes a pathspec file excluding files too large to
// snapshot, and returns its path ("" when there is nothing to exclude). It also
// reports each skipped path once per session, so a missing file is never a silent
// surprise.
//
// Three things here are not incidental, and each was a bug first:
//
//   - `ls-files -z`. Without it git *quotes* any path with a non-ASCII or control
//     character, so `rapport-financiér.bin` came back as a C-quoted string, Lstat
//     failed on it, and the file was never excluded — the size cap simply did not
//     apply to a large class of ordinary filenames. A newline in a name also split
//     one path into two bogus lines.
//   - `:(exclude,literal)`. A bare `:(exclude)<path>` is a *glob*, so a file
//     literally named `*` excluded everything and the snapshot captured nothing at
//     all — the safety net silently dead for the rest of the run. `literal` is what
//     makes the path a path.
//   - `--pathspec-from-file`. The list can be long; as argv it hit
//     "argument list too long", which failed the add, and three consecutive
//     failures disable snapshotting for the run.
func (s *Snapshotter) oversizedExcludes(ctx context.Context, env []string) string {
	out, err := run(ctx, s.workspace, env, "ls-files", "-o", "-m", "-z", "--exclude-standard")
	if err != nil || out == "" {
		return ""
	}
	var specs []byte
	var newlySkipped []string
	truncated := false
	count := 0
	for _, rel := range strings.Split(out, "\x00") {
		if rel == "" {
			continue
		}
		fi, err := os.Lstat(filepath.Join(s.workspace, rel))
		if err != nil || !fi.Mode().IsRegular() || fi.Size() <= maxSnapshotFileBytes {
			continue
		}
		if count >= maxOversizedExcludes {
			truncated = true
			break
		}
		count++
		specs = append(specs, ":(exclude,literal)"...)
		specs = append(specs, rel...)
		specs = append(specs, 0)

		s.mu.Lock()
		if s.skipped == nil {
			s.skipped = map[string]bool{}
		}
		if !s.skipped[rel] && len(s.skipped) < maxOversizedExcludes {
			s.skipped[rel] = true
			newlySkipped = append(newlySkipped, fmt.Sprintf("%s (%d MiB)", termsafe.Clean(rel), fi.Size()>>20))
		}
		s.mu.Unlock()
	}
	if len(specs) == 0 {
		return ""
	}
	specFile := filepath.Join(filepath.Dir(s.indexFile), "exclude-pathspecs")
	if err := os.WriteFile(specFile, specs, 0o600); err != nil {
		return ""
	}
	for _, m := range newlySkipped {
		fmt.Fprintf(os.Stderr, "sandbox-cli: snapshot skipping %s — over the %d MiB per-file limit\n",
			m, maxSnapshotFileBytes>>20)
	}
	if truncated {
		fmt.Fprintf(os.Stderr, "sandbox-cli: more than %d oversized files; the rest are being snapshotted\n",
			maxOversizedExcludes)
	}
	return specFile
}

// contentEnv redirects the commands that read the workspace — `add -A` and
// `write-tree` — at a scratch git directory, while still writing their objects
// into the user's repository.
//
// This is what actually stops clean filters, and githard's GIT_ATTR_SOURCE was
// not enough on its own. That variable redirects the *tree* attributes layer, but
// git always additionally reads `$GIT_DIR/info/attributes` — a gitdir-local layer
// with no `-c` override — so an agent that wrote `.git/info/attributes` plus a
// `filter.<x>.clean` in `.git/config` still had its command executed on the host
// by the next snapshot. Confirmed end to end before this existed.
//
// Chasing the attribute layers is the wrong shape of fix: a filter runs only if
// an attribute selects a driver *and* that driver is defined in a config file git
// reads. The attribute side is unbounded — arbitrary driver names, several
// layers, one of them unoverridable — but the definition side is not. `-c`
// outranks local config, and swapping GIT_DIR replaces which config *is* local.
// With a scratch GIT_DIR the repository's config is never read, so no driver can
// be defined, so no attribute can select one. It closes the whole class rather
// than the instances, and it does not depend on the git version — which
// GIT_ATTR_SOURCE does (2.40+).
//
// The objects still land in the user's repository via GIT_OBJECT_DIRECTORY, which
// is what keeps a snapshot recoverable with ordinary git. Only these two commands
// use it: commit-tree and update-ref need the real refs, and hooks there are
// already neutralised by githard.
//
// One behavior change worth knowing: `.git/info/exclude` is a gitdir-local file
// too, so it no longer filters what a snapshot captures. A locally-excluded path
// is now snapshotted. That errs toward capturing more of the user's work, which
// is the right direction for a safety net, and the size cap bounds the cost.
func (s *Snapshotter) contentEnv() []string {
	if s.contentDir == "" {
		return nil
	}
	return []string{
		"GIT_DIR=" + s.contentDir,
		"GIT_OBJECT_DIRECTORY=" + s.objectDir,
		"GIT_WORK_TREE=" + s.workspace,
	}
}

// prepareContentDir builds the scratch git directory, once per session. It must
// use the repository's own object format: a sha1 scratch against a sha256
// repository would compute object ids the repository cannot resolve.
//
// Returns false when it cannot be built, and the caller then snapshots the old
// way — degraded, but the alternative is no safety net at all.
func (s *Snapshotter) prepareContentDir(ctx context.Context) bool {
	objDir, err := run(ctx, s.workspace, nil, "rev-parse", "--git-path", "objects")
	if err != nil || objDir == "" {
		return false
	}
	if !filepath.IsAbs(objDir) {
		objDir = filepath.Join(s.workspace, objDir)
	}
	format, err := run(ctx, s.workspace, nil, "rev-parse", "--show-object-format")
	if err != nil || format == "" {
		format = "sha1"
	}
	dir := filepath.Join(filepath.Dir(s.indexFile), "content.git")
	if err := os.RemoveAll(dir); err != nil {
		return false
	}
	if _, err := run(ctx, filepath.Dir(s.indexFile), nil,
		"init", "-q", "--bare", "--object-format="+format, dir); err != nil {
		return false
	}
	s.contentDir, s.objectDir = dir, objDir
	return true
}

// LastCommit is the snapshot this run last wrote, or "" if it never wrote one.
//
// Exposed so a caller can use a snapshot as a *before-image* rather than only as
// a crash net: taken at launch and compared against the workspace later, it is
// the difference between "what is uncommitted here" and "what this run changed",
// which are not the same question in a checkout the user also works in.
//
// Read under the same lock the snapshot loop writes it with, because a caller
// asking mid-run is asking while that loop is running.
func (s *Snapshotter) LastCommit() string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastCommit
}

// TreeOf writes a tree object for a workspace as it stands right now, and
// returns its id. No ref, no commit, no session, no manifest — this is a *read*
// of the working tree, not a safety net.
//
// It exists so a caller holding a snapshot commit can ask what has changed since
// it. Comparing that commit to the working tree directly does not answer the
// question: a snapshot is written with `add -A` and therefore holds untracked
// files, while `git diff <commit>` only considers files git tracks — so every
// untracked file in the snapshot reads as a deletion. Two trees built the same
// way compare correctly.
//
// The safety argument is the snapshotter's, unchanged and for the same reasons:
// a private GIT_INDEX_FILE, so the user's index, HEAD, branches and working tree
// are never written; a scratch git directory for reading content, so the
// repository's own config cannot define a filter driver; and every git call
// through run(), which carries the githard flags. Adding a call here that
// bypasses run() reopens hostile_repo_test.go.
func TreeOf(workspace string) (string, error) {
	if !Available() {
		return "", fmt.Errorf("git is not available")
	}
	repoRoot, err := MainRepoRoot(workspace)
	if err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(context.Background(), snapshotTimeout)
	defer cancel()

	// Scratch everything: this leaves nothing behind but the objects it wrote,
	// which are unreferenced and age out with the repository's own gc.
	//
	// The parent is created first. MkdirTemp does not, and the rescue directory
	// for a repository need not exist yet — a caller that has never taken a
	// snapshot here is the ordinary case, not an error.
	parent := indexDir(repoRoot, "tree")
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return "", err
	}
	scratch, err := os.MkdirTemp(parent, "read-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(scratch)

	s := &Snapshotter{workspace: workspace, repoRoot: repoRoot, indexFile: filepath.Join(scratch, "index")}
	if !s.prepareContentDir(ctx) {
		return "", fmt.Errorf("preparing a scratch git directory")
	}

	env := append([]string{"GIT_INDEX_FILE=" + s.indexFile}, snapshotIdentity...)
	readEnv := append(append([]string{}, env...), s.contentEnv()...)

	addArgs := []string{"add", "-A"}
	if specFile := s.oversizedExcludes(ctx, readEnv); specFile != "" {
		addArgs = append(addArgs, "--pathspec-from-file="+specFile, "--pathspec-file-nul")
	}
	if _, err := run(ctx, workspace, readEnv, addArgs...); err != nil {
		return "", err
	}
	return run(ctx, workspace, readEnv, "write-tree")
}
