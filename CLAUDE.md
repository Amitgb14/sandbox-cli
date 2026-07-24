# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

`sandbox-cli` runs AI coding agents (Claude Code, Codex, Gemini, OpenCode, Cline, Goose, Crush, Aider,
Copilot CLI, Cursor, Qwen, Amp, Continue, OpenHands, Droid) or any command inside a disposable,
isolated Docker container. Only the chosen project is bind-mounted at `/workspace`; `HOME` is a
fake ephemeral path (`/sandbox/home`) and the container is `--rm`. The goal is to give an agent
"Allow All" autonomy while limiting the blast radius to the project it is editing.

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
