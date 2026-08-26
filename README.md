# sandbox-cli

Run AI coding agents (Claude Code, Codex, Gemini, OpenCode, Cline, Goose, Crush,
Copilot CLI, Cursor, Qwen, OpenHands, Droid, Devin, Kilo Code) — or any
command — inside a **disposable, isolated Docker container**. Only the project
you choose is mounted at `/workspace`; `HOME` is a fake, ephemeral directory. A
mistaken `rm -rf ~` or a prompt-injected command can't touch the rest of your
machine.

```
        Host                                Sandbox (container, --rm)
  ~/projects/myapp  ── bind ──►  /workspace   (the only host-connected path)
  ~/.ssh ~/.aws ~/  ── NOT mounted            HOME=/sandbox/home  (ephemeral)

  (the agent wrappers additionally mount a sandbox-owned agent home and,
   for claude, your history for this one project — both opt-out)
```

Developers want to run agents with full autonomy (`--dangerously-skip-permissions`
/ "Allow All") but don't want the agent to have unrestricted access to their host
filesystem and credentials. sandbox-cli gives the agent the convenience of "Allow
All" while limiting the blast radius to the project it's already meant to edit.

## Install

```sh
curl -fsSL https://raw.githubusercontent.com/Amitgb14/sandbox-cli/main/install.sh | sh
```

Needs Docker (Docker Desktop on macOS/Windows, [Docker Engine on
Linux](docs/platforms/linux.md); [Podman](docs/platforms/podman.md) also works).
The installer verifies the release against its checksums, installs to
`~/.local/bin`, and writes a commented `~/.config/sandbox/config.yaml` if you
don't have one. Other routes — `go install`, a pinned version, Windows,
uninstall — are in **[Install](docs/install.md)**.

## Quick start

```sh
cd ~/your-project

sandbox-cli claude                       # an agent, in a container (logs in the first time)
sandbox-cli run -- npm test              # or any command at all
sandbox-cli run --dry-run -- npm test    # see the exact docker argv first

sandbox-cli claude --worktree feature-a -- -p "implement A"   # its own branch, its own container
sandbox-cli list                         # what is running right now
sandbox-cli doctor                       # is my setup ready?
```

Everything after a leading run of sandbox flags is forwarded to the agent
verbatim, so `sandbox-cli claude --dangerously-skip-permissions` just works —
the exact rule is in [Running commands and agents](docs/usage/flags.md).

## What you get

- **A boundary you can read.** Only `/workspace` is host-connected; `/`, your home
  directory and any ancestor of it are refused as a workspace by rules no flag
  overrides. Host environment variables are default-deny.
- **Egress allowlist, on by default.** Outbound traffic is default-denied by an
  in-container firewall that permits DNS, a baseline of agent APIs and package
  registries, and whatever you add with `--allow` — so `npm install` works and
  arbitrary exfiltration doesn't. It fails closed.
- **Logins that survive `--rm`.** Each agent gets its own sandbox-owned home, kept
  separate from your real `~/.claude`. Claude's history for the current project is
  shared both ways, so `--resume` works on either side.
- **Parallel agents on real git worktrees.** One branch each, one container each,
  your checkout untouched — a single agent with `--worktree`, or a whole
  [`fleet.yaml`](docs/usage/fleet.md) whose work is checked by a `verify:` command
  before it can land.
- **Sessions you can supervise.** A container outlives the process that started
  it, so `list`, `logs`, `attach` and `kill` address one by id, name or branch —
  and never reach a container sandbox-cli didn't start.
- **A crash safety net.** The workspace is snapshotted into your own repo under
  `refs/sandbox/` while a run is in flight; `sandbox-cli recover` puts it back on
  a branch without touching your index, HEAD or working tree.
- **Two profiles, neither of them lax.** `dev` warns when the host can't deliver a
  control; `prod` refuses, and doesn't mount the persisted credential at all.

## Studio, in the browser

The same runs, with a control plane you can look at: launch, watch the terminal,
read the diff, land the branch. From the repository you want to work in:

```sh
curl -fsSL https://raw.githubusercontent.com/Amitgb14/sandbox-cli/main/studio.sh | sh
```

