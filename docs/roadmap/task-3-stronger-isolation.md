# Task 3 — Stronger isolation for Linux production (Kata)

**Goal.** When you need real isolation for untrusted code, give each sandbox its own
lightweight VM — Kata Containers on Linux — instead of a shared kernel.

**Status.** §4 (honest reporting) and §2/§3 (doctor's teeth, prod's demand) are
built. §1 — a tested Kata path and its setup documentation — is what remains, and
it needs a Linux host with hardware virtualization to verify.

**One deviation from §3 as written, and the reason.** The scope said "prod sets a
runtime rather than inheriting the host default". It does not: prod **demands**
one and refuses to **name** one. Which of Kata or gVisor a machine has is a
property of the machine, so a profile that wrote `runsc` into itself would refuse
every host that has Kata, and `kata-runtime` every host that has gVisor — a
profile that guesses fails on the machine it was meant to protect. The demand is
enforced by `ValidateProfile` and reported early by `doctor`; the choice stays
the user's, in a config only they can write.

**And one rule the scope did not anticipate.** The demand applies where a
stronger runtime *can* exist. Docker Desktop cannot register a custom OCI runtime
at all, so demanding one on macOS or Windows would be a refusal to run on the
platform most developers use, in exchange for a boundary Docker Desktop already
provides with its own VM. The decision follows the daemon before the platform: a
host with a stronger runtime registered and none selected fails under prod
wherever it is.

```
Runtime interface
├── docker       ← local default (today)
├── kata         ← first production step on Linux (this task)
└── firecracker  ← stronger production path later (not this task)
```

---

## Why this is last, and why it is real

A container shares the host kernel. Everything sandbox-cli does — dropped capabilities,
`no-new-privileges`, seccomp, a non-root user, a default-deny egress firewall — narrows what
an agent can do *through* that kernel, and none of it changes the fact that a kernel
vulnerability is a host compromise. For `dev` that is an honest trade: the code being run is
the user's own project. For a `prod` profile that may carry genuinely untrusted code, a
shared kernel is not a boundary, and the `doctor` command already says so in as many words.

It is last because it is the only item here that cannot be approximated. Tasks 1 and 2 are
about the tool being good; this one is about the boundary being real, and it is worth
paying for only once someone is actually running code they do not trust.

---

## What exists today

The seam is already there, and it is smaller than it looks:

- **`RunSpec.Runtime`** — the OCI runtime is a field on the resolved spec, and
  `runtime.BuildArgs` renders it as `--runtime NAME`. `""` means the host default (runc).
- **`--runtime` flag and `runtime:` config key** — a user with Kata or gVisor registered
  can already select it per run.
- **`doctor` reports registered runtimes** — it recognises `runsc`, `runsc-kvm`, `kata`,
  `kata-runtime`, `kata-qemu`, `crun-vm`, and under `prod` says plainly that a shared
  kernel is not a boundary for untrusted agents and names the fix.
- **The prod profile deliberately does *not* select one.** It leaves `Runtime` at the host
  default rather than guessing a name that may not be registered. `doctor`'s runtime check
  therefore reports rather than refuses — failing a check for something the tool does not
  yet *do* would be theatre.

So this task is not "build a runtime abstraction". It is "make the existing seam actually
carry a microVM, and make prod able to demand one".

---

## Required features

### 1. Make Kata selectable, verified, and documented

- A tested path for `runtime: kata-runtime` (and `kata-qemu`) on Linux: the base image
  runs, `/workspace` is writable, git works, the egress firewall still programs from
  inside the VM, and the agents start.
- Where any of that *doesn't* hold, the finding is documented rather than papered over.
  The egress firewall is the one to watch: it is programmed as root inside the guest before
  the privilege drop, and a microVM changes what that guest is.
- Setup instructions per distribution, in `docs/`, in the same register as the existing
  Podman page: what to install, how to register the runtime, how to check it took.

### 2. `doctor` gains teeth for prod — ✅

Once the tool can *select* a stronger runtime, the runtime check stops being advisory:

- Under `prod`, a host with no stronger runtime registered **fails**, with a non-zero exit,
  exactly as the seccomp and firewall checks already do.
- A question that cannot be asked still counts as a failure under prod. That rule does not
  change.
- Under `dev`, unchanged: a warning, because a laptop is allowed not to have Kata.

### 3. The prod profile demands it — ✅

- `prod` sets a runtime rather than inheriting the host default, and `ValidateProfile`
  asserts it against the configuration that will actually run — the same mechanism that
  stops prod being hollowed out today.
- The escape hatch stays where every other one is: the user's own config or an explicit
  `--config`, never a project `.sandbox.yaml`. `runtime` is already on the refused list in
  `internal/config/trust.go`, and it stays there — a hostile repository must not be able to
  select a *weaker* runtime any more than it can select a weaker profile.

### 4. Honest reporting of what you got — ✅

A run under a microVM and a run under runc must not look identical after the fact:

- The audit log records the runtime actually used (it records the resolved network posture
  already; this is the same kind of fact).
- `list` / `stats` can say which sessions are on a stronger runtime — otherwise the one
  thing you most want to know about a prod run is the one thing the tool does not say.

### 5. Only then, consider whether the interface needs to change

The `Runtime` interface exists (`internal/runtime`) and `docker_cli.go` is its only
backend, with podman as a *dialect* of it rather than a second implementation. Kata and
gVisor are also selected *through* the same engine, so they may need no new backend at all.

The question of a full backend abstraction — a second implementation that does not shell
out to a CLI, or a remote/VM executor — is deferred until something actually needs it.
Building it first would be designing against an imagined constraint.

---

## Not in this task

- **Firecracker.** The path after Kata, not with it. One microVM runtime, working and
  documented, beats two half-integrated ones.
- **gVisor as the goal.** It is a useful nearer step and is already recognised by
  `doctor`; if the Kata work stalls on a platform, gVisor is the fallback to document, not
  the destination.
- macOS and Windows. Kata needs Linux and hardware virtualization. On a Mac the honest
  answer stays "Docker Desktop already runs everything in a VM, and the boundary you have
  is the container one".
- Cloud/remote runners, BYOC, or anything that moves execution off this machine.
- Rewriting the isolation invariants. `runtime.BuildArgs` and `sandbox.ResolveWorkspace`
  stay the single choke point, and the `--dry-run` golden test stays the tripwire.

---

## Done when

1. A documented, tested Kata setup on at least one Linux distribution, running the full
   agent path end to end — including the egress allowlist.
2. `doctor --profile prod` fails on a host with no stronger runtime, and passes on one
   with Kata registered.
3. `prod` selects the stronger runtime itself, and `ValidateProfile` asserts it.
4. The audit log and the session listing say which runtime a run actually got.
5. `docs/` explains, without hedging, what a container boundary does and does not protect
   against, and what changes when the sandbox gets its own kernel.
