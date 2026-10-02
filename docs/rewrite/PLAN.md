# Rewrite plan: sandboxes as a product, three ways to run them

**Status:** direction set 2026-10-01; M0 done. Branch `rewrite`; `main` stays
the shipping line until the rewrite reaches parity, then is replaced. Nothing is
deleted from history.

## What we are building

Isolated microVM sandboxes for agents, behind **one API**, available three ways:

| Mode | Where it runs | Backend | Who operates it |
|---|---|---|---|
| **Local** | the user's Mac | the native macOS `container` runtime (macOS 26+, arm64) | the user |
| **Self-hosted** | a Linux machine the user controls (KVM) | Firecracker | the user |
| **Cloud** | our fleet of bare-metal Linux hosts | Firecracker | us |

The same request — create a sandbox, run a command, read a file, attach a
terminal, set an egress policy — behaves the same in all three. The CLI, the
agent wrappers, Studio and the SDKs are clients of that API and never know which
mode they are talking to, beyond a capability list.

**Decisions taken:**

- Rewrite on a branch; **port the security core** with its tests; redesign the rest.
- **VMs only.** The native `container` runtime on macOS, Firecracker on Linux. No docker, no
  podman, no containers as a fallback.
- **Mac is the local platform; Linux is the server platform.** Linux runs as a
  system service (self-hosted and cloud alike), never as an unprivileged tool on a
  laptop — which is what lets it use tap networking, a host firewall and the
  Firecracker jailer, all of which need root.
- **One API, one conformance suite.** A test suite that runs against any endpoint
  is the definition of "the same in all three modes".

## The reference product

The closest existing product is a hosted microVM sandbox service, reviewed
2026-10-01 from its public documentation. It is the clearest statement of what
customers will compare us with. It is not named here; what matters is its shape.

**What they have.**
- *Sandboxes:* Firecracker / Cloud Hypervisor microVMs, cold start claimed at
  84 ms. Six states: pending, running, snapshotting, suspending, suspended,
  terminated. **Named** sandboxes suspend on idle; **ephemeral** ones terminate.
  `timeout_secs` is an *idle* threshold, not a lifetime. Sizing is 1 CPU / 1 GiB /
  10 GiB disk by default.
- *Operations:* run, background processes with streamed output, stdin, signals;
  file read, write, list and delete; PTY sessions with reattach; SSH; IDE gateway.
- *Snapshots:* filesystem snapshots (cold boot) and memory snapshots (warm start,
  processes intact). Clone N copies from a running or suspended sandbox. Suspend
  and resume keep memory.
- *Networking:* open by default. `allow_out` with domains, wildcards and CIDRs
  switches to default-deny; `deny_out` wins over it. Live policy updates are an
  atomic firewall swap. Ingress is either exposed ports at
  a per-sandbox public hostname (HTTP, WebSocket, gRPC, SSH) or authenticated TCP
  tunnels over WebSocket.
- *Images:* built from a Dockerfile in a builder VM, from an SDK image builder, or
  imported from an OCI registry.
- *Around the sandbox:* pre-warmed pools (`warm_containers`, `max_containers`,
  queueing when full), durable shareable filesystems with snapshots, hosted git
  with server-side merges, an orchestration layer, desktop/VNC for computer use,
  docker inside a sandbox.
- *Business:* per-second metering — CPU $0.042–0.07 per core-hour, RAM
  $0.009–0.015 per GB-hour, snapshots $0.07 per GB-month. SOC 2 Type II and HIPAA.
  BYOC requires bare-metal `/dev/kvm` hosts in the customer's cloud account, **with
  the vendor's control plane**. The runtime is Apache-2.0.

**What they do not have, which is our position.**
1. **Local.** There is no laptop version of it. A developer iterating on an
   agent harness gets the same API on their Mac, offline, at no cost per second.
2. **Self-hosted with no phone-home.** Their BYOC keeps their control plane. Ours
   runs entirely on the user's machine, for code that cannot leave the building.
3. **Agent-native, not agent-agnostic.**
   - Fifteen agents with verified headless modes and login persistence.
   - Git-safe bring-back: work returns as a verified bundle into
     `refs/sandbox/`, and nothing in the guest can plant hooks or config on the
     host.
   - Prod profile: no refresh token in reach of the agent.
   - Credential broker and an audit log of every run.

   These are the parts of beta.15 that were hard to get right, and they are what
   M1 ports.
4. **Same API everywhere**, so moving from laptop to own box to our cloud is a
   context switch, not a migration.

