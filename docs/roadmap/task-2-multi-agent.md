# Task 2 — Multi-agent support

**Goal.** Run several agents safely in parallel — usually one per git worktree — with basic
orchestration from the CLI. No GUI required.

**Status.** Shipped — merged to `main` as
[#36](https://github.com/Amitgb14/sandbox-cli/pull/36) from `feature/multi-agent`. The
one check still outstanding is recorded at the bottom of this document: a mixed-agent
`fleet run` that actually starts containers.

This task is mostly *consolidation*. Two thirds of it already exists as `worktree` and
`fleet`; what is missing is the part that makes them feel like one feature with one mental
model rather than two subsystems that happen to share a repository.

**Where it stands.** Everything under *Required features* is built except the two items
marked ⏳ below, and the open questions are answered where the code forced an answer. The
acceptance criteria at the bottom carry their state.

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

### 1. One mental model with task 1 — ✅

A fleet container is a session. After task 1 exists, this task makes that true everywhere:

- ✅ Fleet runs appear in `sandbox-cli list` (they already carry the labels), marked as
  fleet rather than interactive — a `KIND` column, from the same `sandbox.fleet` label
  `fleet stop --all` and `fleet clean` filter on, so the listing and the reaper cannot
  disagree.
- ✅ `attach`, `logs` and `kill` work on a fleet session by branch name (`resolveSession`
  already matched the branch label; confirmed rather than built).
- ✅ `fleet status` stays branch-oriented — the branch is the fleet's unit of work — and
  its ID column matches `list`'s. It gained an `AGENT` column at the same time, which a
  mixed fleet needs to be legible at all.

### 2. Fleet lifecycle gaps — ✅

- ✅ **`fleet land --all`**, oldest first. The rule it applies is a distinction rather
  than a list: a refusal about *the branch* skips it and the rest carry on, a refusal
  about *the base branch* stops there, because it will be just as wrong for the next one.
  What already landed is printed either way — those merges are commits whatever happens
  next.
- ✅ **`fleet run --only BRANCH`** (repeatable, comma-separated). A branch the file does
  not contain is an error listing what it does contain, because launching nothing looks
  exactly like success.
- ✅ **`fleet run --resume`** — skips branches still running and branches whose last
  container exited 0, starts everything else. Composes with `--only`.
- ✅ **`fleet status --watch`**.

### 3. Per-task expressiveness — ✅

- ✅ **A different agent per task** (`agent:` on a task, overriding the spec-level one).
  The fleet-wide `agent:` became optional when every task names one. Eligible agents grew
  from two to five — claude, codex, gemini, opencode, droid — each with a headless argv
  pinned by `TestEveryAgentHasAVerifiedHeadlessArgv`, so adding a sixth means verifying it
  rather than guessing at its flags.
- ✅ **Per-task `memory` / `cpus` / `allow`**. Caps replace the fleet-wide value; `allow`
  adds to it, because a task that could subtract would be asking for a narrower allowlist
  than the file's author wrote a line above.
- ✅ **Explicit ordering or dependency** — decided, and the answer is no. `max_parallel: 1`
  plus file order is the ordering primitive, and it is enough for the one real case
  (producer before consumer, §5). A `depends_on:` is the beginning of a workflow engine.

### 4. Concurrency and resource sanity — ✅

- ✅ `max_parallel` now really does count only fleet containers. It did not: `waitForSlot`
  filtered on repo alone, so one open interactive `--detach` session held a slot it never
  freed — the exact failure `sandbox.fleet` exists to prevent, in the one place that did
  not use the label. Fixed and tested.
- ✅ Obviously unrunnable fleets are refused up front — but on the *concurrent* agent
  count, not the task count, since bounding concurrency is what `max_parallel` is for.
  Host memory comes from the engine rather than `/proc`, because on macOS the daemon's VM
  budget is the number a `--memory` cap competes for. A host that cannot be measured
  proceeds: this is resource sanity, not a boundary control.
- ⏳ A per-fleet total budget, if the above proves too blunt. Not yet needed.

### 5. Getting work between agents — ✅

✅ `fleet run --share` / `--share-name` now exist, going through the same `shareMount` the
run path uses. It stays a **flag rather than a `fleet.yaml` key**: a cross-project
directory should be something you type, not something a file copied between repositories
turns on.

✅ The convention is documented in the guide — a producer and a consumer named in the
prompts, `max_parallel: 1` when the consumer genuinely needs the producer's output, and a
consumer prompt that says what to do when the file is not there (an agent that invents the
API rather than stopping is the failure mode, and `verify` is what catches it).

Decided: **no namespace per run automatically.** The whole point is that two agents see the
same directory; a per-run namespace would default the feature to not working, and
`--share-name` is there for the collision case.

Explicitly **not** an agent-to-agent protocol. Files in a shared directory, or nothing.

### 6. Documentation — ✅

✅ `docs/GUIDE.md` now has one **Running agents in parallel** section with four rungs —
one agent per branch, in the background, a fleet, handing files over — stated as one
feature that grows rather than four. `docs/AGENTS.md` gained *Agents a fleet can run*.

---

## Open questions

Recorded rather than guessed. Two are now answered, by the code:

- **Does `fleet` become the front door for parallel work, with `--worktree` as the
  single-agent special case, or do they stay separate commands?** They stay separate, and
  the guide now says why: they are rungs of one ladder, not alternatives. A fleet task
  *is* a `--worktree --detach` run — same `sandbox.Options`, same boundary — so making
  `fleet` the front door would mean writing a file to run one agent.
- Should `land` gain a `--no-ff` alternative (rebase, squash) or stay one merge strategy?
  One strategy is easier to reason about when the thing being merged was written
  unattended. **Still open** — `land --all` did not force it, and the argument for one
  strategy got stronger now that five merges can happen from one command.
- **Is there a case for a fleet across *repositories*, or is one repo the boundary?** One
  repo. `Runner` is built around a single `RepoID`, every label is scoped by it, and
  `land` merges into one checkout. A cross-repo fleet is several fleets and a shared
  directory, which already works.

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

1. ✅ A running fleet agent can be listed, attached to, followed and stopped with the same
   commands as any other session.
2. ✅ `fleet land --all` and `fleet run --only BRANCH` exist, and an interrupted scheduled
   run can be resumed (`fleet run --resume`).
3. ✅ A fleet can mix agents across tasks.
4. ✅ The guide has one story about running agents in parallel, not two.
5. ✅ Every gate on the `run` path is verifiably repeated on the fleet path — the
   `persist_auth` class of leak has a test, not a comment. `internal/fleet/gates_test.go`
   classifies every field of `sandbox.Options` and fails when the struct grows one that is
   not classified, so the next field of that class is a decision rather than an omission.

**Verified against a live daemon** (Docker 28.0.4 on macOS, 7.7g / 10 CPUs): `make
test-integration` passes, `internal/fleet` included. Two things that could only be
guessed at without one were checked directly — `HostMemoryBytes` against both dialects
(docker answers `MemTotal`, podman answers `memTotal` under `.Host`, which is why podman
is asked for JSON and read by key), and the capacity refusal, which declined a 3 × 8g
fleet on that machine before starting anything.

**What has not been done: a mixed-agent `fleet run` that actually starts containers.**
Everything up to the launch is covered; the launch itself is not. It needs a login per
agent in the persisted agent homes and spends real API quota, so it stays a deliberate
manual check rather than something a test suite does. The shape of it: a two-task file
with one Claude task and one Codex task, `max_parallel: 1`, trivial prompts, then `fleet
status` → `fleet land --all`.
