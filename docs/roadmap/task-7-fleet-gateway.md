# Task 7 — a gateway in front of many sandboxd nodes: a platform for AI agents and microservices, beside Kubernetes

**Status:** proposed 2026-10-04, not scheduled. Nothing here is built.
**Answers:** open question 2 in [`../rewrite/PLAN.md`](../rewrite/PLAN.md),
*"Multi-node self-hosted: does a customer with five Linux boxes get the control
plane too?"* — and goes further, because the requirement is hundreds of boxes.
**Relates to:** M9 (the cloud control plane, decided 2026-10-02 to live in a
separate private repository). This proposal is the part of that control plane
a self-hosted fleet needs, and asks where it should live (decision 1).

## The requirement

As stated by the maintainer:

1. `sandboxd` runs on one remote Linux machine, or on hundreds.
2. Users never reach those machines. They reach **one gateway**, over a secure
   API.
3. A user asks the gateway for a sandbox, and may then **SSH into it**.
4. Production creates 100 or 1,000 sandboxes at a time. Anything per sandbox
   that costs a port, a listener or a tunnel set up in advance does not scale
   and is ruled out.
5. It is a **second platform beside Kubernetes**, not a replacement for it:
   for AI agents first, and for microservices that need a VM's isolation or
   fast, short-lived compute. Kubernetes keeps what it runs today, and the two
   work together.

## What exists today

Each node already has almost everything a single machine needs:

- the Sandbox API v1, over TLS with a bearer token (`sandboxd --listen
  0.0.0.0:7443 --tls-cert … --token-file …`), refusing a network address
  without both;
- create, get, list (by label), terminate, suspend and resume, snapshots and
  forks, volumes, processes with and without a terminal, attach, files,
  tunnels to a guest port, audit events;
- pools of sandboxes booted ahead of requests, an image and disk cache, the
  egress proxy, server-side policy where a request may tighten and never
  loosen;
- `capabilities`, so a client asks what an endpoint can do;
- the conformance suite, which any endpoint must pass.

What does not exist anywhere:

| Missing | Why it matters here |
|---|---|
| more than one node behind one address | requirement 1–2 |
| users and API keys (one token per node is all there is) | requirement 2 |
| a scheduler: which node gets this sandbox | requirement 1, 4 |
| SSH | requirement 3 |
| tenancy, quotas, per-user ownership of sandboxes | anyone with the node token can do anything on that node |
| node health, drain, losing a node | hundreds of machines means some are always broken |
| services: replicas, restarts, health checks, HTTP ingress | requirement 5 |

## Deployment shapes

The gateway is a layer on top, never a requirement. Every shape speaks the same
Sandbox API v1, so a client moving between them changes its address and its
credential and nothing else.

| Shape | What runs | How clients connect | Gateway |
|---|---|---|---|
| **Local, a Mac** | `sandboxd` (macOS backend) under launchd | `sandbox-cli`, Studio and the SDKs over a unix socket only its owner can open; `shell` and `exec` to get inside a sandbox | **No.** One user, and the socket's permission is the login |
| **One remote Linux machine** | `sandboxd` (Firecracker) under systemd, TLS and a token | a context (`sandbox-cli context add box https://box:7443 --token-file …`), the SDKs; `shell`, `exec` and `tunnel` over the API | **Optional**, on the same machine |
| **Many Linux machines** | `sandboxd` on every node, gateway replicas in front | everything through the gateway | **Yes** |

**Local** is unchanged by this plan. SSH adds nothing on a machine you are
already at; `sandbox-cli shell` is the way into a sandbox.

**One remote machine** works today without a gateway: one token, one trusted
party, and `shell`, `exec` and `tunnel` over the API. A gateway is worth adding
there when any of these is wanted:

- more than one person: their own API keys, ownership of sandboxes, quotas;
- plain `ssh <sandbox>@box`, so stock `ssh`, `scp` and `rsync` work, and tools
  that connect to sandboxes over SSH need nothing special;
- room to grow: a second machine joins as another node, and clients do not
  change, because they already talk to the gateway.

On one machine the gateway is one more process, and `sandboxd` stops listening
on the network at all:

```sh
sandboxd --listen unix:///run/sandboxd.sock …           # the node, not on the network
sandbox-gateway --node unix:///run/sandboxd.sock \
  --listen :443 --ssh-listen :22 …                      # users reach only this
```

**Why the gateway is its own binary,** rather than a mode of `sandboxd`:
`sandboxd` and the guest agent keep the stdlib-only rule, and only the gateway
takes the SSH dependency (decision 2). And one machine is simply a gateway with
one node, so there is no single-node mode to keep in step with the fleet.

