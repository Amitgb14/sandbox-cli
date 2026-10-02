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
3. **A general sandbox first, with agents as a first-class layer on top.**
   The top level of the CLI is the sandbox: every command there works for any
   command run in one. The agent layer is `sandbox-cli agent`, built from the
   same API calls, and none of it is required to use the product. *(Decided
   2026-10-02. It was "agent-native, not agent-agnostic" until then, and the
   CLI had grown fifteen agent names and a claude-only `usage` command beside
   `run`.)* What the agent layer carries:
   - fifteen agents, five with verified headless modes, and login persistence;
   - git-safe bring-back (general, not agent-specific): work returns as a verified bundle into
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
- **M4 — guestd and images.** *Done.*
  - **`internal/guestproto`** is the host↔guest protocol, over any byte stream:
    one request per connection, a JSON line each way, then length-prefixed
    frames for process I/O. The host treats the guest as hostile: every line,
    frame and announced size is bounded before it is read or allocated. A
    dropped connection kills the process, so a host that goes away leaves
    nothing running. Tested end to end over a unix socket, including a fake
    hostile guest.
  - **`cmd/sandbox-guestd`** is the guest agent. `serve` answers over vsock,
    stdio (macOS) or a unix socket. Processes run as uid 1001 in their own
    process group, with the image's environment. `init` is a Firecracker
    guest's PID 1:
    - it mounts the essentials;
    - it makes the root an overlay of the shared read-only image disk plus a
      per-sandbox sparse scratch disk;
    - it creates the workspace and home for uid 1001, brings up loopback and
      sets the hostname;
    - it supervises `serve` and reaps orphans.
  - **`internal/vsock`**: the guest listener, and the host dial through the
    VMM's unix-socket bridge.
  - **`internal/image`** pulls an OCI image anonymously, unpacks it on the
    host, and builds a read-only ext4 disk with the agent injected:
    - every blob is verified against its digest and declared size before it
      is cached;
    - a manifest pinned by digest must hash to it;
    - the platform is resolved from the index;
    - layers are unpacked with every path resolved *inside* the root, with
      symlinks followed only within it and `..` clamped at it;
    - device nodes are never created on the host;
    - disks are cached by image digest plus a hash of the agent, so a changed
      agent rebuilds the disk.

    Ten escape attempts are tested, and a mutation check showed that a naive
    resolver fails five of them. Built without root, the disk's files belong
    to the building user; that is reported, not hidden.
  - **`images/base/Dockerfile`** has the agents, the tools and uid 1001, and
    none of beta.15's root-phase firewall: egress is enforced on the host now.
    `.github/workflows/base-image.yml` publishes it for amd64 and arm64.
  - **Proved on a real microVM** (`internal/backend/firecracker/vm_test.go`,
    tag `vm`). It builds the disk from the public `alpine:3.20` image (1.2 s),
    and a 2 GiB scratch disk (28 ms). Then:
    - the guest agent answered **56 ms** after the VMM started;
    - processes run as uid 1001;
    - the root is a writable overlay while the shared image disk stays
      byte-identical, and the sandbox user cannot write `/etc/passwd`;
    - loopback is up;
    - 16 MiB written and read back over vsock in 47 ms.

    The same test passed on the real base image, built locally and served from
    a throwaway local registry. The disk build took 14 s from 3.6 GB, and is
    cached after that. The agent answered at 56 ms, `whoami` is `sandbox`, and
    node 22, git, Claude Code 2.1.287 and codex 0.160.0 all run inside the
    microVM.
