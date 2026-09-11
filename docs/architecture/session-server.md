# The session server

One daemon owns a catalog of sandboxed **panes** grouped by git worktree; the CLI,
Studio and fleet all read it through one protocol; isolation still comes only
from `runtime.BuildArgs`.

The shape is borrowed from Herdr — a session server with thin clients and
worktree-grouped panes. What is borrowed is the **control plane**. Herdr's
host-PTY default, its TUI, its plugin surface and its multi-machine mesh are not,
and the first of those is the one that matters: a pane here is a container, and
the reason this document exists at all is that adding a daemon must not add a
second way to start one.

## Why

Today there is no session daemon. A detached run *is* a container, and docker is
the state store — which works, and is the reason `sandbox-cli list` can survive a
`kill -9` of the CLI. What it cannot do is answer a question about more than one
container at a time without asking the engine again, or hold a fact the engine has
no field for.

Three things follow from that, and they are the problem being solved:

- **Grouping is re-derived on every command.** `list` reads labels, `fleet status`
  reads labels, Studio reads labels, and each builds its own idea of what belongs
  together. A worktree with two panes — an agent and the verify that judges it —
  has no representation anywhere.
- **Studio is a second control plane rather than a client of the first.** It
  builds `sandbox.Options` itself, which is why `internal/fleet`'s rule with
  teeth ("every gate on the run path must be repeated by every caller that builds
  `Options`") had to be written down, and why `persist_auth` leaked through it
  once already.
- **Agent state is nobody's job.** Whether a pane is working, blocked on a
  permission prompt, or finished is derivable from its transcript, and nothing
  derives it.

## Layers

```
CLI    Studio    SDK
          │
          ▼   unix socket, NDJSON
   sandbox-cli serve
     ~/.config/sandbox/sessions/<sid>/{sock,session.json,events.jsonl,pid}
          │
          ├── Workspace (a repository)
          │     ├── Worktree main ──── Pane agent
          │     └── Worktree feat/x ── Pane agent, Pane verify
          ▼
   runtime.BuildArgs → docker | podman
```

`session.json` holds **layout**: which worktrees exist, which panes are in them,
what each pane's container is called and how it is doing. It is not a second
snapshot system — workspace *files* stay where they already are, in git under
`refs/sandbox/snapshots/` (`internal/rescue`). The two layers answer different
questions and neither can answer the other's: `recover restore` gives you back a
file, `session.snapshot` gives you back a layout, and restoring a layout
deliberately does **not** start any agent.

## Phases

| Phase | What lands |
|---|---|
| 0 | Freeze the boundary: this document, `SandboxKind`, goldens unchanged |
| 1 | Read-only catalog: `serve`, `session.snapshot`, `pane.list`, adoption |
| 2 | Write path: every detached run is a pane, with labels and a catalog row |
| 3 | Worktree grouping, and `worktree rm` refusing a live pane |
| 4 | `internal/detect` — agent state, `pane.wait`, `pane.state` events |
| 5 | Fleet materializes through the session |
| 6 | Studio becomes a gateway and starts zero containers |
| 7 | Layout restore across a `serve` restart, with no auto-respawn |

Phase N+1 is not started until Phase N's acceptance is green. Phases 8 (a second
sandbox backend) and 9 (a TUI) are explicitly not part of this track.

## Phase 2, as built

Every detached run is now a pane. `--detach` mints a pane id, stamps it as a label,
and records a row; `pane spawn` is the same launch named as one; and `kill`, `logs`
and `attach` resolve a pane id alongside the forms they already took.

Three decisions are worth keeping.

**`session.Spawn` takes already-built `sandbox.Options` and does not construct
them.** `internal/fleet`'s rule with teeth — every gate on the run path must be
repeated by every caller that builds `Options` — is a rule about *builders*, and
`gates_test.go` exists because it was broken once. A spawn that assembled its own
Options would inherit that whole obligation in exchange for three labels and a row
in a file. So the caller resolves the config, applies the flags and passes the
gates; this sets the three pane fields and nothing else, on a copy.

**The pane id is a label because labels are the only thing that survives.** Docker
cannot add one after creation, so the id is minted before the container exists. A
pane whose id is not on its container is one no later `serve` can rebind — it would
come back from a restart as a "legacy" pane addressable only by name, which is the
failure this phase exists to remove.

**The catalog is a courtesy, not a gate.** `run --detach` has always worked outside
a git repository, and a session is scoped to one — so a launch does not depend on
being able to open a catalog. A failure to *record* after the container is up is
reported and the run carries on, because the id is already on the container and the
next `Adopt` recovers the row.

`TestPaneSpawnArgvMatchesTheRunPath` is the invariant made checkable: a pane and
the equivalent `run --detach` must produce the same engine argv, compared as
strings, masking only the timestamped container name a branchless run gets by
design. A session server is exactly the kind of thing that grows a second
launcher, and the way `BuildArgs` stops being the only one is not a rewrite — it is
a new caller that assembles *almost* the same spec.

