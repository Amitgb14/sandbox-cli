# Roadmap

> Make the safest and most convenient way to run coding agents from the terminal.
> Isolation gets stricter later; the CLI experience comes first.

sandbox-cli is one thing: **a CLI that runs coding agents (or any command) inside an
isolated environment so they cannot mess up the host.** Everything else is machinery in
service of that, and most of it is machinery for *later*.

The work is ordered, and the order is the point — each task is only worth starting once
the one before it is good enough to use daily.

| # | Task | What it really means | State |
|---|------|----------------------|-------|
| 1 | [Better local / dev agent experience](task-1-local-agent-experience.md) | Make the everyday loop pleasant and reliable: sessions you can see, attach to, follow and stop; a `doctor` that answers "is my setup good?"; errors that say what to do. | **In progress** — branch `feature/local-agent-experience` |
| 2 | [Multi-agent support](task-2-multi-agent.md) | Run several agents safely in parallel (git worktrees), orchestrated from the CLI. No GUI. | **In progress** — branch `feature/multi-agent` |
| 3 | [Stronger isolation for Linux production](task-3-stronger-isolation.md) | When the code is untrusted, give each sandbox its own kernel — Kata on Linux, Firecracker later. | Not started |

## Why this order

Task 1 is where every user starts and where they spend every day. A tool whose daily loop
is confusing does not get used long enough for its isolation story to matter.

Task 2 is the first thing people ask for once the daily loop is good, and it is mostly a
matter of making what already exists (`fleet`, `worktree`) feel like one feature rather
than three.

Task 3 is a real boundary change, and it is the one thing here that cannot be faked. It is
last because it is only worth the cost once someone is running code they actually do not
trust — and by then the two tasks above will have told us what the runtime seam has to
look like.

## What is deliberately deferred

These are all reasonable and none of them are next. Recording them here is how they stop
leaking into the work that is:

- A full `Runtime` interface abstraction (today's seam is enough — see task 3)
- Firecracker, and anything microVM beyond a Kata spike
- A2A / agent-to-agent communication protocols
- A credential broker that terminates TLS and injects secrets
  (`internal/creds` is a stub seam; prod's answer today is simply not to mount the
  refresh token)
- BYOC / cloud runners / remote execution
- Distributed tracing, OpenTelemetry, Prometheus, centralized logging
- Formal threat-model documents beyond the ledger already in
  [`docs/security/audit-2026-07-26.md`](../security/audit-2026-07-26.md) and the live
  backlog in [`docs/security/open-items.md`](../security/open-items.md)

They become relevant *after* the basic CLI experience feels good and people start asking
for stronger isolation or multi-agent orchestration.

## How to read these documents

Each task document has the same four sections:

- **What exists today** — verified against the code, not from memory. This is what stops
  the task from re-building something already shipped.
- **Required features** — the whole list, each with the CLI shape it takes and what has to
  be true for it to count as done.
- **Not in this task** — the scope fence.
- **Done when** — the acceptance criteria for the task as a whole.

These are scope documents, not designs. Where a decision is genuinely open it says so
rather than guessing; the design notes live in `docs/proposals/` (untracked) and the
per-feature reasoning lives in the code comments, which is where this repository keeps it.