- **M5 — self-hosted Linux.** *Done, except two checks that need real root
  (below).*
  - **`backend/firecracker`.** Each sandbox is:
    - the image's shared read-only root disk;
    - a sparse scratch disk the guest agent overlays on it;
    - the VMM, reached only through vsock.

    Sandboxes boot with M3's fast kernel line and are ready in ~60 ms. `BuildConfig`
    renders the VM configuration as a pure function. VMs left by an earlier
    sandboxd are reaped at start, and sandboxd terminates its own on shutdown.
  - **Host-enforced egress** (`network.go`). Each sandbox has a tap on its own
    /30, in nftables table `inet sandboxd`:
    - tcp/80 and tcp/443 are redirected to one proxy in sandboxd, which picks
      the sandbox's allowlist by source address (`egressproxy.Server.MatchFor`);
    - DNS goes to a stub resolver (`egressproxy.DNS`) that answers only
      allowlisted names, with the host's address, and **forwards nothing**, so
      DNS is not a way out;
    - everything else from the tap is dropped and nothing is forwarded.

    Every packet the guest may send the host arrives DNAT'd (M3's firewalld
    finding), and the proxy and resolver ports drop anything not redirected.
    `deny` now wins inside the matcher, wildcards included (`NewPolicyMatcher`):
    folding deny into the allowlist only removed exact names, so `*.github.com`
    with `gist.github.com` denied would have let gist through. Caught before it
    shipped. A live policy change is one atomic step in nftables and one in
    the proxy.
  - **The jailer** (`jailer.go`). Each VMM gets its own unprivileged uid
    (one per sandbox: a shared uid would let one VMM signal or trace another),
    and a chroot holding hard links to the shared disks.
  - **Fail closed on capability.** `spec.FitTo` narrows the server's policy to
    what the backend can enforce. Unprivileged, there are no taps, so the
    ceiling is `none`, and the startup line says so. The server then refuses,
    with `unsupported`, any allowlist a backend cannot enforce, even if a
    misconfigured policy would allow it. New conformance run: an endpoint with
    no egress at all.
  - **Idle timeout:** per sandbox, with a policy default and limit. The reaper
    counts a running process as activity, and a request naming the sandbox
    does too.
  - **Workspace in and out:**
    - `POST /v1/sandboxes/{ref}/workspace` streams a git bundle in, and the
      guest clones it into `/workspace`;
    - `GET …/workspace/bundle?base=&branch=` returns `base..branch` as a bundle:
      a 409 when there is nothing new, an honest status code rather than a
      truncated 200, with guest messages passed through `termsafe`.

    The host never mounts the repository, and verifying and fetching what
    comes back is the client's job (M7).
  - **sandboxd** gains:
    - `--backend firecracker` and an operator policy file (`--policy`, YAML,
      unknown keys refused);
    - TLS, required with a token on any non-loopback address;
    - `--insecure-registry` for a local registry, named explicitly.

    Also: a systemd unit, an example policy, `docs/self-hosting.md`, and
    `api.NewClientWithCA` for a private CA.
  - **Image pulls work offline** from the manifest last fetched for a reference;
    an unmapped owner inside a user namespace is counted, not fatal.
  - **The in-image proxy embedding is removed** (`egressproxy/embed.go` and its
    two tests): the proxy runs on the host now.
  - **Verified on this machine, with real microVMs:**
    - the whole conformance suite against the backend, both unprivileged
      (30 pass, 3 skips that say why) and with networking in a user+network
      namespace (all pass, 1 skip by design);
    - the real `sandboxd` binary over its unix socket, and the fake over TLS
      with a private CA;
    - egress from inside a guest:
      - allowed names resolve;
      - denied and unlisted names do not;
      - TLS without a name is refused, and so is a denied name dialled by
        address;
      - other ports go nowhere;
      - a live update to `none` cuts both DNS and connections.
  - **Needs real root, and is handed over:**
    - the jailer under the backend;
    - egress with an uplink (allowed names actually connecting);
    - the full networked suite outside a namespace.

    Commands are in `docs/testing/end-to-end.md`.
- **M6 — local Mac.** *Written and tested on Linux; not yet run on a Mac.*
  - **`backend/macos`** drives the `container` CLI through `os/exec`.
    `BuildRunArgs` is pure, with a golden file:
    - the guest agent is mounted read-only from beside sandboxd, so any image
      works and the agent matches the server;
    - `--network none` unless open egress was asked for;
    - `--` before the image.

    Each request is one `container exec --interactive … sandbox-guestd serve
    --stdio`, the same protocol as on Linux. Closing the connection ends the
    exec, and the guest agent then kills the process it was running.
    Leftovers carrying the backend's label are removed at start.
  - **Capabilities:** egress `none` and `open` only, no allowlist until M3
    measures whether one can be enforced here. Also bind and workspace bundles;
    no live network change. `spec.FitTo` keeps an operator's `ceiling: open`
    (the only thing this backend can filter to) and turns an allowlist default
    into `none`. `BuildRunArgs` refuses an allowlist outright, so it can never
    be rendered as an open sandbox.
  - **Bind.** `bind: {host_path, read_only}` on create is honoured only with
    the operator's `allow_bind` / `--allow-bind` and a backend that can mount.
    The host path goes through `hostpath.ResolveWorkspace`, the
    non-overridable refusals of `/`, home and its ancestors.
  - The guest agent gains `idle`, to be a sandbox's main process.
  - Also: `sandboxd --backend macos`, a launchd agent, `docs/local-macos.md`,
    and the M3 macOS script now records each runtime behaviour this backend
    relies on.
  - **Verified on Linux:** the golden argv, plus the whole conformance suite
    through the real driver against a fake `container` that runs the real
    guest agent in a user-namespace chroot of alpine (all pass; 4 skips that
    say why: no allowlist, and no git in alpine).
  - **Not verified:** the runtime itself. `scripts/m3/macos/run-all.sh`, then
    the conformance suite against `sandboxd --backend macos`, is the hand-over.
