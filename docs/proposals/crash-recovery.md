# Design: crash recovery

**Status:** implemented. `internal/rescue`, `sandbox-cli recover`.
**Depends on:** `--worktree` (shipped), the git worktree host-path mounts in `internal/cli/root.go`.

## Problem

A sandbox writes straight through to the user's real files. `/workspace` is a
read-write bind mount, and for a `--worktree` run the parent `.git` is a second
read-write bind at its own host path (`internal/cli/root.go:133-149`). There is
no overlay, no copy-on-write, nothing in between. That is the design — an agent
has to be able to edit and commit — and it means an abnormal end lands on the
host permanently.

The files themselves survive. What does not survive is git's ability to show them.

**1. git stops working entirely.** A linked worktree's `.git` is a pointer file
naming `<repo>/.git/worktrees/<name>`. That directory holds the per-checkout
`HEAD`, `index` and `commondir`. Delete it — a `git worktree prune`, or the one
`git gc` runs for itself, reaching out through the read-write `.git` mount — and
every command in the checkout answers:

```
fatal: not a git repository: /home/you/proj/.git/worktrees/feature-a
```

`git worktree repair` does not fix it: repair reconnects worktrees that *moved*,
and a deleted admin directory has nothing left to reconnect. The user's files are
all present and completely unreachable through git.

**2. The agent discards its own work.** One `git reset --hard` and the commits it
just made are on no branch. The reflog has them; nobody who needs this knows that.

**3. Nothing could act on the way out.** `execute` called
`sess.Run(context.Background(), …)` and the process had Go's default signal
disposition, so SIGINT/SIGTERM killed it without running a single deferred
function. `warnDirtyWorktree` — the one existing "your work is only in the
worktree" notice — never fired on interrupt, which is precisely when it mattered.

**4. There was no backup of any kind.** No snapshot, stash, reflog capture or
copy anywhere in the codebase.

Put together: after a crash the work is on disk, git cannot show it, and nothing
records which branch it belonged to.

## Design

Two halves. A safety net that runs during every sandbox, and a recovery command
for when something has already gone wrong.

### The safety net

While a run is in flight, commit the workspace into the repository's own object
store on a ref nobody else uses. One snapshot is four plumbing commands:

```
git add -A            # against a private GIT_INDEX_FILE
git write-tree        # unchanged tree -> stop here, no commit, no ref write
git commit-tree       # parents: previous snapshot, and HEAD when not already reachable
git update-ref        # compare-and-swap on refs/sandbox/snapshots/<session>
```

Every part of that shape is load-bearing:

| Choice | Why |
|---|---|
| Private `GIT_INDEX_FILE` under `~/.config/sandbox/rescue/` | The user's `.git/index` is never written. It also persists between snapshots, so its cached stat data makes each one incremental. |
| Kept *outside* the repository | The repository is often the broken thing. Also: with the index inside the repo, the user's later `git add -A` would sweep it into their commit. |
| `write-tree` compared to the previous tree | An idle agent costs one `add -A` per interval and nothing else — no commit, no ref write. |
| `HEAD` as an extra parent | This is what makes commits the agent later `reset --hard`s away stay reachable. Skipped when `HEAD` is already an ancestor, so the history stays readable. |
| `refs/sandbox/…`, not `refs/heads/…` | Invisible to `git branch` and `git log`, never pushed, but still a real ref — which is what keeps its objects safe from `gc`. |
| Compare-and-swap on `update-ref` | Two sandboxes on one repository can never overwrite each other. |
| Pinned identity, `-c gc.auto=0` | `commit-tree` fails outright with no `user.email` configured, which would turn a missing config into a lost snapshot. And a snapshot must never be what kicks off a repack in a repository an agent is working in. |
| Plumbing only | No hook runs. `add`, `write-tree`, `commit-tree` and `update-ref` do not trigger them. |

**The invariant, stated once:** rescue only ever *creates* objects and refs under
`refs/sandbox/`. It never writes `HEAD`, a branch, the repository index, or a
file in the working tree. `TestSnapshotLeavesTheRepositoryUntouched` pins all
four byte-for-byte.

Cadence is start, every 2 minutes, and once on the way out — which required
giving the run path signal handling it never had. On SIGINT/SIGTERM/SIGHUP:
restore the default disposition first (so a second Ctrl-C still kills
immediately), take a final snapshot, then exit `128+signal`. The container is
deliberately not killed: with a TTY, docker holds the terminal in raw mode so
Ctrl-C is a keystroke for the agent, not a signal, and when a signal does arrive
it already reached the whole process group.

Failure is never fatal. Each snapshot has a 30-second budget; three consecutive
failures print one line and disable snapshotting for the run. No git, no
repository, a bare repository, or `--no-snapshot` means no safety net and no
complaint — but an unwritable rescue directory says so, because a safety net that
quietly is not there is worse than none.

### The session manifest

`~/.config/sandbox/rescue/<repo>-<hash>/<session>.json` records repo, workspace,
branch, agent, start/end, `HeadAtStart`, and the last snapshot. It lives outside
every repository for the same reason the index does: after a crash, the
repository may be unreadable, and *"which branch was that work on?"* has to
survive that. It is what lets `recover repair` name the branch when the directory
that held that fact has been deleted.