One thing improved on the way past: the duplicate-name refusal now explains
itself. The engine's atomic rejection is the one-agent-per-branch lock, and it
surfaced as `exit status 125` under a line of docker's own help. `conflictNote`
names the container holding the name, and whether it is running or merely unreaped.
It runs **after** the refusal, never before: a list-then-launch check has a window
in which two launches both pass, and two agents in one checkout is silent data
loss.

Deferred from this phase, deliberately: **`pane.spawn` over the socket.** Nothing
in phase 2's acceptance needs it — the catalog, the labels, the resolution and the
argv equality are all in the CLI — and the daemon answering it means a second thing
that builds `Options` from a request, which is the obligation above arriving by
another door. It is what phase 6 needs, and it belongs with the work that needs it.

## Invariants this track may not touch

Everything in the trust-boundary section of `CLAUDE.md` continues to hold
unchanged, and three of them are worth restating because a daemon is exactly the
thing that erodes them:

- **`runtime.BuildArgs` stays the only function that turns policy into engine
  argv.** A pane spawned by the server and the equivalent CLI flags must produce
  byte-identical argv, which is a test rather than an intention (Phase 2).
- **The session socket stays on the host.** It is never bind-mounted into
  `/workspace`, `/sandbox/home` or `/shared`. An agent that could reach it could
  start containers.
- **A project `.sandbox.yaml` may tighten and never loosen.** Every new
  privilege-relevant key is added to `config.restrictedProjectKeys` in the same
  commit that adds the key, and pinned by
  `TestProjectConfigRefusesPrivilegedKeys`.

The socket is `0600` inside a `0700` directory, and same-uid is the
authentication: there is no token on the unix socket, because a caller who can
open it can already run `docker` as you. Studio keeps its loopback bind and its
bearer token; that boundary is a different one and does not move.

## Decisions taken against the original plan

The handoff pack this track came from was written against the repository from the
outside, and five of its details were wrong about what is already here. They are
recorded as decisions rather than silently fixed, because each one is a place a
later reader would otherwise reintroduce the duplication.

**1. `runtime.Inspector` is the engine seam, not a new `EngineContainer`.** The
plan asked for a stub struct (ID, Name, Labels, Running) and a `Lister` interface
to inject in tests. Both already exist: `runtime.Inspector.Containers` returns
`[]runtime.ContainerInfo`, which carries those four fields plus `ExitCode`,
`StartedAt`, `FinishedAt`, `OpenStdin` and `TTY` — and every one of those is
needed to decide a pane's state. A parallel struct would have to be kept in step
with it by hand.

**2. `sandbox.repo` already is the repo hash.** The plan adds a `sandbox.repo_hash`
label. `worktree.RepoID` is `<dir-name>-<8 hex of sha256(abs path)>`, and it is
already what stamps `sandbox.repo`, builds the container name and names the
managed worktree directory. A second label for the same fact is a second identity
that can disagree with the first.

**3. `sandbox.kind` would collide with a user-visible word.** `sandbox-cli list`
has a `KIND` column and it already means `interactive` or `fleet`, from
`sandbox.fleet`. The plan's `kind` means `agent|shell|command|verify|console`,
which is a different axis entirely. The pane axis is therefore
`sandbox.pane_kind`, and the existing column keeps its meaning.

**4. `sandbox.profile` is already stamped.** Listed by the plan as new.

**5. `sandbox` as a config key is privilege-relevant, and "dev-only" does not
protect it.** The plan introduces `SandboxKind` with values
`docker|podman|bwrap|none`, and says `none` is dev-only. Dev is the *default*
profile, so a project `.sandbox.yaml` setting `sandbox: none` would ask for an
agent running on the host with no container at all — on the profile almost every
run uses. The key is refused from a project file for the same reason `engine` and
`runtime` already are, and `none`/`bwrap` are refused everywhere until a phase
implements them, so the field cannot be a silent downgrade while it is a stub.

Two smaller ones, both about `session.json`:

- **Panes do not store `argv`.** The plan's own security notes say not to copy
  prompts into `session.json`, while its schema has a plain `argv` array and its
  example holds a prompt in one. `audit.SessionMeta` records environment
  variables by name and has nowhere to put a value, for the same reason; this
  follows it. A pane records what it *is*, not what it was told.
- **The catalog is scoped to a repository.** The plan left the choice open
  ("or all managed, document which"). A session belongs to one repository root:
  its id is derived from `worktree.RepoID`, so two repositories cannot share a
  socket, and `pane.list` shows that repository's containers — the same set
  `sandbox-cli list` shows, which is what makes the two comparable.

## Reading order

- `docs/architecture/session-server.md` — this file
- `docs/roadmap/README.md` — where this sits among the other tracks
- `CLAUDE.md`, trust-boundary section — the rules above, in full
