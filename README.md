# sandbox-cli

Run AI coding agents (Claude Code, Codex, Gemini, OpenCode, Cline, Goose, Crush,
Aider, Copilot CLI, Cursor, Qwen, Amp, Continue, OpenHands, Droid) — or any
command — inside a
**disposable, isolated Docker container**. Only the project you choose is mounted
at `/workspace`; `HOME` is a fake, ephemeral directory. A mistaken `rm -rf ~` or
a prompt-injected command can't touch the rest of your machine.

```
        Host                                Sandbox (container, --rm)
  ~/projects/myapp  ── bind ──►  /workspace   (the only host-connected path)
  ~/.ssh ~/.aws ~/  ── NOT mounted            HOME=/sandbox/home  (ephemeral)

  (the agent wrappers additionally mount a sandbox-owned agent home and,
   for claude, your history for this one project — both opt-out; see below)
```

> **Per-agent setup — prerequisites, how to log in without a browser, and what
> each agent needs under `--allow` — is in the [Agent reference](docs/AGENTS.md).**

> New here? Start with the **[User Guide](docs/GUIDE.md)** — setup, everyday
> usage, and a friendly tour of every feature.

## Why

Developers want to run agents with full autonomy (`--dangerously-skip-permissions`
/ "Allow All") but don't want the agent to have unrestricted access to their host
filesystem and credentials. sandbox-cli gives the agent the convenience of "Allow
All" while limiting the blast radius to the project it's already meant to edit.

## Requirements

- Docker (Docker Desktop on macOS)
- Go 1.25+ only if you build from source

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/Amitgb14/sandbox-cli/main/install.sh | sh
```

That detects your OS and CPU, downloads the matching release archive, verifies it
against the release `checksums.txt`, and installs the binary to
`~/.local/bin/sandbox-cli` — no root, no package manager. It prints a PATH hint if
that directory isn't on your `PATH`.

<details>
<summary>Other ways to install</summary>

```sh
# a specific release, or a different directory
sh install.sh --version 0.0.1beta.3 --dest ~/bin

# while the repo is private, authenticate with a token
GITHUB_TOKEN=ghp_... sh install.sh

# Go users
go install github.com/Amitgb14/sandbox-cli/cmd/sandbox-cli@latest

# build from source (needs Go 1.25+)
make install        # go install ./cmd/sandbox-cli
make build          # -> bin/sandbox-cli
```

Windows: download the `.zip` from the
[releases page](https://github.com/Amitgb14/sandbox-cli/releases) — the shell
installer covers Linux and macOS only.

Release targets: linux, macOS and Windows on amd64 and arm64.
</details>

## Uninstall

```sh
curl -fsSL https://raw.githubusercontent.com/Amitgb14/sandbox-cli/main/install.sh | sh -s -- --uninstall
```

That removes the `sandbox-cli` binary and then *reports* what else is on disk
without deleting it — because `~/.config/sandbox` holds your agent logins, and
silently deleting it would sign you out of Claude/Codex with no warning. To
remove everything, including those logins, the base image, and the cache volumes:

```sh
sh install.sh --uninstall --purge
```

| What | Where | Removed by |
|---|---|---|
| Binary | `~/.local/bin/sandbox-cli` (also checks `/usr/local/bin`) | `--uninstall` |
| Config + agent logins | `~/.config/sandbox/` | `--purge` |
| Base image | `sandbox-base:*` Docker images | `--purge` |
| Package caches | `sandbox-cache-*` Docker volumes | `--purge` |

Containers are `--rm`, so nothing lingers between runs. Your projects and their
`.sandbox.yaml` files are never touched by either flag.

## Usage

```sh
# Any command in the sandbox
sandbox-cli run -- bash
sandbox-cli run -- sh -c 'echo $HOME; ls /workspace'

# See the exact docker command without running it
sandbox-cli run --dry-run -- npm test