- **M7 — the CLI as a client, and parity.** *Done on the branch; merging to
  `main` is the maintainer's call (it drops platforms beta.15 users are on).*
  - **Terminals end to end.** The guest agent allocates a pty (`/dev/ptmx`,
    controlling terminal, resize). `GET …/processes/{pid}/attach` upgrades the
    HTTP connection (`Upgrade: sbx-stream/1`) to the guest protocol's frames,
    over the unix socket and TLS alike:
    - output is replayed from the start, so a late or second attach sees
      everything;
    - disconnecting detaches without stopping the process.

    New conformance test; a `run` with a terminal is refused, so a terminal
    session is always a background process.
  - **The CLI is a client.**
    - `context add/use/ls/rm`, with `local` built in.
    - `run`.
    - Fifteen agent wrappers from the descriptor table. The ten interactive-only
      agents are ported verbatim from beta.15's per-agent files, comments
      included. A wrapper consumes only leading sandbox flags.
    - `list`, `logs`, `attach` (raw mode, SIGWINCH), `kill` (never infers its
      target), `bring-back` and `doctor`.
  - **The workspace round trip.** The repository's `HEAD` goes in as a bundle
    (`HEAD`, so the CLI never creates a branch in your repository) and is
    checked out as `sandbox`. At the end, uncommitted work is committed in the
    guest, and `base..sandbox` comes back. The bundle is verified on the host
    and must carry exactly one ref; it is fetched only into
    `refs/sandbox/<name>`. Every host git call goes through `githard`.
  - **Logins.** Each agent descriptor names its login files (`AuthPaths`). They
    are copied in at start and out at the end, into an owner-only directory
    that refuses symlinks; the prod profile turns this off. The user config and
    `.sandbox.yaml` layer under the flags with the ported trust refusals, and
    secrets are resolved by the credential broker on the host.
  - **Verified on this machine** against `sandboxd` on Firecracker:
    - `run` mirrored the exit code (3);
    - the agent's commit and its uncommitted file came back to `refs/sandbox/…`
      with your branches untouched, and the sandbox was terminated;
    - a run under a real pty got the host terminal's size and `TERM`;
    - `--detach`, `logs` and `kill` all worked;
    - a user config's env and brokered secret reached the guest, and a
      project `.sandbox.yaml` setting `env` was refused.
  - **Docs.** A new README; the beta.15 user docs moved to `_old/docs/`; a
    CHANGELOG entry that names what is removed.
  - **Changed from the plan:** `_old/` stays until M10. Fleet, rescue,
    routing and Studio are rebuilt from it, and deleting the reference before
    the rebuild would mean rebuilding from memory, which `port-from-old`
    exists to prevent.