**Where we will be behind, deliberately, for a while:** memory snapshots on macOS
(the macOS `container` runtime does not expose them), pools, durable filesystems, hosted git,
desktop/VNC, an orchestration layer. Each is a capability flag. None is a reason
to delay the core.

## Architecture

```
 clients:   sandbox-cli · agent wrappers · Studio · Python/TS SDKs
                 │  Sandbox API v1 (HTTP/JSON + WebSocket streams)
                 ▼
 ┌───────────────────────────────── cloud only ──┐
 │ control plane: tenants, API keys, scheduling   │
 │ across nodes, metering, image + snapshot store │
 └───────────────────────┬────────────────────────┘
                         ▼  (local and self-hosted talk to the node directly)
 sandboxd  (one per machine) ── the same API, served for this machine
   ├─ backend/macos        macOS: the native container runtime
   ├─ backend/firecracker  Linux: VMM, jailer, tap + nftables, egressproxy
   ├─ image, snapshot, state, policy
   └─ vsock ──▶ sandbox-guestd (inside every VM): exec, pty, files, bundles, metrics
```

**`sandboxd`** is the product. It serves Sandbox API v1 for one machine:
- *Local:* a unix socket, started by launchd, access by file permission.
- *Self-hosted:* TLS with API tokens, under systemd.
- *Cloud:* the same binary on every node, behind the control plane.

It grows out of today's `internal/studioapi`, whose hard-won rules come with it:
- A request names a repository by id, never by path.
- A write has no fixture.
- `Origin` and `Host` are guarded.
- The bearer token is compared in constant time.
- The websocket read loop is what notices a closed client.

**`sandbox-guestd`** runs inside every VM and is the only thing the host talks to,
over vsock. It runs processes with or without a pty, streams stdio, resizes,
signals, reads and writes files on request, takes a bundle in, hands a bundle out
and reports metrics. Both backends use it, so macOS and Linux cannot drift.

**The control plane** exists only in the cloud: tenants, keys, quotas,
scheduling, metering, image and snapshot storage. Self-hosted is one node with no
control plane. A multi-node self-hosted control plane is a later decision (see
open questions).

### The API, v1 core

Modelled on what agents actually need, with the reference product's shape where it is right:

- **Sandboxes**: create (image, cpus, memory, disk, env, idle timeout, name →
  named vs ephemeral, network policy, workspace), get, list, update (network
  policy live), terminate. States: pending, running, stopped, terminated — plus
  suspended where the backend can.
- **Processes**: run (wait, capture), start (handle), stream output, write and
  close stdin, signal, list.
- **PTY**: create, attach, resize, reattach by session.
- **Files**: read, write, list, delete — inside the guest, through guestd, never a
  host path.
- **Workspace**: clone-in from a git bundle or a repository URL, and bring-back
  as a verified bundle. Clone is the only mode in self-hosted and cloud: a remote
  machine has no host path of the user's to bind. Bind exists only locally on
  macOS, as an option, and is never the prod default.
- **Network policy**: `none`, `allowlist` (`allow` + `deny`, deny wins, names
  matched by `egressproxy`), `open` — set at create and updatable live.
- **Ingress**: authenticated TCP tunnels over WebSocket. Public exposed ports are
  cloud-only and come later.
- **Capabilities**: what this endpoint supports (memory snapshots, suspend,
  bind workspace, pools…), so a client asks rather than assumes.

Later, versioned as additions to v1: snapshots and clone, suspend/resume, pools,
exposed ports, durable volumes.

**Protocol:** HTTP/JSON with WebSocket for streams, standard library only. It is
what `studioapi` already speaks, browsers and both SDKs can use it directly, and
the project's dependency rule (stdlib, cobra, yaml.v3) holds. gRPC would add a
toolchain for no gain a client would notice.

### What each mode needs that the others do not

| | Local (Mac) | Self-hosted (Linux) | Cloud |
|---|---|---|---|
| Transport | unix socket | TLS + token | TLS + API key, via control plane |
| Workspace | bind (option) or clone | clone | clone |
| Egress enforcement | Phase-0 decides: in-guest firewall or vsock to host proxy | tap + nftables on host + `egressproxy` | same as self-hosted |
| Jailer / cgroups | n/a (the OS runs the VM) | yes (root service) | yes, plus per-tenant network isolation |
| Images | pull OCI, or `container build` | pull OCI → ext4; Dockerfile via builder VM | same, plus a shared image store |
| Persisted agent HOME | a directory sandboxd owns | a drive sandboxd owns | a volume per tenant |
| Suspend / memory snapshot | no (capability off) | Firecracker snapshots | same, stored in object storage |
| Multi-tenancy, metering, abuse controls | — | — | yes |