# AI agents (forward their API key from your host env only if it's set)
ANTHROPIC_API_KEY=... sandbox-cli claude
ANTHROPIC_API_KEY=... sandbox-cli claude --dangerously-skip-permissions
OPENAI_API_KEY=...    sandbox-cli codex exec 'run the tests'
GEMINI_API_KEY=...    sandbox-cli gemini --yolo
ANTHROPIC_API_KEY=... sandbox-cli opencode run 'run the tests'

# Scaffold a project config
sandbox-cli init
```

### Passing flags to the agent

For every agent wrapper (`claude`, `codex`, `gemini`, `opencode`, `cline`, …),
**everything you type is forwarded to the agent** — so `sandbox-cli claude --dangerously-skip-permissions` just works, and there
are no collisions with sandbox's own flags.

The rule is one sentence: **a leading run of sandbox long-flags is consumed by
sandbox; the first token that isn't one ends the sandbox portion, and everything
from there goes to the agent verbatim.** A short flag, a positional, an unknown
long flag, or an explicit `--` all end it.

```sh
#              ├── sandbox ──┤  ├──── claude ────┤
sandbox-cli claude --worktree feature-a -- -p "implement A"
sandbox-cli claude --worktree feature-a    -p "implement A"   # same thing
```

The `--` is optional here because `-p` is a short flag, which always ends the
sandbox portion. Writing it is still the clearer habit, and it's *required* when
the first agent argument is a positional or would otherwise be ambiguous.

Order is what matters, not the separator. A sandbox flag placed **after** the
boundary is forwarded to the agent instead of being applied to the sandbox:

```sh
sandbox-cli claude --worktree feature-a --model opus     # ✅ worktree applies
sandbox-cli claude --model opus --worktree feature-a     # ❌ --worktree goes to claude
```

`--model` isn't a sandbox flag, so it ends the sandbox portion — and the
`--worktree` after it is passed straight through to Claude, which will reject it.
When in doubt, put every sandbox flag first and confirm with `--dry-run`:

```sh
sandbox-cli claude --worktree feature-a --dry-run -- -p "implement A"
```

`sandbox-cli run` uses the opposite default: sandbox flags first, the command after `--`
(`sandbox-cli run --dry-run -- npm test`).

### Persistent agent login

Every agent wrapper **persists the agent's login by default**, so you
authenticate once and it survives the throwaway containers. A dedicated,
sandbox-owned host directory is bind-mounted as the agent's whole home:

```
~/.config/sandbox/agents/claude    ->  /sandbox/home   (sandbox-cli claude)
~/.config/sandbox/agents/codex     ->  /sandbox/home   (sandbox-cli codex)
~/.config/sandbox/agents/gemini    ->  /sandbox/home   (sandbox-cli gemini)
~/.config/sandbox/agents/opencode  ->  /sandbox/home   (sandbox-cli opencode)
~/.config/sandbox/agents/cline     ->  /sandbox/home   (sandbox-cli cline)
~/.config/sandbox/agents/goose     ->  /sandbox/home   (sandbox-cli goose)
~/.config/sandbox/agents/crush     ->  /sandbox/home   (sandbox-cli crush)
~/.config/sandbox/agents/aider     ->  /sandbox/home   (sandbox-cli aider)
~/.config/sandbox/agents/copilot   ->  /sandbox/home   (sandbox-cli copilot)
~/.config/sandbox/agents/cursor    ->  /sandbox/home   (sandbox-cli cursor)
~/.config/sandbox/agents/qwen      ->  /sandbox/home   (sandbox-cli qwen)
~/.config/sandbox/agents/amp       ->  /sandbox/home   (sandbox-cli amp)
~/.config/sandbox/agents/continue  ->  /sandbox/home   (sandbox-cli continue)
~/.config/sandbox/agents/openhands ->  /sandbox/home   (sandbox-cli openhands)
~/.config/sandbox/agents/droid     ->  /sandbox/home   (sandbox-cli droid)
```

The whole home is persisted (not just `~/.claude`) because agents keep their
"onboarding done" flag and account info in `~/.claude.json` — a file in the home
root — and write config via atomic rename, which a single-file bind mount can't
capture. This directory is **separate from your host `~/.claude`** — the sandbox
does not read or write your real Claude/Codex *config* or credentials. (Your host
*conversation history* for the project is shared by default; see
[Shared conversation history](#shared-conversation-history).) The first
`sandbox-cli claude` prompts you to log in; subsequent runs reuse the stored
session. Opt out for a one-off, throwaway session with `--no-persist-auth`:

```sh
sandbox-cli claude --no-persist-auth
```

The first run builds the `sandbox-base` image (Node + git + common tools, with
Claude Code and Codex pre-installed). Rebuild with `--build`.

**Claude Code stays current.** The baked copy is a fallback; on first use
`sandbox-cli claude` installs Claude Code into the persisted HOME (`~/.local`, via
the official installer) where it is writable, so it self-updates from then on and
matches the version you'd get on the host — no rebuild needed.

### Shared conversation history

`sandbox-cli claude` mounts **your host Claude history for the current project**
into the sandbox by default, so a session works the same on either side of the
boundary:

```
~/.claude/projects/<project>  ->  /sandbox/home/.claude/projects/-workspace   (read-write)
```

That means `claude --resume` inside the sandbox lists and resumes sessions you
started on the host, and sessions you run in the sandbox show up on the host
afterwards. Only the directory for the project you're running in is mounted — not
your whole `~/.claude/projects`.

The mount is **read-write**, so an agent in the sandbox can modify or delete the
host-side transcripts for that one project. If you'd rather keep the sandbox's
history completely separate, opt out:

```sh
sandbox-cli claude --no-sync
```

If the host has no history for the project yet, there's nothing to mount and the
flag is a no-op. History sharing assumes the default `HOME` and workdir; with
`--workdir` overridden, session IDs may not line up.

### Live resource gauge

For **non-interactive** runs (`--no-tty`, or piped/redirected stdio), sandbox-cli
pins a live resource gauge to the bottom of the terminal showing the container's
memory, CPU, and elapsed time, with the workspace's git branch on the right —
output scrolls above it, and it's erased when the run ends:

```
work line 3
 sandbox-cli │ mem 512MiB/7.6GiB ▕▓░░░░░░▏ cpu 82% · 0m47s        ⎇ feature/login
