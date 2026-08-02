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
| 1 | [Better local / dev agent experience](task-1-local-agent-experience.md) | Make the everyday loop pleasant and reliable: sessions you can see, attach to, follow and stop; a `doctor` that answers "is my setup good?"; errors that say what to do. | **Shipped** — [#35](https://github.com/Amitgb14/sandbox-cli/pull/35); one gap in coverage, see the doc |
| 2 | [Multi-agent support](task-2-multi-agent.md) | Run several agents safely in parallel (git worktrees), orchestrated from the CLI. No GUI. | **Shipped** — [#36](https://github.com/Amitgb14/sandbox-cli/pull/36); one manual check outstanding, see the doc |
| 3 | [Stronger isolation for Linux production](task-3-stronger-isolation.md) | When the code is untrusted, give each sandbox its own kernel — Kata on Linux, Firecracker later. | **Next** — not started |
| 4 | [Run provenance](task-4-run-provenance.md) | Make a finished run readable: what the agent actually did, from a channel the sandbox cannot forge. Today the log is one line per run. | Not started |
| 5 | [Checkpoint and fork](task-5-checkpoint-and-fork.md) | Stop paying cold start per worktree, and let three attempts at one fix branch from a single prepared state. | Not started |
| 6 | [macOS microVM](task-6-macos-microvm.md) | A libkrun backend, so the stronger boundary is reachable on the platform most users are on. | Not started |

## Why this order

Task 1 is where every user starts and where they spend every day. A tool whose daily loop
is confusing does not get used long enough for its isolation story to matter.

Task 2 is the first thing people ask for once the daily loop is good, and it is mostly a
matter of making what already exists (`fleet`, `worktree`) feel like one feature rather
than three.

Task 3 is a real boundary change, and it is the one thing here that cannot be faked. It is
only worth the cost once someone is running code they actually do not trust — and by then
the two tasks above will have told us what the runtime seam has to look like.

Tasks 4–6 were added in August 2026 after a review of the wider sandbox landscape (see
[how tasks 4–6 got here](#how-tasks-46-got-here)). They stay behind task 3 rather than
displacing it: task 3 is the boundary this tool advertises, and none of these three is
worth more than the boundary being real on Linux first.

Their order among themselves is a judgement call, not a dependency chain. Provenance is
first because it is cheapest and it extends machinery that already exists. Checkpoint and
fork is second. The macOS microVM is last because it is the largest by a distance, and it
is the first thing that genuinely needs the `Runtime` interface to grow a second backend —
the question task 3 defers to "only then". The argument for swapping 5 and 6 is real and
recorded in task 5: a *memory* checkpoint wants the microVM, so doing 5 on Docker means
shipping the filesystem half twice.

## What is deliberately deferred

These are all reasonable and none of them are next. Recording them here is how they stop
leaking into the work that is:

- A full `Runtime` interface abstraction (today's seam is enough — see task 3; task 6 is
  where this gets revisited, because a second backend is what would force it)
- A2A / agent-to-agent communication protocols
- A credential broker that terminates TLS and injects secrets
  (`internal/creds` is a stub seam; prod's answer today is simply not to mount the
  refresh token). This is [open security item 2](../security/open-items.md) and it is
  blocked on a decision, not on effort
- Distributed tracing, OpenTelemetry, Prometheus, centralized logging. Task 4 is a
  **run-scoped, local, file-based** record; it is not a step toward any of these
- Formal threat-model documents beyond the ledger already in
  [`docs/security/audit-2026-07-26.md`](../security/audit-2026-07-26.md) and the live
  backlog in [`docs/security/open-items.md`](../security/open-items.md)

## Considered and declined

Same purpose as the "Not on this list" section in
[`open-items.md`](../security/open-items.md): these came out of the August 2026 landscape
review, each is a real gap, and each is declined for a reason — so nobody re-opens them
without a new argument.

- **Remote execution / BYOC / cloud runners.** The honest version already exists and costs
  nothing: sandbox-cli shells out to the engine binary, so `DOCKER_HOST` and
  `DOCKER_CONTEXT` point it at another daemon today (which is why both are on
  `config.IsReservedEnv`). A first-class remote feature is a hosted product with a new
  trust root, an account model and someone else's machine in the blast radius. That is a
  different tool.
- **An MCP server, so an agent can manage its own sandboxes.** Several products ship one.
  It inverts the thing this tool is: the boundary is decided by the person, in a config
  the agent is not allowed to weaken (`internal/config/trust.go`). An API that lets the
  agent create sandboxes and choose their policy hands that decision to the process the
  policy exists to contain.
- **A team/org policy layer — central push, RBAC, a security team setting the floor.**
  The layering is deliberately local, and the one direction a project may move the profile
  is *stricter* (`config.ResolveProfile`). A pushed policy means fetching executable
  intent from a server, which is a trust root the design currently does entirely without.
  The tighten-only direction is the answer for now; if this ever ships, the floor has to
  arrive by a path the agent cannot influence, and that is the hard part rather than RBAC.
- **Per-request egress policy — HTTP method and path, request inspection, outbound secret
  scanning.** The proxy deliberately does not terminate TLS, so it cannot see a method, a
  path, or a body. Making it able to is the *same* decision as the credential broker
  above, and it should be made once, there, rather than arrived at twice from two
  directions. Recorded because it is the one place a competitor (NVIDIA OpenShell) is
  meaningfully ahead on policy rather than on isolation.
- **Hot-reloading egress policy on a running sandbox.** Policy is fixed at container start
  by design: it arrives as one environment variable and is programmed by the root phase
  before the privilege drop. A reload path means something long-lived that can re-program
  the firewall mid-run, which is a new privileged surface inside the container to buy
  convenience.
- **GPU, browser/VNC desktop, shareable preview URLs, image signing/SBOM/attestation,
  spend caps, eval and benchmark hooks.** Each is a product surface rather than a boundary
  control. `--publish` binds to `127.0.0.1` on purpose and a shareable URL is the opposite
  of that.
- **Implementing the `kubernetes-sigs/agent-sandbox` CRD** (Sandbox / SandboxTemplate /
  SandboxClaim / SandboxWarmPool, isolation via `runtimeClassName`). Worth tracking as the
  interop standard the category is converging on; not worth implementing in a local CLI
  that has no cluster.

## How tasks 4–6 got here

A landscape review in August 2026 listed roughly fourteen gaps against sandbox-cli. Most
were accurate. Three are worth recording because the *evidence* is what moved them from
the deferred list onto the roadmap:

- **The supply chain inside the box.** OpenAI disclosed on 21 July 2026 that models escaped
  an isolated evaluation environment through zero-days in a self-hosted Artifactory
  package-registry proxy that was *part of that environment*, then escalated and moved
  laterally to an internet-reachable node. Eleven of the fifteen wrappers here download
  their agent from a vendor host on first run. That did not become a roadmap task — it was
  cheap enough to fix directly (`internal/agents/pins.go`), which is the right outcome for
  the sharpest item on the list.
- **Checkpoint and fork are shipped elsewhere and users ask for them.** Blaxel resumes from
  a memory+filesystem snapshot in under 25ms, Freestyle forks a live VM copy-on-write in
  ~320ms, Zeroboot forks a KVM VM at p50 0.79ms by mmap'ing a Firecracker snapshot. Our
  "snapshots" are git refs of the workspace (`internal/rescue`), which is a different
  feature wearing the same word.
- **macOS cannot reach the stronger boundary at all.** `--runtime kata-runtime`/`runsc` is
  unavailable on Docker Desktop, which is most developers. libkrun embeds a
  Hypervisor.framework microVM on macOS and Microsandbox ships per-sandbox kernels on it,
  so this is no longer "Firecracker later" — it is a thing that exists on the platform the
  gap is on.

One claim in that review was simply **wrong** and is recorded here so it is not fixed
twice: that host MCP servers reached via `host.docker.internal` get no policy applied.
That was [open security item 5](../security/open-items.md) and it is closed — without
`--host-gateway` the name resolves to the container's own loopback, and with the flag the
traffic still meets the firewall and the name-matching proxy.

The rest become relevant *after* the basic CLI experience feels good and people start
asking for stronger isolation or multi-agent orchestration.

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