## Architecture

```
  users: ssh · scp · sandbox-cli · SDKs · Studio
            │  HTTPS :443  (Sandbox API v1 + API keys)
            │  SSH   :22   (one port for every sandbox)
            ▼
  ┌──────────────────────── gateway (N stateless replicas) ─────────────────┐
  │  API front    auth (API keys) · quotas · scheduler · route by id        │
  │  SSH front    key or token auth · session → node attach stream          │
  │  HTTP router  https://<sandbox>-<port>.<domain> → node tunnel (phase 4) │
  └───────────────┬──────────────────────────────────────┬──────────────────┘
                  │ mTLS, private network                │ state: users, keys,
                  ▼                                      ▼ ownership, quotas
   node 1 … node N:  sandboxd (unchanged API)        state store
                     └─ vsock → sandbox-guestd (inside every VM)
```

Three rules hold the design together:

1. **The gateway speaks the same Sandbox API v1 as a node.** A client changes
   its base URL and its credential, nothing else. The CLI's contexts, both
   SDKs and Studio work against it unchanged, and **the conformance suite runs
   against the gateway** exactly as it runs against a node. "The same in every
   mode" stays true with a fleet in between.
2. **Nodes are never reachable by users.** They listen on a private network,
   accept only the gateway (mutual TLS plus a node token), and keep every rule
   they have today. A compromised user credential reaches the gateway's
   authorisation, not a node.
3. **Nothing is allocated per sandbox at the edge.** One HTTPS port and one SSH
   port serve every sandbox. Each connection costs one stream from the gateway
   to the node that holds the sandbox, opened when the connection arrives and
   closed with it.

### The gateway

Stateless, so it scales by adding replicas behind a plain TCP load balancer.

- **Authentication.** API keys the gateway issues, hashed at rest, scoped to a
  user (or tenant) and to permissions: `sandbox:create`, `sandbox:ssh`,
  `sandbox:delete`, read-only. OIDC for people can come later; keys come
  first because programs are the main client.
- **Authorisation.** Every sandbox is owned. The gateway stamps
  `owner=<id>` (and `tenant=<id>`) as labels at create time and refuses any
  request whose labels try to set them. Every later call checks ownership
  before it is forwarded. This is the existing rule — a request may tighten,
  never loosen — moved up one layer.
- **Quotas.** Sandboxes, vCPUs and memory per tenant, checked before
  scheduling, so 1,000 creates from one user cannot starve everyone else.
- **Scheduling.** Pick a node for each create, from the capacity nodes report:
  free vCPU, memory and disk, running count, which images are cached, which
  pools have warm sandboxes, labels such as `gpu=true` or `zone=a`. Start with
  "most free capacity among nodes that can satisfy the request, preferring one
  with the image cached or a warm pool"; it is enough for hundreds of nodes and
  easy to reason about.
- **Routing.** A sandbox id names its node: `sbx_<node>_<random>`. The gateway
  routes every later call from the id alone, with no lookup on the hot path,
  and a gateway replica that just started knows where everything is. The state
  store is still the authority for *ownership*; the id is only a routing hint
  it verifies.

### SSH, on one port

`ssh <sandbox-id>@gateway.example.com` — the same host and port for one
sandbox or ten thousand.

- **The gateway is the SSH server.** It authenticates the user, then turns the
  session into calls on the node that holds the sandbox:
  - a shell or `ssh -t` → a process on a terminal, and an attach stream (what
    `sandbox-cli shell` does today);
  - `ssh host command` → a process, its exit status returned;
  - `scp`, `sftp`, `rsync` → the guest's `sftp-server` or the command, run the
    same way;
  - `ssh -L` port forwarding → the node's existing tunnel to a guest port.
- **Logging in.** Two ways, both supported:
  - *your key:* the user registers public keys with the gateway (per user, or
    per sandbox through `POST /v1/sandboxes/{id}/ssh-keys`). The private half
    never leaves their machine.
  - *a short-lived token:* `POST /v1/sandboxes/{id}/ssh-access` returns a token
    valid for minutes, used as the SSH username. It is what a program hands to
    a tool that cannot carry an API key, and it expires on its own.
- **No `sshd` in the guest.** Nothing changes in the image, no key material
  lives inside a hostile guest, the guest needs no network at all, and there is
  no SSH server per sandbox to patch. The audit log records SSH sessions the
  way it records every process, because that is what they become.
- The gateway's SSH host key is one key for the whole fleet, published so
  clients can pin it.

### Nodes

`sandboxd` keeps its API. It gains only what being one of many needs:

