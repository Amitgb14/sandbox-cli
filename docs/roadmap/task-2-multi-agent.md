# Task 2 — Multi-agent support

**Goal.** Run several agents safely in parallel — usually one per git worktree — with basic
orchestration from the CLI. No GUI required.

**Branch.** Not started. To be cut from `main` after task 1 ships and has been used for a
while.

This task is mostly *consolidation*. Two thirds of it already exists as `worktree` and
`fleet`; what is missing is the part that makes them feel like one feature with one mental
model rather than two subsystems that happen to share a repository.

---

## What exists today

| Capability | Where | State |
|---|---|---|
| One worktree per branch (`--worktree BRANCH`) | `internal/worktree` | Good — addressed by *branch*, never by a directory name derived from one |
| `worktree list / path / rm / git / commit` | `internal/cli/worktree.go` | Good — `commit`/`git` mean no `cd` is needed |
| Detached per-branch containers | `--detach`, `sandbox.BuildSpec` | Good — named `sandbox-<repo>-<branch>`, so docker's own duplicate-name refusal enforces one agent per branch |
| A fleet from one file | `internal/fleet`, `fleet.yaml` | Good — `agent`, `max_parallel`, `defaults` (memory, cpus, allow, cache, git), `tasks` (branch, prompt, args, verify) |
| `fleet run / status / logs / stop / land / clean` | `internal/cli/fleet.go` | Good |
| `verify:` — a check that makes a run *autonomous* rather than merely headless | `internal/fleet/verify.go` | Good — runs in the container, its exit code becomes the container's, and `land` refuses a branch that failed it |
| `land`'s two refusals (recorded base vs `HEAD`; worktree must still be on the branch) | `internal/fleet/land.go` | Good — both load-bearing, neither cautious |
| Labels as the state store, `sandbox.fleet` separating a fleet run from someone's live `--detach` session | `internal/sandbox/labels.go` | Good |
| Agents with a verified headless mode, as data | `internal/agents` | Good — only agents that will not stop to ask permission are in it |
| A cross-sandbox handoff channel (`--share`, `--share-name`) | `internal/cli/share.go` | Exists, opt-in |

**The isolation rule that constrains everything here:** the fleet owns *no* isolation
policy. Every task becomes the same `sandbox.Options` a `--worktree` run produces, with
`Detach` set. That inheritance is a rule with teeth — a gate added to the run path must be
repeated here, and the one that was missed (`persist_auth`) meant prod's "the refresh token
is never mounted" held for `run` and not for `fleet`. Any feature below that adds a knob
adds it in one place, for both.

---

## Required features

### 1. One mental model with task 1

A fleet container is a session. After task 1 exists, this task makes that true everywhere:

- Fleet runs appear in `sandbox-cli list` (they already carry the labels), marked as
  fleet rather than interactive.
- `attach`, `logs` and `kill` work on a fleet session by branch name, so there is one way
  to reach a running agent regardless of how it was started.
- `fleet status` stays branch-oriented — the branch is the fleet's unit of work — but its
  ID column matches `list`'s.

### 2. Fleet lifecycle gaps

- **`fleet land --all`** — land every branch that passed its verify, in order, stopping at
  the first conflict. Landing five branches one command at a time is the common case
  today.
- **Re-run a single task** (`fleet run --only BRANCH`) without editing the file, for the
  one task that failed.
- **Resume an interrupted `fleet run`** — with `max_parallel` set, the command stays
  attached to start later tasks as earlier ones exit; a Ctrl-C there currently ends the
  scheduling, not the running containers. It should be resumable rather than restart-only.
- **`fleet status --watch`** — the thing people currently do with `watch -n2`.

### 3. Per-task expressiveness

- **A different agent per task** (`agent:` on a task, overriding the spec-level one), so a
  fleet can put Claude on one branch and Codex on another and compare.
- **Per-task `defaults` overrides** — memory, cpus, allow — for the one task that needs
  more than the rest.
- **Explicit ordering or dependency** between tasks, if and only if a real case turns up.
  Recorded here so it is a decision rather than a drift; the default answer is no, because
  a dependency graph is the beginning of a workflow engine and this is a CLI.

### 4. Concurrency and resource sanity

- `max_parallel` counts only fleet containers today (correctly — otherwise one open
  interactive session blocks a `max_parallel: 1` fleet forever). It should also refuse
  obviously unrunnable fleets up front: N tasks × the per-task memory limit against what
  the machine has.
- A per-fleet total budget, if the above proves too blunt.

### 5. Getting work between agents

`--share` / `--share-name` is the mechanism and it is deliberately opt-in — a
cross-project channel is exactly the reach the sandbox otherwise refuses. What is missing
is the *convention*: a documented pattern for how two fleet agents hand a file over, and
whether a fleet should get a namespace per run automatically.

Explicitly **not** an agent-to-agent protocol. Files in a shared directory, or nothing.

### 6. Documentation

`docs/GUIDE.md` already has "Running a fleet" and the worktree cycle. This task merges
them into one narrative — parallel agents, from `--worktree` for one to `fleet` for many —
because today they read as two unrelated features.

---

## Open questions

Recorded rather than guessed:

- Does `fleet` become the front door for parallel work, with `--worktree` as the
  single-agent special case, or do they stay separate commands?
- Should `land` gain a `--no-ff` alternative (rebase, squash) or stay one merge strategy?
  One strategy is easier to reason about when the thing being merged was written
  unattended.
- Is there a case for a fleet across *repositories*, or is one repo the boundary?

---

## Not in this task

- A GUI, a TUI dashboard, or a web view.
- A2A or any agent-to-agent messaging protocol.
- Distributed execution — every agent in a fleet runs on this machine.
- [Task 3](task-3-stronger-isolation.md)'s stronger runtimes. A fleet inherits whatever
  isolation a single run has; when task 3 lands, the fleet gets it for free, and that is
  the whole benefit of the fleet owning no policy of its own.

---

## Done when

1. A running fleet agent can be listed, attached to, followed and stopped with the same
   commands as any other session.
2. `fleet land --all` and `fleet run --only BRANCH` exist, and an interrupted scheduled
   run can be resumed.
3. A fleet can mix agents across tasks.
4. The guide has one story about running agents in parallel, not two.
5. Every gate on the `run` path is verifiably repeated on the fleet path — the
   `persist_auth` class of leak has a test, not a comment.
