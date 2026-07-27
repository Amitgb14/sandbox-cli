package rescue

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Amitgb14/sandbox-cli/internal/githard"
)

// Snapshot is one recoverable point: a session plus the commit its ref actually
// resolves to right now.
type Snapshot struct {
	Session
	Commit    string // "" when nothing was ever captured
	Reachable bool   // the objects are still in the repository
}

// List returns the snapshot sessions for the repository containing dir, newest
// first. With all, it returns them for every repository sandbox-cli has
// snapshotted — the escape hatch for "I don't remember which project it was".
func List(dir string, all bool) ([]Snapshot, error) {
	if all {
		sessions, err := AllSessions()
		if err != nil {
			return nil, err
		}
		return resolve(sessions), nil
	}
	repoRoot, err := MainRepoRoot(dir)
	if err != nil {
		return nil, err
	}
	sessions, err := Sessions(repoRoot)
	if err != nil {
		return nil, err
	}
	return resolve(sessions), nil
}

// resolve turns manifests into snapshots by asking each repository what its ref
// points at now, falling back to the sha the manifest recorded when the ref is
// gone (pruned, or deleted by hand) but the objects survive.
func resolve(sessions []Session) []Snapshot {
	ctx := context.Background()
	out := make([]Snapshot, 0, len(sessions))
	for _, s := range sessions {
		snap := Snapshot{Session: s}
		if sha, err := run(ctx, s.Repo, nil, "rev-parse", "--verify", "--quiet", s.Ref); err == nil && sha != "" {
			snap.Commit, snap.Reachable = sha, true
		} else if s.LastSnapshot != "" {
			snap.Commit = s.LastSnapshot
			snap.Reachable = objectExists(ctx, s.Repo, s.LastSnapshot)
		}
		out = append(out, snap)
	}
	return out
}

// Find resolves a session id (or unambiguous prefix) for the repository
// containing dir.
func Find(dir, id string) (Snapshot, error) {
	repoRoot, err := MainRepoRoot(dir)
	if err != nil {
		return Snapshot{}, err
	}
	sess, err := findSession(repoRoot, id)
	if err != nil {
		return Snapshot{}, err
	}
	snaps := resolve([]Session{sess})
	snap := snaps[0]
	if snap.Commit == "" {
		return snap, fmt.Errorf("session %s captured no snapshot (the run had no changes, or died before the first one)", sess.ID)
	}
	if !snap.Reachable {
		return snap, fmt.Errorf("session %s snapshot %s is no longer in the repository (garbage collected after its ref was deleted)", sess.ID, short(snap.Commit))
	}
	return snap, nil
}

// Show streams `git show --stat` for a snapshot, so the user can see what is in
// it before deciding to restore.
func Show(dir, id string, patch bool) error {
	snap, err := Find(dir, id)
	if err != nil {
		return err
	}
	// githard.NoExternalDiff(): `show -p` renders content through diff.<x>.textconv /
	// diff.<x>.command when .gitattributes selects one, and both are commands from
	// the repository the agent was working in — which is exactly the repository a
	// user inspects *after* a run they already distrust.
	args := append([]string{"show", "--stat"}, githard.NoExternalDiff()...)
	if patch {
		args = append([]string{"show"}, githard.NoExternalDiff()...)
	}
	return stream(snap.Repo, append(args, snap.Commit)...)
}

// RestoreMode selects what Restore does with a snapshot.
type RestoreMode int

const (
	// RestoreBranch points a new branch at the snapshot. The default, because it
	// is the only mode that cannot destroy anything: nothing in the working tree
	// or on any existing branch changes.
	RestoreBranch RestoreMode = iota
	// RestorePatch writes the snapshot out as a patch.
	RestorePatch
	// RestoreWorktree puts the files back where they were.
	RestoreWorktree
)

// RestoreOptions configures Restore.
type RestoreOptions struct {
	Mode   RestoreMode
	Branch string // RestoreBranch: override the generated name
	Out    string // RestorePatch: file to write, or "-" for stdout
}

// RestoreResult describes what happened, so the caller can print the exact next
// command for the user.
type RestoreResult struct {
	Snapshot Snapshot
	Branch   string
	Patch    string
	Files    int

	// MatchesWorkingTree reports that the files on disk are already what the
	// snapshot holds, so nothing was actually rescued.
	//
	// This is the common case after a crash and it deserves saying out loud.
	// /workspace is a bind mount, so everything the agent wrote is on the host's
	// disk the instant it is written — the snapshots are the belt, not the
	// braces. A user who is told only "created branch X" reasonably concludes
	// that the branch is where their work now lives, and goes looking there for
	// something that was never missing, while the thing that *is* gone after a
	// kill — the conversation — goes unmentioned.
	MatchesWorkingTree bool
}

