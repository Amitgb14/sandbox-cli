# sandbox-cli — User Guide

Run AI coding agents (Claude Code, Codex CLI) — or any command — inside a
disposable, isolated Docker container. Only your chosen project is visible to the
agent; everything else on your machine stays out of reach. This lets you hand an
agent "Allow All" / `--dangerously-skip-permissions` autonomy while keeping the
blast radius to one project.

- [Why use it](#why-use-it)
- [Requirements](#requirements)
- [Install](#install)
- [Quick start](#quick-start)
- [Everyday use](#everyday-use)
- [Features](#features)
- [Configuration](#configuration)
- [Command reference](#command-reference)
- [Troubleshooting](#troubleshooting)

---

## Why use it

When an agent can run shell commands, a bad `rm -rf`, a prompt-injection attack,
or a stray `cat ~/.aws/credentials` can reach your whole machine. sandbox-cli puts
the agent in a throwaway container where:

- only your **project folder** is mounted (`/workspace`); your home dir, SSH keys,
  and other projects are invisible;
- `HOME` is **fake and ephemeral** — wiped when the container exits (`--rm`);
- **nothing** from your host environment (API keys, tokens) is passed in unless
  you opt in;
- the container is **hardened** (dropped Linux capabilities, no privilege
  escalation, process-count cap) by default.

So you can let the agent go fast without babysitting every action.

---

## Requirements

- **Docker** installed and running (Docker Desktop on macOS/Windows, or a Docker
  daemon on Linux).
- **Go 1.25+** — only to build the CLI (not needed once you have the binary).
- Git — only if you use the `--worktree` feature.

The container image is built automatically on first run — you don't need to pull
or build anything by hand.

**Platform note:** the tool and nearly every feature work on macOS, Linux, and
Windows wherever Docker runs. The one exception is selecting a microVM/gVisor
runtime (`--runtime kata-runtime` / `runsc`), which needs **native Linux** —
Docker Desktop on macOS/Windows doesn't allow custom runtimes (it already runs
containers in its own Linux VM). See the
[Platform support table](../README.md#platform-support) for the full breakdown.

---

## Install

The one-line installer detects your OS and CPU, downloads the matching release
archive, verifies it against the release `checksums.txt`, and installs to
`~/.local/bin/sandbox-cli` — no root, no package manager:

```sh
curl -fsSL https://raw.githubusercontent.com/Amitgb14/sandbox-cli/main/install.sh | sh
```

If you already have Go, install straight from the module instead:

```sh
go install github.com/Amitgb14/sandbox-cli/cmd/sandbox-cli@latest
```

Or build from a clone (Go 1.25+):

```sh
git clone https://github.com/Amitgb14/sandbox-cli.git
cd sandbox-cli
make build        # -> ./bin/sandbox-cli
make install      # or onto your PATH: $(go env GOPATH)/bin/sandbox-cli
```

Verify:

```sh
sandbox-cli version
```

The first real run builds the base image (a few minutes, one time).

---

## Quick start

```sh
# 1. Go to a project you want the agent to work on
cd ~/code/my-app

# 2. Run Claude Code in the sandbox (logs you in the first time)
sandbox-cli claude

# ...or another agent
sandbox-cli codex
sandbox-cli gemini
sandbox-cli opencode

# 3. Or run any command in the sandbox
sandbox-cli run -- npm test
```

That's it. The agent sees your project at `/workspace` and nothing else.

> **Tip:** add `--dry-run` to any command to print the exact `docker` command it
> would run, without running it. Great for understanding or debugging.

---

## Everyday use

**Let the agent run unattended, safely:**

```sh
sandbox-cli claude --dangerously-skip-permissions
```

Because it's boxed in, "skip permissions" is far less scary — the agent still
can't touch anything outside the project.

**Work on a different project without `cd`:**

```sh
sandbox-cli claude --project ~/code/other-app
```

**Give the agent a helper folder (read-only by default):**

```sh
sandbox-cli run --mount ~/datasets:/workspace/data:ro -- python train.py
```

**Pass an API key in (only this one, only if it's set):**

```sh
sandbox-cli run --env-allow ANTHROPIC_API_KEY -- some-tool
```

**Run several agents at once, each on its own branch:** see
[Parallel agents](#parallel-agents-with-git-worktrees).

---

## Features

### Strong isolation by default
- Only `/workspace` (your project) is writable and connected to the host.
- `HOME`, `/etc`, `/` inside the container are ephemeral and destroyed on exit.
- Runs as a non-root user; your host home is **never** mounted (sandbox-cli
  refuses to mount `/`, your home, or any parent of it).

### Hardened container
Every run drops all Linux capabilities (`--cap-drop=ALL`), forbids privilege
escalation, and caps the process count to blunt fork bombs. Add resource limits
when you want them:

```sh
sandbox-cli run --memory 2g --cpus 1.5 -- npm run build
```

### Network egress allowlist
By default the container has normal network access. To lock it down so the agent
can reach package registries and the model API **but nothing else** (blocking
data exfiltration), use allowlist mode:

```sh
sandbox-cli claude --allow example.com
```

This default-denies outbound traffic and permits only a built-in baseline
(`api.anthropic.com`, `registry.npmjs.org`, `pypi.org`, `github.com`, …) plus any
domains you add — so `npm install` / `pip install` / `git` keep working. *(Needs
a Linux Docker host; resolves domains at startup.)*

### Persistent caches
`--rm` containers normally re-download dependencies every run. Turn on shared,
persistent caches for npm/pip/cargo/go:

```sh
sandbox-cli run --cache -- npm ci
```

### Credential broker
Pass secrets in **without** putting them on the command line, in a config file,
or in your shell history. The value is fetched at run time and forwarded by name:

```sh
# from a file, a command, or a host env var
sandbox-cli claude \
  --secret ANTHROPIC_API_KEY=file:~/.secrets/anthropic \
  --secret GITHUB_TOKEN=cmd:"gh auth token"
```

`cmd:` sources are great for short-lived tokens (`gh auth token`, `op read`,
`vault read`).

### Parallel agents with git worktrees

Normally the sandbox mounts your working copy at `/workspace`. Two agents running
at once would then edit the same files and fight over the same branch.
`--worktree BRANCH` solves that: each run gets its own git worktree, its own
branch, and its own container.

```sh
sandbox-cli claude --worktree feature-a -- -p "implement A"
sandbox-cli claude --worktree feature-b -- -p "implement B"   # in parallel
```

Run those in two terminals and they work simultaneously without touching each
other or your checkout.

**What actually happens.** For each run sandbox-cli resolves a worktree, creating
it from your current HEAD if the branch doesn't exist:

```
~/.config/sandbox/worktrees/<repo>-<hash>/<branch>
```

and mounts *that* directory at `/workspace` instead of your project. It prints
which one it used:

```
sandbox-cli: created worktree "feature-a" at /Users/you/.config/sandbox/worktrees/myapp-f379c0cd/feature-a
```

Your own checkout never changes branch and never gets modified.

**Getting the work back.** These are real `git worktree` entries, so the branch is
visible in your repo the moment it's created — no fetching or copying. The whole
cycle runs from your normal checkout:

```sh
# 1. Run the agent on its own branch
sandbox-cli claude --worktree feature-a -- -p "implement A"

# 2. See what it did
git log feature-a
git diff main...feature-a

# 3. Commit anything it left uncommitted (skip if it committed its own work)
sandbox-cli worktree git feature-a status
sandbox-cli worktree commit feature-a -m "implement A"

# 4. Merge
git checkout main
git merge feature-a

# 5. Clean up
sandbox-cli worktree rm feature-a
```

Step 4 is ordinary git — nothing sandbox-specific. If the merge conflicts, resolve
it exactly as you would for any branch.

**The commands:**

```sh
sandbox-cli worktree list                    # branch -> path
sandbox-cli worktree path BRANCH             # just the path, for scripts
sandbox-cli worktree git BRANCH <git args>   # run git in there, by branch name
sandbox-cli worktree commit BRANCH -m MSG    # stage everything and commit
sandbox-cli worktree rm BRANCH               # remove when you're done
```

**You don't need to go into the worktree directory.** Committed work is already
on the branch, and anything the agent left *uncommitted* can be handled by branch
name from your own checkout:

```sh
sandbox-cli worktree git feature-a status
sandbox-cli worktree git feature-a diff
sandbox-cli worktree commit feature-a -m "implement A"
```

`worktree commit` stages everything and commits — after that, `git log feature-a`
and `git merge feature-a` work as usual. `worktree git` forwards anything after
the branch name to git, so `add -p`, `restore`, `push` and friends all work too.
If you *do* want a shell in there, `cd "$(sandbox-cli worktree path feature-a)"`.

Both are scriptable: git's output and its **exit code** pass straight through, so
`sandbox-cli worktree git b rev-parse --verify X` fails with git's own 128 rather
than a flattened 1. They run your real git, so your config, hooks, credential
helpers and commit signing all apply. Put `--` before any flag you want git to
receive that sandbox-cli might otherwise read as its own:

```sh
sandbox-cli worktree git feature-a -- log --oneline -5
```

A run warns you when there's uncommitted work, rather than letting you find out
days later:

```
sandbox-cli: worktree "feature-a" has uncommitted changes:
  src/api.ts
  README.md
  Commit with: sandbox-cli worktree commit feature-a -m "..."
```

`rm` deletes the worktree directory only — the branch and its commits stay in
your repo. If the worktree has uncommitted work it refuses:

```
worktree for branch "feature-a" has uncommitted work at
  /Users/you/.config/sandbox/worktrees/myapp-f379c0cd/feature-a
Commit it first:  sandbox-cli worktree commit feature-a -m "..."
Or discard it:    sandbox-cli worktree rm --force feature-a
```

That work exists in exactly one place, so commit it before reaching for
`--force` — the flag deletes it permanently.

**Committing from inside the sandbox works.** A worktree's `.git` is a pointer
file to a path inside the parent repo, which isn't part of the workspace — so
sandbox-cli mounts the parent repo's `.git` at that same path. Note this is
read-write and reaches outside `/workspace`: an agent in a worktree sandbox can
write to your repo's object store and refs. If you'd rather it couldn't, don't
use `--worktree`; run the agent in a normal checkout instead.

**Gotchas:**

- Untracked and ignored files aren't in a worktree (it starts from a committed
  tree). A local `.env` or `node_modules` won't be there — `--mount` it, or let
  the agent reinstall.
- While a worktree exists, git won't let you check that branch out in your main
  copy (`fatal: 'feature-a' is already checked out at ...`). Run
  `sandbox-cli worktree rm feature-a` first.
- Don't run two agents on the *same* branch — they'd share one worktree and
  collide. One branch per agent.
- Commit before you start: an agent can only build on what's in HEAD.

### Crash recovery

Everything an agent does lands directly on your disk — that is the point of the
bind mount — so when a run dies mid-write, the damage lands there too. Two ways
it bites:

- **git stops working.** A worktree's `.git` is a pointer into
  `<repo>/.git/worktrees/<name>`. Delete that directory (a `git worktree prune`,
  or the one `git gc` runs for itself, reaching out through the read-write
  mount) and every command answers `fatal: not a git repository`, with all your
  files still sitting right there.
- **the agent discards its own work.** One `git reset --hard` and the commits it
  just made are on no branch at all.

So sandbox-cli keeps a running copy. While a sandbox is up, the workspace is
committed into your repository's own object store under
`refs/sandbox/snapshots/` — at the start, every two minutes, and on the way out,
including on Ctrl-C. Each snapshot is written against a private index file, so
your index, `HEAD`, branches and working tree are never written to: nothing shows
in `git branch`, nothing is pushed, `git status` is unchanged.

```sh
sandbox-cli recover                  # what's broken here, and what there is to recover
sandbox-cli recover list             # every recorded run for this repo, newest first
sandbox-cli recover show ID          # what's in a snapshot
sandbox-cli recover restore ID       # put it back, on a branch
sandbox-cli recover repair           # fix a repository a crashed sandbox broke
```

`restore` creates a branch and touches nothing else. After a crash the files on
disk may themselves be the newest copy of your work, so nothing overwrites them
unless you ask: `--into-worktree` puts the files back in place (refused unless
the tree is clean) and `--patch` writes the changes out as a patch instead.

Run it from your normal checkout even when the crash happened in a `--worktree`
sandbox — worktrees share the repository the snapshots live in.

`repair` handles the other half. It rebuilds a deleted worktree administrative
directory and clears locks a killed git left behind, then rebuilds the index from
`HEAD` — never `--hard`, and never moving a ref, since the uncommitted work is
the whole point. An interrupted merge or rebase is reported but never touched:
only you know whether to finish it or abort it.

#### After a crash, step by step

Work from your normal checkout. Everything up to step 5 is read-only or additive,
so you can run it before deciding anything.

**1. Change nothing yet.** Your files are still on disk — that is the usual case,
not the exception. The one way to make this worse is to overwrite them while
trying to recover them, so resist `git checkout .`, `git reset --hard`, and
deleting the worktree until you know what you have.

**2. Ask what happened.**

```sh
sandbox-cli recover
```

It prints two things: what is broken in the repository here, and every run it has
a record of. A run marked `crashed` is one that nothing closed.

**3. If it reported a problem, fix it.**

```sh
sandbox-cli recover repair        # add --yes to skip the confirmations
git status
```

This is the whole fix for the most common failure — a worktree whose
administrative directory was deleted, where `git status` answered `fatal: not a
git repository`. Your commits and your uncommitted edits come back untouched.
**Very often you are done here**, because nothing was ever lost; git had merely
been made unable to see it.

**4. If work is actually missing, restore a snapshot.** That means the agent
reset it away, or the worktree directory itself is gone. Find it and look before
you take it:

```sh
sandbox-cli recover list                  # note the SESSION id
sandbox-cli recover show 20260724-2318    # any unambiguous prefix works
```

```sh
sandbox-cli recover restore 20260724-2318
```

That creates `sandbox-recover/<branch>-<id>` and changes nothing else — not your
current branch, not your working tree, not any existing branch.

**5. Review it, then take what you want.**

```sh
git diff HEAD sandbox-recover/feature-a-20260724-231800-08df8e   # what's in it
git switch sandbox-recover/feature-a-20260724-231800-08df8e      # work on it directly
# or, from your own branch:
git cherry-pick <commit>          # a commit the agent made
git checkout sandbox-recover/… -- path/to/file.go   # one file
```

**6. Commit the work.** Once a branch holds it, the snapshot is a duplicate and
is deleted automatically at the start of your next run in that repository.

**7. Clean up the recovery branch** when you no longer need it:

```sh
git branch -D sandbox-recover/feature-a-20260724-231800-08df8e
```

Two variations on step 4, when a branch is not what you want:

```sh
sandbox-cli recover restore <id> --patch -o crash.patch   # then: git apply crash.patch
sandbox-cli recover restore <id> --into-worktree          # files straight back in place
```

`--into-worktree` is refused unless your working tree is clean, for the reason in
step 1. It leaves the restored files staged, so `git status` shows you exactly
what came back before you commit.

**If `recover list` is empty**, there is no snapshot to restore — the run
predates this feature, ran with `--no-snapshot`, worked in a directory that is not
a git repository, or died before its first snapshot. Your files on disk are still
the source of truth; step 3 is what gets git looking at them again.

Snapshots clean themselves up. Once you commit the work for real, the snapshot
holds a tree some branch already holds, and it is deleted at the start of the
next run in that repository — being superseded, not timing out, is a snapshot's
normal end. The test is an *exact* tree match, never "the branch moved on":
commit half of what the agent left and the snapshot stays, because it is still
the only copy of the other half.

```sh
sandbox-cli recover prune                      # both kinds, by hand
sandbox-cli recover prune --superseded=false   # only the 14-day expiry
```

**Gotchas:**

- Snapshots honour `.gitignore`. Ignored paths are not captured — which is what
  stops `node_modules` from being committed every two minutes, and means a
  gitignored `.env` the agent wrote is not recoverable this way.
- Work you never commit is pruned after 14 days (`snapshot.retention`), because a
  ref keeps its objects alive forever.
- A sandbox that is still running is never pruned, so its next snapshot can't be
  broken by housekeeping in another terminal.
- It needs a git repository. A sandbox on a plain directory gets no safety net.
- **`--detach` runs are not snapshotted.** sandbox-cli exits as soon as the
  container is up, so no host process is left to take them. The container still
  has your project mounted and `sandbox-cli recover repair` still works
  afterwards — there is just nothing periodic behind it. Commit from inside a
  detached run, or use `sandbox-cli worktree commit BRANCH` when it finishes.
- `--no-snapshot` turns it off for a run.
### Running an agent in the background

`--worktree` gives each agent its own branch, but every run still holds a
terminal. `--detach` starts the container in the background and returns
immediately, so one terminal can launch several:

```sh
sandbox-cli claude --worktree feature-a --detach -- -p "implement A"
sandbox-cli claude --worktree feature-b --detach -- -p "implement B"
```

```
sandbox-cli: started sandbox-myapp-f379c0cd-feature-a in the background
  logs:  docker logs -f sandbox-myapp-f379c0cd-feature-a
  stop:  docker stop sandbox-myapp-f379c0cd-feature-a
  note:  nothing is attached — the agent must be in a mode that exits on its own
```

The container name goes to stdout on its own line, so `NAME=$(sandbox-cli claude
--worktree feature-a --detach -- -p "…")` works in a script.

**Step by step.** The whole cycle, from launching two agents to landing their
work, runs from your normal checkout:

```sh
# 1. Log in once per agent, if you haven't. A detached run cannot answer a login
#    prompt, so do this interactively first — it persists across runs.
sandbox-cli claude

# 2. Fan out. --git so the agent's commits carry your name and email.
sandbox-cli claude --worktree feature-a --detach --git -- -p "implement A, then commit"
sandbox-cli claude --worktree feature-b --detach --git -- -p "implement B, then commit"

# 3. Check on them. Running, exited, and with what exit code.
docker ps -a --filter label=sandbox.repo --format \
  'table {{.Names}}\t{{.Status}}\t{{.Label "sandbox.branch"}}'

# 4. Read the transcript of one.
docker logs sandbox-myapp-f379c0cd-feature-a          # add -f to follow live

# 5. Stop one early if it is going wrong.
docker stop sandbox-myapp-f379c0cd-feature-a

# 6. Review the work. The branch is already in your repo.
git log feature-a
git diff main...feature-a

# 7. Commit anything the agent left uncommitted (skip if it committed its own).
sandbox-cli worktree git feature-a status
sandbox-cli worktree commit feature-a -m "implement A"

# 8. Merge.
git checkout main
git merge feature-a

# 9. Reap both halves: the container, then the worktree.
docker rm sandbox-myapp-f379c0cd-feature-a
sandbox-cli worktree rm feature-a
```

Steps 6–8 are the same as for a foreground `--worktree` run — detaching changes
how the agent is launched, not how its work comes back. Step 9 has the one extra
piece: a detached container is not removed on exit, so it stays until you say so.

**Try it without launching anything.** `--dry-run` prints the exact docker
command, including `-d`, the labels and the absence of `--rm`:

```sh
sandbox-cli claude --worktree feature-a --detach --dry-run -- -p "implement A"
```

**The guest must exit by itself.** There is no terminal inside a detached
container: an agent started in its normal interactive mode will draw a UI nobody
can see and wait for a keystroke that never comes, until you stop it. Use the
agent's non-interactive form — `claude -p "…"`, `codex exec "…"`,
`droid exec "…"` — or an ordinary command like `npm test`.

**The container is kept after it exits**, unlike every other sandbox run. That is
the point: its exit code and its output are the only record that the work
happened, and `--rm` would delete both at the moment they become useful. Read
them back with ordinary docker commands, and reap when you're done:

```sh
docker ps -a --filter label=sandbox.branch=feature-a   # state and exit code
docker logs sandbox-myapp-f379c0cd-feature-a           # what the agent said
docker rm  sandbox-myapp-f379c0cd-feature-a            # when you've read both
```

Each run is stamped with `sandbox.repo`, `sandbox.branch`, `sandbox.agent` and
`sandbox.base` labels, which is how you find a container later without having
kept its name.

**One agent per branch is enforced**, and by construction rather than by a check:
a detached container is named `sandbox-<repo>-<branch>`, and docker refuses a
duplicate name. A second detached run on a branch that already has one fails
immediately rather than putting two agents in one checkout, which would lose
work silently. If the first one has finished, `docker rm` its container to free
the name.

Everything else is unchanged — the same mounts, the same fake HOME, the same
hardening. A background container reaches exactly what a foreground one does.

### Running a fleet

`--detach` launches agents one command at a time and leaves you to track them
with docker. A **fleet** does the same fan-out from one file, and then answers the
question the section above cannot: *which of these actually worked?*

Write a `fleet.yaml` (there is a fuller, commented one in
[`docs/examples/fleet.yaml`](examples/fleet.yaml)):

```yaml
agent: claude
max_parallel: 2
defaults:
  memory: 4g
  cpus: "2"
  git: true          # so the agents' commits carry your name and email

tasks:
  - branch: feature-login
    prompt: Implement the login form in src/auth/. Add tests. Commit when they pass.
    verify: go build ./... && go test ./...

  - branch: feature-ratelimit
    prompt: Add per-IP rate limiting to src/server/. Add tests. Commit when they pass.
    verify: go test ./src/server/...
```

Then the whole loop, all of it from your normal checkout:

```sh
# 0. Log in once per agent, if you haven't. A fleet cannot answer a login prompt.
sandbox-cli claude

# 1. See what it would do, without launching anything.
sandbox-cli fleet run --dry-run

# 2. Fan out. One branch, one worktree and one container per task.
sandbox-cli fleet run

# 3. Watch. Whether each agent is running, how it ended, and what it produced.
sandbox-cli fleet status

# 4. Read one agent's transcript.
sandbox-cli fleet logs feature-login          # -f to follow live

# 5. Stop one early if it is going wrong.
sandbox-cli fleet stop feature-login          # or --all

# 6. Land the work: commits whatever the agent left, then merges into your branch.
sandbox-cli fleet land feature-login

# 7. Reap the finished containers (and --worktrees for their checkouts).
sandbox-cli fleet clean
```

`fleet run` looks for `fleet.yaml` in the current directory; `-f path` names
another.

**`verify` is the point.** Without it, a fleet is a fan-out with a nicer status
table — every agent "succeeds" the moment it stops talking, including the one
that confidently did nothing and the one that deleted the failing test. A
`verify:` command runs *inside the container after the agent*, and its exit code
is the verdict:

```
BRANCH             STATE       ELAPSED  DIRTY  AHEAD
feature-login      exited 0    4m12s    0      3
feature-ratelimit  exited 90   2m03s    7      0
```

Exit `90` means the agent finished and its verify said no. `fleet land` refuses
that branch until you fix it or override with `--force`.

A task with no `verify` still runs — this is a fleet of agents, not a CI system —
but `fleet land` reports it as *unverified* rather than passed, because nothing
checked it.

**`fleet land` refuses rather than guessing.** It is the only command that writes
to your base branch, so it stops when:

- the agent is still running — its next action could change what you just merged
- the work failed its verify (`--force` to land anyway)
- **your checkout has moved** since the agent was launched. Each container records
  the branch its work was meant for, so if you started the fleet on `main` and are
  now on `release-2`, landing stops rather than putting the work on a branch
  nobody chose. Check out the recorded branch, or say `--onto release-2` to mean
  it deliberately
- another agent is working in the checkout being merged into — the merge would
  rewrite files under it
- that checkout is dirty, or the agent moved its own worktree to another branch

On a conflict it stops with git's own message and leaves the merge in place for
you to finish. It never resolves anything itself.

**For an unattended run, add `--profile prod`:**

```sh
sandbox-cli fleet run --profile prod
```

dev *warns* when a control cannot be satisfied; prod *refuses*. Nobody is
watching a fleet, so a warning goes into a log no one reads. prod also declines
to mount the persisted agent login at all — worth knowing, because it means each
task needs credentials from the environment instead (`ANTHROPIC_API_KEY`).

**Two constraints worth knowing before you write a fleet file:**

- **Only agents with a verified headless mode may appear** — `claude` and `codex`
  today. Anything else is rejected when the file is parsed, before a single
  container starts, because the alternative is an unattended agent waiting
  forever on a keystroke.
- **One agent per branch**, enforced by docker's own refusal to reuse a container
  name. A task whose branch already has a running agent is refused rather than
  joined; two agents in one checkout lose work silently.

**Fleet containers are kept after they exit**, like any detached run — their logs
and exit codes are the only record. `fleet clean` reaps them; add `--worktrees` to
remove the checkouts too, which skips any with uncommitted work rather than
discarding it.

`fleet` commands only ever touch containers the fleet started. An interactive
`sandbox-cli claude --detach` session in the same repository is not stopped by
`fleet stop --all`, not reaped by `fleet clean`, and does not count against
`max_parallel`.

### Handing files between two sandboxes

Two sandboxes are blind to each other by design — each sees its own project and
nothing more. When one agent produces something another needs (an API contract, a
schema, a generated client), `--share` gives them one directory in common:

```sh
sandbox-cli claude --share --project ~/web-ui     # produces /shared/openapi.yaml
sandbox-cli claude --share --project ~/backend    # consumes it
```

Then say it in the prompt: *"write the API contract to `/shared/openapi.yaml`"*,
and on the other side *"read `/shared/openapi.yaml` and implement it"*. The same
directory shows up for every sandbox using the flag — different worktrees,
different projects, doesn't matter. It lives on the host at
`~/.config/sandbox/shared`, so you can inspect and edit it like any folder.

It's read-write for every sandbox that mounts it and keeps no history. For a
one-way channel, mount it by hand instead
(`--mount ~/.config/sandbox/shared:/shared:ro` on the consumer). For history,
`git init --bare` a repo inside it and push from both sides.

Add `--share-name NAME` (alongside `--share`) to mount
`~/.config/sandbox/shared/NAME` at `/shared/NAME` instead — a namespace for two
concurrent runs that would otherwise clobber each other's filename. It stops the
collision, but it is **not** an isolation boundary: any sandbox using a bare
`--share` has the whole shared directory read-write and can read and modify
every namespace in it. Use a bare `--share` on both sides when the point is to
hand a file across, and don't put anything secret in a namespace.

### Finding your past conversations

Agent logins persist in a sandbox-owned home, and so do the conversations the
agent writes there — the container is disposable, its session history is not.
`context list` shows you those conversations, newest first:

```sh
sandbox-cli claude context list
```

```
ID        WHEN      TURNS  TITLE
37888763  just now  4      Share context between sandbox instances with resume
95ad79ff  35m ago   17     sandbox-run-signal-handling
1bbbda97  53m ago   11     Review project and agent harness sandbox-cli

resume: sandbox-cli claude --resume 37888763
```

The id is the one the agent resumes by, so the listing is something to copy out
of. TURNS counts the prompts *you* sent, not the messages exchanged. The title is
the one Claude Code generates for the session; a session too short to have earned
one shows its first prompt instead.

Ids are abbreviated, and sandbox-cli expands one back to the full id before the
agent sees it. **`-f` / `--full` prints whole ids**, which you need when running
the agent directly rather than through sandbox-cli:

```sh
sandbox-cli claude context list -f
claude --resume 37888763-3d07-451a-920c-d458c987cda8   # plain claude, no sandbox
```

That works because a Claude session recorded in a sandbox is written into your
real `~/.claude` history — same conversation, inside the container or out. Plain
`claude` won't take the abbreviated form; the expansion is sandbox-cli's own.

It's scoped to the project you're standing in, because that's the question you're
usually asking. `--all` lists every project in the store and adds a PROJECT
column, `--limit 0` shows everything (the default stops at 20 and tells you how
many it held back), and `--json` is for scripts. Without an agent name it lists
every agent that has sessions. `sandbox-cli context` on its own does the same
thing as `context list`.

**When there's nothing to list**, it tells you why in the same breath, including
where it looked:

```
$ sandbox-cli codex context list
no codex sessions found on this machine — has codex run in a sandbox yet?
  looked in ~/.config/sandbox/agents/codex/.codex/sessions
  looked in ~/.codex/sessions
```

`--verbose` shows the same location line next to a listing that *did* work.

Sessions from a store sandbox-cli can find but not yet read are still listed,
with `?` where the title and turn count would be — found but not understood is
not the same as empty. And an agent sandbox-cli has no store layout for says so
plainly rather than reporting "no sessions"; the agent still runs, only the
listing is missing.

Nothing here is taken on trust. sandbox-cli ships candidate layouts and only
treats one as real once it's been found on *your* machine holding sessions, then
records it in `~/.config/sandbox/contexts/stores.json` so it stays known. A store
confirmed earlier isn't forgotten when a later look can't see it — an agent home
that isn't mounted right now is not the same thing as a store that never existed
— it's just reported as not currently visible.

For Claude Code these ids resolve the same inside the sandbox and out, so a
conversation started on your host can be continued in a container and back again
(that's what `--no-sync` turns off).

**Each agent spells resume differently**, and the listing prints the right one
per agent — copy that line rather than typing it from memory:

```sh
sandbox-cli claude --resume 37888763     # a flag
sandbox-cli codex  resume 019f87bb       # a subcommand
```

`sandbox-cli claude resume <id>` is *not* an error you'll notice: Claude Code has
no `resume` subcommand, so the words become your first prompt and you get a brand
new conversation that looks nothing like the one you wanted.

The short ids above are shortened for the table, and the agents themselves reject
them — Claude Code wants a full UUID. sandbox-cli expands the short form to the
full id before the agent sees it, and prints which session it resolved to. If two
sessions share the prefix it leaves the value alone rather than guessing.
(Claude Code also accepts a session *title*, so
`--resume "Run integration tests"` works too.)

**You can only resume a session from the project it belongs to.** A sandbox
mounts one project's history, so a session recorded elsewhere isn't visible
inside the container however right the id is — including a session from a
`--worktree` run, which counts as its own project. `--all` lists those too, so
it's easy to pick one you can't reach from here; sandbox-cli checks before
starting the container and tells you where it actually lives:

```
$ sandbox-cli claude --resume ba2e2c56
session ba2e2c56 is not in this project's claude history
  it is in ~/.claude/projects/-Users-you-other-project
  a sandbox mounts only the current project's history, so claude cannot open it from here
  run sandbox-cli from that project, or pass --project <dir>
```

**Ids are not interchangeable between agents.** With several agents listed it's a
natural mistake to take an id off any row and hand it to whichever agent you
like:

```sh
sandbox-cli codex resume 37888763    # a claude id
ERROR: No saved session found with ID 37888763.
```

That's not a sandbox-cli failure — Codex looked in its own store and the id was
never there. A session id is a key into one vendor's private store, not a
portable handle: different namespaces, different file formats, and a Claude
transcript is full of tool calls Codex doesn't have. Carrying a conversation
across agents is a **handoff** (summarise, then brief the other agent), not a
resume. It isn't built yet — see `docs/proposals/shared-context.md` for the
design and what it can and can't preserve.

### Passing Claude's own flags

The wrappers forward everything they don't recognize, so Claude's flags work
normally. The rule: **sandbox flags first; the first token that isn't a sandbox
long-flag ends the sandbox portion, and the rest goes to the agent.**

```sh
#              ├── sandbox ──┤  ├──── claude ────┤
sandbox-cli claude --worktree feature-a -- -p "implement A"
```

`--` marks the boundary explicitly. It's optional when the next token is a short
flag like `-p` (short flags always end the sandbox portion), but it costs nothing
and reads better. Some combinations:

```sh
# headless prompt in a worktree
sandbox-cli claude --worktree feature-a -- -p "implement A"

# full autonomy — safe here, the container is disposable
sandbox-cli claude -- --dangerously-skip-permissions

# pick a model, continue the last session, add context
sandbox-cli claude -- --model opus
sandbox-cli claude -- --continue
sandbox-cli claude --worktree feature-a -- -p "fix the failing tests" --model opus

# sandbox options and agent options together
sandbox-cli claude --worktree feature-a --cache --allow example.com -- -p "build it"
```

**The one thing to get right is order.** A sandbox flag written *after* an agent
flag is forwarded to the agent, which will reject it:

```sh
sandbox-cli claude --worktree feature-a --model opus   # ✅
sandbox-cli claude --model opus --worktree feature-a   # ❌ claude gets --worktree
```

`--model` isn't a sandbox flag, so it ends the sandbox portion and everything
after it — including `--worktree` — is forwarded. Put sandbox flags first, and
check any command you're unsure about with `--dry-run`:

```sh
sandbox-cli claude --worktree feature-a --dry-run -- -p "implement A"
```

That prints the `docker` invocation, including which directory becomes
`/workspace` and the exact arguments Claude receives, without running anything.

### Stronger isolation on demand (microVM / gVisor)
By default you get a normal Docker container (shared kernel). If your host has a
stronger OCI runtime installed, ask for it and get a harder boundary — nothing
else changes:

```sh
sandbox-cli claude --runtime kata-runtime   # microVM: its own kernel
sandbox-cli claude --runtime runsc          # gVisor: userspace-kernel filter
```

*(Requires the runtime to be registered with Docker; Kata needs a Linux host with
nested virtualization.)*

### git & host services that "just work"
```sh
# attribute commits to you + trust the workspace (no "dubious ownership" errors)
sandbox-cli claude --git

# let the agent reach an MCP server running on your host (needed on Linux)
sandbox-cli claude --host-gateway
```

Your timezone comes along too. The container's own clock is UTC, so without this
a commit made inside a sandbox records `+0000` while your own commits record your
real offset — the same instant, but `git log` shows them hours apart. The host's
zone is forwarded as `TZ` (read from `$TZ`, `/etc/localtime` or `/etc/timezone`;
nothing extra is mounted). To run on a different clock, say so — `--env TZ=UTC`,
or any zone name — and that wins.

### Seeing a web app the agent is running
The sandbox publishes no ports, so a dev server started inside it is invisible
from your browser — the container is on Docker's own network, and on Docker
Desktop that network lives inside a Linux VM your host cannot route to. Publish
the port to change that:

```sh
sandbox-cli run -P 3000 -- npm run dev        # then open http://localhost:3000
sandbox-cli claude --publish 3000             # same, for an agent session
```

Two things to know:

- **Bind to `0.0.0.0` inside the container.** Most dev servers listen on
  localhost by default, which inside a container means the container's own
  loopback — the published port would answer with nothing. Next.js and Vite want
  `--hostname 0.0.0.0` / `--host 0.0.0.0`; others use `HOST=0.0.0.0`.
- **The host side binds to `127.0.0.1` unless you say otherwise.** `-P 3000` is
  reachable from your machine and nowhere else. To share it with a phone on the
  same wifi, ask for that explicitly: `-P 0.0.0.0:3000:3000`.

Put the port in `.sandbox.yaml` and you never type it again:

```yaml
ports:
  - 3000:3000
```

Flags add to that list rather than replacing it, so `-P 9229` opens a debugger
port for one run without disturbing the project's own.

### Live resource metrics
Non-interactive runs show a live memory/CPU gauge; every run prints a peak-usage
summary at the end. `sandbox-cli stats` shows a live table of running sandboxes.
Disable with `--no-metrics`.

During an interactive agent session, only `claude` shows the gauge on screen — it
has a `statusLine` hook sandbox-cli can render into (`--no-statusline` turns it
off). `gemini`, `opencode` and `codex` have no such hook, so for those run
`sandbox-cli stats` in a second terminal.

### How much of the plan is left
Claude's status line also carries the two subscription windows and the model —
`· opus 5 · … · 5h 23% (2h14m) · wk 49%`: the session window with the time until it
resets, the weekly one, and what is answering (how fast a window drains depends on
that). Claude Code reports the windows only on a Claude.ai plan and only after its
first request, so under API-key auth that part of the line is simply not there.

```sh
sandbox-cli usage           # the same windows, outside a Claude session
sandbox-cli usage --refresh # spend one throwaway turn to make them current
sandbox-cli usage --json
```

Useful when several sandboxes are running at once: they are separate containers
but one account quota. A row labelled with a model in parentheses — `week (Fable)`
— is a cap on that model alone, which some plans meter separately.

The numbers are cached rather than live: they come from what Claude Code keeps
for its own `/usage`, refreshed only when the agent talks to the server, which is
why the command always prints how old the reading is. Left alone long enough, a
window resets and the cached figure stops describing it at all — those rows show
`rolled over` and no percentage, rather than a number that is about last week:

```
5h            —  rolled over  (03:09)
week          —  rolled over  (Fri 22:59)
week (Fable)  —  rolled over  (Fri 23:00)

claude, as of 17h ago — ~/.claude.json
run `sandbox-cli usage --refresh` for a current reading
```

`--refresh` asks claude for one throwaway turn first, which is the only way to
bring the numbers up to date — it is opt-in because that request comes out of the
window being measured, and Claude Code still refetches on its own schedule, so it
makes a reading minutes old rather than hours, never one stamped now.

### Works with Claude, Codex, Gemini, OpenCode and Cline
`sandbox-cli claude` / `codex` / `gemini` / `opencode` / `cline` wrap each agent, forward
its flags untouched (so `--dangerously-skip-permissions` just works), and
**persist each agent's login** in a sandbox-owned folder — one per agent — so you
only log in once, kept separate from your real `~/.claude`, `~/.gemini`, etc.

**Setting one up:** prerequisites, the login flow for each (none of them need a
browser inside the container), what the sandbox sets for you, and the extra
`--allow` domains are all in the **[Agent reference](AGENTS.md)**.

Adding another agent is a small, well-defined piece of work; the queue and the
per-adapter checklist live in
[docs/proposals/agent-adapters.md](proposals/agent-adapters.md).

---

## Configuration

Zero config is required. Configuration lives in two files, and **the split
between them is a security boundary, not a convenience**.

### Your config — `~/.config/sandbox/config.yaml`

Only you write this file, so it may set anything:

```yaml
image: my-org/dev:latest
user: sandbox                 # non-root by default
runtime: ""                   # runsc / kata-runtime for a stronger boundary

mounts:
  - { host: ~/data, container: /workspace/data, mode: ro }

env:
  NODE_ENV: development

env_allow:                    # only these host vars are forwarded, and only if set
  - ANTHROPIC_API_KEY
  - OPENAI_API_KEY

security:                     # secure-by-default; tune here
  memory: ""                  # e.g. 2g (opt-in)
  cpus: ""                    # e.g. 1.5 (opt-in)

secrets:                      # resolved at run time, forwarded by name only
  GITHUB_TOKEN: { command: gh auth token }
  ANTHROPIC_API_KEY: { file: ~/.secrets/anthropic }
```

### The project file — `.sandbox.yaml`

Drop one in your project (scaffold with `sandbox-cli init`) to describe *the
project*:

```yaml
# .sandbox.yaml
network:
  mode: allowlist             # may only TIGHTEN: default -> allowlist -> none
  allow:
    - internal.registry.example.com
  # baseline: false           # drop the built-in domains; `allow` becomes the whole list

ports:                        # published to the host; no address => 127.0.0.1
  - 3000:3000

hostname: devbox

cache:
  enabled: false              # or use --cache

snapshot:                     # crash safety net (sandbox-cli recover)
  enabled: true               # or use --no-snapshot
  interval: 2m                # how often the workspace is snapshotted
  retention: 336h             # 14d, then old snapshots are pruned
```

**`.sandbox.yaml` is treated as untrusted.** It travels with the repository —
anyone who clones the repo runs it — and the agent working in the sandbox has
read-write access to the very file that configures its next run. So the keys that
could run commands on your machine, reach host paths, or weaken the container are
**refused** from it:

> `image`, `workdir`, `user`, `home`, `runtime`, `mounts`, `secrets`, `env`,
> `env_allow`, `security.*`, `cache.paths`, and any `network.mode` /
> `network.baseline` that *weakens* what you already have in force.

A project may ask for stricter confinement than your default; it may not ask for
less. If a project file sets a refused key, sandbox-cli stops and names it rather
than quietly applying it.

If you have read a project's config and want it anyway, load it deliberately:

```sh
sandbox-cli --config ./.sandbox.yaml claude
```

Discovery is also bounded: sandbox-cli looks for `.sandbox.yaml` from the current
directory up to the repository root (or your home directory when there is no
repository), never above it — so a stray config in a shared parent like `/tmp`
cannot be picked up.

Settings merge in this order (later wins): built-in defaults →
`~/.config/sandbox/config.yaml` → nearest `.sandbox.yaml` → command-line flags.
Run `sandbox-cli config show` to see the effective, merged config, and
`sandbox-cli config path` to see which files were used.

---

## Command reference

| Command | What it does |
|---|---|
| `sandbox-cli run -- <cmd>` | Run any command in the sandbox |
| `sandbox-cli claude [args]` | Run Claude Code (args forwarded to the agent) |
| `sandbox-cli codex [args]` | Run Codex CLI |
| `sandbox-cli gemini [args]` | Run Gemini CLI |
| `sandbox-cli opencode [args]` | Run OpenCode |
| `sandbox-cli cline [args]` | Run Cline (installed on first use) |
| `sandbox-cli goose [args]` | Run Goose (installed on first use) |
| `sandbox-cli crush [args]` | Run Crush (installed on first use) |
| `sandbox-cli aider [args]` | Run Aider (installed on first use, via uv) |
| `sandbox-cli copilot [args]` | Run GitHub Copilot CLI (installed on first use) |
| `sandbox-cli cursor [args]` | Run Cursor CLI (installed on first use) |
| `sandbox-cli qwen [args]` | Run Qwen Code (installed on first use) |
| `sandbox-cli amp [args]` | Run Amp (installed on first use) |
| `sandbox-cli continue [args]` | Run Continue CLI (installed on first use) |
| `sandbox-cli openhands [args]` | Run OpenHands CLI (installed on first use) |
| `sandbox-cli droid [args]` | Run Droid (installed on first use) |
| `sandbox-cli init` | Scaffold a `.sandbox.yaml` |
| `sandbox-cli config show\|path\|validate` | Inspect the effective config |
| `sandbox-cli stats` | Live table of running sandboxes |
| `sandbox-cli worktree list\|path\|rm` | Manage `--worktree` worktrees |
| `sandbox-cli worktree git BRANCH ...` | Run git inside a worktree, by branch name |
| `sandbox-cli worktree commit BRANCH -m ...` | Commit what the agent left there |
| `sandbox-cli recover` | What a crashed run left behind, and what's broken ([runbook](#after-a-crash-step-by-step)) |
| `sandbox-cli recover list\|show\|restore` | Find and restore work from a crashed run |
| `sandbox-cli recover repair` | Fix a repository a crashed sandbox broke |
| `sandbox-cli version` | Print the version |

Common flags (work on `run` and on every agent wrapper):

| Flag | Meaning |
|---|---|
| `-p, --project DIR` | Host dir to mount at `/workspace` (default: cwd) |
| `-m, --mount H:C[:ro\|rw]` | Extra mount (repeatable) |
| `-e, --env K=V` / `--env-allow NAME` | Set / forward an env var |
| `--allow DOMAIN` | Egress allowlist mode (repeatable) |
| `--cache` | Persist package caches across runs |
| `--secret NAME=file:\|cmd:\|env:...` | Brokered credential (repeatable) |
| `--worktree BRANCH` | Run in a git worktree for BRANCH |
| `--detach` | Start in the background, print the container name (guest must exit on its own) |
| `--share` | Mount `~/.config/sandbox/shared` at `/shared` (exchange files between sandboxes) |
| `--share-name NAME` | With `--share`, mount `~/.config/sandbox/shared/NAME` at `/shared/NAME` instead of the root (avoids collisions; not an isolation boundary) |
| `--paste` | Mount `~/Desktop`, `~/Downloads`, `~/Pictures` read-only at their host paths (pasted image paths resolve) |
| `--git` | Forward git identity + trust the workspace |
| `-P, --publish 3000` | Publish a container port to the host (repeatable; `127.0.0.1` unless you give an address) |
| `--host-gateway` / `--add-host H:IP` | Reach host services / add a host mapping |
| `--memory 2g` / `--cpus 1.5` | Resource limits |
| `--dry-run` | Print the docker command and exit |
| `--build` | Force a rebuild of the base image |

Flag rule for the agent wrappers: sandbox flags come **first**; everything else
is passed straight to the agent. Use `--` to be explicit, e.g.
`sandbox-cli claude --worktree feat -- -p "do the thing"`. See
[Passing Claude's own flags](#passing-claudes-own-flags) for the details and the
one ordering mistake worth avoiding.

---

## Troubleshooting

**A `--detach` run never finishes** — almost always the agent was started in its
interactive mode. There is no terminal inside a detached container, so it drew a
UI to nothing and is waiting for a keystroke. `docker logs NAME` shows escape
codes and a prompt rather than work. Stop it (`docker stop NAME`), remove it
(`docker rm NAME`) and relaunch with the agent's non-interactive form: `claude -p
"…"`, `codex exec "…"`, `droid exec "…"`.

**"Conflict. The container name … is already in use"** on a detached run — that
branch already has an agent, and the refusal is the feature: two agents in one
checkout overwrite each other's work silently. If the first is still running,
wait for it or `docker stop` it; if it has finished, `docker rm NAME` frees the
name.

**A detached run finished but `docker ps` shows nothing** — `docker ps` lists only
running containers. A finished sandbox is still there, holding the exit code and
the logs you came back for: `docker ps -a --filter label=sandbox.repo`.

**Detached containers piling up** — they are kept on purpose and reaped by hand.
`docker rm $(docker ps -aq --filter label=sandbox.repo --filter status=exited)`
clears the finished ones; the branches and worktrees are untouched by it.

**A detached agent asks for a login and dies** — the persisted login is created by
an interactive run. Run the agent once in the foreground (`sandbox-cli claude`),
log in, then detach. The same applies to an agent installed on first use: let it
install once interactively, since a detached first run does that download with
nobody watching.

**"Cannot connect to the Docker daemon"** — Docker isn't running. Start Docker
Desktop, or your Linux Docker daemon.

**First run is slow** — it's building the base image once. Later runs are fast.
Force a rebuild anytime with `--build`.

**`npm install` fails with `ENOTFOUND` under `--allow`** — the registry isn't in
the allowlist. The common ones are built in; add others with another `--allow`.

**The agent can't reach my local MCP server** — add `--host-gateway` (Linux) and
point the agent at `host.docker.internal`.

**A run crashed and I don't know where my work is** — follow
[After a crash, step by step](#after-a-crash-step-by-step). Short version:
`sandbox-cli recover`, then `sandbox-cli recover repair` if it reported a
problem, then `git status`.

**`fatal: not a git repository` after a sandbox crashed** — the worktree's
administrative directory in the parent repo was deleted, so the `.git` pointer
file leads nowhere. Your files are untouched. Run `sandbox-cli recover repair`
(from the worktree or from your main checkout — it finds orphaned worktrees
either way); `git worktree repair` will not help, since it reconnects worktrees
that *moved* and there is nothing left here to reconnect.

**The agent reset away work I wanted** — `sandbox-cli recover list`, then
`sandbox-cli recover restore <id>` puts it on a branch. Snapshots carry the
commit `HEAD` was on, so commits a `git reset --hard` orphaned come back too.

**Files in `/workspace` are owned by the wrong user (Linux)** — run as your own
uid: `--user "$(id -u):$(id -g)"`. On macOS Docker Desktop this is handled
automatically.

**I can't select text with the mouse** — the agent's UI turns on mouse reporting,
so your terminal hands the drag to the application instead of making a selection.
Hold your terminal's override key while dragging: `Option` in iTerm2, `Shift` in
Ghostty. For a code block, add the rectangular-selection modifier (`Cmd+Option`
in iTerm2, `Ctrl+Alt` in Ghostty) so you get the code columns without the
surrounding frame. None of this involves the sandbox — it behaves the same way
running the agent directly on the host.

**Pasting an image into the agent does nothing** — on the host, pasting a copied
or dragged image file works because your terminal inserts its absolute path and
the agent reads it. The path is all that crosses into the sandbox, and
`/Users/you/Desktop/shot.png` names nothing in a container whose only host mount
is your project, so the agent reports a missing file. Pass `--paste` to mount
`~/Desktop`, `~/Downloads` and `~/Pictures` read-only *at their own host paths*,
which makes the pasted path resolve to the same bytes it names on the host:

```sh
sandbox-cli claude --paste
```

It is opt-in because it is genuinely wider reach — the agent can then read
everything in those three directories, not only the file you pasted. For one
directory elsewhere, mount it yourself at the same path:
`--mount ~/shots:/Users/you/shots:ro` (both sides must be the host path). The
narrowest option needs no flag at all: copy the image into the project first,
where it is already mounted, and paste `./shot.png`.

An image copied as *raw bits* rather than as a file — a browser's "Copy Image",
a screenshot sent straight to the clipboard — is a different problem and `--paste`
does not help. There is no path involved: the agent reads the OS clipboard
directly, and the container has no clipboard to read (see the next two entries).
Save it to a file on the host first.

**Claude's `/copy` doesn't reach my clipboard** — `/copy` is Claude Code's own
command, and it shells out to a platform clipboard tool (`pbcopy` on macOS,
`xclip`/`xsel`/`wl-copy` on Linux). None of those can work in a container, so the
image ships a shim under all four names that writes an OSC 52 escape sequence to
the terminal instead; your emulator reads it off the tty and puts the text on the
real clipboard. If nothing arrives, the terminal is refusing the sequence — see
the next entry. For very long output, sidestep the clipboard entirely: ask the
agent to write the text to a file in `/workspace` and copy it host-side
(`pbcopy < snippet.md`), which also avoids the hard line wraps a screen selection
picks up. Pasting *into* the sandbox from the host clipboard is not supported and
reports an error rather than returning nothing.

**A tool says it copied, but nothing pastes** — something in the container is
using an OSC 52 escape sequence to reach the host clipboard, and your terminal
has to permit that. iTerm2 gates it behind Settings → General → Selection →
"Applications in terminal may access clipboard" (off by default); tmux needs
`set -g set-clipboard on` or it swallows the sequence; macOS Terminal.app has no
support at all. Test the terminal on its own first, with no container involved:

```sh
printf '\033]52;c;%s\a' "$(printf hello | base64)"   # then paste
```

If that doesn't paste, it's terminal configuration and nothing in the sandbox can
change it.

**I want to see what it will do without running** — add `--dry-run`.

**The agent refuses `--dangerously-skip-permissions` as root** — that's by
design; the default non-root `sandbox` user is what makes skip-permissions safe.
Don't override with `--user root` unless you have a reason.

---

For the security model in depth and how sandbox-cli compares to other tools, see
[README.md](../README.md).
