# Proposal: the multi-agent fleet — launch, supervise, land

**Status:** Phase 2 is implemented and shipped as `--detach` (detached runs,
`sandbox.*` labels, deterministic container names, `Runtime.Start`). Phases 1 and
3–6 are proposed.
**Depends on:** `--worktree` (shipped), `worktree commit`/`git` (shipped).
**Reviewed in:** [`multi-agent-fleet-review.md`](multi-agent-fleet-review.md) — read
that before writing code. The phases below already incorporate the mechanical
corrections it found; what it *changes* is the order they should be built in, and
what it *adds* is two shared-state hazards this document does not address.

## Problem

sandbox-cli's strongest differentiator is not isolation depth. Docker, gVisor and
Kata are commodity, and the first parties are shipping their own agent sandboxes.
The differentiator is the workflow around running *several* agents at once — and
that layer is half-built.

- **Launch is manual.** `--worktree BRANCH` (`internal/cli/root.go:107-122`,
  `internal/worktree/worktree.go`) gives each agent its own branch, checkout and
  container, but every run is foreground. N agents means N terminals.
- **Supervise does not exist.** `worktree list` prints branch → path;
  `sandbox-cli stats` (`internal/cli/stats.go`) prints memory and CPU for running
  containers. Neither answers the only question that matters mid-flight: *which
  agents are still working, which finished, which failed, and which produced
  anything.* Nothing even correlates a container to a branch — `containerName()`
  (`internal/sandbox/spec.go:315-317`) is a bare timestamp.
- **Land is partly there.** `worktree commit` / `worktree git`
  (`internal/cli/worktree.go`) already operate by branch name without cd-ing
  anywhere. What is missing is the last hop into the base branch, and any guard
  against landing work whose agent is still running.

This milestone closes the loop: fan out from one file, watch one table, land by
branch name.

## Design constraints

These are the four rules the design is answerable to. A change that breaks one of
them is a different proposal, not an implementation detail.

1. **Docker is the state store — sandbox-cli gains no daemon and no database.**
   State comes from `docker ps -a` + `docker inspect` + `docker logs`, keyed by
   labels. Nothing to corrupt, nothing to garbage-collect after a crash.
2. **One agent per worktree, enforced.** Two agents in one checkout is silent
   data loss. A launch is refused when a container already holds that branch.
3. **Detached runs must be non-interactive.** A detached agent that stops at a
   permission prompt hangs forever, so the fan-out builds an autonomous argv per
   agent and says so loudly in the docs.
4. **The isolation invariants are unchanged.** Detached runs get the identical
   mount set, fake HOME, and hardening as their foreground twin. The single
   deliberate exception is `--rm`: a detached container is retained *only* so its
   logs and exit code survive, and it is reaped by `fleet clean` / `fleet land`.

## Phase 1 — `internal/agents`: agent descriptors as data

Each wrapper hard-codes its own env allowlist and persist dir
(`internal/cli/claude.go:17-23`, `internal/cli/codex.go`). The fleet needs the
same knowledge, and duplicating it would guarantee drift.

A new package `internal/agents` holds a `Descriptor`: name, the guest argv (a
binary name for the baked agents, or the `agentBootstrap` script for the rest),
`EnvAllow`, `PersistDir`, and `Autonomous(prompt string) []string` returning the
non-interactive argv — `claude -p <prompt> --dangerously-skip-permissions`,
`codex exec <prompt>`. Only agents with a verified headless mode get an
`Autonomous`; the fleet refuses the others by name.

Lookup is by agent name, and that is the whole mechanism behind a mixed fleet:
resolving a descriptor per task rather than once per file is what lets one
`fleet.yaml` run task A under `claude` and task B under `codex`.

## Phase 2 — detached runs in the runtime layer

- **`internal/sandbox/spec.go`** — `Options` gains `Detach`. `BuildSpec` folds it
  in: `Remove: !opts.Detach`, `TTY: false`, metrics and summary off, and the
  labels `sandbox.repo`, `sandbox.branch`, `sandbox.agent`, `sandbox.base`
  whenever the branch is known. `containerName()` becomes
  `sandbox-<repo>-<branch>` for detached runs (reusing `sanitizeBranch`,
  `internal/worktree/worktree.go:317`) and keeps its timestamp for foreground
  ones. The repo component is the identity `worktreeBase` already computes
  (`internal/worktree/worktree.go:300-308`), exported as `worktree.RepoID`.
- **`internal/runtime/runtime.go`** — `RunSpec` gains `Detach bool` and
  `Labels map[string]string`; the `Runtime` interface gains `Start`.
