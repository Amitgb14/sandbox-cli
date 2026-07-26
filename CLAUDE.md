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
  `timezone.go` forwards the host zone as `TZ` so timestamps written in the container (a git
  commit above all) carry the user's offset instead of the image's UTC — a **name**, never a
  mount of `/etc/localtime`, since a name is a string and a mount is another host path. It
  yields to any `TZ` the user set themselves, and an unresolvable zone forwards nothing rather
  than guessing. `hostTimezone` is a var so tests can pin the one input that differs per machine.
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
  `baseline: false` drops the built-in domains so `allow` is the whole list — it exists because
  `allow` could only ever *add*, leaving no way to decline `github.com`, which is a write endpoint
  and so an exfiltration channel for any token the agent holds. It is tri-state (`*bool`) for the
  same reason the security fields are: a nearer config must be able to turn it back **on**. The
  edge that matters is the empty one — the firewall is wired only when there are domains to
  permit, so `BuildSpec` **refuses** an allowlist that resolved to nothing rather than handing back
  a container with no filtering at all, which is the strictest request producing the weakest
  result. `mode: none` is how you ask to reach nothing.

  Two limits worth knowing before trusting the mode. The rules match resolved **IPs**, not names,
  so a host sharing an allowlisted address rides in on it (`gist.github.com` is reachable under the
  baseline via `github.com`'s IP, and it was never listed) — and names are resolved **once** at
  container start, so a rotating record can break a domain the user did allowlist, mid-session. It
  is also egress-only: `INPUT`/`FORWARD` are left at ACCEPT, so anything that can reach the
  container on the bridge gets a bidirectional channel that the allowlist never sees (the reply
  rides the `ESTABLISHED,RELATED` rule). Fixing either properly means name-based enforcement — the
  egress proxy `internal/creds` already names as future work.
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
- **`internal/agentusage`** — how much of the subscription window is spent and when it
  resets, for the host side (`sandbox-cli usage`). The container does not use it: Claude
  pipes the status line a documented `rate_limits` object, and reaching past a supported
  contract for an internal file to get the same two numbers would be a bad trade. Off-line
  there is no such pipe and no supported query at all, so this reads the cache Claude Code
  keeps for its own `/usage` (`~/.claude.json` → `cachedUsageUtilization`) — which makes the
  two rules non-negotiable: **read only** (nothing here ever opens that file for writing;
  it belongs to Claude Code), and **always aged** (every `Snapshot` carries the `fetchedAtMs`
  it came with and the command always prints it — these refresh only when the agent talks to
  the server, so an unlabelled percentage can be hours stale). A shape the parser no longer
  recognizes yields *no windows* rather than a zero, the same bargain `agentctx` makes with
  transcripts — and a window **past its reset** prints no percentage either, because the
  cached figure then measures the period before the reset rather than a stale amount of the
  current one. Two candidate paths — the persisted agent HOME and the user's real home —
  resolved by whichever was refreshed last, since both describe the same account.
  `usage --refresh` (`refresh.go`) is the only way to make a reading current: it runs one
  throwaway `claude -p` turn, in a scratch cwd so the turn stays out of the project's
  transcript history, and re-reads. That keeps both rules — Claude Code still writes its own
  file, we only give it a reason to — and it stays opt-in because the request is spent from
  the window being measured. It bounds staleness to Claude Code's own refetch interval; it
  does not stamp the reading now, so the printed age still governs.
  Design: `docs/proposals/usage-stats.md`.
- **`internal/creds`, `internal/audit`** — deliberate **stub seams** for a future credential broker
  and audit trail. Today nothing extra is forwarded and audit goes to a no-op sink; keep these seams clean.

### The trust boundary (read before touching config, mounts, or the entrypoint)

An audit found the container→host boundary does not hold, and the fixes are only
partly landed. `docs/proposals/security-hardening.md` has the reproduced findings,
the threat model and the phased plan. Three rules that follow from it:

- **A project `.sandbox.yaml` is untrusted input** and the privilege-relevant keys are
  *refused* from it (`internal/config/trust.go`): `image`, `workdir`, `user`, `home`,
  `runtime`, `mounts`, `secrets`, `env`, `env_allow`, `security.*`, `cache.paths`, and
  any `network.mode`/`network.baseline` that **weakens** what is already in force. A
  project may tighten (`default` → `allowlist` → `none`), never loosen. The escape
  hatches are the user's own config and an explicit `--config <path>`, where typing the
  path is the deliberate act discovery never involves. Discovery is also bounded — it
  stops at the repository root, else the home directory, else the starting directory —
  so a `.sandbox.yaml` in a shared parent like `/tmp` is no longer picked up.
  When adding a config key, decide which side it is on: if a hostile repo setting it
  could widen what the container reaches or reach the host, it belongs on the refused
  list, and `TestProjectConfigRefusesPrivilegedKeys` is where that gets pinned.
- **Anything running as root before the privilege drop must resolve names from the
  image only.** The entrypoint scripts pin an absolute interpreter and reset `PATH`
  as their first statement, and the agent's writable HOME is deliberately *not* on
  the image `PATH` (`assets/Dockerfile`). This is why an agent can no longer plant a
  `bash` in its own HOME and have root run it.
- **`SANDBOX_RUN_AS` and `SANDBOX_EGRESS_ALLOW` are instructions, not settings**
  (`config.IsReservedEnv`). They cannot be set or forwarded from outside. The list is
  exact names, not a `SANDBOX_*` prefix, because `SANDBOX_STATUSLINE_*` is a documented
  user knob read *after* the drop — check which side of the drop a new variable lands on
  before adding it.

- **Host-side `git` runs inside a repository the agent controls**, so every git
  invocation sandbox-cli makes on its own behalf goes through `internal/githard`
  (`internal/rescue`, `internal/worktree`). git is a programmable tool: `add -A`
  runs `filter.*.clean` and `core.fsmonitor`, `update-ref` runs the
  `reference-transaction` hook, `worktree add` runs smudge filters and
  `post-checkout`, `show`/`diff` run `diff.*.textconv` — every one of them naming
  a command from a file the agent can write. `githard.Args()` overrides the
  config keys (`-c` outranks the repo's local config, which
  `GIT_CONFIG_GLOBAL/SYSTEM` do **not** cover); `githard.Env()` points
  `GIT_ATTR_SOURCE` at an empty tree, because filter driver names come from
  `.gitattributes` and cannot be enumerated ahead of time. The deliberate
  exception is `worktree.Git` — `sandbox-cli worktree git …` is the user's own
  command in their own repo. Adding a git call that bypasses `rescue.run`/
  `runGit` reopens this; `internal/rescue/hostile_repo_test.go` is the guard.

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

### The memory/CPU/usage status line

Only `claude` gets one on screen, and that is a deliberate limit, not an oversight:

- **`claude`** — Claude Code's `statusLine` hook runs `sandbox-statusline` (baked in the image)
  and renders it in its own UI. Injected via a managed-settings.json mounted read-only, which
  never touches the user's own Claude settings. `--no-statusline` opts out.
- **every other agent** — nothing on screen. Neither Gemini CLI nor OpenCode has a
  status-line hook (verified upstream; see `docs/proposals/agent-adapters.md`). Running them
  inside tmux to get one was tried and reverted: it made the agents' TUIs render badly, which is
  a bad trade for a gauge. `sandbox-cli stats` in a second terminal is the answer for these.

Don't reach for a terminal multiplexer here again without checking that the agent's UI survives it.

The line also carries the subscription windows and the model
(`· opus 5 · mem … · 5h 23% (2h14m) · wk 49%`), both from the JSON Claude already pipes to
the hook — `rate_limits` and `model.display_name`, documented fields, so the script needs no
mount and no file whose shape it is not entitled to know. Two rules hold it together:
**absent means absent** (no `rate_limits`, API-key auth, before the first response, one
window and not the other — all print nothing, never a placeholder), and **what the agent
reports about itself is dropped before the branch** when the row is too narrow — usage
first, then the model, so a terminal that fit the line before this feature still shows
exactly what it did. `SANDBOX_STATUSLINE_NO_USAGE=1` / `_NO_MODEL=1` opt out individually.
Design and rejected alternatives: `docs/proposals/usage-stats.md`.

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