Two consequences worth stating:
- **The agent's login stops being a directory the host reads.** Agent HOMEs live
  where sandboxd keeps them. History, `context list` and `usage` become API calls
  through guestd or sandboxd, not filesystem reads on the host. Locally that is
  invisible. Remotely it is the only design that works at all.
- **Remote changes the trust model.** Today the trusted party is "the human at
  the terminal". On a remote or cloud endpoint it is "whoever holds a token", so
  every privileged setting the CLI took from the user's own config becomes a
  server-side policy, or a scoped token permission.
  - The principle carries over unchanged: a request may tighten what the daemon
    decided, never loosen it. That is the `Launch` screen's read-only egress line,
    generalised.

## What is ported, what is rewritten

| Old | Fate | Notes |
|---|---|---|
| `config` (`trust.go`, `profile.go`) | **port** | becomes client-side config plus **server-side policy**; the trust tests carry over |
| `sandbox/mounts.go` (`RefuseUnsafeHostPath`) | **port** | guards macOS bind and every host path sandboxd reads |
| `githard` | **port** | already fixed on `fix/host-escapes`; every host-side git call in bring-back |
| `creds` | **port** | values reach the guest through guestd, never an argv or a log |
| `egressproxy` | **port + move** | host-side on Linux; `allow` + `deny`; live update |
| `agents` (descriptor table) | **port** | the agent wrappers become clients of the API |
| `audit`, `termsafe`, `version`, `timezone` | **port** | audit becomes per-sandbox events served by the API |
| `studioapi` | **port + reshape** | the seed of `sandboxd`: guard, tokens, websocket, project registry |
| `runtime` (docker/podman) | **drop** | its pure-renderer discipline carries into each backend |
| `sandbox/hostgroup.go`, `writable.go`, `guestdir.go` | **defer** | docker bind-mount uid problems; back only if macOS virtio-fs has them |
| `cli` | **rewrite** | a thin API client with contexts: `local`, `<self-hosted>`, `cloud` |
| `worktree`, `image`, `metrics`, `doctor` | **rewrite** | against the API |
| `fleet`, `routing`, `handoff`, `rescue`, `agentctx`, `agentusage`, `studio/` | **later** | rebuilt on the API after parity |
| `netpolicy` | **drop** | vestigial |

## Layout

```
cmd/sandbox-cli         client
cmd/sandboxd            the API server, one per machine
cmd/sandbox-guestd      inside every VM (static Linux binary)
internal/
  api/          v1 types, the Go client, and the conformance suite      (new)
  server/       HTTP + WebSocket serving, auth, guard (from studioapi)  (ported)
  backend/      Backend interface + Capabilities                        (new)
    macos/  firecracker/  fake/
  guestproto/   host <-> guestd over vsock                              (new)
  policy/  hostpath/  githard/  creds/  agents/  egressproxy/  audit/   (ported)
  workspace/    clone-in, bring-back                                    (new)
  image/  snapshot/  state/                                             (new)
  cli/          cobra wiring, contexts                                  (new)
```

The M0 skeleton's `backend/oci`, `spec` and `session` are renamed or folded in
at M2's first commit: `spec` becomes the server's request validation, and
`session` becomes API calls.

## Milestones

Every milestone ends with `go vet`, `go test ./...` and `go test -race` green.
From M4 on, it also ends with **the conformance suite green against every mode
that exists so far**, run by hand on a Mac and a KVM Linux host.

- **M0 — clear the tree.** *Done.* The old code is in `_old/`, and the binary
  builds.
- **M1 — ported core green.** *Done.*
  - `githard`, `creds`, `agents`, `egressproxy`, `audit` and `termsafe` moved
    with `git mv`; their tests pass untouched.
  - `config` became `policy`. The `engine` and `runtime` keys are gone with the
    engines they chose, and so are their tests. The default image is a variable
    the image package sets, so `policy` imports nothing below it.
  - `hostpath` is the host half of the old `mounts.go`: the root, home and
    ancestor refusals and `ResolveWorkspace`, with their tests.
  - The guest half (`ValidateMountTarget`, the protected-target list) stays in
    `_old/` for the macOS backend (M6), the only one with bind mounts.
  - `timezone` moved into `spec` with its pure tests. The tests that go through
    `BuildSpec` wait in `_old/` until `spec` builds requests again.