- **Registration and heartbeat.** On start, a node registers with the gateway
  and then reports, every few seconds: capacity, running and pooled
  sandboxes, cached images, capabilities, labels, version. A node that stops
  reporting is marked unhealthy and receives nothing new.
- **Cordon and drain.** Stop placing sandboxes on a node, and optionally
  terminate or move what runs there, so a machine can be patched.
- **A gateway-only listener.** Mutual TLS: the node trusts only the gateway's
  certificate authority, on top of its token. Today's TLS-plus-token listener
  becomes this with one more check.
- **Ownership labels are trusted only from the gateway.** A node already keeps
  labels and filters by them; it must refuse `owner` and `tenant` from anyone
  but the gateway.

### The state store

What must survive a gateway restart: users, tenants, API keys (hashed), SSH
public keys, sandbox ownership, quotas, node registry.

- Phase 1 can run one gateway with an embedded file store.
- Several gateway replicas need a shared database: PostgreSQL, because it is
  what operators already run and back up. Placement can always be rebuilt
  from the nodes' own listings, so losing the database loses who owns what,
  not where sandboxes are.

## Flows

**Create.** `POST /v1/sandboxes` with an API key → the gateway checks the key,
the quota and the request against its policy → the scheduler picks a node,
claiming a warm pool sandbox where one fits → the request goes to that node
with `owner` and `tenant` labels stamped → the reply comes back with an id
that names the node. With pools warm, a create is a claim taking under a
millisecond on the node; without, about 100 ms of boot plus the network hop.

**SSH.** `ssh sbx_n17_9f3c…@gateway` → the gateway checks the key or token,
and that this user owns `sbx_n17_9f3c…` → it opens a terminal process and an
attach stream on node 17 → bytes flow until either side closes. No port was
opened for this sandbox before the connection, and none stays open after.

**Everything else** — processes, files, logs, events, suspend, snapshots,
delete — is an API call the gateway authorises and forwards.

## Why it scales to 1,000 at once

- **Creates** spread across nodes; each node boots in parallel and pools make
  most creates a claim. The gateway rate-limits per tenant and queues briefly
  rather than overloading one node. The cost a 1,000-sandbox burst actually
  pays is image pulls and disk builds on cold nodes, which pre-pulling the
  common images removes.
- **SSH sessions** cost two connections each (client → gateway, gateway →
  node), only while open. Capacity grows with gateway replicas.
- **The gateway holds no per-sandbox state in memory** beyond open
  connections, and routes by id, so replicas are interchangeable.

## Security

- Users hold gateway API keys or short-lived SSH tokens, never node tokens.
- Nodes sit on a private network and accept only the gateway, by mutual TLS.
- API keys are scoped, hashed at rest, revocable, and named in the audit log by
  key id, never by value — the same rule as secrets today.
- SSH accepts keys and short-lived tokens only, never passwords.
- Ownership is enforced at the gateway on every call. Nodes refuse ownership
  labels from anyone but the gateway.
- Sandboxes cannot reach one another. That is already a conformance test; per
  tenant network isolation across nodes is a phase 3 item.
- Every existing rule carries over unchanged: fail closed, refuse rather than
  weaken, a request may tighten and never loosen.

## Beside Kubernetes

The fleet runs next to an existing Kubernetes cluster. Each platform takes the
work it is built for, and neither depends on the other to run.

| Kubernetes keeps | The fleet takes |
|---|---|
| existing services and their deployments | AI agents: one-off runs, batches of hundreds, long-lived agents |
| databases, queues and anything with state | code an agent wrote, or a customer sent, that must not share a kernel |
| workloads already tuned for it | short-lived jobs: builds, evaluations, data tasks |
| | microservices that need a VM's isolation or start-up in milliseconds |

A sandbox is a stronger isolation unit than a container in a pod: its own
kernel, and egress enforced on the host where the guest cannot reach it. That,
plus what already exists for agents, is why agents come first.

### How the two connect

- **Kubernetes → fleet.** A service in the cluster calls the gateway's API,
  with the SDK, to start agents or sandboxes, then reads their results: output,
  files, exit status, events. This is expected to be the most common pattern:
  the application stays where it is and sends agent work to the fleet.
- **Fleet → Kubernetes.** An agent or a service on the fleet reaches cluster
  services by name, through its allowlist (for example the cluster's ingress
  for `orders.prod`), and nothing else. The egress proxy enforces it on the
  node, as it enforces any allowlist.
- **Identity.** The gateway issues API keys to cluster workloads, scoped like
  any key (`sandbox:create` only, say). Later, the gateway can accept a
  workload's own cluster identity instead of a stored key, so nothing long-lived
  sits in the cluster.