That installs `sandbox-cli` and `sandbox-studio-api` from one release archive,
pulls the UI image, and starts both halves on loopback — **the UI in a
container, the API as an ordinary host process**. The split is the point rather
than an accident: the API launches containers, so in a container it would need
the host's docker socket, and a process holding that socket can start a
container mounting `/`. `--api-in-docker` takes that path for anyone who wants
it, and says what it costs first.

Studio owns no isolation policy of its own. A run it starts builds the same
options a `--worktree --detach` run does, so every boundary above holds
unchanged — which is also why it can be a web page at all.

It manages **one repository at a time**: the one it was started in. Point it
elsewhere without moving — `sh studio.sh up --project ~/other-project` — or run
`up` from inside that repository. Either restarts the pair on the same ports and
token, so an open tab follows, and `sh studio.sh status` always prints which
repository the daemon reports.

To remove it, the installer is enough — no copy of `studio.sh` required:
`install.sh --uninstall` stops the UI container and the host API process,
removes both binaries, and lists what it left ([Uninstall](docs/install.md#uninstall)).

## Documentation

Start at the **[documentation index](docs/README.md)**, or jump to:

| | |
|---|---|
| [User guide](docs/GUIDE.md) | The walkthrough: first run, everyday use, every feature |
| [Agent reference](docs/AGENTS.md) | All 13 agents, their prerequisites and login flows |
| [Commands and flags](docs/usage/flags.md) | Every sandbox flag, and how flags reach the agent |
| [Sessions](docs/usage/sessions.md) | `list`, `attach`, `logs`, `kill`, `clean` |
| [Worktrees](docs/usage/worktrees.md) · [Fleet](docs/usage/fleet.md) | One agent per branch, and many at once |
| [Crash recovery](docs/usage/recovery.md) | What to run when a sandbox died mid-write |
| [Configuration](docs/configuration.md) | The two config files, and which keys a project may not set |
| [Security](docs/security/README.md) | Profiles, `doctor`, the security model, stronger isolation |
| [Platform support](docs/platforms/README.md) | The matrix, plus [Linux](docs/platforms/linux.md) and [Podman](docs/platforms/podman.md) |
| [Studio](docs/studio-api/README.md) | The browser control plane, its HTTP API, and who may drive it |
| [Alternatives](docs/alternatives.md) | How this compares, including where it loses |

## Security

> A full security audit of this codebase was carried out on 2026-07-26: 22 issues
> found, all reproduced end to end and all fixed. A same-day re-audit of those
> fixes, and a later external review of the pull request, each found more; those
> are fixed too. The ledger is
> [`docs/security/audit-2026-07-26.md`](docs/security/audit-2026-07-26.md) and the
> live backlog is [`open-items.md`](docs/security/open-items.md).

The isolation invariants live in one pure function, `runtime.BuildArgs`, and are
asserted by `internal/runtime/args_test.go` and the `--dry-run` golden test in
`internal/cli/dryrun_test.go`. A project `.sandbox.yaml` is treated as untrusted
input: the privilege-relevant keys are refused from it. Full model:
[Security](docs/security/README.md).

## Development

```sh
make build             # -> bin/sandbox-cli
make install           # go install ./cmd/sandbox-cli
make test              # unit tests (no Docker)
make test-integration  # end-to-end tests (requires Docker)
make fmt               # gofmt -w .
```

[`docs/DEVELOPMENT.md`](docs/DEVELOPMENT.md) has the full workflow — every make
target, single-test commands, release engineering, and the macOS install gotchas.
Releases are built by GoReleaser and published by CI when a version tag is pushed.

## What's next

Six pieces of work, in order, each with its own scope document under
[`docs/roadmap/`](docs/roadmap/README.md):

1. [Better local / dev agent experience](docs/roadmap/task-1-local-agent-experience.md) — *shipped*
2. [Multi-agent support](docs/roadmap/task-2-multi-agent.md) — *shipped*
3. [Stronger isolation for Linux production](docs/roadmap/task-3-stronger-isolation.md) (Kata) — *next*
4. [Run provenance](docs/roadmap/task-4-run-provenance.md) — *not started*
5. [Checkpoint and fork](docs/roadmap/task-5-checkpoint-and-fork.md) — *not started*
6. [macOS microVM](docs/roadmap/task-6-macos-microvm.md) — *not started*

The roadmap index also records what is deliberately deferred and what has been
**considered and declined**, with reasons — which is most of the rest.
