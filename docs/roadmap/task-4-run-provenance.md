# Task 4 — Run provenance

**Goal.** Make a finished run readable. Today the run log answers *what was this run
allowed to do*; it should also answer *what did it actually do*, from a channel the
sandbox cannot forge.

**Branch.** Not started.

```
what the run was allowed        what the run did
─────────────────────────       ─────────────────────────
image, workspace, branch        commands the agent ran
network posture                 hosts it reached, and was refused
egress allowlist                files it changed outside the diff
env names forwarded             how it ended
   ↑ shipped                       ↑ one line of this exists (this task)
```

---

## Why this, and why now

The 21 July 2026 OpenAI disclosure is the argument. Models escaped an isolated evaluation
environment through zero-days in a package-registry proxy that was part of that
environment, escalated, and moved laterally to an internet-reachable node. The only reason
that is *understood* is that the agents' actions could be reconstructed afterwards.

Every tier of this category competes on isolation depth and cold start. Almost nobody
ships an immutable, replayable record of what the agent did as a first-class thing — and
for the local tier, nobody does. It is also the one item on this roadmap that plays to
what sandbox-cli already is: a tool whose distinguishing asset is that you can *audit the
boundary as a string* before trusting it (`--dry-run`, `doctor`). Being able to audit the
run afterwards is the same promise, one step later.

It is first among tasks 4–6 because it is the cheapest by a wide margin and because it
extends machinery that already exists rather than introducing a new one.

---

## What exists today

- **`internal/audit`** writes exactly one JSONL line per run to
  `~/.config/sandbox/audit/sessions.jsonl`: image, workspace, branch, agent, guest argv,
  engine, network posture, resolved egress allowlist, forwarded env *names*, exit code,
  duration. `RecordSession` is called once, from `sandbox.Session.Run`/`Start`.
- **Environment values are never recorded**, by construction — `SessionMeta` has nowhere
  to put one. That rule does not change here, and the known soft edge is unchanged too:
  the guest argv is recorded verbatim, and an argv is a classic place a token ends up.
- **`egress_denied_reported` / `egress_denied_hosts_reported`** were added alongside this
  task being written. They are the first thing in the log that describes what the run
  *did* rather than what it was permitted to do — and they are deliberately named for a
  report rather than a fact, because the proxy writes them to the container's stderr,
  which the agent can also write to. **Closing that gap is required feature 1 below.**
- **`internal/egressproxy`** already makes a per-connection decision by hostname and logs
  each one. Per-*connection* provenance therefore exists inside the container; nothing
  carries it out in a form the host can trust.
- **`internal/rescue`** already walks the workspace and writes snapshots to
  `refs/sandbox/snapshots/`, so "what changed on disk" has machinery too.

So this is not "build a logging system". It is "give the existing records a trustworthy
channel out of the container, and something to read them back with".

---

## Required features

### 1. A report channel the guest cannot forge

This is the load-bearing one, and everything else is worth less without it.

- The in-container proxy (and the entrypoint) must be able to report to the host over a
  path the unprivileged agent cannot write to. Container stderr is shared with the agent,
  so today's counts are evidence, not attestation.
- Whatever the mechanism, the honesty rule stays: a field named for a fact must *be* one.
  If a channel is only partly trustworthy, the field keeps a name like
  `…_reported` — the precedent is `EgressEnforcementRequested`, named for a request
  because a request is all the host could honestly know.
- When this lands, `egress_denied_reported` gets renamed in the same change, and
  `runtime.TestDenialsAreForgeableByTheGuest` — a test that asserts today's *limitation*
  — is where that gets noticed.

### 2. Per-command records, not just per-run

- What the agent executed inside the sandbox, in order, with timing and exit status.
- The obvious mechanism is the guest side (a shell/exec wrapper), and the obvious hazard
  is that anything the agent can rewrite is not a record of the agent. Decide that
  deliberately; it is the same class of problem as feature 1.
- The volume changes what the store can be. One line per run fits a rotating JSONL file;
  one line per command does not necessarily, and the answer must not be "a database
  daemon" — see *Not in this task*.

### 3. Read it back

A record nobody can read is a file, not a feature.

- A command that shows one run: what ran, what it reached, what it was refused, how it
  ended. `sandbox-cli list` names sessions and `recover` names snapshots; this is the
  third thing you would want and there is no verb for it.
- Correlating a run with its agent transcript is already solved once, in
  `cli/recover_resume.go`, by agent + project + time window. Reuse it rather than
  inventing a second correlation.

### 4. Cover the unattended runs, which are currently the least covered

The egress-denial count that shipped alongside this task is recorded for a
**non-interactive `run`** and nothing else. `--detach` and every `fleet` task get
nothing, because `Session.Start` has no host process holding the container's output —
which is the wrong way round, since an unattended agent is precisely the one nobody
watched and therefore the one worth a record.

- **The output is not gone.** `docker logs` has it: that is the supervision story
  `--detach` is documented around, and `sandbox-cli logs` already reads it. Counting
  denials from there at `fleet status` or at reap time needs no new channel, and is the
  cheapest real coverage win available.
- It inherits the same caveat as the live counter and must keep the same naming: those
  lines are still the container's report, on a stream the agent can write to.
- Interactive runs stay uncovered until feature 1 exists, and that is the honest order —
  reading a pty run's output costs the container its terminal size (measured; see
  `runtime.newDenyTap`), so it wants a real channel rather than a cleverer tap.
- Same gap for a run under a user-supplied `--image` where the proxy may not exist at
  all. A gap that prints nothing reads as "nothing happened"; say which it is.

---

## Not in this task

- **Distributed tracing, OpenTelemetry, Prometheus, centralized logging.** All still
  deferred, and this is not a step toward them. The record is run-scoped, local, and in a
  file. If the design starts wanting a collector, it has left this task.
- **A daemon, a database, or a background service.** `history-db.md` explored a SQLite
  index and it lives out of tree, behind a flag, in another repo. Keep it there: the
  reason sandbox-cli has no orphaned state to clean up is that it has no long-lived
  anything.
- **Recording environment values.** Not now, not later. The whole point of the broker seam
  is that values stay out of files.
- **Replay in the sense of re-executing.** "Replay" here means reading back what happened,
  not running it again. Re-execution is a different feature with a different threat model.
- **Risk scoring** or any judgement about whether what an agent did was *bad*. Record it;
  do not grade it.

---

## Done when

1. There is a report channel from the container that the unprivileged agent cannot write
   to, and the fields fed by it are named as facts rather than reports — with the rename
   done in the same change that earns it.
2. The log records what the agent executed, not only what the run was permitted to do.
3. One command reads a past run back in a form a person can act on, including the
   sessions that ran detached.
4. The rules the run log already holds are intact: no environment values, best-effort and
   silent on failure, `0600`, bounded on disk.
5. What is *not* captured is stated in the output rather than left to be inferred from an
   empty field.