The fleet does not run on Kubernetes: microVMs need bare-metal KVM hosts and a
scheduler that knows about pools, cached images and snapshots. It sits beside
the cluster and calls into it.

## Agents, jobs and services (phase 4)

Phases 1–3 give a fleet that creates sandboxes on request and lets people SSH
into them. Phase 4 adds the layer that runs work after the request that
started it has gone: agent runs first, then jobs, then services.

### Agents

What an agent needs already exists on a single node: verified headless modes,
saved logins copied in and out, fallbacks when a provider is down, the agent's
own API on every allowlist, "waiting for you" state, suspend and resume, and
snapshots with forks. Phase 4 makes it a fleet-wide API.

```yaml
# agent-run.yaml (illustrative; nothing here is built)
agent: claude
prompt: "clone github.com/you/app, fix issue 412, and open a pull request"
image: ghcr.io/you/agent-base:3
from_snapshot: app-deps-2026-10-04   # optional: start from a prepared state
fallback: [codex]                    # if claude's provider is down
network: { mode: allowlist, allow: [github.com, api.github.com] }
secrets: [GITHUB_TOKEN]
timeout: 45m
keep: { output: true, files: [/sandbox/home/app/report.md] }
notify: https://hooks.internal/agents   # optional: run finished, or waiting for you
```

- **One agent run.** `POST /v1/agent-runs` (or `sandbox-cli agent claude
  --detach` against the gateway) starts a sandbox, restores the agent's login,
  runs it headless with the prompt, keeps its output and named files, and
  terminates the sandbox. The result stays readable after the sandbox is gone.
- **A batch.** One request, N prompts (or one prompt over N inputs), with a
  limit on how many run at once. Each run is a sandbox; 1,000 at once is the
  burst the gateway is sized for.
- **From one prepared state.** A snapshot taken once — repository cloned,
  dependencies installed — and every run starts from it in milliseconds
  instead of rebuilding.
- **Long-lived agents.** A reviewer or an on-call assistant runs as a service
  (below) with one replica and a health check that asks the agent's state.
- **Waiting for you.** An interactive agent that stops to ask is reported
  `blocked` (the state exists today); the gateway calls `notify`, and a person
  answers by `ssh` or Studio's terminal through the gateway.

### Jobs

A job runs a command to completion: a build, an evaluation, a data task.

```yaml
name: nightly-eval
image: ghcr.io/you/evals:2
command: [python, run_eval.py]
parallelism: 50        # sandboxes at once
completions: 500       # successful runs wanted
retries: 2             # per run, on a non-zero exit
timeout: 30m
keep: { output: true, files: [/sandbox/home/out] }
```

An agent batch is a job whose command is an agent; both use the same
controller.

### Services

A service is a sandbox spec and a count, kept true by the gateway: a
microservice, or a long-lived agent.

```yaml
name: review-bot
image: ghcr.io/you/review-bot:1.4.2
replicas: 3
resources: { cpus: 1, memory_mb: 1024 }
port: 8080
health: { http: /healthz, every: 10s, failures: 3 }
secrets: [GITHUB_TOKEN]
network: { mode: allowlist, allow: [api.github.com, orders.prod.svc.example] }
placement: { spread: node }
```

`sandbox-cli deploy -f service.yaml` sends it to the gateway (`POST
/v1/services`). A controller in the gateway then:

- **keeps the count**, replacing a replica that exits, fails its health check,
  or is lost with its node;
- **spreads replicas across nodes**, so losing one machine costs one replica;
- **checks health through the guest agent**: an HTTP probe over the node's
  tunnel, or a command run as a process, so no guest networking is needed;
- **rolls out a change one replica at a time**, waiting for each new one to be
  healthy and stopping if new ones keep failing;
- **routes traffic**: `https://review-bot.<domain>` through the gateway's HTTP
  router, and `review-bot.internal` for other sandboxes that name it in their
  allowlist.

The controller's state — specs, desired counts, rollouts in progress, kept
results — lives in the state store, so any gateway replica can take over.

### What stays on Kubernetes

Anything with state stays on the cluster. A volume lives on one node today, so a
database in a sandbox would be lost with its machine. Services on the fleet are
stateless, and keep their state in the cluster's databases, reached by name
through their allowlist. That is a deliberate boundary, not a gap to close.

## Phases

**Phase 0 — decisions** (below). Nothing is built before they are made.

**Phase 1 — one gateway, many nodes, sandboxes by API.**
- node registration, heartbeat, mutual TLS, cordon;
- API keys and ownership; create, get, list, delete, processes, files, logs
  forwarded;
