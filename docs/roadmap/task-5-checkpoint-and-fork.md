# Task 5 — Checkpoint and fork

**Goal.** Stop paying a cold start for every parallel agent, and let several attempts at
the same problem branch from one prepared state instead of each building it again.

**Branch.** Not started.

```
today                              this task
─────────────────────────────      ─────────────────────────────
worktree A ─► cold container       prepared state
worktree B ─► cold container         ├─► fork ─► attempt 1
worktree C ─► cold container         ├─► fork ─► attempt 2
  each installs deps again           └─► fork ─► attempt 3
```

---

## Why this, and what it is not

`--worktree` and `fleet` already give one agent per branch, which is the half of parallel
exploration that is about *isolation*. The half that is missing is *state*: every
container starts cold, so three agents attempting the same fix each run `npm install`
again, and "run three attempts, keep the one that passes tests" costs three full setups.

The rest of the category has converged on this and the numbers are not close. Blaxel
resumes a full memory+filesystem snapshot in under 25ms; Freestyle forks a live VM
copy-on-write in ~320ms median, independent of VM size; Zeroboot forks a KVM VM at p50
0.79ms by mmap'ing a Firecracker snapshot `MAP_PRIVATE`. Fly's Sprites argue the whole
ephemeral-sandbox framing is wrong and that dependencies should be installed once.

**The word "snapshot" is already taken here, and it means something else.**
`internal/rescue` writes commits under `refs/sandbox/snapshots/` so a crash cannot destroy
the agent's work. That is a *safety net for the workspace*, not a restorable machine
state, and the two must not be conflated in the CLI vocabulary — `recover` restores files,
this would restore an environment.

---

## What exists today

- **`--cache`** mounts named volumes for five package-manager paths (`.npm`, `.cache/pip`,
  `.cargo/registry`, `go/pkg/mod`, `.cache/yarn`) and the list is user-extensible via
  `cache.paths`. This is the cheap 80% of "don't download it twice" and it already works.
  It covers nothing else: apt packages, built artifacts, anything compiled.
- **`--worktree` and `fleet`** give the isolation half, with one container per branch and
  the boundary defined in one place for both (`sandbox.Options` → `BuildSpec`).
- **Containers are `--rm`**, with `--detach` the single exception. Nothing survives a run
  by design, and that is a property the tool is careful about: it has no orphaned state to
  clean up because it creates none.
- **`internal/rescue`** is the workspace safety net described above.
- The `Runtime` interface has one backend (`docker_cli.go`, with podman as a dialect).

---

## Required features

### 1. Decide what a checkpoint *is* here, before building one

There are two halves and they are not equally reachable:

- **Filesystem state** is reachable on Docker today (`docker commit`-shaped: the prepared
  container becomes an image, later runs start from it). This is most of the practical
  win — it is what makes "dependencies installed once" true for apt and for build output,
  which `--cache` cannot cover.
- **Memory state** — a running process resumed mid-flight — is not portable. CRIU is
  Linux-only and does not work through Docker Desktop's VM. The sub-25ms resumes quoted
  above are all microVM snapshot restores, which is [task 6](task-6-macos-microvm.md).

**So the honest scope of this task is the filesystem half**, done in a way the memory half
can be added behind later. If that reads as a reason to do task 6 first, that is a real
argument — it is recorded in the roadmap index rather than settled here.

### 2. Fork, as a verb the CLI actually has

- Take a prepared state and start N sandboxes from it, each in its own worktree, each
  isolated from the others exactly as a `fleet` run is today.
- Reuse `sandbox.Options` → `BuildSpec` rather than growing a second path to a container.
  `fleet/gates_test.go` exists because a second path is how `persist_auth` came to hold
  for `run` and not for `fleet`; a third one gets the same treatment or it does not ship.
- The isolation invariants do not soften for a forked sandbox: it is not more trusted for
  having descended from one the user prepared.

### 3. A prepared state is an artifact, and artifacts need rules

This is where the feature can quietly become a liability:

- **It is attacker-controlled after the first run.** A state prepared by a session in
  which the agent ran arbitrary code carries whatever that agent left. Forking it into a
  new run is executing that. Say so, and decide whether a fork of a state the agent
  touched is a different thing from a fork of a state the *user* prepared.
- **It must not become cross-project.** `--cache` volumes are shared across agents and
  projects by design and that is already recorded as a residual risk in
  [`open-items.md`](../security/open-items.md) item 8. A checkpoint is far more than a
  package cache; it should not inherit that sharing by default.
- **It must be reapable.** `sandbox-cli clean` reaps containers and the shared network.
  Anything this creates goes on that list on the same day it is created, or the tool
  acquires the orphaned-state problem it has so far avoided.

### 4. Say what it cost

- A forked run and a cold run must be distinguishable after the fact: the audit log
  records the resolved network posture already, and "this run started from state X" is the
  same kind of fact. This is the same rule task 3 applies to the runtime and task 4 to
  everything else.

---

## Not in this task

- **The memory half.** Deliberately, per feature 1. Revisit after task 6.
- **Remote or shared checkpoints**, a registry of prepared states, or anything that
  distributes one to another machine. That is BYOC by another route and is declined in the
  roadmap index.
- **Replacing `--cache`.** It is cheaper, it already works, and it should keep working for
  people who want nothing else.
- **Reusing the word "snapshot".** `recover` owns it. Find another one.
- **Auto-forking** — the tool deciding on its own to run three attempts. This provides the
  primitive; choosing to use it stays the user's.

---

## Done when

1. A prepared state can be created, listed, used to start N isolated sandboxes, and
   removed — with `clean` reaping it.
2. A forked run is built through the same `BuildSpec` path as `run` and `fleet`, with the
   gate table extended to cover it.
3. Installing dependencies once and running three parallel attempts is measurably cheaper
   than three cold runs, on a real project.
4. The trust status of a prepared state is documented rather than implied, including what
   changes when the state was touched by an agent.
5. The audit log says whether a run started cold or from a state, and which.
