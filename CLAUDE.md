# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

`sandbox-cli` runs AI coding agents (Claude Code, Codex, Gemini, OpenCode, Cline, Goose, Crush, Aider,
Copilot CLI, Cursor, Qwen, Amp, Continue, OpenHands, Droid) or any command inside a disposable,
isolated Docker container. Only the chosen project is bind-mounted at `/workspace`; `HOME` is a
fake ephemeral path (`/sandbox/home`) and the container is `--rm` (the single exception is
`--detach`, below). The goal is to give an agent "Allow All" autonomy while limiting the blast
radius to the project it is editing.

## Commands

```sh
make build              # -> bin/sandbox-cli (embeds version via -ldflags)
make install            # go install ./cmd/sandbox-cli
make test               # unit tests, no Docker required
make test-integration   # end-to-end tests; requires a running Docker daemon (builds base image on first run)
make fmt                # gofmt -w .

# Run a single test
go test ./internal/runtime -run TestBuildArgs
go test -tags docker_integration -run TestClaude ./internal/cli   # a single integration test
```

Integration tests are gated behind the `docker_integration` build tag, so `make test` (plain
`go test ./...`) never touches Docker. Go 1.25+.

## Architecture

Data flows one direction through layered packages, each behind an interface so backends can be
swapped without touching callers:

```
cmd/sandbox-cli  →  internal/cli  →  config.Load + sandbox.BuildSpec  →  runtime.BuildArgs  →  docker
```

- **`internal/config`** — the layered config schema and merge. Precedence (later wins): built-in
  `Default()` → `~/.config/sandbox/config.yaml` → nearest `.sandbox.yaml` (walking up from cwd) →
  CLI flags. Mount host paths are resolved to absolute relative to the file that declared them.
- **`internal/sandbox`** — composition layer. `BuildSpec(cfg, opts)` folds config + per-invocation
  `Options` into a fully-resolved `runtime.RunSpec`. `mounts.go/ResolveWorkspace` enforces the
  **non-overridable safety refusals**: never mount `/`, the host home, or an ancestor of it.
- **`internal/runtime`** — `BuildArgs(RunSpec) []string` is a **pure, deterministic function** that
  produces the `docker` argv. This is the single choke point for the isolation invariants (only
  declared mounts are host-connected; `HOME` is always the fake path; host home is never mounted)
  and is exhaustively unit-tested. `docker_cli.go` is the only backend today, hidden behind the
  `Runtime` interface.
- **`internal/image`** — lazily builds the embedded base image (`assets/Dockerfile`, `//go:embed`)
  on first use via the `Runtime`'s builder hook.
- **`internal/metrics`** — the sticky-footer live resource gauge for non-interactive runs only.
- **`internal/worktree`** — `--worktree <branch>` and the `worktree` subcommands. A worktree is
  addressed by **branch**, never by a directory name derived from one: an agent that runs
  `git checkout -b` inside its worktree puts the two out of sync, so `Resolve`, `Path` and `List`
  ask git which worktree has the branch checked out. The path is symlink-resolved as soon as the
  directory exists, so the string handed out when a worktree is created is the same one git reports
  when it is reused (`/var` vs `/private/var` on macOS) — the mount, `worktree rm` and
  `worktree git` all address it by that string, so the three must agree. The worktree is mounted at
  its own host path so git cannot prune it away mid-session.
- **`internal/netpolicy`** — a vestigial seam that only turns `network: none` into
  `--network none`. The egress allowlist (`network: allowlist` / `--allow`) does **not** live here:
  `config.NetworkSpec.EgressDomains` resolves it (baseline domains ∪ configured ones),
  `sandbox.BuildSpec` passes it in as `SANDBOX_EGRESS_ALLOW`, and it is enforced *inside* the
  container by `sandbox-firewall` / `sandbox-egress-setup` (`assets/Dockerfile`), which programs a
  default-deny firewall as root and then drops to the sandbox user. Keep it failing closed: a run
  that asks for an allowlist and cannot program it refuses to start rather than running open.
