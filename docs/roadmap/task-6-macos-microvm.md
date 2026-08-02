# Task 6 — macOS microVM

**Goal.** Make the stronger boundary reachable on the platform most users are actually on.
Today `--runtime kata-runtime` and `--runtime runsc` cannot be selected on Docker Desktop
at all, so every mac and Windows user is on a shared kernel with no way off it.

**Branch.** Not started.

```
Runtime interface
├── docker / podman   ← shells out to a CLI (today; podman is a dialect, not a backend)
├── kata / runsc      ← selected *through* that CLI, Linux only (task 3)
└── libkrun           ← a second backend: no daemon, no CLI, own kernel per sandbox (this task)
```

---

## Why this is on the list at all

The roadmap deferred "Firecracker, and anything microVM beyond a Kata spike", and that was
right when it was written: microVM meant Linux, and the answer on a Mac was "Docker
Desktop already runs everything in a VM, and the boundary you have is the container one".

Two things changed. The gap is now the largest single hole in what the tool claims — the
README's own comparison table concedes isolation is the one row it does not win, and
`doctor` cannot even *ask* the question on macOS. And the platform answer now exists:
**libkrun** is a Red Hat-backed library that embeds a KVM (Linux) or Hypervisor.framework
(macOS) microVM directly in a process, and **Microsandbox** ships it today with a dedicated
kernel per sandbox and sub-200ms starts. This is no longer "Firecracker later"; it is a
thing that runs on the platform the gap is on.

It is last because it is the largest by a distance, and because it is the first thing that
genuinely forces the question task 3 defers to *"only then"*: whether the `Runtime`
interface needs a second implementation.

---

## What exists today

- **`RunSpec.Runtime`** carries an OCI runtime name and `BuildArgs` renders `--runtime
  NAME`. That is the entire seam, and it only works because the *engine* accepts the flag.
  There is no engine on Docker Desktop that does.
- **`doctor`** recognises `runsc`, `runsc-kvm`, `kata`, `kata-runtime`, `kata-qemu`,
  `crun-vm` and reports which are registered. On macOS the honest answer is always "none,
  and none can be".
- **The `Runtime` interface** (`internal/runtime/runtime.go`) exists with exactly one
  implementation, `docker_cli.go`, which shells out to a binary. Podman is handled as a
  **dialect** of that — three (now four) points of difference, not a second backend. The
  package doc says the interface is there so an SDK-based or VM backend *can* be dropped
  in; nothing has needed to yet.
- **`BuildArgs` is a pure function producing docker argv**, pinned by
  `internal/runtime/args_test.go` and the `--dry-run` golden test. A backend that does not
  produce argv at all is the first thing that does not fit that shape.

---

## Required features

### 1. Answer the interface question first, in code

The honest first step is a spike, not a design document. libkrun is a C library with no
CLI and no daemon; there is no argv to build. That breaks the assumption underneath the
current backend, and the shape of the fix should be discovered by making one sandbox run,
not decided in advance.

- Boot a libkrun microVM on macOS with the workspace mounted and a shell running in it.
- Only then decide whether `Runtime` grows a second implementation, whether `RunSpec`
  needs to change, and what `--dry-run` means for a backend with no command line — because
  **`--dry-run` printing the exact thing that will run is a load-bearing property of this
  tool**, not a convenience. A backend that cannot be previewed is a backend that breaks
  the one thing the README claims is genuinely novel.

### 2. Keep every isolation invariant, or fail

The invariants are not docker's, they are sandbox-cli's, and they must survive a backend
that has no `--mount` flag:

- Only declared mounts are host-connected; `HOME` is always the fake path; the host home
  is never mounted. `sandbox.ResolveWorkspace` and `RefuseUnsafeHostPath` stay the choke
  point and must be reached by this path too.
- The egress allowlist must still hold, and it is the hard one: the firewall is programmed
  by a root entrypoint inside the guest, and the guest is now a different kind of thing.
  Same warning task 3 carries, one layer further down. **If it cannot be programmed, the
  run refuses** — the fail-closed rule does not get a platform exemption.
- If any invariant cannot be delivered, this backend does not ship as an *option* with a
  caveat. A weaker boundary offered under the same flag name is worse than no flag.

### 3. `doctor` can ask the question on macOS

- Today the runtime check reports rather than refuses, because "failing a check for
  something the tool does not do would be theatre". Once the tool does it on macOS, that
  reasoning expires and the same asymmetry applies as everywhere else: dev warns, prod
  refuses.
- A question that cannot be *asked* still counts as a failure under prod. Unchanged.

### 4. Say which boundary a run actually got

Same rule as tasks 3, 4 and 5, and it matters most here because two runs on the same
machine can now differ: the audit log and `list`/`stats` say which backend and which
kernel a session was on.

---

## Not in this task

- **Windows.** WSL2 is a different route with a different answer, and doing one platform
  properly beats two half-done.
- **Replacing docker.** The docker backend stays the default everywhere. This is an
  opt-in stronger boundary, not a migration.
- **Firecracker.** Still the Linux production path (task 3's successor), and still not
  this.
- **Chasing the cold-start numbers.** The reason to do this is *isolation on macOS*, not
  boot time. If it also makes [task 5](task-5-checkpoint-and-fork.md)'s memory half
  reachable, that is a consequence worth taking, not the goal.
- **Cloud, BYOC, remote runners.** Declined in the roadmap index and unaffected by this.

---

## Done when

1. A sandbox runs on macOS with its own kernel, started by sandbox-cli, with the workspace
   mounted and an agent working in it.
2. Every isolation invariant is enforced on that path, with `internal/runtime` tests
   covering it the way `args_test.go` covers the docker path — and the egress allowlist
   either holds or refuses.
3. `--dry-run` still shows the user what will run, in whatever form is honest for a
   backend with no argv.
4. `doctor` under `prod` fails on a macOS host that cannot provide the stronger boundary,
   and passes on one that can.
5. The audit log and the session listing say which backend a run used.
6. The README comparison table's isolation row can be rewritten truthfully — which is the
   real acceptance test for this task.
