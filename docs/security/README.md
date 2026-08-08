# Security

What the boundary is, what it is not, and the two profiles you choose between.

- [Security profiles](#security-profiles)
  - [prod demands a kernel of its own](#prod-demands-a-kernel-of-its-own-where-one-can-exist)
- [Check the host first (`doctor`)](#check-the-host-first)
- [Security model](#security-model)
- [Stronger isolation (microVM / gVisor)](#stronger-isolation-microvm--gvisor)
- [Not built](#not-built)

Deeper reading in this directory:

| Document | What it is |
|---|---|
| [`audit-2026-07-26.md`](audit-2026-07-26.md) | The audit ledger: findings, threat model, per-round counts, all fixed |
| [`open-items.md`](open-items.md) | The live backlog — what is still open, each reproduced by execution |
| [`secrets.md`](secrets.md) | What sandbox-cli protects about a secret, and what it does not |

## Security profiles

Two profiles, and neither is the lax one. Local development is where a
prompt-injected agent has the most valuable thing in reach — your machine, your
credentials, your other repositories — so no profile relaxes the host boundary.
**Both are secure; they differ in what they optimise within a secure baseline.**

The one difference of kind: a control this host cannot provide is a **warning**
under `dev`, because you are there to read it, and a **refusal** under `prod`,
because nobody is watching an unattended run and one that degraded quietly is
worse than one that stopped.

| | `dev` (default) | `prod` |
|---|---|---|
| Egress | allowlist + baseline | allowlist, **baseline off** |
| Persisted agent login | on | **off** — no long-lived credential in the container |
| Host history mount | on | off |
| seccomp missing on the daemon | warns | **refuses** |
| memory / cpus / pids | as configured | bounded |
| a kernel of its own | reports what is available | **required where the engine proves it can** |

```sh
sandbox-cli claude --profile prod
```

A profile is the **base** config layer — under your own config rather than over
it — so a trusted config can still tune a setup. A project `.sandbox.yaml` may
**demand** the stricter profile and may never ask for the weaker one, the same
direction-of-travel rule the network keys follow.

### prod demands a kernel of its own, where one can exist

A container shares the host kernel, and no amount of capability-dropping changes
what a kernel vulnerability means. prod may carry untrusted agents, so on a host
that **can** give a run its own kernel, prod requires one: the run refuses unless
`runtime:` names a microVM or gVisor runtime, and `doctor --profile prod` fails
before you schedule anything on that machine.

**The engine is asked, and only what it answers is acted on.** Which boundary is
possible is a fact about the daemon, which may not be on the machine you typed
the command on — so prod reads what the engine reports, and refuses only what
that proves:

| The engine says | prod |
|---|---|
| the run gets a runtime with a kernel of its own — named by you **or by the engine's own default** | runs |
| the run names something else non-default (`sysbox-runc`, an unrecognised name) | runs, and says it is **not** vouching for it |
| the run names a runtime this engine has not registered | **refuses** — the launch would fail anyway |
| a stronger runtime is registered and neither the run nor the default uses it | **refuses** — the boundary was there and unused |
| no stronger runtime reported | runs, and **warns on every run**, naming what the engine did report |
| the engine could not be asked | **refuses** — prod does not assume the answer it would prefer |

The warning row is a deliberate limit rather than an oversight. An engine that
names no stronger runtime has not shown there are none — podman answers this
question with its *active* runtime rather than its registered set, which is also
why a name it does not list is only a refusal on docker — and no signal
distinguishes a Linux host that could install Kata from a VM image its user does
not compose.

The **engine's default runtime counts**: a host whose `daemon.json` sets
`default-runtime: runsc` gives every container a kernel of its own without a word
in any sandbox-cli config, and prod reads that rather than refusing the setup
that had already done the work. An earlier version tried to infer that from the daemon's product
name and from podman's `serviceIsRemote`, and was wrong in both directions:
colima and OrbStack users were refused with a remedy they could not act on, while
a podman client talking to bare metal had the demand waived silently. A boundary
control that guesses is worse than one that says what it does not know.

So the way to *have* the guarantee is to name the runtime: `runtime: kata-runtime`
in your own config, or `--runtime` on the run. What prod will not do is choose
for you — which of Kata or gVisor a machine has is the machine's business, and a
profile naming either would refuse every host that has the other.

`sandbox-cli doctor --profile prod` reports the same verdict from the same shared
classification, so the preflight and the launch cannot disagree, and
`doctor --runtime NAME` asks it about the run you are about to make rather than
about the config alone.

The demand is enforced on the runtime a run **actually gets**, not on the one its
config names — `--runtime` reaches a run through a different path, and a check
made against the configuration alone would pass while the container launched on
a shared kernel.

prod turning persisted auth off is the substantive answer to the credential
problem: the default auth path is not an API key but an **OAuth refresh token**
in the persisted HOME, readable by the agent. prod does not mount it, so there is
nothing to steal — no TLS-terminating proxy required.

## Check the host first

`prod` runs unattended, so ask whether the machine can deliver it *before* you
schedule anything on it:

```console
$ sandbox-cli doctor --profile prod
profile: prod

  ok    docker daemon      reachable
  FAIL  seccomp            no syscall filter is applied; the container gets the full syscall table
  ok    egress firewall    a container here can program the nat, redirect, owner and conntrack rules
  FAIL  isolation runtime  this engine can give a run its own kernel and nothing selected one: kata-runtime

sandbox-cli: this host cannot satisfy the prod profile: seccomp, isolation runtime
```

Non-zero exit under `prod`, so a scheduler notices. The firewall check is
*tried*, not queried — rootless and userns-remapped daemons cannot program
iptables, and that is a property of the daemon rather than something you can ask
about. A question that cannot be *asked* counts as a failure under prod too: it
does not get to assume the answer it would prefer. Under `dev` the same host
passes with warnings, and `sandbox-cli doctor` with no flags reads as an everyday
"is my setup ready?" check.

## Security model

> A full security audit of this codebase was carried out on 2026-07-26: 22 issues
> found, all reproduced end to end and all fixed. A same-day re-audit of those
> fixes, and a later external review of the pull request, each found more; those
> are fixed too. The findings, the threat model, the per-round counts and the
> limits that are still open are recorded in
> [`audit-2026-07-26.md`](audit-2026-07-26.md).

- **Only `/workspace` is host-connected and writable** for `sandbox-cli run`.
  `HOME`, `/etc`, `/` inside the container are ephemeral and destroyed on exit
  (`--rm`). The agent wrappers add two more host paths by default,
  both scoped and both opt-out: the sandbox-owned agent home
  (`~/.config/sandbox/agents/<agent>`, `--no-persist-auth`) and your Claude
  history for the current project (`--no-sync`). When the workspace is a git
  worktree, the parent repo's `.git` is mounted read-write too — git cannot work
  otherwise. Anything else needs `--mount`.
- **The host home is never mounted.** sandbox-cli refuses to mount `/`, your home
  directory, or any ancestor of it as the workspace. The comparison is by
  identity (device + inode), not by string, so a differently-cased path cannot
  slip past it.
- **Default-deny credentials.** Nothing from your host env crosses the boundary
  unless you opt in via `env_allow` / `--env-allow` / `--env`. Each agent wrapper
  ships a suggested allowlist (e.g. `ANTHROPIC_API_KEY`) applied only if the value
  is set. For OAuth-file logins, mount just the agent's own dir, e.g.
  `--mount ~/.claude:/sandbox/home/.claude:rw`.
- **Credential broker.** For secrets you'd rather not put on the command line or
  in a config file, `secrets:` / `--secret NAME=file:PATH|cmd:COMMAND|env:VAR`
  resolves the value at run time (from a file, a host command like `gh auth
  token` / `op read`, or a host env var) and forwards it into the container *by
  name*, so the raw value never appears on the docker argv, in `--dry-run`, in
  config, or in shell history — and `cmd:` sources can be short-lived tokens
  fetched fresh each run. The agent process still receives the value as an env
  var; the full posture is in [`secrets.md`](secrets.md).
- **Hardened container by default.** Every run drops all Linux capabilities
  (`--cap-drop=ALL`), forbids privilege escalation (`--security-opt
  no-new-privileges`), and caps process count (`--pids-limit`) to blunt fork
  bombs. Tune these under `security:` in config; add memory/CPU limits with
  `--memory` / `--cpus`; or use `--no-hardening` to fall back to the unhardened
  behavior while debugging.
- **Egress allowlist, one word away.** It is the built-in default, and the
  config the installer writes starts you at `mode: default` (unrestricted)
  instead — change that one word, or pass `--allow DOMAIN` for a single run, and
  outbound traffic is default-denied by an in-container firewall that permits only
  DNS, established flows, a baseline of agent APIs + package registries
  (`api.anthropic.com`, `registry.npmjs.org`, `pypi.org`, `github.com`, …), and any
  domains you add. This lets `npm install` / `pip install` / `git` keep working
  while blocking arbitrary exfiltration from a prompt-injected agent. The firewall
  is programmed at startup (needs `NET_ADMIN`, added only in this mode) and then
  the run drops back to the non-root `sandbox` user; it fails closed if setup
  errors. Requires a Linux-capable Docker host (iptables).
- **Ingress is filtered in allowlist mode too.** `INPUT` is default-deny with
  three exceptions — loopback, established flows, and the container ports you
  named with `--publish`. Without that, a connection dialled *in* got a working
  reply path back out past the allowlist.
- **Nothing is reachable inward unless you publish it.** No container port is
  exposed to the host by default. `--publish` / `ports:` opens one, and a spec
  that names no address binds to `127.0.0.1` rather than every interface — the
  one place sandbox-cli deliberately differs from `docker -p`, because "let me
  see my dev server" should not also mean "serve it to the network". Write
  `0.0.0.0:3000:3000` when you mean that.
- **A project `.sandbox.yaml` is untrusted input** and the privilege-relevant keys
  are refused from it — see
  [Configuration](../configuration.md#a-project-config-is-untrusted).

## Stronger isolation (microVM / gVisor)

By default the container is a normal (shared-kernel) Docker container. If your
host has a stronger OCI runtime registered, select it per run for a harder
boundary — no other change to how the sandbox is built:

```sh
sandbox-cli claude --runtime kata-runtime   # microVM: own kernel (hardware boundary)
sandbox-cli claude --runtime runsc          # gVisor: userspace-kernel syscall filter
```

Set it once in config with `runtime: kata-runtime`. This requires the runtime to
be installed and registered with the Docker daemon (e.g. Kata needs a Linux host
with nested virtualization; it is not available on stock macOS Docker Desktop —
see [Platform support](../platforms/README.md)). Everything else — mounts,
hardening, egress allowlist, caches, secrets — works unchanged on top of it.

A run does not have to be taken on trust afterwards: the runtime it asked for is
recorded in the audit log (`"runtime": "kata-runtime"`, omitted when the run took
the host default), and `sandbox-cli list` grows a `RUNTIME` column as soon as any
session is on something other than the default — read back from the engine rather
than remembered from the launch.

Making this a first-class, tested path, and letting `prod` *demand* it, is
[roadmap task 3](../roadmap/task-3-stronger-isolation.md).

## Not built

Clean seams are left in the code for these. Two have since become roadmap work
rather than permanent exclusions:

- **A header-injecting secrets proxy**, so the agent never sees the raw value.
  Still out of scope: it requires terminating TLS, which is a decision rather than
  a task ([open item 2](open-items.md)).
- **Per-request egress policies** (HTTP method and path, request inspection).
  Still out of scope, and for the same reason — it needs the same TLS
  termination, so it should be decided once alongside the above rather than
  twice.
- **Snapshots** in the checkpoint/restore sense, and **audit trails** in the
  per-command, replayable sense. Both are now planned: roadmap tasks
  [4](../roadmap/task-4-run-provenance.md) and
  [5](../roadmap/task-5-checkpoint-and-fork.md).
- **Risk scoring.** Not planned.

Two limits of the allowlist worth knowing before trusting it: the firewall rules
match resolved **IPs** rather than names, and names are resolved once at container
start. The in-container proxy closes both gaps for tcp/80 and tcp/443 by deciding
on the hostname it reads from SNI, `CONNECT` or the `Host` header — it
deliberately does **not** terminate TLS.

---

Next: [Configuration](../configuration.md) · [Platform support](../platforms/README.md) ·
[documentation index](../README.md)