```

The branch is what tells parallel `--worktree` sandboxes apart at a glance. It is
dropped when the terminal is too narrow to fit both halves on one line, and absent
when the project isn't a git repository (a detached HEAD shows the commit id).

It is intentionally **not** drawn during an interactive agent session (Claude/Codex
own the full screen). Instead, **every** run (interactive included) prints a one-line
peak-usage summary after it exits — so you still get the numbers for a Claude session:

```
sandbox-cli: peak mem 412MiB · cpu peak 138% · 12m04s · ⎇ feature/login
```

The summary is sampled in the background without touching the screen, and is skipped
for containers too short-lived to sample. Disable all of this with `--no-metrics`.
Measurement only — no limits are placed on the container.

**Inside a `sandbox-cli claude` session**, a status line at the bottom of the Claude
UI shows the container's live memory/CPU (via Claude Code's `statusLine`, injected
through a managed-settings file that never touches your own Claude settings):

```
⬢ sandbox · mem 412MiB · cpu 82%                          ⎇ feature/login
```

The branch sits at the right edge, padded against the terminal width read from
`/dev/tty`. If the width can't be determined, or the line is too narrow for both
halves, the branch is dropped rather than shown truncated. Two escape hatches, in
case Claude renders the status line in an area narrower than the terminal:

```sh
sandbox-cli claude --env SANDBOX_STATUSLINE_RULER=1     # print a column ruler instead: read off the real width
sandbox-cli claude --env SANDBOX_STATUSLINE_COLS=76     # align against that width instead of the terminal's
sandbox-cli claude --env SANDBOX_STATUSLINE_RESERVE=8   # keep more columns free (default 4)
```

Start with the ruler: the last number still on screen *is* the usable width. Claude's
status-line JSON carries no terminal geometry (it reports `worktree.branch`, `model`,
`pr`, … but no width), so the alignment has to work from the tty width, and these
exist for when Claude's frame is narrower than the terminal.

Disable it with `--no-statusline`.

**To watch memory/CPU live for any run**, run `sandbox-cli stats` in a second
terminal — a refreshing table of all running sandbox containers:

```sh
sandbox-cli stats            # live table, refreshes every 2s, Ctrl-C to exit
sandbox-cli stats --once     # a single snapshot (scriptable)
sandbox-cli stats --interval 1s
```

```
sandbox-cli — live stats  15:04:05  (Ctrl-C to exit)