- **M8 — what makes sandboxes cheap to keep.** *Done, except two things noted
  below.*
  - **Suspend/resume and snapshots,** as optional backend interfaces
    (`Suspender`, `Snapshotter`) behind capabilities:
    - suspend is refused while a process runs (its stream would be cut);
    - a snapshot fixes the image and resources of the forks made from it.

    On Firecracker, each sandbox's own files are named relative to the VMM's
    working directory, so a snapshot restores into another sandbox's directory.
    Forks get reflinked copies of the scratch disk and the memory file.
  - **Measured on this machine through the conformance suite:**
    - two forks restored and answering in **20 ms** each, then diverging;
    - suspend and resume with files intact.
  - **Forks with a network are refused, not half-done.** A snapshot carries the
    guest's address and tap name, so per-fork network identity needs a network
    namespace per VM; that is a later step. A networked sandbox can still be
    suspended and resumed, since it keeps its tap.
  - **Tunnels.** The guest agent dials `127.0.0.1:<port>` only, so a tunnel
    reaches a server in the sandbox and is never a way around egress. On top of
    that: `GET …/tunnel` upgraded to raw bytes, and `sandbox-cli tunnel ID
    [LOCAL:]PORT`, which listens on 127.0.0.1 only.
  - **Two real bugs, found by the tunnel test hanging on a real guest:**
    - The guest agent's vsock descriptors were blocking, so Go deferred
      `close(2)` until a blocked read returned, and the host never saw EOF.
      They are now non-blocking and run through the poller.
    - Every hop tore the whole tunnel down when one direction ended, losing the
      reply of any client that sends and then waits for EOF. Half-closes are
      now passed through at each hop.
  - **CLI:** `suspend`, `resume`, `snapshot`, `tunnel`, and `run
    --from-snapshot`.
  - **SDKs** (`sdk/`):
    - Python, standard library only (unix socket, TLS with a private CA, typed
      errors): six tests run against a real sandboxd (`make test-sdk`), clean
      with resource warnings as errors;
    - TypeScript, `fetch`-based, written to the same contract and **not yet
      run**, since this machine has no Node.

    Attach and tunnels stay in the Go client and the CLI for now.
  - **Root disks have their own cache directory** (`Config.ImageDir`), which
    tests share. Every VM test run had been rebuilding a 3.6 GB disk into a
    fresh state directory, and the runs killed while debugging filled the disk.
  - **Not verified:** suspend and fork under the jailer (real root).
- **M9 — cloud.**
  - Control plane: tenants, API keys, scheduling across nodes, metering, quotas.
  - Image and snapshot storage in object storage.
  - Per-tenant network isolation, abuse controls (egress rate limits, mining
    detection), and the public exposed-ports proxy.
  - The compliance groundwork customers will ask for first: audit export and data
    deletion.
