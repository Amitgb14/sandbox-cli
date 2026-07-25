# Review: the multi-agent fleet plan

**Reviews:** [`multi-agent-fleet.md`](multi-agent-fleet.md)
**Date:** 2026-07-24
**Verdict:** the premise and the four design constraints hold. The mechanical
gaps found here are folded into the proposal already. What remains open is the
build order, two shared-state hazards, and the scope of Phase 1.

## What this review changed in the plan

The plan as originally written is recorded in the proposal with these
corrections applied. They are listed here so the reasoning survives, not only the
result.

| Original | Corrected to | Why |
|---|---|---|
| `RunSpec` gains `Detach`; `Options` unchanged | `Options` gains `Detach` too | `BuildSpec` is what resolves TTY, metrics and `--rm`; without `Options.Detach` all three resolve from the *launching* terminal |
| `BuildArgs` forces `Remove=false` when `Detach` | `BuildSpec` sets `Remove: !opts.Detach` | `BuildArgs` is documented as a pure renderer of an already-resolved spec (`internal/runtime/args.go:8-12`); a silent override there puts two places in charge of what the container is |
| `sandbox-<repo>-<branch>-<ts>`, plus a `docker ps` check for one-per-worktree | `sandbox-<repo>-<branch>` for detached runs, no check | The check is TOCTOU — two `fleet run`s can both pass it. Docker's own name uniqueness enforces the constraint atomically and for free |
| `sandbox.repo=<id>`, id undefined | `worktree.RepoID`, the identity `worktreeBase` already computes | Two clones of a same-named repo would otherwise share a label namespace (`internal/worktree/worktree.go:300-308`) |
| Phase 5 refuses to land onto an unexpected branch | `sandbox.base` label stamped at launch, or `base:` in the schema | Nothing recorded what "expected" meant |
| `max_parallel: 3` | caps the launch burst; `fleet run` is idempotent and re-running fills free slots | A true running count needs something to notice an exit and launch the next task — a supervisor process, which constraint 1 forbids |
| `EnsureImage` on the existing `Session.Run` path | built once, before the fan-out | N launches on a cold image is N concurrent builds (`internal/sandbox/sandbox.go:51`) |
| `fleet land` refuses if *this branch's* container runs | also refuses if a container runs against the main checkout | The merge target may be the directory another agent is working in |

## The failure mode the plan was one field away from

`BuildSpec` computes `tty := detectTTY()` (`internal/sandbox/spec.go:267`), which
is true whenever `fleet run` is invoked from a terminal — which is always. Without
`Options.Detach`, every detached container is launched `-dit`, and the agent
starts its **full-screen TUI in a container nobody is attached to**. No amount of
`-p` or `exec` in the autonomous argv prevents it, because the agent is choosing
its renderer from the presence of a pty, not from its flags.

The same field controls two lesser versions of the same mistake: `ShowMetrics`
and `ShowSummary` (`internal/sandbox/spec.go:276-278`) are derived from
`isTerminal(os.Stderr)` and are meaningless for a container the launching process
does not wait on.

## Two shared-state hazards the plan does not address

Both exist today with parallel `--worktree` runs. The fleet does not create
them — it makes them the default path, and raises the concurrency at which they
bite from "occasionally two" to "N by design".

**N concurrent agents share one persisted HOME.** `AuthPersistDir` is keyed by
*agent name*, not by run: `~/.config/sandbox/agents/claude` is bind-mounted as
the entire container HOME (`internal/sandbox/spec.go:107-117`). Three
simultaneous claude containers write `~/.claude.json`, credentials and session
state into the same host directory. Config writes are rename-atomic, so this
degrades to last-writer-wins rather than corruption — but "agent 3 silently
reverted agent 1's state" is the kind of bug that costs a day to see. The cheap
mitigation is a per-task HOME seeded by copy from the persisted one at launch,
credentials only.