- **M2 — the API, on paper and in Go.** *Done.*
  - `docs/api/v1.md`: capabilities, sandboxes, processes (run, background,
    output stream, stdin, signals), files, network policy, errors. PTY, tunnels,
    workspace, idle timeout and snapshots are listed as later additions.
  - `internal/api`: wire types and a client that speaks HTTP or a unix socket.
  - `internal/spec`: requests resolved against the server's policy. Tighten,
    never loosen: mode ceiling, `may_allow` for added names, `deny` always
    accepted, an empty allowlist refused, reserved environment names refused.
  - `internal/server`: the handlers, with the Studio daemon's request guard
    carried over (Host, Origin, constant-time token, content type, body caps).
    It keeps its own sandbox list, so a reference never reaches the backend
    unresolved. Process output is capped at 8 MiB per stream, and the cut is
    reported.
  - `internal/backend`: the interface. `backend/fake` is in memory with seven
    builtin commands and runs no host process.
  - `cmd/sandboxd`: a unix socket at 0600 by default. It refuses a non-loopback
    address without a token, a token file readable by others, and a second
    instance on the same socket.
  - **Conformance suite** (`internal/api/conformance`): 27 tests that know only
    the client. They run against the fake under two policies, so the
    capability-gated and `may_allow`-gated branches both execute. They also ran,
    unchanged, against a real `sandboxd` over its unix socket.
  - Found by the suite before anything shipped: `allow: []` was collapsing into
    "the default list" through `omitempty`. The field now keeps null and empty
    distinct on the wire.
  - Decided along the way: the skeleton's `backend/oci` and `session` are gone;
    `backend/macos` and `backend/firecracker` hold their places. The token in a
    query string returns only with the first WebSocket endpoint.
- **M3 — measure (no product code merged).**
  - *Firecracker as root:* boot time, memory, the jailer, tap + nftables egress
    through `egressproxy`, vsock throughput for bundles, snapshot and restore
    time.
  - *macOS `container`:* boot time, read-only bind honoured, virtio-fs
    ownership, whether the guest can program iptables or egress must go over
    vsock, labels across restarts, and stdio over `exec -i`.
  - *Status: Linux done, macOS outstanding.* Scripts are in `scripts/m3/`. The
    Linux results come from the maintainer's run on 2026-10-02: an x86_64 EL10
    host (kernel 6.12) on xfs with firewalld active, Firecracker 1.17.0, guest
    kernel 6.1.155, the full suite as root.
    - **Boot is dominated by the kernel command line, not by the VM.** From
      starting the VMM to the guest's init being ready, the median was 742 ms
      with default arguments, 578 ms with `quiet`, and **53 ms** with `quiet`
      plus the keyboard-controller probe disabled (`i8042.noaux i8042.nomux
      i8042.nopnp i8042.dumbkbd`). The guest kernel's own boot fell from 620 ms
      to 20 ms. `reboot=k` still ends the VM. The backend's kernel command line
      is a measured decision, and these arguments are its starting point.
    - **The jailer costs almost nothing.** The guest was ready 60 ms after
      start. The VMM ran as uid 65534 in the chroot, and the only `/dev/kvm` it
      had was the chroot's own `crw------- nobody nobody`. Linux sandboxd runs
      as root to use it, as planned.
    - **Memory:** the VMM process holds 68 MB with an idle 512 MiB guest, and
      97 MB with 2 GiB configured. Guest memory is only resident once touched.
    - **vsock:** a fresh connection round trip takes 115 µs. Throughput is
      1.33 GB/s host→guest and 1.66 GB/s guest→host, so the guest agent and
      bundles over vsock are not a bottleneck.
    - **Snapshots, 1 GiB guest:**
      - pausing takes 5 ms and a full snapshot 229 ms;
      - the memory file is the full 1 GiB, on disk as well as apparent, even
        for an idle guest;
      - restoring into a fresh VMM takes 6 ms, and the guest answers over vsock
        9 ms after the load starts.

      Suspend, resume and fork are cheap enough to be ordinary operations. The
      cost to manage is disk: a dense memory file per snapshot. M8 should
      measure diff snapshots, and punching holes in what an idle guest never
      touched.
    - **Disk:** a 1 GiB copy takes 271 ms, against 7 ms as a reflink on xfs.
      Where the filesystem shares blocks, a per-sandbox root disk is close to
      free; elsewhere it is a full copy, so M5 should prefer a read-only base
      plus a per-sandbox overlay.
    - **Egress enforced on the host works.** 10 of the 11 probes from inside the
      guest behaved as designed:
      - the allowed name went through by TLS SNI and by HTTP Host;
      - a name not on the allowlist was refused, as were TLS with no SNI, other
        TCP ports, DNS over UDP, and the host's own ports;
      - nftables dropped 24 packets at input and 4 at forward;
      - the proxy's decisions log shows exactly the expected allows and denials.
    - **The one surprise, and two decisions it forces.** An explicit
      `CONNECT` to the proxy port was refused ("no route to host"), even though
      our own table accepts it. The reason is firewalld:
      - it accepts any packet whose connection was DNAT'd, so the redirected
        traffic passed;
      - it rejects other unexpected inbound traffic with `admin-prohibited`;
      - a reject in any nftables table wins over an accept in ours.

      It failed closed, but it shows the design leaning on another firewall's
      defaults. So:
      1. **Redirect only.** The guest never addresses the proxy, so there is no
         proxy port to open and no explicit-proxy mode to support. Every client
         is caught by the redirect whether or not it honours `HTTPS_PROXY`.
      2. **sandboxd must not assume it owns the host firewall.** At startup it
         checks, from a probe VM, that an allowed name passes and a denied one
         does not, and refuses to serve if either answer is wrong. It also
         registers its tap devices with a running firewalld (or ufw) rather than
         hoping that defaults line up. This belongs in `doctor` too.
    - **Still to run:** macOS, the whole of `scripts/m3/macos/run-all.sh`.
