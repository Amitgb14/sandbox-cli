# Security

What the boundary is, what it is not, and the two profiles you choose between.
This describes the design as it ships since the rewrite merged: every sandbox is
a virtual machine with its own kernel, served by `sandboxd` behind one API. The
container design before it (0.0.1 and earlier) is described in `_old/docs/`.

- [The boundary](#the-boundary)
- [What crosses it, and how it is checked](#what-crosses-it-and-how-it-is-checked)
- [Security profiles](#security-profiles)
- [Who can reach the API](#who-can-reach-the-api)
- [Check an endpoint first (`doctor`)](#check-an-endpoint-first)
- [Not built, or still open](#not-built-or-still-open)

Deeper reading in this directory:

| Document | What it is |
|---|---|
| [`secrets.md`](secrets.md) | What sandbox-cli protects about a secret, and what it does not |
| [`open-items.md`](open-items.md) | The backlog, with where each item stands in the rewrite |
| [`audit-2026-07-26.md`](audit-2026-07-26.md) | The audit of the container design: findings and the reasons behind its rules |
| [`../rewrite/invariants.md`](../rewrite/invariants.md) | Every rule from that design, and the test that pins it here or why it is obsolete |

## The boundary

**A sandbox is a VM.** On Linux it is a Firecracker microVM; on a Mac, a VM of
the native `container` runtime. Either way the agent runs on a kernel of its
own, so a kernel bug inside it is the guest's, not the host's. No profile or
flag runs an agent without one.

**The host mounts nothing the guest wrote.** Your repository goes in as a git
bundle and is cloned inside the VM. The host never mounts the workspace,
`.git`, or the agent's home. The only exception is `--bind`, on local
endpoints only. A host path passes `hostpath` first, which refuses the
filesystem root, your home directory and any ancestor of it, compared by device
and inode rather than by string.

**`sandbox-guestd` is the only way in.** It runs inside every VM, does what the
host asks over a vsock channel, and nothing else. No guest command makes the
host read an arbitrary path back on the guest's initiative. Processes run as an
unprivileged user (`ProcessesDoNotRunAsRoot`, conformance).

**Egress is by name, enforced outside the VM.** Under a root `sandboxd` with
`--network`, a sandbox's traffic goes through a tap device and an nftables
ruleset on the host. Its tcp/80 and tcp/443 traffic reaches a proxy that decides
by the name asked for (SNI, `CONNECT`, `Host`), resolved per connection, and
subdomains are not implied. Everything else is dropped. Sandboxes cannot reach
each other (`SandboxesCannotReachEachOther`, conformance). An unprivileged
`sandboxd` gives sandboxes no network at all, and says so.

**Fail closed.** A control that was asked for and cannot be delivered refuses
the run. A backend that cannot enforce an allowlist reports it through its
capabilities, and an allowlist request is refused rather than run open.

## What crosses it, and how it is checked

Everything from inside a guest is untrusted input. Each thing that comes back
is checked before the host acts on it:

| What comes back | How it is checked |
|---|---|
| The run's commits | A git bundle, `git bundle verify`'d against your repository. It must carry exactly the expected branch, and it lands only in `refs/sandbox/<name>`. No branch of yours moves; merging is a git command you run. |
| Checkpoints | Built through a private index, so the agent's index, HEAD and branches are untouched, and checked the same way into `refs/sandbox/checkpoints/<id>`. |
| A saved agent login | Only named files, written owner-only and never through a symlink. Settings files keep only their login keys (`agents.FilterAuth`), so an MCP server or other command an agent planted does not reach later runs in other repositories. |
| A transcript (routing handoffs, agent states) | Parsed as data, bounded in size and number. Only formats verified against real sessions are read. It is quoted to the next agent, never acted on by the host. |
| Mirrored work fetched back | It must carry exactly the commit its name says, descend from your repository's root, and on the uploading machine match the commit recorded at upload. It lands only in `refs/sandbox/mirror/`. |
| Text printed to your terminal | Labels, names and paths go through `termsafe`. |

**Every git command sandbox-cli runs on the host goes through `githard`.**
Hooks, `core.fsmonitor`, clean and smudge filters, textconv, merge drivers and
signing programs that the repository's config names are neutralised. The rule
is pinned by a scan that fails on any host-side git call that bypasses it.

**A project's `.sandbox.yaml` may tighten what is in force, never loosen it.**
It cannot set `secrets`, `env`, `env_allow`, `routing`, `providers` or `mirror`,
or lower the profile or the network mode. A key this version does not read is
refused rather than ignored, naming what replaced it.

**What an agent runs from is read-only to it.** An agent the image does not carry
is installed once per endpoint into a tools volume, by a sandbox that runs only
the install. Its runs mount that volume read-only, enforced by the drive, so a
compromised agent cannot rewrite what its next run executes.

## Security profiles

Two profiles, and neither is the lax one. Local development is where a
prompt-injected agent has the most valuable things in reach, so no profile
relaxes the boundary above.

| | `dev` (default) | `prod` |
|---|---|---|
| Egress | allowlist with the server's baseline | allowlist, **baseline off**: only the hosts you name |
| Agent login between runs | saved and restored | **off**: no long-lived credential enters a sandbox |
| An allowlist that resolves to nothing | — | **refused**, never replaced by the server's default |
| `--network open` | allowed where the server's ceiling allows | **refused** |

```sh
sandbox-cli agent claude --profile prod --allow api.example.com -p "…"
```

The profile is checked against the run as it will be, with the flags folded
in, so `--network` and `--allow` cannot take a prod run out of prod. A project
`.sandbox.yaml` may demand `prod` and never ask for `dev`. Memory, CPU and disk
limits are sandboxd's: its policy file sets them and caps every request.

## Who can reach the API

- **Locally**, `sandboxd` listens on a unix socket only its owner can open, and
  needs no token there.
- **On TCP it refuses to start without a token, loopback included.** A loopback
  port is reachable by every user on the machine. An address other machines can
  reach needs TLS as well.
- **Every request** passes a guard first: a loopback `Host` (or one configured),
  no foreign `Origin`, the bearer token, and a typed body.
- **Studio** listens on loopback with a token of its own for each launch. The
  token arrives in the URL's fragment, which is never sent to a server.
  sandboxd's token stays server-side; Studio's proxy adds it.
- **The audit log** records environment variables by name and each process by
  its program, argument count and a hash, never its arguments, because an
  agent's arguments are its prompt.

## Check an endpoint first

```sh
sandbox-cli doctor
```

It prints what the current context's `sandboxd` can do: its backend, the
capabilities it has (an allowlist, volumes, snapshots), its network default and
ceiling, and its limits. A request for something it lacks is refused at create
time, not discovered halfway through a run.

## Not built, or still open

- **The agent can read every secret it is given.** That is permanent by design;
  [`secrets.md`](secrets.md) says what is protected instead and how to make a
  leak cheap.
- **Misuse of a credential the agent legitimately holds** survives every
  option considered, and so does posting one to a host you allowed. They are
  the argument for keeping what an agent can reach small. DNS is not a channel
  under an allowlist: the guest's resolver never forwards a query. It answers
  an allowed name with an address on the host, which the proxy handles, and
  every other name with NXDOMAIN (`internal/egressproxy/dns.go`). `--network
  open` is open, DNS included.
- **The macOS backend has not yet been run against the real runtime.** Its
  isolation between sandboxes in open mode is end-to-end row 31.
- The rest of the backlog, with where each item stands in the rewrite, is
  [`open-items.md`](open-items.md).