Per-task adapters (Phase 3) partition this hazard without removing it: two tasks
on different agents get different HOMEs, two tasks on the *same* agent still
collide. They also add a sharper version of it. An agent that is not baked into
the image installs itself into that shared HOME on first use —
`npm install -g --prefix "$HOME/.local"` (`internal/cli/agents.go:93`) — so a
fleet whose first-ever run of an adapter has two tasks using it runs two installs
into one directory concurrently, which is how you get a half-written `node_modules`
rather than a lost setting. One interactive run per adapter before it joins a
fleet avoids it entirely, which is why the proposal makes pre-warming a documented
step rather than an optimization.

**N containers share one read-write `.git`.** Every `--worktree` run bind-mounts
the parent `.git` read-write (`internal/cli/root.go:134`), and it has to —
without it a worktree's `.git` pointer file resolves to nothing and the agent
cannot commit at all. Concurrent commits across worktrees are safe by design
(loose objects, per-ref locks). `git gc --auto` firing inside one container is
not: it operates on object and ref storage shared by every other container.
Setting `gc.auto=0` for fleet containers via the `GIT_CONFIG_*` mechanism already
used for `safe.directory` (`internal/sandbox/spec.go:194-199`) removes the class
for three lines.

## Phase 1 is scoped wrong

As originally written, `internal/agents` rewrites all fifteen wrappers and
`TestAgentWrappersShareTheContract` (`internal/cli/wrapper_test.go`) so the fleet
can read three fields for the two or three agents that have a verified headless
mode. That is the highest-cost, lowest-value item in the plan, and it puts the
fifteen shipped adapters inside the blast radius of a feature none of them use.

Three specifics:

1. **A descriptor returning a bare binary name is wrong for eleven of them.**
   Only four agents are baked into the image; the rest start through
   `agentBootstrap` (`internal/cli/agents.go:73`), which is
   `sh -c <install-or-exec script> <bin>` with the guest args appended as `"$@"`.
   A descriptor handing the fleet `["claude", "-p", prompt]` produces a container
   that exits 127 for any lazily-installed agent. The descriptor must carry the
   bootstrap argv.
2. **Claude is not the only special case.** `goose.go` and `openhands.go` carry
   per-agent behavior too, so "adding a wrapper becomes a table entry" would
   become "a table entry plus a hook" — which is what `finishAgentCmd` already
   is.
3. **Most agents have no verified headless mode**, so `fleet.yaml`'s free-form
   `agent:` needs validation that rejects anything without an `Autonomous`.

**Recommendation:** make `internal/agents` purely additive — a descriptor table
the fleet consumes, covering only agents with a verified headless mode. Leave the
wrappers as they are. Migrating them is a separate cleanup that can happen later,
or never.

## Recommended build order

The plan has no shippable milestone until Phase 4, and its single riskiest
unknown — *does a detached autonomous agent run to completion, or does it stop at
a trust prompt?* — is not tested until the manual end-to-end at the very end.
Invert that:

1. **Prove the premise.** `Options.Detach` + `RunSpec.Detach`/`Labels`, `Start`,
   deterministic names, labels in `BuildSpec`, and a plain `--detach` flag on
   `run` and the claude wrapper. Ship it, then run one real detached Claude task
   in a worktree and find out whether it finishes. Everything else depends on
   that answer.
2. **Supervision.** `fleet status` / `logs` / `stop` / `clean` over labels.
   Useful even for containers launched by hand, and it is the half that does not
   exist at all today.
3. **Fan-out.** `fleet.yaml` + `fleet run`, with `internal/agents` introduced
   here as additive support rather than as a prerequisite refactor.
4. **`fleet land`.**

## Not in dispute

The four design constraints, `fleet land`'s conservatism, and docs shipping with
the code all stand as written. In particular "Docker is the state store" is
well-supported by what already exists: `RunSpec.Name`
(`internal/runtime/runtime.go:29`), the `sandbox-` prefix filter `stats` already
uses (`internal/cli/stats.go:102`), and `worktree.List`/`Dirty` operating by
branch without cd-ing anywhere.