- scheduler with capacity, image and pool awareness; ids that name their node;
- `sandbox-cli context add fleet https://gateway… --token-file key`.
- *Done when* the conformance suite passes against the gateway in front of one
  node on the same machine and in front of three nodes, and 1,000 creates
  across three nodes succeed with no errors. Local and single-machine use
  without a gateway keep passing as they do today.

**Phase 2 — SSH.**
- the gateway's SSH server on one port: shell, command, scp, sftp, rsync, port
  forwarding;
- SSH keys per user and per sandbox; short-lived SSH tokens;
- SSH sessions in the audit log.
- *Done when* 1,000 concurrent SSH sessions run across the fleet through one
  gateway port, and a stock OpenSSH client needs no configuration beyond the
  host name.

**Phase 3 — production fleet.**
- several gateway replicas on PostgreSQL;
- tenants and quotas; per-tenant network isolation across nodes;
- drain, node failure detection, metrics export, audit export;
- rolling upgrades of `sandboxd` across nodes.

**Phase 4 — agents, jobs and services.** See
[Agents, jobs and services](#agents-jobs-and-services-phase-4).
- agent runs and batches, with kept results, snapshots to start from, and
  notifications; then jobs; then services with replicas, health checks and
  rolling updates;
- the HTTP router on one wildcard name, and internal service names;
- a server-side secret store; API keys for cluster workloads.
- *Done when* a batch of 1,000 agent runs started from one snapshot completes
  with every result kept, a service in Kubernetes starts agents through the
  SDK and reads their results, and a stateless service on the fleet keeps
  serving through a rolling update and the loss of a node.

## Decisions needed before phase 1

1. **Where the gateway lives.** M9 put the cloud control plane in a private
   repository. A self-hosted fleet needs most of the same pieces.
   - *Recommended:* an open `sandbox-gateway` in this repository — nodes,
     keys, scheduling, SSH — with metering and billing staying private for the
     cloud. It answers open question 2 with "yes, open", and keeps "self-hosting
     earns trust" true for a fleet as well as a box.
   - *Alternative:* everything in the private M9 repository, and self-hosted
     fleets use that.
2. **The SSH dependency.** An SSH server has to implement the protocol. That
   means `golang.org/x/crypto/ssh`, the Go project's own SSH package, which the
   dependency rule (stdlib, cobra, yaml.v3) does not allow without a decision.
   *Recommended:* allow it, for the gateway binary only; `sandboxd` and the
   guest agent stay stdlib-only. Writing SSH by hand is not an option.
3. **The state store.** *Recommended:* an embedded file store for a single
   gateway in phase 1, PostgreSQL (a second dependency, gateway only) from
   phase 3.
4. **Sandbox ids that name their node** (`sbx_<node>_<random>`).
   *Recommended:* yes, for routing without a lookup.
5. **How users authenticate.** *Recommended:* gateway-issued API keys first;
   OIDC single sign-on for people later.

## Not chosen

- **A port, listener or tunnel per sandbox** — requirement 4 rules it out, and
  it would put thousands of SSH servers on the network.
- **`sshd` inside every guest** — key material inside a hostile guest, an image
  dependency, and guest networking that unprivileged nodes do not have.
- **SSH only through a ProxyCommand over HTTPS** — scales, needs no new
  dependency, but every client must be configured for it, which is not what
  hosted sandbox services offer. It remains a fallback if decision 2 says no.
- **Running the fleet on Kubernetes** — microVMs need bare-metal KVM hosts and
  a scheduler that knows pools, cached images and snapshots. The fleet runs
  beside the cluster and calls into it.
- **Replacing Kubernetes** — not the goal. Stateful services and what already
  runs well there stay there.
- **Exposing nodes to users directly** — one leaked node token would be the
  whole node, with no ownership or quotas in front of it.

## Verification

- The conformance suite against the gateway, in front of one node and three.
- Load: 1,000 concurrent creates across N nodes, with and without warm pools;
  1,000 concurrent SSH sessions through one gateway port.
- Failure: kill a node mid-burst (its sandboxes are reported lost, new creates
  go elsewhere); kill a gateway replica (clients retry and land on another);
  revoke a key (its sessions end and new ones are refused).
- Security: a user cannot reach, list, SSH into or delete another user's
  sandbox, set `owner` or `tenant` labels, or reach a node directly.
- Beside Kubernetes: a cluster workload with a `sandbox:create` key starts
  agents and reads their results, and cannot do anything else; an agent on the
  fleet reaches the cluster services named in its allowlist and is refused
  every other address in the cluster.