CONTAINER             MEM                CPU     PIDS
sandbox-dk0gtrd15s2g  412MiB / 7.6GiB   82.00%  24
```

### Common flags (run and every agent wrapper)

| Flag | Meaning |
|---|---|
| `-p, --project` | Host dir mounted at `/workspace` (default: cwd) |
| `-i, --image` | Override the container image |
| `-w, --workdir` | Working dir inside the container |
| `--user` | `sandbox` (non-root default) \| `root` \| `uid:gid` |
| `-m, --mount` | Extra mount `host:container[:ro\|rw]` (repeatable) |
| `-e, --env` | `KEY=VALUE`, or bare `KEY` to forward the host value |
| `--env-allow` | Host env var forwarded only if present (repeatable) |
| `--tty` / `--no-tty` | Force TTY on/off (default: auto-detect) |
| `--dry-run` | Print the docker command and exit |
| `--build` | Force a rebuild of the base image |
| `--no-metrics` | Disable the live resource gauge (non-interactive runs) |
| `--memory` | Container memory limit, e.g. `2g` (default: unlimited) |
| `--cpus` | Container CPU limit, e.g. `1.5` (default: unlimited) |
| `--no-hardening` | Disable the default cap-drop / no-new-privileges / pids-limit (debug) |
| `--allow` | Enable the egress allowlist and permit a domain, e.g. `--allow example.com` (repeatable; baseline registries always allowed) |
| `--cache` | Persist package-manager caches (npm/pip/cargo/go) in named volumes across runs |
| `--secret` | Brokered credential `NAME=file:PATH \| cmd:COMMAND \| env:VAR`, resolved at run time and kept off the command line (repeatable) |
| `--worktree` | Run in a git worktree for `BRANCH` (created if absent) — parallel per-branch agents |
| `--share` | Mount the shared dir (`~/.config/sandbox/shared`) at `/shared` so agents in different projects can exchange files |
| `--paste` | Mount `~/Desktop`, `~/Downloads` and `~/Pictures` read-only at their host paths, so an image path pasted into the agent resolves inside the container |
| `--git` | Forward host git identity and trust the workspace so `git` commits just work in-container |
| `--host-gateway` | Map `host.docker.internal` to the host (reach host MCP servers; needed on Linux) |
| `--add-host` | Extra `HOST:IP` mapping passed to docker (repeatable) |
| `--runtime` | OCI runtime for stronger isolation, e.g. `kata-runtime` (microVM) or `runsc` (gVisor) |
| `--no-snapshot` | Disable the crash safety net (periodic snapshots of the workspace under `refs/sandbox`) |
| `--snapshot-interval` | How often to snapshot the workspace, e.g. `30s` (default: 2m) |

## Crash recovery

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
anything. The rest of this section explains what it is doing and why.

---

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

**Snapshots clean themselves up.** Once you commit the work for real, the
snapshot holds a tree some branch already holds, so it is deleted at the start of
the next run in that repository — a snapshot's normal end is being superseded,
not timing out. It has to be an *exact* tree match: commit half of what the agent
left and the snapshot stays, because it is still the only copy of the other half.
Anything never committed is pruned after 14 days, since a ref keeps its objects
alive forever.

```sh
sandbox-cli recover prune                      # both, by hand
sandbox-cli recover prune --superseded=false   # only the 14-day expiry
```

`snapshot.retention` changes the window and `--no-snapshot` turns the whole thing
off. One limitation worth knowing: snapshots honour `.gitignore`, so ignored
paths are not captured — that is what keeps a `node_modules` from being committed
every two minutes.

## Parallel agents (git worktrees)

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

### The full cycle

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

### Commands

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

**How the agent can commit.** A worktree's `.git` is a pointer file holding an
absolute path into the parent repo, which is outside the workspace — so
sandbox-cli also mounts the parent repo's `.git` directory at that same path.
Without it every git command in the container fails with `not a git repository`
and the agent can edit files but never commit them. This is a third host path
reaching outside `/workspace`, and it is read-write: an agent in a worktree
sandbox can write to your repository's object store and refs (its own branch, but
also others). It applies whenever the workspace is a worktree, including running
`sandbox-cli` from one directly without `--worktree`.

A few more things worth knowing:

- **Untracked files don't come along.** A worktree starts from a committed tree,
  so anything in `.gitignore` or not yet committed (a local `.env`, `node_modules`)
  won't be there. Mount what's needed with `--mount`, or let the agent reinstall.
- **The branch is checked out in the worktree**, so you can't `git checkout` the
  same branch in your main copy while it exists. Use `worktree rm` first.
- **One container per worktree.** Parallel runs on the *same* branch would collide;
  give each agent its own.
- **Requires git** — it's the only feature that does.

## Sharing files between sandboxes

Two sandboxes can't see each other. Each one has its own project at `/workspace`
and nothing else — which is the point, but it leaves no way to hand something
over when a UI agent finishes an API contract the backend agent needs.

`--share` mounts one host directory at `/shared` in every sandbox that asks for
it:

```sh
sandbox-cli claude --share --project ~/web-ui     # writes /shared/openapi.yaml
sandbox-cli claude --share --project ~/backend    # reads it
```

Then just say so in the prompt — there's nothing else to learn:

> "Write the API contract to `/shared/openapi.yaml`"

> "Read `/shared/openapi.yaml` and implement the endpoints"

It works the same across git worktrees of one project, or across two unrelated
projects — the directory is the same for all of them. On the host it lives at
`~/.config/sandbox/shared`, so you can open it in an editor, diff it, or drop
files in yourself. It's created on first use with a README explaining what it is.

A few things worth knowing:

- **Opt-in.** A channel between projects is exactly the reach the sandbox refuses
  by default, so it only exists when you pass the flag.
- **Read-write for everyone using it.** There's no per-sandbox permission split;
  treat it as scratch space with one owner per file. For a one-way channel,
  mount it yourself instead: `--mount ~/.config/sandbox/shared:/shared:ro`.
- **Not versioned.** You get the current file, not its history. If the contract
  starts changing and you want to see what moved, `git init --bare` a repo inside
  `/shared` and push to it from both sides. For the fuller version of that idea —
  immutable version tags and a `contracts.lock` in the consuming repo, so a
  handoff becomes a pinned dependency instead of a shared mutable file — see
  [docs/proposals/pinned-contracts.md](docs/proposals/pinned-contracts.md)
  (proposed; needs no code, works with `--share` today).
- **Files, not messages.** The agents don't get notified; the reader sees whatever
  is on disk when it looks.

## Stronger isolation (microVM / gVisor)

By default the container is a normal (shared-kernel) Docker container. If your
host has a stronger OCI runtime registered, select it per run for a harder
boundary — no other change to how the sandbox is built:

```sh
sandbox-cli claude --runtime kata-runtime   # microVM: own kernel (hardware boundary)
sandbox-cli claude --runtime runsc          # gVisor: userspace-kernel syscall filter
```

Set it once in config with `runtime: kata-runtime`. This requires the runtime to
be installed and registered with the Docker daemon (e.g. Kata needs a Linux host
with nested virtualization; it is not available on stock macOS Docker Desktop).
Everything else — mounts, hardening, egress allowlist, caches, secrets — works
unchanged on top of it.

## Making git, MCP, and SSH "just work"

- **git** — `--git` forwards your host `user.name` / `user.email` (so commits are
  attributed to you) and marks the mounted workspace as trusted, avoiding git's
  "dubious ownership" refusal when the container user's uid differs from the
  host's. Pairs naturally with `--worktree`.
- **Host MCP servers** — an agent inside the container reaches services on your
  host via `host.docker.internal`. That name resolves automatically on Docker
  Desktop; on Linux add `--host-gateway` (it maps `host.docker.internal` to the
  host gateway). Use `--add-host HOST:IP` for any other host mapping.
- **SSH (manual)** — to push over SSH, forward your agent socket:
  `--mount "$SSH_AUTH_SOCK:/ssh-agent" --env SSH_AUTH_SOCK=/ssh-agent` (on macOS
  Docker Desktop use the socket path `/run/host-services/ssh-auth.sock`).
- **File ownership on Linux** — files written to `/workspace` are owned by the
  container user's uid. On macOS Docker Desktop this is virtualized to your host
  user automatically; on native Linux, run as your own uid with
  `--user "$(id -u):$(id -g)"` if ownership matters (note: the agent's ephemeral
  HOME is owned by the image's `sandbox` user, so prefer this for non-agent runs).

## Platform support

sandbox-cli runs anywhere Docker does. Almost everything works identically across
platforms; the differences are all about the boundary the host can provide.

| Capability | macOS (Docker Desktop) | Linux (native Docker) | Windows (Docker Desktop / WSL2) |
|---|---|---|---|
| Core: `run` + the agent wrappers, mounts, env, hardening, metrics | ✅ | ✅ | ✅ |
| `--cache`, `--secret`, `--worktree`, `--git`, `--share` | ✅ | ✅ | ✅ |
| Egress allowlist (`--allow`) | ✅ ¹ | ✅ | ✅ ¹ |
| `--host-gateway` | auto ² | ✅ (needed) | auto ² |
| `/workspace` file ownership | virtualized to you | container uid ³ | virtualized to you |
| `--runtime kata-runtime` / `runsc` (microVM / gVisor) | ❌ ⁴ | ✅ ⁵ | ❌ ⁴ |

1. The firewall runs `iptables` inside the (Linux) container, so it works wherever
   the container kernel is Linux — including Docker Desktop. Verified in CI on
   native Linux; not yet independently verified on Docker Desktop.
2. `host.docker.internal` resolves automatically on Docker Desktop, so the flag is
   optional there; it's required on native Linux.
3. On native Linux, `/workspace` files are owned by the container user's uid — use
   `--user "$(id -u):$(id -g)"` if that matters. Docker Desktop virtualizes this.
4. Docker Desktop runs containers inside its own managed Linux VM and doesn't allow
   registering custom OCI runtimes — so you can't *select* Kata/gVisor. (You already
   get a VM boundary from Docker Desktop itself.)
5. Requires the runtime registered with the daemon; Kata additionally needs KVM /
   nested virtualization.

**In short:** on macOS/Windows everything works except *selecting* a microVM
runtime — and Docker Desktop already sandboxes containers in a Linux VM. For a
hardware microVM boundary you choose per run, use native Linux with Kata or gVisor.

## Configuration

Merged in precedence order (later wins): built-in defaults →
`~/.config/sandbox/config.yaml` → nearest `.sandbox.yaml` (walking up from cwd) →
CLI flags. Run `sandbox-cli config show` to see the effective config.

```yaml
# .sandbox.yaml
image: sandbox-base:0.0.1-9f95ae16   # default; tag is content-addressed
workdir: /workspace
user: sandbox           # non-root; agents refuse --dangerously-skip-permissions as root
# runtime: kata-runtime # stronger isolation (microVM); or runsc for gVisor. default: runc
mounts:
  - { host: ./data, container: /workspace/data, mode: rw }