- **`internal/rescue`** — the crash safety net and `sandbox-cli recover`. Snapshots the workspace
  into `refs/sandbox/snapshots/<session>` while a run is in flight, using a **private
  `GIT_INDEX_FILE`** so the user's index, `HEAD`, branches and working tree are never written.
  Session manifests live outside every repo (`~/.config/sandbox/rescue/<repo-id>/`) because the
  repo is often the broken thing. Keep the rule: rescue only ever *creates* objects and refs
  under `refs/sandbox/`. Design and rejected alternatives: `docs/proposals/crash-recovery.md`.
- **`internal/agentctx`** — where each agent keeps its conversation transcripts, and the
  persisted record of what has actually been confirmed. The paths in `stores.go` are
  *candidates*, not facts: `Probe` looks for them on this machine and the `Registry`
  (`~/.config/sandbox/contexts/stores.json`) writes down what was found. The merge is
  deliberately **sticky** — a probe that finds nothing never erases a store verified
  earlier, because an agent HOME that is not mounted today is not the same as a store that
  never existed. `sessions.go` reads a verified store into `Session` values —
  the claude-jsonl reader is the only one written against a confirmed format, so
  everything else lists `Partial` (id and dates real, title and turn count shown as
  `?`). Surfaced by the single command `sandbox-cli context list` — where a store
  lives is reported *inside* that listing (inline when it is empty, under `--verbose`
  when it is not) rather than by a second command, which read as two overlapping
  things to learn. First step of
  `docs/proposals/shared-context.md`; keep the two rules that make it honest — an
  agent with no verified descriptor is reported `untracked` rather than guessed at,
  and a *user* turn is a prompt someone typed, never a tool result coming back as a
  user message (they outnumber real prompts ~30:1).
  The listing prints ids abbreviated, so `resume.go/expandResumeID` grows one back into the full id
  before the agent sees it (the agents resolve no prefixes; Claude Code rejects anything that is not
  a whole UUID). That rewrite is deliberately timid — it fires only on the token after a known
  resume argument from the verified descriptor, only within *this* project's history, and only when
  exactly one session matches; anything else is forwarded untouched, because the agent's own error
  beats resuming the wrong conversation.
- **`internal/creds`, `internal/audit`** — deliberate **stub seams** for a future credential broker
  and audit trail. Today nothing extra is forwarded and audit goes to a no-op sink; keep these seams clean.

### Two invariants to preserve when changing behavior

1. **Isolation lives in `runtime.BuildArgs` and `sandbox.ResolveWorkspace`.** Any change that could
   affect what the container can reach must keep `internal/runtime/args_test.go` and the `--dry-run`
   golden test (`internal/cli/dryrun_test.go`) honest — update the golden output intentionally, never
   just to make the test pass.

2. **The two subcommand flag-parsing modes are different on purpose** (`internal/cli`):
   - `run` — sandbox flags first, guest command after `--` (`sandbox-cli run --dry-run -- npm test`).
   - agent wrappers (one per agent, listed in `agentCmds()`) — `DisableFlagParsing: true`; `splitWrapperArgs` consumes a *leading*
     run of recognized sandbox long-flags, then forwards **everything else verbatim** to the agent, so
     `sandbox-cli claude --dangerously-skip-permissions` just works and agent short flags never collide.
     A sandbox option after the boundary needs a `--` separator.
     The **one exception** to "everything else is forwarded" is `wrapperSubcommands`
     (`context.go`): a leading token naming one of sandbox-cli's own subcommands is answered
     by sandbox-cli, so `sandbox-cli claude context list` works. It fires only without an
     explicit `--`, which is the escape hatch (`sandbox-cli claude -- context …` still goes
     to the agent). Adding a word to that list makes it un-forwardable — do it rarely.

### Detached runs