The bucket key is the **main** repository — `git rev-parse --git-common-dir`,
which answers identically from the main checkout and from every linked worktree.
That is what makes a crash in a `--worktree` sandbox recoverable from the user's
normal checkout, which is where they are standing when they go looking.

### Recovery

```
sandbox-cli recover                  # diagnosis + what there is to recover
sandbox-cli recover list [--all]
sandbox-cli recover show ID [--patch]
sandbox-cli recover restore ID [--branch NAME | --patch [-o FILE] | --into-worktree]
sandbox-cli recover repair [--yes] [--branch NAME]
sandbox-cli recover prune [--older-than D] [--superseded]
```

`restore` defaults to creating a branch and changing nothing else. After a crash
the files on disk may themselves be the newest copy of the user's work, so
nothing overwrites them unless asked: `--into-worktree` uses `read-tree -m -u`
(which handles deletions and leaves the result staged for review) and is refused
outright on a dirty tree rather than offered with a `--force`.

`repair` rebuilds the three files a linked worktree needs — `HEAD`, `commondir`,
`gitdir` — then runs a plain `git reset` to rebuild the index. Never `--hard`:
the uncommitted work is the whole point. It never moves a ref either, because
another checkout may be sitting on it and moving it would silently dirty the
user's own working tree. Interrupted merges and rebases are reported and never
touched; only the user knows whether to finish or abort one.

### Retention

A ref pins its objects forever, so snapshots have to end. Two ways, both run
automatically at the start of the next run in that repository:

- **Superseded** — a commit reachable from a branch has the snapshot's *exact*
  tree. The content is in real history; the ref is pinning a duplicate. This is
  the normal end of a snapshot's life.
- **Expired** — no activity for 14 days.

"Superseded" is an exact tree match, never "the branch moved on". Commit half of
what the agent left and the trees differ, so the snapshot stays: it is still the
only copy of the other half. The search is bounded by `HeadAtStart` — anything
capturing this content must have been committed since — plus that commit itself,
which catches a run that changed nothing or changed and reverted.

A session that has not ended and was active in the last hour is never pruned, by
either rule: a running sandbox snapshots every couple of minutes, and deleting
its ref would break its next compare-and-swap.

## How each problem is answered

| Problem | Resolution |
|---|---|
| Worktree admin directory deleted | `recover repair` rebuilds it from the pointer file plus the branch recorded in the session manifest |
| Agent `reset --hard`s its own commits | The snapshot carries `HEAD` as a parent, so they stay reachable; `recover restore` puts them on a branch |
| Uncommitted work lost with the checkout | Captured every 2 minutes into the shared object store, recoverable from the main checkout |
| Nothing ran on interrupt | SIGINT/SIGTERM/SIGHUP handled: final snapshot, dirty-worktree warning, `128+signal` |
| Which branch was that? | Session manifest, kept outside the repository |
| Snapshots accumulating forever | Superseded + expired pruning at the start of the next run |

## Rejected

- **Snapshotting from inside the container.** It would depend on git being
  present in every agent image and would die with the container — exactly when it
  is needed. The host process outlives it.
- **Tarballs or bundles under `~/.config/sandbox`.** Full copies, no dedup, and a
  separate format to restore from. Git's object store already dedups by content
  and `restore` becomes `git branch`.
- **Committing to a real branch, or moving one.** Any ref the user can see is a
  ref the tool can surprise them with; a snapshot on `refs/heads/*` would show up
  in `git branch`, get pushed, and confuse every workflow it touched.
- **`git stash`.** Writes the repository index and the working tree, and its own
  ref is a stack that the user shares — the opposite of the invariant.
- **A `--force` for `--into-worktree`.** Overwriting a dirty tree during recovery
  can destroy the newest copy of the work. Refusing, with a pointer to the branch
  mode, loses nothing.
- **Capturing `.gitignore`d paths.** A `node_modules` snapshot every two minutes
  would cost more than the safety net saves. The consequence is real and
  documented: a gitignored `.env` the agent wrote is not recoverable this way.
- **Pruning only when every snapshot in a session's chain is superseded.** More
  conservative, but the intermediate snapshots it would protect are not surfaced
  by any command — `restore` gives you the ref's tip. Not worth the complexity.
- **Killing the container on Ctrl-C.** With a TTY, Ctrl-C never reaches
  sandbox-cli as a signal at all; stealing it would change how every agent behaves.

## Non-goals

- Recovering work in files `.gitignore` covers.
- Protecting anything outside `/workspace`.
- Snapshotting a workspace that is not a git repository — there is no object
  store to write into, and inventing one somewhere else is a different feature.
- Undoing what an agent did to the *host* outside the mounts. The sandbox already
  prevents that; rescue is about the one directory it deliberately exposes.

## Known consequences

- A run that changes something and then reverts it before exiting leaves a
  snapshot whose tree matches `HEAD`, so it is pruned as superseded at the next
  run. The intermediate attempt goes with it. That state was only reachable by
  walking the ref's history by hand.
- Snapshots are per-repository, so a sandbox whose workspace spans no repository
  gets nothing.
- The 2-minute cadence bounds the loss window; a hard kill can still lose up to
  one interval of work. `--snapshot-interval` tightens it.