env:
  NODE_ENV: development
env_allow:            # default-deny: only these host vars are forwarded, if set
  - ANTHROPIC_API_KEY
  - OPENAI_API_KEY
network:
  mode: default       # default | none | allowlist
  allow:              # allowlist mode only: extra domains beyond the baseline
    - internal.registry.example.com
security:             # secure-by-default hardening; override per project/user
  no_new_privileges: true     # block setuid privilege escalation
  cap_drop: [ALL]             # drop all Linux capabilities (cap_add: [] to add back)
  pids_limit: 1024            # fork-bomb guard; 0 disables
  memory: ""                  # e.g. 2g — opt-in, empty = unlimited
  cpus: ""                    # e.g. 1.5 — opt-in, empty = unlimited
cache:                # opt-in: persist package caches across --rm runs
  enabled: false      # or use --cache; mounts named volumes for npm/pip/cargo/go
  paths: []           # extra container cache dirs beyond the defaults
snapshot:             # crash safety net; see "Crash recovery"
  enabled: true       # or use --no-snapshot
  interval: 2m        # how often the workspace is snapshotted while a sandbox runs
  retention: 336h     # 14d; older snapshots are pruned at the start of the next run
secrets:              # broker: resolve at run time, forward by name (never on the argv/dry-run)
  GITHUB_TOKEN: { command: gh auth token }   # short-lived token from your own tool
  ANTHROPIC_API_KEY: { file: ~/.secrets/anthropic }
  OPENAI_API_KEY: { env: OPENAI_API_KEY }     # from host env, but kept off the command line