- **M10 — rebuilt on the API.** Fleet, routing and handoff, rescue, context and
  usage, Studio, pools and durable volumes. *In progress.*
  - **Fleet: done.** `fleet run -f fleet.yaml`, `fleet status`, `fleet land`.
    `spec.go` and `verify.go` came across unchanged with their tests. What
    changed is what a task *is*:
    - a task is a sandbox created over the API, cloned into, and run headless
      with its verify wrapped round the agent;
    - its work comes back into `refs/sandbox/fleet/<branch>`, and `land` merges
      that ref, never a worktree an agent could still be writing;
    - state is a file under the config dir, because the containers whose labels
      were beta.15's state store are gone;
    - `land`'s refusals are beta.15's: the branch-versus-base split, the HEAD
      that is not the recorded base, and a dirty tree;
    - `run` exits non-zero when any task did not verify.

    The first real run found a bug no unit test had: tasks created together
    built one root disk together, sharing a partial file. Builds are now
    serialized per key, and each writes a partial file of its own.
  - **Routing and handoff: done.** `routing` came across unchanged with its
    tests. `handoff` builds its briefing in memory, and the CLI writes it into
    the next sandbox, since there is no mount to put it on. Its tests keep
    every assertion they made. `perms_test.go` is dropped: it pinned group
    bits on a host directory bind-mounted into a container. The claude
    transcript reader moved from `_old/internal/agentctx` into a new
    `internal/agentctx`, with a `ParseTranscript` that reads bytes from a
    guest. The rest of that package waits for `context list`. What changed:
    - "the workspace changed" is bring-back's answer: no ref after a
      successful bring-back means unchanged, and anything else is unknown;
    - each attempt is a fresh sandbox, so a guest that lies about having done
      nothing cannot pass work to the next agent;
    - `--detach --fallback` is refused rather than left unwatched.

    Route ids and `route.from` reach the audit log as labels; see below.
  - **Usage: dropped.** It was ported (`a783ae0`) and then removed with the
    decision above. It read one agent's private cache file and had nothing to
    do with sandboxes. `agentusage` and its tests are in git history if Studio
    ever wants the figures; `humanAge` and `shortenHome` went back to `_old/`
    for `recover` and `context list`.
  - **Agents under `sandbox-cli agent`.** The fifteen wrappers and `fleet` moved
    there, with `agent ls` showing which agents can run unattended and whose
    login is saved. There are no aliases for the old spellings: there is no
    backward compatibility with beta.15's command line.
    `TestTopLevelIsAgentNeutral` pins the top level.
  - **Recover: rebuilt, not ported.** beta.15's `rescue` had two halves, and
    the bind mount was the reason for both:
    - *Repair* fixed a host repository a container had broken: a pruned
      `.git/worktrees`, an index git could not read. It is **dropped**,
      because a guest never writes to the host repository now.
    - *Snapshots* committed the host workspace mid-run through a private
      index. They become **checkpoints**, built inside the guest the same way:
      a private index and a hidden ref (`refs/sandbox/checkpoint`), so the
      agent's index, HEAD and branches are untouched. They come back through
      the existing bundle endpoint, verified like bring-back, into
      `refs/sandbox/checkpoints/<id>`.

    `recover` lists the session records whose work never came back, asks each
    one's sandboxd about its sandbox, and says what is left: alive (bring it
    back), gone with a checkpoint, or gone. `restore` and `show` are dropped,
    since a checkpoint is already a ref. Resuming the conversation
    (`recover_resume`) is dropped too: transcripts stay in the guest and are
    not synced. `_old/internal/rescue` stays as the reference until `_old/`
    goes.

  - **Audit: done, on the server, as the port table said** ("audit becomes
    per-sandbox events served by the API"). `sandboxd` appends an event per
    action to `<state-dir>/audit/events.jsonl` and serves a sandbox's events at
    `GET /v1/sandboxes/{ref}/events` (capability `audit`, on by default). It
    records whatever the client was: the CLI, Studio, an SDK or curl. The
    `audit` package kept its rotation and generations code and their tests.
    beta.15's per-run record and its docker-specific tests (firewall denial
    reports, OCI runtimes) are dropped with the client-side log.
    - **Labels** are how a client says why a sandbox exists without the server
      learning what an agent is. They are generic metadata on create, filter
      the listing, and are recorded in `sandbox.created`. sandbox-cli's own
      labels (`agent`, `route.*`, `fleet.branch`) win over a user's `--label`.
    - Conformance: `LabelsAreKeptAndFilterTheListing` and
      `TheAuditLogRecordsWhatHappened`, the second asserting that an env value
      appears nowhere in the events. The Python SDK is tested; the TypeScript
      SDK is changed but untested here (no Node).
    - The log is **best-effort**, so an unwritable file never fails a request.
      `docs/self-hosting.md` says so as a decision an operator may want
      reversed.

  - **Volumes: done on Firecracker.** Each volume is a sparse ext4 file
    attached as one more drive. The guest agent mounts it at boot from
    `sbx.volumes`, and **the host never mounts one**. A volume is attached to
    one live sandbox at a time. It cannot go in `/workspace`, `/tmp` or a
    system directory, and cannot be snapshotted, since a fork would share it.
    `read_only` is the drive's, not just the mount's.
    - Two things real VMs found that the fake could not. A volume left with
      its journal unreplayed could not be mounted from a read-only drive, so
      stopping now flushes and remounts volumes read-only (`sync` with
      `final`), and a read-only mount falls back to `noload` after a crash.
      And killing a VMM drops the guest's page cache, so terminate and
      suspend flush first.
    - Conformance: `AVolumeOutlivesItsSandbox`, `VolumeMountsAreChecked`. Both
      pass on the fake and on a real Firecracker sandboxd, 36 tests in all.
    - The macOS backend reports no `volumes` capability. Whether the
      `container` runtime's volumes fit is a question for a real Mac.
  - **Pools: done.** `pools:` in the policy keeps sandboxes of one image's
    default shape booted. A create resolving to exactly that shape claims
    one; env, name, labels and the idle timeout are the server's and apply
    as usual. Anything fixed at boot that differs boots fresh. Measured on
    the dev host: under 1 ms against 95 ms for a boot.

    Not yet: checkpoints run only while a CLI is attached. Detached runs and
    fleet tasks have nothing driving them, so a detached run's protection is
    still bring-back before the idle timeout.

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