`--detach` (`run` and every wrapper) is the one case that breaks the container's usual shape, and
each break is load-bearing: `-d` **replaces** `-i`/`-it` (nobody is attached, and an agent handed a
pty draws its TUI for an audience of none), and `Remove` is false — the exit code and `docker logs`
are the entire supervision story, so `--rm` would discard both at the moment they become
interesting. Those two are resolved in `sandbox.BuildSpec`; `BuildArgs` only renders what it is
handed. Docker is the state store: a detached container is named `sandbox-<repo>-<branch>` (so
docker's own duplicate-name refusal enforces one agent per branch) and labelled with its repo,
branch, agent and base branch — a fact not stamped as a label is one no later command can recover.

### Agent wrappers

Each wrapper is one file in `internal/cli` (`claude.go`, `gemini.go`, `aider.go`, …), listed in
`agentCmds()`, carrying a suggested opt-in env allowlist (e.g. `ANTHROPIC_API_KEY`, applied only if set) and ending
in `finishAgentCmd(cmd, rf, "<agent>")` (`agents.go`), which adds the shared sandbox flags and
**persists the agent login by default** by bind-mounting a sandbox-owned host dir
(`~/.config/sandbox/agents/<name>`) as the agent's whole HOME. This is separate from the host's real
`~/.claude`. `--no-persist-auth` opts out. Agents that may be missing from the baked base image use
`npmAgentBootstrap(bin, pkg)`, which installs into the persisted HOME on first run.

`TestAgentWrappersShareTheContract` pins that shape for every wrapper. Adding an agent means: a file
in `internal/cli`, a line in `NewRootCmd`, and an entry in the test table — **no Dockerfile change**.
New agents are installed lazily by `agentBootstrap` on first use, not baked into the base image:
baking every adapter would put hundreds of megabytes in front of every user for agents most of them
will never run, while a lazily-installed adapter costs the image nothing. The four the image already
carries (claude, codex, gemini, opencode) stay baked so today's users see no change. The queue of
agents still to adapt, ordered by popularity, plus the full checklist, is in
`docs/proposals/agent-adapters.md`. User-facing setup for each agent — prerequisites, login
flow, forwarded variables, `--allow` domains — is `docs/AGENTS.md`; a new adapter needs a
section there.

### The memory/CPU status line

Only `claude` gets one on screen, and that is a deliberate limit, not an oversight:

- **`claude`** — Claude Code's `statusLine` hook runs `sandbox-statusline` (baked in the image)
  and renders it in its own UI. Injected via a managed-settings.json mounted read-only, which
  never touches the user's own Claude settings. `--no-statusline` opts out.
- **every other agent** — nothing on screen. Neither Gemini CLI nor OpenCode has a
  status-line hook (verified upstream; see `docs/proposals/agent-adapters.md`). Running them
  inside tmux to get one was tried and reverted: it made the agents' TUIs render badly, which is
  a bad trade for a gauge. `sandbox-cli stats` in a second terminal is the answer for these.

Don't reach for a terminal multiplexer here again without checking that the agent's UI survives it.

`claude` additionally read-write mounts the host's Claude history for the current project
(`~/.claude/projects/<bucket>`) into the persisted HOME by default, so host sessions resolve
inside the sandbox and vice versa. `--no-sync` opts out. This is the one default that reaches a
host path outside the workspace — keep it scoped to the single project bucket.

## Conventions

- Non-root by default (`user: sandbox`): agents refuse `--dangerously-skip-permissions` as root, and
  on macOS Docker Desktop bind-mount ownership is virtualized so files are still written as the host user.
- Module path is `github.com/Amitgb14/sandbox-cli`. Standard library + `cobra` + `yaml.v3` only.
- Do not add a `Co-Authored-By` trailer to commit messages.
- After every release (tagging a new version), update `CHANGELOG.md`: move the
  `Unreleased` entries under a new version heading dated with the release date.
