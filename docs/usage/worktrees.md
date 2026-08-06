# One agent per branch (git worktrees)

`--worktree BRANCH` runs the sandbox in a dedicated git worktree for `BRANCH`
instead of your working copy, so you can run several agents at once — each on its
own branch, in its own container, with no collisions:

```sh
sandbox-cli claude --worktree feature-a -- -p "implement A"
sandbox-cli claude --worktree feature-b -- -p "implement B"   # in parallel
```

The worktree is created from the current HEAD if the branch doesn't exist, and
lives in a sandbox-owned directory so your project folder stays clean:

```
~/.config/sandbox/worktrees/<repo>-<hash>/<branch>
```

The `<hash>` disambiguates same-named repos in different locations. That worktree
path — not your working copy — becomes `/workspace` inside the container, so the
agent only ever sees its own branch. Your checkout is untouched and stays on
whatever branch you had.

- [The full cycle](#the-full-cycle)
- [Running them in the background](#running-them-in-the-background)
- [Commands](#commands)
- [Things worth knowing](#things-worth-knowing)

## The full cycle

Because these are real `git worktree` entries, the branch shows up in your repo
immediately — everything below runs from your normal checkout:

```sh
# 1. Run the agent on its own branch — --git lets it commit as you
sandbox-cli claude --worktree feature-a --git -- -p "implement A, then commit"

# 2. See what it did (the branch is already in your repo)
git log feature-a
git diff main...feature-a

# 3. Commit anything it left behind — no cd required
sandbox-cli worktree git feature-a status
sandbox-cli worktree commit feature-a -m "implement A"

# 4. Merge it
git checkout main
git merge feature-a

# 5. Clean up
sandbox-cli worktree rm feature-a
```

**The agent can commit its own work.** git is fully usable inside a worktree
sandbox, so you can just tell Claude to commit as it goes and skip step 3 —
`git log feature-a` will already show its commits. Add `--git` so those commits
carry your name and email; without it git in the container has no identity and
the commit fails with *"Please tell me who you are"*.

Step 3 is the fallback for when the agent didn't commit — a `--worktree` run
tells you when there's anything left over.

Step 5 deletes the worktree directory, not the branch. Until you run it, `git
checkout feature-a` in your main copy fails with *"already checked out"* — that's
git protecting the worktree, not an error.

## Running them in the background

The runs above each hold a terminal. `--detach` starts the container in the
background and returns immediately, so one terminal can launch all of them:

```sh
# 1. Fan out — each on its own branch, all at once
sandbox-cli claude --worktree feature-a --detach --git -- -p "implement A, then commit"
sandbox-cli claude --worktree feature-b --detach --git -- -p "implement B, then commit"

# 2. See who is still working (state, exit code)
sandbox-cli list --all

# 3. Read what an agent did — by branch name
sandbox-cli logs feature-a --follow

# 4. Land the work — ordinary git, from your normal checkout
git log feature-a
git checkout main && git merge feature-a

# 5. Clean up both halves
sandbox-cli clean
sandbox-cli worktree rm feature-a
```

Every one of those sessions can also be stopped early (`sandbox-cli kill
feature-a`) or watched live (`sandbox-cli attach feature-a`) — see
[Sessions](sessions.md).

Three things to know before using `--detach`:

- **The agent must exit on its own.** A detached container has no terminal
  inside it, so an agent in its normal interactive mode draws a UI nobody can
  see and waits forever. Use the non-interactive form — `claude -p "…"`,
  `codex exec "…"`, `droid exec "…"` — or a plain command like `npm test`.
- **The container is kept after it exits**, unlike every other sandbox run. Its
  exit code and logs are the only record that the work happened, so `--rm` would
  delete exactly what you came back for. `sandbox-cli clean` is how it gets reaped.
- **One agent per branch is enforced by construction.** The container is named
  `sandbox-<repo>-<branch>`, and docker refuses a duplicate name — a second
  detached run on a busy branch fails instead of putting two agents in one
  checkout. If the first finished, `sandbox-cli clean` frees the name.

The container name is printed on stdout by itself, so it is scriptable:

```sh
NAME=$(sandbox-cli claude --worktree feature-a --detach -- -p "implement A")
docker wait "$NAME"    # blocks until it finishes, prints the exit code
sandbox-cli logs "$NAME"
```

Isolation is identical to a foreground run — same mounts, same fake HOME, same
hardening.

## Commands

```sh
sandbox-cli worktree list                    # branch -> path
sandbox-cli worktree path BRANCH             # just the path, for scripts
sandbox-cli worktree git BRANCH <git args>   # run git in there, by branch name
sandbox-cli worktree commit BRANCH -m MSG    # stage everything and commit
sandbox-cli worktree rm BRANCH               # remove when you're done
```

**You never have to `cd` into that directory** — the worktree is addressable by
branch name. `worktree commit` stages everything (including untracked files) and
commits it; `worktree git` forwards anything after the branch name straight to
git, output and exit code included, so it scripts cleanly and your git config,
hooks and commit signing all still apply.

A run tells you when there's uncommitted work, so it doesn't surface days later
as a confusing `worktree rm` refusal:

```
sandbox-cli: worktree "feature-a" has uncommitted changes:
  src/api.ts
  README.md
  Commit with: sandbox-cli worktree commit feature-a -m "..."
```

`worktree rm` removes the worktree directory, not the branch — your commits
survive. It refuses if the worktree has modified or untracked files, since that
work exists nowhere else; commit or copy it first, or `--force` to discard it:

```sh
sandbox-cli worktree rm --force BRANCH   # permanent
```

## Things worth knowing

**How the agent can commit.** A worktree's `.git` is a pointer file holding an
absolute path into the parent repo, which is outside the workspace — so
sandbox-cli also mounts the parent repo's `.git` directory at that same path.
Without it every git command in the container fails with `not a git repository`
and the agent can edit files but never commit them. This is a third host path
reaching outside `/workspace`, and it is read-write: an agent in a worktree
sandbox can write to your repository's object store and refs (its own branch, but
also others). It applies whenever the workspace is a worktree, including running
`sandbox-cli` from one directly without `--worktree`.

- **Untracked files don't come along.** A worktree starts from a committed tree,
  so anything in `.gitignore` or not yet committed (a local `.env`, `node_modules`)
  won't be there. Mount what's needed with `--mount`, or let the agent reinstall.
- **The branch is checked out in the worktree**, so you can't `git checkout` the
  same branch in your main copy while it exists. Use `worktree rm` first.
- **One container per worktree.** Parallel runs on the *same* branch would collide;
  give each agent its own.
- **Requires git** — it's the only feature that does.

---

Next: [Many agents at once (`fleet`)](fleet.md) · [Sessions](sessions.md) ·
[documentation index](../README.md)