// Restore brings a snapshot back. The default mode never touches the working
// tree: after a crash the files on disk may themselves be the newest copy of the
// user's work, and clobbering them to "recover" would be the worst possible
// outcome.
func Restore(dir, id string, opts RestoreOptions) (RestoreResult, error) {
	snap, err := Find(dir, id)
	if err != nil {
		return RestoreResult{}, err
	}
	res := RestoreResult{Snapshot: snap}

	switch opts.Mode {
	case RestorePatch:
		patch, err := diff(snap)
		if err != nil {
			return res, err
		}
		if strings.TrimSpace(patch) == "" {
			return res, fmt.Errorf("snapshot %s is identical to the commit the run started from; there is nothing to restore", short(snap.Commit))
		}
		if opts.Out == "" || opts.Out == "-" {
			fmt.Print(patch)
			res.Patch = "-"
			return res, nil
		}
		if err := os.WriteFile(opts.Out, []byte(patch), 0o600); err != nil {
			return res, fmt.Errorf("writing patch: %w", err)
		}
		res.Patch = opts.Out
		return res, nil

	case RestoreWorktree:
		// Refuse on a dirty tree rather than offering a --force. The one thing
		// worse than not recovering the work is overwriting a newer copy of it.
		status, err := run(context.Background(), dir, nil, "status", "--porcelain")
		if err != nil {
			return res, err
		}
		if strings.TrimSpace(status) != "" {
			return res, fmt.Errorf("%s has uncommitted changes; commit or stash them first, "+
				"or restore onto a branch instead (sandbox-cli recover restore %s)", dir, id)
		}
		// read-tree -m -u updates the index and the working tree to the snapshot
		// (including files the agent deleted) without moving HEAD, so the recovered
		// work shows up as ordinary staged changes the user can review and commit.
		if _, err := run(context.Background(), dir, nil, "read-tree", "-m", "-u", snap.Commit); err != nil {
			return res, err
		}
		res.Files = countFiles(snap)
		return res, nil

	default:
		name := opts.Branch
		if name == "" {
			branch := snap.Branch
			if branch == "" {
				branch = "detached"
			}
			name = RestoreBranchPrefix + sanitizeRef(branch) + "-" + snap.ID
		}
		if _, err := run(context.Background(), snap.Repo, nil, "show-ref", "--verify", "--quiet", "refs/heads/"+name); err == nil {
			return res, fmt.Errorf("branch %q already exists; pass --branch NAME to choose another", name)
		}
		if _, err := run(context.Background(), snap.Repo, nil, "branch", name, snap.Commit); err != nil {
			return res, err
		}
		res.Branch = name
		res.Files = countFiles(snap)
		res.MatchesWorkingTree = matchesWorkingTree(dir, snap)
		return res, nil
	}
}

// diff renders the snapshot against the commit the run started from, which is
// the answer to "what did that run change?". It falls back to the snapshot's
// first parent when the starting commit is unknown or gone.
func diff(snap Snapshot) (string, error) {
	ctx := context.Background()
	base := snap.HeadAtStart
	if base == "" || !objectExists(ctx, snap.Repo, base) {
		base = snap.Commit + "^"
		if _, err := run(ctx, snap.Repo, nil, "rev-parse", "--verify", "--quiet", base); err != nil {
			// A snapshot with no parent at all — the run started in a repository
			// with no commits yet. `show` on a root commit renders every file as an
			// addition, which is exactly the patch wanted here.
			return runRaw(ctx, snap.Repo, nil, append([]string{"show", "--format="}, append(githard.NoExternalDiff(), snap.Commit)...)...)
		}
	}
	return runRaw(ctx, snap.Repo, nil, append([]string{"diff"}, append(githard.NoExternalDiff(), base, snap.Commit)...)...)
}

// countFiles reports how many files the snapshot changed relative to the run's
// starting commit; display only, so any failure just yields 0.
// matchesWorkingTree reports whether the files in dir are already identical to
// the snapshot.
//
// It builds a tree from the working tree and compares object ids, rather than
// running `git diff <commit>`. The obvious diff is wrong for the case that
// matters most: a snapshot captures untracked files, and `git diff` compares
// through the index, so every untracked file in the snapshot is reported as a
// deletion — the answer would be "different" precisely when an agent's new file
// had survived on disk, which is the common outcome after a crash.
//
// The tree is built the way the snapshotter builds one: a scratch GIT_DIR, so
// the repository's own config is never read and no filter driver can be defined,
// let alone selected. This runs `add -A` over a directory the agent controlled,
// which is the exact operation that executed host commands before that
// protection existed.
//
// Objects go to a throwaway store, not the user's repository. A tree id is a
// hash of content, so it compares identically wherever it was written, and a
// question the user asked in passing should not add objects to their repo.
//
// Uncertainty answers false: claiming "you already have this" wrongly would talk
// someone out of looking at a branch that does hold something.
func matchesWorkingTree(dir string, snap Snapshot) bool {
	if snap.Commit == "" || dir == "" {
		return false
	}
	ctx := context.Background()
	want, err := run(ctx, snap.Repo, nil, "rev-parse", "--verify", snap.Commit+"^{tree}")
	if err != nil || want == "" {
		return false
	}

	scratch, err := os.MkdirTemp("", "sandbox-match-")
	if err != nil {
		return false
	}
	defer os.RemoveAll(scratch)

	format, err := run(ctx, dir, nil, "rev-parse", "--show-object-format")
	if err != nil || format == "" {
		format = "sha1"
	}
	gitDir := filepath.Join(scratch, "content.git")
	if _, err := run(ctx, scratch, nil, "init", "-q", "--bare", "--object-format="+format, gitDir); err != nil {
		return false
	}
	env := []string{
		"GIT_DIR=" + gitDir,
		"GIT_OBJECT_DIRECTORY=" + filepath.Join(gitDir, "objects"),
		"GIT_WORK_TREE=" + dir,
	}
	if _, err := run(ctx, dir, env, "add", "-A"); err != nil {
		return false
	}
	got, err := run(ctx, dir, env, "write-tree")
	if err != nil || got == "" {
		return false
	}
	return got == want
}

func countFiles(snap Snapshot) int {
	ctx := context.Background()
	base := snap.HeadAtStart
	if base == "" || !objectExists(ctx, snap.Repo, base) {
		return 0
	}
	out, err := run(ctx, snap.Repo, nil, append([]string{"diff", "--name-only"}, append(githard.NoExternalDiff(), base, snap.Commit)...)...)
	if err != nil || strings.TrimSpace(out) == "" {
		return 0
	}
	return len(strings.Split(strings.TrimSpace(out), "\n"))
}

func short(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}