```

## Security model

- **Only `/workspace` is host-connected and writable** for `sandbox-cli run`.
  `HOME`, `/etc`, `/` inside the container are ephemeral and destroyed on exit
  (`--rm`). The agent wrappers add two more host paths by default,
  both scoped and both opt-out: the sandbox-owned agent home
  (`~/.config/sandbox/agents/<agent>`, `--no-persist-auth`) and your Claude
  history for the current project (`--no-sync`). When the workspace is a git
  worktree, the parent repo's `.git` is mounted read-write too — git cannot work
  otherwise. Anything else needs `--mount`.
- **The host home is never mounted.** sandbox refuses to mount `/`, your home
  directory, or any ancestor of it as the workspace.
- **Default-deny credentials.** Nothing from your host env crosses the boundary
  unless you opt in via `env_allow` / `--env-allow` / `--env`. Each agent wrapper
  ships a suggested allowlist (e.g. `ANTHROPIC_API_KEY`) applied only if the value
  is set. For OAuth-file logins, mount just the agent's own dir, e.g.
  `--mount ~/.claude:/sandbox/home/.claude:rw`.
- **Credential broker.** For secrets you'd rather not put on the command line or
  in a config file, `secrets:` / `--secret NAME=file:PATH|cmd:COMMAND|env:VAR`
  resolves the value at run time (from a file, a host command like `gh auth
  token` / `op read`, or a host env var) and forwards it into the container *by
  name*, so the raw value never appears on the docker argv, in `--dry-run`, in
  config, or in shell history — and `cmd:` sources can be short-lived tokens
  fetched fresh each run. (The agent process still receives the value as an env
  var; hiding it from the agent entirely needs a header-injecting egress proxy,
  which is future work.)
- **Hardened container by default.** Every run drops all Linux capabilities
  (`--cap-drop=ALL`), forbids privilege escalation (`--security-opt
  no-new-privileges`), and caps process count (`--pids-limit`) to blunt fork
  bombs. Tune these under `security:` in config; add memory/CPU limits with
  `--memory` / `--cpus`; or use `--no-hardening` to fall back to the unhardened
  behavior while debugging.
- **Optional egress allowlist.** With `network: allowlist` (or `--allow DOMAIN`),
  outbound traffic is default-denied by an in-container firewall that permits only
  DNS, established flows, a baseline of agent APIs + package registries
  (`api.anthropic.com`, `registry.npmjs.org`, `pypi.org`, `github.com`, …), and any
  domains you add. This lets `npm install` / `pip install` / `git` keep working
  while blocking arbitrary exfiltration from a prompt-injected agent. The firewall
  is programmed at startup (needs `NET_ADMIN`, added only in this mode) and then
  the run drops back to the non-root `sandbox` user; it fails closed if setup
  errors. Requires a Linux-capable Docker host (iptables); resolves domains to IPs
  once at startup, so extremely dynamic CDNs may need extra `allow` entries.

Deliberately out of scope, with clean seams left in the code for them: a
header-injecting secrets proxy (so the agent never sees the raw value),
per-request egress policies, snapshots, risk scoring, and audit trails.

## Alternatives & prior art

Running an agent in a disposable container is a crowded space: there are official
options (Docker Sandboxes' `sbx`, Anthropic's devcontainer and Sandbox Runtime,
Codex's built-in OS sandbox) and many open-source twins. sandbox-cli's edge is code
quality and a focused feature set (tested isolation invariants, default-deny env,
dual-agent wrapping, observability) rather than a hard security boundary — for that,
reach for microVM tooling.

| Feature / Aspect | sandbox-cli (Amitgb14) | Built-in agent sandboxes (Claude/Codex) | Docker Sandboxes (`sbx`) | Native OS tools (Seatbelt/Landlock) | Cloud microVMs (E2B, Daytona, …) |
|---|---|---|---|---|---|
| Isolation strength | Good (Docker + hardening; optional gVisor/Kata) | Medium (OS-level, shared kernel) | Excellent (microVM / Firecracker) | Good (kernel/OS primitives) | Excellent (microVMs) |
| Local / no cloud | Yes | Yes | Yes | Yes | No |
| Persistent agent auth | Excellent (dedicated persistent home) | Varies | Good | Varies | Varies |
| Package cache persistence | Yes (`--cache` volumes) | Limited | Good | Manual | Often built-in |
| Parallel agents (worktrees) | Excellent (built-in `--worktree`) | Poor | Good | Poor | Varies |
| Credential broker | Excellent (`--secret` with file/cmd/env) | Basic | Good (proxy) | Varies | Good |
| Egress / network control | Strong (allowlist with baselines) | Basic | Strong | Varies | Strong |
| Observability / metrics | Excellent (live gauge, stats, summaries) | Limited | Good | Poor | Varies |
| Project config | Excellent (`.sandbox.yaml`) | Limited | Good | Poor | API / config |
| Dry-run / preview | Yes | No | Varies | No | Varies |
| Ease of use | High (CLI-focused, good docs) | High | High | Medium | Medium (setup) |
| Cross-platform | Good (macOS/Linux/Windows) | Good | Excellent | Platform-specific | N/A |
| Docker dependency | Yes | No | Yes | No | No |
| Best for | Local multi-agent workflows, ergonomics | Quick minimal protection | Strongest local isolation | Lightweight, zero deps | Scale & long-running tasks |

This is our own read of the landscape, and the ratings for other projects are a
snapshot that will age — check their docs before choosing.

## Development

```sh
make build             # -> bin/sandbox-cli
make install           # go install ./cmd/sandbox-cli
make test              # unit tests (no Docker)
make test-integration  # end-to-end tests (requires Docker)
make fmt               # gofmt -w .
make snapshot          # dry-run release into ./dist (needs goreleaser)
```

See [`docs/DEVELOPMENT.md`](docs/DEVELOPMENT.md) for the full workflow — every
make target, single-test commands, release engineering, and the macOS install
gotchas (PATH shadowing and re-signing after `cp`).

Releases are built by GoReleaser (`.goreleaser.yaml`) and published by CI when a
version tag is pushed — see `.github/workflows/release.yml`.

The isolation invariants live in one pure function, `runtime.BuildArgs`, and are
asserted by `internal/runtime/args_test.go` and the `--dry-run` golden test in
`internal/cli/dryrun_test.go`.
