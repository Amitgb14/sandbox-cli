# Roadmap

> Isolated microVM sandboxes for agents, behind one API — on your Mac, on your own
> Linux machine, or in our cloud.

**Direction changed on 2026-10-01.** sandbox-cli started as a CLI that runs coding
agents in a Docker container. It is being rebuilt as a sandbox product that runs three
ways from the same API:

- **Local**, on a Mac, with the native macOS `container` runtime.
- **Self-hosted**, on a Linux machine the user controls, with Firecracker.
- **Cloud**, on our own fleet, with Firecracker.

Docker and podman are dropped. The full plan, the review of the nearest existing
product and the milestones are in [`docs/rewrite/PLAN.md`](../rewrite/PLAN.md)
on the `rewrite` branch. This page maps the old tasks onto that plan, and records which
earlier decisions are reversed and why.

## Where the old tasks went

| # | Task | Now | State |
|---|------|-----|-------|
| 1 | [Better local / dev agent experience](task-1-local-agent-experience.md) | Carried into the CLI-as-client (rewrite M7): sessions, `doctor`, errors that say what to do | **Shipped** on beta.15 ([#35](https://github.com/Amitgb14/sandbox-cli/pull/35)); to be re-earned on the new backends |
| 2 | [Multi-agent support](task-2-multi-agent.md) | Fleet rebuilt on the API (M10); one sandbox per branch is just N `create` calls | **Shipped** on beta.15 ([#36](https://github.com/Amitgb14/sandbox-cli/pull/36)) |
| 3 | [Stronger isolation for Linux](task-3-stronger-isolation.md) | **Superseded.** Every sandbox is a microVM; Linux is Firecracker by default, not Kata as an option (M5) | Kata and gVisor work stops; the gVisor findings stay as history |
| 4 | [Run provenance](task-4-run-provenance.md) | Per-sandbox events served by the API, recorded host-side by `sandboxd` and the host egress proxy, which the guest cannot forge | Not started; becomes easier, since egress is now observed outside the guest |
| 5 | [Checkpoint and fork](task-5-checkpoint-and-fork.md) | Snapshots, suspend/resume and clone on Firecracker (M8); macOS reports the capability off | Not started; the "do it on Docker first" path is gone |
| 6 | [macOS microVM](task-6-macos-microvm.md) | **Superseded.** The native macOS `container` runtime instead of libkrun; the OS runs the VM, sandbox-cli ships the image and the guest agent (M6) | Not started |

## New work

| # | Task | State |
|---|------|-------|
| 7 | [A gateway in front of many sandboxd nodes](task-7-fleet-gateway.md): sandboxes by API and SSH on one port across hundreds of machines, then agents, jobs and services, as a platform beside Kubernetes | **Proposed** 2026-10-04; five decisions needed before phase 1 |
| 8 | [The gateway's state in PostgreSQL](task-8-postgres-store.md): several gateways serving one fleet, row-level security as a second wall between tenants, the file store kept as the default | **Proposed** 2026-10-04; five decisions, the driver first |

## Reversed decisions

The roadmap used to decline several of these for good reasons. The reasons were about
a **local CLI with a person at the terminal**, and the product is no longer only that.
Each reversal keeps the original concern as a constraint on the new design.

- **Remote execution, BYOC, cloud runners.** *Was:* a different tool, with a new trust
  root and someone else's machine in the blast radius. *Now:* self-hosted and cloud are
  two of the three modes.
  - *Constraint kept:* the trust root is explicit. On a remote endpoint the trusted
    party is a token, never a request body.
  - Auth and server-side policy ship with self-hosted (M5), before any cloud code.
- **A team/org policy layer.** *Was:* fetching executable intent from a server is a
  trust root the design did without. *Now:* that server is `sandboxd`, and its policy
  is the floor.
  - *Constraint kept:* tighten-only. A request may narrow what the daemon decided and
    never widen it, which is the `trust.go` rule, applied to API requests.
- **Changing egress policy on a running sandbox.** *Was:* it needed a privileged
  re-programming path inside the container. *Now:* on Linux, egress is enforced on the
  host (tap + nftables + `egressproxy`), so updating it touches nothing in the guest.
  - *Constraint kept:* the swap is atomic, with no window where egress is unfiltered.
  - On macOS the update is offered only if measurement (rewrite M3) shows enforcement
    can live outside the guest there too.
- **Shareable preview URLs.** *Was:* the opposite of binding `--publish` to
  `127.0.0.1`. *Now:* authenticated TCP tunnels in every mode. Public exposed ports
  come later and only in the cloud.
  - *Constraint kept:* nothing is public unless explicitly requested.
- **The `Runtime` abstraction.** *Was:* deferred until a second backend forced it. Two
  backends force it now. It becomes a `Backend` interface that declares its
  capabilities, so a request a backend cannot honour is refused rather than quietly
  weakened.

## Still deferred

- A2A / agent-to-agent protocols.
- Pre-warmed pools, durable shared filesystems, hosted git, desktop/VNC sessions, GPU
  (rewrite M10 and later; each is a capability flag, not a boundary).
- Distributed tracing, OpenTelemetry, Prometheus. The cloud will need metering
  (M9), which is a narrower thing.
- Formal threat-model documents beyond
  [`docs/security/audit-2026-07-26.md`](../security/audit-2026-07-26.md) and
  [`docs/security/open-items.md`](../security/open-items.md). The cloud's
  multi-tenant threat model is the exception, and it is written before M9 starts.

## Still declined

- **A credential broker that terminates TLS to inject secrets, and per-request egress
  inspection** (method, path, body scanning). Same reason as before
  ([open item 2](../security/open-items.md), decided 2026-08-04): one process holding
  every token, every prompt in plaintext and a CA key trades frequent small leaks for a
  rare total one. That is more true with tenants, not less.
- **Handing an agent the means to manage its own sandboxes.** The API exists for
  harnesses and people. A sandbox gets no API token by default, and a token issued to
  one is scoped to what its creator could already do. The floor is set by `sandboxd`,
  not by the process the floor exists to contain.
- **Implementing the `kubernetes-sigs/agent-sandbox` CRD.** Still worth tracking as an
  interop standard. Worth revisiting only if cloud customers ask to run the control
  plane in their own clusters.

## History

### The original order (beta.1–beta.15)

The local CLI experience came first, then multi-agent, then stronger isolation. Each
task was only worth starting once the one before it was good enough to use daily. That
order shipped tasks 1 and 2, and it is why the rewrite has a working product to port
from rather than a design to guess at.

### How tasks 4–6 got here

A landscape review in August 2026 listed roughly fourteen gaps against sandbox-cli. Most
were accurate. Three are worth recording because the *evidence* is what moved them from
the deferred list onto the roadmap:

- **The supply chain inside the box.** A model developer disclosed on 21 July 2026 that models escaped
  an isolated evaluation environment through zero-days in a self-hosted
  package-registry proxy that was *part of that environment*, then escalated and moved
  laterally to an internet-reachable node. Eleven of the fifteen wrappers here download
  their agent from a vendor host on first run. That did not become a roadmap task — it was
  cheap enough to fix directly (`internal/agents/pins.go`), which is the right outcome for
  the sharpest item on the list.
- **Checkpoint and fork are shipped elsewhere and users ask for them.** One hosted service resumes from
  a memory+filesystem snapshot in under 25ms, another forks a live VM copy-on-write in
  ~320ms, and an open-source project forks a KVM VM at p50 0.79ms by mmap'ing a Firecracker snapshot. Our
  "snapshots" are git refs of the workspace (`internal/rescue`), which is a different
  feature wearing the same word.
- **macOS cannot reach the stronger boundary at all.** `--runtime kata-fc`/`runsc` is
  unavailable on Docker Desktop, which is most developers. libkrun embeds a
  Hypervisor.framework microVM on macOS and at least one open-source project ships per-sandbox kernels on it,
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
