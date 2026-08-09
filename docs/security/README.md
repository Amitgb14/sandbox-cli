# Security

What the boundary is, what it is not, and the two profiles you choose between.

- [Security profiles](#security-profiles)
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
| `--publish`, `--user root`, `--memory 0`, `--cpus 0`, `--no-hardening` | allowed | **refused** |

```sh
sandbox-cli claude --profile prod
```

That last row is checked on the **run**, not only on the configuration. Those
five arrive as flags rather than config keys, so a check that read the resolved
config alone never saw them — `--profile prod --publish 0.0.0.0:8022:22` used to
succeed, publish the container on every interface, and have the entrypoint open a
matching hole in the default-deny INPUT chain. A flag may tighten what the
profile guarantees and may not widen it; `--profile dev` is how you ask for the
looser thing.

A profile is the **base** config layer — under your own config rather than over
it — so a trusted config can still tune a setup. A project `.sandbox.yaml` may
**demand** the stricter profile and may never ask for the weaker one, the same
direction-of-travel rule the network keys follow.

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
  ok    isolation runtime  only the default runtime is registered: runc

sandbox-cli: this host cannot satisfy the prod profile: seccomp
```

`doctor` answers for the run you are about to make, not just for the config on
disk: `--network` and `--runtime` take the same values the run does, so
`doctor --profile prod --network allowlist --runtime runsc` preflights that
command. Without them the preflight and the launch can reach different verdicts,
which is the one thing this check must never do.

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
see [Platform support](../platforms/README.md)). Mounts, hardening, caches and
secrets work unchanged on top of it.

**The egress allowlist is the exception, and on gVisor it does not work.**
Measured on Rocky Linux 10.2 with gVisor installed:

- gVisor gates iptables behind a flag its installer leaves off, so the allowlist
  cannot be programmed at all until you `runsc install -- --net-raw`.
- With that flag on, gVisor serves only the older iptables backend, which
  sandbox-cli now selects automatically.
- But gVisor provides **no connection tracking** — neither `-m conntrack` nor
  `-m state` exists — and the allowlist's "accept replies" rules have no
  equivalent without it.

So a run that asks for both a stronger runtime and an egress allowlist is
refused rather than run unfiltered, which means `--profile prod` (which requires
an allowlist) and gVisor cannot currently be combined. Kata, being a real kernel,
is not subject to this; it has not been measured here yet.

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
