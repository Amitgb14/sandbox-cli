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

### Live resource metrics
Non-interactive runs show a live memory/CPU gauge; every run prints a peak-usage
summary at the end. `sandbox-cli stats` shows a live table of running sandboxes.
Disable with `--no-metrics`.

During an interactive agent session, only `claude` shows the gauge on screen — it
has a `statusLine` hook sandbox-cli can render into (`--no-statusline` turns it
off). `gemini`, `opencode` and `codex` have no such hook, so for those run
`sandbox-cli stats` in a second terminal.

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

Zero config is required. To customize, drop a `.sandbox.yaml` in your project
(scaffold one with `sandbox-cli init`):

```yaml
# .sandbox.yaml
workdir: /workspace
user: sandbox                 # non-root by default

mounts:
  - { host: ./data, container: /workspace/data, mode: ro }

env_allow:                    # only these host vars are forwarded, and only if set
  - ANTHROPIC_API_KEY
  - OPENAI_API_KEY

network:
  mode: default               # default | none | allowlist
  allow:                      # extra domains for allowlist mode
    - internal.registry.example.com

security:                     # secure-by-default; override per project
  memory: ""                  # e.g. 2g (opt-in)
  cpus: ""                    # e.g. 1.5 (opt-in)

cache:
  enabled: false              # or use --cache

secrets:
  GITHUB_TOKEN: { command: gh auth token }
  ANTHROPIC_API_KEY: { file: ~/.secrets/anthropic }
```

Settings merge in this order (later wins): built-in defaults →
`~/.config/sandbox/config.yaml` → nearest `.sandbox.yaml` → command-line flags.
Run `sandbox-cli config show` to see the effective, merged config.

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
| `--paste` | Mount `~/Desktop`, `~/Downloads`, `~/Pictures` read-only at their host paths (pasted image paths resolve) |
| `--git` | Forward git identity + trust the workspace |
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