- **M4 — guestd and images.**
  - `sandbox-guestd` and `guestproto`, tested over a unix socket with no VM.
  - Image pipeline: OCI pull → ext4 rootfs (Linux) and OCI pull (macOS).
  - CI publishes the base image.
- **M5 — self-hosted Linux.** `backend/firecracker` and `sandboxd` under systemd
  with TLS and tokens. Clone-in and bring-back. Network policy enforced on the
  host. Idle timeout. Conformance green.
- **M6 — local Mac.** `backend/macos` and `sandboxd` under launchd on a unix
  socket. Bind (option) and clone. Conformance green, minus the capabilities
  reported off.
- **M7 — the CLI as a client, and parity.**
  - Contexts (`sandbox-cli context use local|<host>|cloud`).
  - The fifteen agent wrappers, prod profile and persisted auth through the API.
  - `--detach`, `list`, `logs`, `attach`, `kill`, `bring-back`.
  - CLAUDE.md rewritten. `rewrite` replaces `main`; `_old/` is deleted. The
    CHANGELOG names the dropped platforms.
- **M8 — what makes sandboxes cheap to keep.** Suspend and resume, filesystem and
  memory snapshots, clone (Firecracker), tunnels, the Python and TypeScript SDKs.
- **M9 — cloud.**
  - Control plane: tenants, API keys, scheduling across nodes, metering, quotas.
  - Image and snapshot storage in object storage.
  - Per-tenant network isolation, abuse controls (egress rate limits, mining
    detection), and the public exposed-ports proxy.
  - The compliance groundwork customers will ask for first: audit export and data
    deletion.
- **M10 — rebuilt on the API.** Fleet, routing and handoff, rescue, context and
  usage, Studio, pools and durable volumes.

## Risks

- **Scope.** This is no longer a CLI rewrite. It is a server, a guest agent, two
  VM backends, an image pipeline and, later, a multi-tenant cloud. M2's
  conformance suite and M3's measurements exist so that each later milestone is
  checked against something fixed rather than against intuition.
- **Cloud is a different business from software.** Abuse handling, tenant
  isolation, on-call and compliance are not features of `sandboxd`, and none of
  them can be bolted on at the end. M9 is where they start, and nothing before it
  should assume a single trusted user in a way M9 has to undo. That is why auth
  and server-side policy are in M5, not M9.
- **Platform loss.** Windows, Intel Macs, macOS 15 and earlier, and Linux laptops
  lose local support at M7. Linux users get self-hosted instead; the others get
  the cloud.
- **Rewrite drift.** Before `_old/` is deleted, every CLAUDE.md invariant needs a
  test in the new tree, or a note that the backend change made it obsolete. Most
  of the docker ones are obsolete: icc, userns, uid mapping, the umask.

## Open questions

1. **Open source boundary.** Recommendation: `sandboxd`, `sandbox-guestd`, the
   CLI and the SDKs open; the cloud control plane closed. Self-hosting is what
   earns trust for a sandbox, and the reference product's runtime is already open source.
2. **Multi-node self-hosted.** One node at M5. Does a customer with five Linux
   boxes get the control plane too (open, closed, or paid)?
3. **API compatibility.** Our own API, shaped like the reference product's where it is
   right (recommended); or a surface compatible with its SDK so those users can
   switch with a base URL?
4. **Names.** Product, binaries (`sandbox-cli`, `sandboxd`) and domains, before
   M7 makes them public.
