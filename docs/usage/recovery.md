# Crash recovery

**A run just died — do this.** From your normal checkout (this works even when
the crash happened in a `--worktree` sandbox):

```sh
sandbox-cli recover          # 1. what's broken here, and what there is to recover
sandbox-cli recover repair   # 2. only if step 1 reported a problem — fixes git
git status                   # 3. often everything is already back; if so, stop here
sandbox-cli recover restore <id>   # 4. otherwise put the snapshot on a branch
git diff HEAD sandbox-recover/…    # 5. review, then merge or cherry-pick
```

Then commit the work as usual — the snapshot deletes itself once you have.
Nothing above overwrites a file, so it is safe to run before you have decided
anything. The rest of this page explains what it is doing and why.

## Why a snapshot exists at all

A sandbox writes straight through to your real files: the project — and, in a
`--worktree` run, the parent `.git` — are bind-mounted at their host paths. When
something kills the run mid-write, the files survive but git sometimes does not.
The two ways that hurts:

- **git stops working entirely.** A worktree's `.git` is a pointer into
  `<repo>/.git/worktrees/<name>`. If that directory is deleted — a `git worktree
  prune`, or the one `git gc` runs for itself, reaching out through the
  read-write mount — every command answers `fatal: not a git repository`, even
  though all your files are still sitting there.
- **the agent throws its own work away.** One `git reset --hard` and the commits
  it just made are on no branch at all.

So sandbox-cli keeps a running copy. While a sandbox is up, the workspace is
committed every couple of minutes into your repository's own object store under
`refs/sandbox/snapshots/`, plus once at the start and once on the way out
(including on Ctrl-C). Snapshots are written with a private index file, so your
index, `HEAD`, branches and working tree are never touched — nothing appears in
`git branch`, nothing is pushed, and `git status` looks exactly as it did.

## The commands

```sh
sandbox-cli recover                  # what's broken here, and what there is to recover
sandbox-cli recover list             # every recorded run for this repo, newest first
sandbox-cli recover show ID          # what's in a snapshot
sandbox-cli recover restore ID       # put it back, on a branch
sandbox-cli recover repair           # fix a repository a crashed sandbox broke
```

```
$ sandbox-cli recover list
SESSION                 BRANCH     WHEN      SNAPSHOTS  STATE
20260724-231800-08df8e  feature-a  4m ago    7          crashed

$ sandbox-cli recover restore 20260724-231800
sandbox-cli: created branch "sandbox-recover/feature-a-20260724-231800-08df8e" from branch feature-a (3 file(s) changed)
  Look at it:  git diff HEAD sandbox-recover/feature-a-20260724-231800-08df8e
  Work on it:  git switch sandbox-recover/feature-a-20260724-231800-08df8e
```

`restore` creates a branch and changes nothing else — after a crash the files on
disk may themselves be the newest copy of your work, so nothing overwrites them
unless you ask (`--into-worktree`, refused unless the tree is clean, or `--patch`
to write the changes out as a patch instead).

Run it from your normal checkout even when the crash happened in a `--worktree`
sandbox: worktrees share the repository the snapshots live in, so the work is
reachable from wherever you are standing.

## Snapshots clean themselves up

Once you commit the work for real, the snapshot holds a tree some branch already
holds, so it is deleted at the start of the next run in that repository — a
snapshot's normal end is being superseded, not timing out. It has to be an
*exact* tree match: commit half of what the agent left and the snapshot stays,
because it is still the only copy of the other half. Anything never committed is
pruned after 14 days, since a ref keeps its objects alive forever.

```sh
sandbox-cli recover prune                      # both, by hand
sandbox-cli recover prune --superseded=false   # only the 14-day expiry
```

`snapshot.retention` changes the window and `--no-snapshot` turns the whole thing
off. One limitation worth knowing: snapshots honour `.gitignore`, so ignored
paths are not captured — that is what keeps a `node_modules` from being committed
every two minutes.

Design and rejected alternatives:
[`docs/proposals/crash-recovery.md`](../proposals/crash-recovery.md).

---

Next: [Worktrees](worktrees.md) · [Sessions](sessions.md) ·
[documentation index](../README.md)