- **`internal/runtime/args.go`** — emit `-d` in place of `-i`/`-it` when
  `Detach`, and `--label k=v` in sorted-key order, mirroring the existing
  `sortedKeys` treatment of `Env` (`internal/runtime/args.go:87-90`). `BuildArgs`
  stays a pure renderer: it never *decides* that a detached container keeps its
  filesystem, it only renders the spec that already says so.
- **`internal/runtime/docker_cli.go`** — a `Start` path alongside `Run`
  (`internal/runtime/docker_cli.go:140-173`): capture the container name from
  stdout, skip the metrics gauge and the terminal-mode restore, return
  immediately. Reuse the existing `checkRuntime` preflight.

## Phase 3 — `internal/fleet`: the orchestration package

The schema (`yaml.v3`, already a dependency):

```yaml
agent: claude            # default adapter; any task may override it
base: main
max_parallel: 3
defaults: { memory: 4g, cpus: "2", allow: [example.com] }
tasks:
  - branch: feature-a
    prompt: implement the login form
  - branch: feature-b
    prompt: add rate limiting
    args: [--model, opus]
  - branch: feature-c
    agent: codex         # a different adapter, same fleet
    prompt: port the CLI tests to table form
    allow: [api.openai.com]
```

**One fleet, many adapters.** `agent:` at the top level is a default, not a
constraint: any task may name its own. Everything that differs per agent is
already per-agent data — the autonomous argv, the `EnvAllow` list, and the
persisted HOME (`~/.config/sandbox/agents/<name>`) all come from that task's
Phase 1 descriptor — so a mixed fleet costs the launcher nothing beyond resolving
a descriptor per task instead of once. The `sandbox.agent` label stops being a
constant and starts being the reason `fleet status` has an AGENT column.

Three consequences worth stating in the docs rather than discovering:

- **Each adapter needs its own login, done before the fleet runs.** The persisted
  HOMEs are separate by design, so a mixed fleet is a mixed set of credentials.
- **`--allow` domains are per-agent.** The baseline covers Anthropic and OpenAI
  only; `docs/AGENTS.md:345` lists what each adapter needs on top. In a mixed
  fleet that makes `allow` a per-task field, not just a `defaults:` one — the
  union across agents is wider than any single task should be granted.
- **Only agents with a verified headless mode may appear.** Today that is
  `claude -p`, `codex exec` and `droid exec` (`docs/AGENTS.md:93`, `:111`,
  `:327`). Validation rejects any other name at parse time, before a single
  container starts, because the alternative is an unattended TUI waiting forever
  on a keystroke.
- **Run each adapter once by hand first.** Anything not baked into the image
  installs itself on first use (`agentBootstrap`, `internal/cli/agents.go:73`).
  In a fleet that first run happens unattended: it needs network at that moment,
  the install host must be on the allowlist under `--allow`, and a failure
  surfaces as exit 127 with the explanation only in `docker logs`. A single
  interactive run beforehand turns that into a no-op.

Per-task caps default to something finite: five unbounded agents will OOM a
laptop, and `Memory`/`CPUs` are opt-in today (`internal/config/config.go:55-56`,
empty means unlimited). `memory`, `cpus` and `allow` resolve the same way as
`agent` — task value first, then `defaults:`, then config.

**Launch**, per task: resolve the task's descriptor by agent name, `worktree.Resolve`
(existing), build `sandbox.Options` with `Detach`, the labels, that descriptor's
`EnvAllow` and persisted HOME, and its autonomous argv, then `Start`. The image is
built **once** before the fan-out (`EnsureImage` today lives inside
`Session.Run`, `internal/sandbox/sandbox.go:51`, which would mean N concurrent
builds on a cold cache). One-agent-per-worktree needs no check: the deterministic
container name makes docker itself refuse the second launch, atomically.

`max_parallel` caps the launch burst rather than maintaining a running count —
maintaining one would require a supervisor process, which constraint 1 forbids.
`fleet run` is idempotent: it skips branches that already have a container and
fills whatever slots are free, so re-running it *is* the queue, and it reports
what it left unstarted.

**Status** joins `docker ps -a --filter label=sandbox.repo=<id>` (state, uptime,
exit code) with `worktree.List`, the existing `worktree.Dirty(dir, branch,
limit)`, and a new `worktree.Ahead(dir, branch, base)` built on the existing
`runGit` helper.

## Phase 4 — `sandbox-cli fleet` commands

A new `internal/cli/fleet.go`, following the `newWorktreeCmd` subcommand shape:

| Command | Behavior |
|---|---|
| `fleet run -f fleet.yaml` | fan out; print the launched table; `--dry-run` prints each docker command |
| `fleet status` | the supervisor table: branch, agent, state, uptime, dirty, ahead |
| `fleet logs BRANCH [-f]` | `docker logs` passthrough, addressed by branch |
| `fleet stop BRANCH\|--all` | `docker stop` |
| `fleet land BRANCH` | commit + merge into base (Phase 5) |
| `fleet clean` | reap exited fleet containers and their worktrees |

`fleet run --dry-run` renders through the existing `dockerCommandLine`
(`internal/cli/run.go:165`) so the detached argv is covered by a golden test.
`sandbox-cli stats` switches from its `name=sandbox-` prefix filter
(`internal/cli/stats.go:102`) to the label filter, so there is one addressing
scheme rather than two.

## Phase 5 — `fleet land`

Deliberately conservative — it is the only command that writes to the base
branch:

1. Refuse if that branch's container is still running.
2. Refuse if a container is running against the main checkout itself.
3. Commit any dirty worktree via the existing `worktree.Git(dir, branch, ...)`
   passthrough, reusing the `worktree commit` flow.
4. Refuse if the main checkout is dirty, or on a branch other than the recorded
   `sandbox.base`.
5. `git merge --no-ff <branch>`, printing the exact command first. No force, no
   auto-resolve; on conflict, stop and point at the worktree.
6. On success, offer to `fleet clean` that branch.

## Phase 6 — documentation (ships with the code, not after)

- **`docs/GUIDE.md`** — a "Running a fleet" section after the existing "Parallel
  agents with git worktrees" (`docs/GUIDE.md:205`), carrying the full loop: write
  `fleet.yaml` → `fleet run` → `fleet status` → `fleet logs` → `fleet land`. It
  states plainly that detached runs are autonomous by construction, that detached
  containers are retained until reaped and why, and the one-agent-per-worktree
  rule. A mixed-adapter example earns its own subsection, with the three
  consequences from Phase 3 (a login per adapter, `--allow` domains per agent,
  and one interactive run per adapter before it joins a fleet).
- **`README.md`** — the multi-agent section leads with `fleet` and `--worktree`
  becomes the single-agent case beneath it. Short: the full loop lives in the
  guide.
- **A `fleet.yaml` example** under `docs/examples/`, referenced from
  `sandbox-cli fleet run --help`.
- **`docs/proposals/pinned-contracts.md`** — a note that the fleet is the
  sequencing mechanism its "sequencing is the shell" section assumes. That
  proposal stays unimplemented.

## Verification

- `make test` — new unit tests: `fleet.yaml` parse and validate (unknown agent,
  agent with no headless mode, empty branch, duplicate branch); per-task `agent`
  overriding the file-level default, and each task resolving its own descriptor,
  `EnvAllow` and persisted HOME; the autonomous argv per agent descriptor;
  `worktree.Ahead` against a temp repo (the existing worktree tests already build
  real repos in `t.TempDir()`); label emission and sort order; and
  `Detach ⇒ no --rm, no -it`.
- **The invariant tests must still pass untouched** for foreground runs:
  `internal/runtime/args_test.go` and the `--dry-run` golden
  (`internal/cli/dryrun_test.go`). A detached-spec case is added asserting the
  mount set and HOME are identical to the foreground spec.
- `make test-integration` (docker tag) — `fleet run` with a two-task file against
  a temp git repo, the two tasks naming **different** adapters: both containers
  start, `fleet status` shows both with the right agent per row, `fleet logs`
  returns output, `fleet land` merges the commit, `fleet clean` leaves no
  containers behind.
- **Manual end-to-end**, and it is the one that matters: in a scratch repo,
  `fleet run -f fleet.yaml` with two real Claude tasks. Confirm `fleet status`
  transitions running → exited, that a second `fleet run` on a still-running
  branch is refused, and that `fleet land` refuses while the agent is live.

## Not chosen

- **A supervisor daemon.** It would make `max_parallel` a true running count and
  allow push notification on exit, at the cost of a process to install, crash,
  and garbage-collect. `docker ps` answers the same questions with nothing to own.
- **Keeping `--rm` for detached runs.** The exit code and the logs are the entire
  supervision story; discarding them at exit would leave `fleet status` unable to
  distinguish "finished" from "never ran".
- **A shared message channel between fleet agents.** That is
  [`pinned-contracts.md`](pinned-contracts.md)'s territory, and its conclusion —
  that the sequencing belongs outside the containers — is what the fleet
  provides.
