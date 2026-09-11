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
| 3 | Worktree grouping; one rule for "this worktree is in use", shared by three callers |
| 4 | `internal/detect` — agent state from the conversation, and `pane.wait` |
| 5 | Fleet launches are panes, so they get the catalog and the safety net |
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

## Phase 3, as built

The grouping was already there — phase 1's `workspacesFor` derives worktrees from
`worktree.List` plus git, so a `worktree create` needs no insertion step and adding
one would be a second source of truth for something git already answers. What phase
3 actually needed was the refusal, and it turned out to be missing in more places
than the plan said.

**`RefuseWorktreeInUse` is one rule with three callers**, because there were three
callers and two answers. `fleet clean --worktrees` had always skipped a branch whose
container was running. `sandbox-cli worktree rm` and Studio's `DELETE /v1/worktrees/
{branch}` had not — so a browser could remove a worktree while an agent was working
in it, pulling the bind-mount *source* out from under a container that then went on
writing into a path with no name. A gate two of three callers apply is a gate the
third will be found missing later; this is the same shape as `internal/fleet`'s rule
about `sandbox.Options`, on a different axis.

**`--force` does not override it**, and that asymmetry with the dirty check is the
decision. `--force` means "I accept losing the uncommitted work I can see", and the
work here is being written by a process that is still running, so nobody can see all
of it yet. The other way to resolve it — killing the pane implicitly — is worse,
because the flag would then mean "stop my agent", which is not what anyone types it
for. So it refuses and names the pane to stop, and says in as many words that
`--force` will not help, since `--force` overriding the *other* refusal is exactly
what invites trying it.

**An unanswerable question is not a refusal.** An engine that cannot be reached is
no evidence that a pane is running, and `docker` missing from `PATH` must not block
a git operation — the dirty check is what protects the files and it still stands.
That is the opposite of prod's rule for boundary controls, and deliberately: this is
not a boundary control, it is a convenience that prevents a specific accident.

One thing found by looking at real output: a worktree's branch now comes from the
container's `sandbox.branch` **label** rather than from un-sanitising its id.
`worktreeIDFor` runs the branch through `sanitizeID`, which maps several characters
onto `_`, so the id cannot be reversed — the snapshot was reporting `live_one` for a
branch actually called `live-one`. The id stays the sanitised form, because that is
what makes it a stable key.

### What the review of phase 3 changed

Five of the six findings were about the guard being keyed on the wrong thing, and
together they rewrote it.

**"Still in use" is `!Finished()`, never `Running()`.** The repo had already decided
this twice — `clean`'s `sessionFinished` and netavark's reaper both treat a paused or
restarting container as somebody's live run in an odd moment, and an unreadable state
as live, because "not knowing is not a licence". The first version asked `Running()`,
so `docker pause` on an agent was enough to let its bind-mount source be deleted:
the accident the guard exists to prevent, arriving through the guard itself. The
predicate now lives on `runtime.ContainerInfo` as `Finished()`, with `clean`'s copy
delegating to it, so the two cannot drift.

**The match is the mount source, not the branch label.** A label records what the
launcher asked for; an agent that runs `git checkout -b other` inside its worktree
puts it out of sync with git — the desync `land` already documents. Keyed on the
branch, `worktree rm other` resolved to the live container's directory while the
guard looked for a label that said `feat`, found nothing, and allowed it.

**A caller with no worktree passes no path**, so the refusal cannot fire where there
is nothing to protect. It used to claim "a sandbox is still running in the worktree
for feat" about a `--detach` run in the *main* checkout, advising the user to kill a
working agent to permit an operation that was always a no-op.

**`fleet clean` now asks it too.** The "three callers, one rule" claim was not true:
that loop's own liveness map is built from containers filtered on `sandbox.fleet`, so
a non-fleet sandbox working in a fleet-created worktree was invisible to it. Both
checks run now, because they answer different questions — a held fleet slot, and an
open directory.

**Worktree ids are unique per branch.** `sanitizeID` maps non-alphanumerics onto `_`
and lowercases, so `live-one`/`live_one` and `Feat`/`feat` shared one id: two
branches in one worktree record, whose reported name was whichever container the
engine listed last, and which changed between refreshes of unchanged state. An id
whose sanitised form lost information now carries eight hex of the branch. Fixing the
scheme removes the question rather than answering it, which is why there is no
collision handling.

The sixth was a missing `termsafe.Clean` on a pane id — a container label value, and
therefore text a repository can influence, printed into tab-aligned lines beside a
container name that *was* cleaned.

## Phase 4, as built

`internal/detect` answers the question the catalog could not: which of these agents
is waiting for me. Until now every running pane reported `unknown`, because an agent
editing a file and an agent parked at a permission prompt are the same running
container.

**It does not match prose, and that is the decision.** The obvious implementation
reads the last assistant message and looks for "Do you want to proceed?", "[y/n]",
"May I" — and that is `internal/creds`' prefix table again: a lookup against claims
about other people's products, which can be neither completed nor kept current. Worse
here than there, because `creds` reports the *evidence* ("begins with `ghp_`") while
this would report a *conclusion*, so a vendor rewording a prompt turns a confident
`blocked` into a confident lie. A user waiting on an agent that is not asking is the
failure this feature would introduce; a user told `unknown` goes and looks.

Three structural facts instead, none of them anybody's wording:

- **Who spoke last.** `agentctx` already separates a prompt somebody typed from a
  tool result arriving as a user message — and from codex's `developer` turns and its
  injected `<environment_context>`. If the last turn is the user's, the agent owes an
  answer, and it owes it whether that was two seconds or two hours ago: "thinking" and
  "hung" are the same evidence, so this does not guess between them.
- **How long ago.** A transcript being appended to is an agent working.
- **Whether anyone can answer.** This is the one that makes `blocked` meaningful. A
  pane with an open stdin is a console: a human can type at it, so an agent that has
  gone quiet there is waiting for one. A headless pane has no keyboard, so "waiting
  for a human" is not a state it can be in — quiet there is `idle`. Without the
  distinction every finished headless run would report as needing attention.

The better signal, named because it is what to do next rather than what was missed: a
claude transcript records `tool_use` blocks, and one with no matching `tool_result` is
an agent waiting — on the tool, or on somebody approving it — which is structural and
exact. `agentctx.Message` drops both, because it carries what was *said*, which is the
half that can contain a question. Reaching it means a second parser or a new shape
there, and neither is worth doing before something needs the precision.

`pane.wait` is what turns the states into something a script can use. Three ways out
and each is a different answer: the state arrives; the deadline passes, which is
`timeout` and **not** a failure of the pane (a caller that conflates them stops work
that was merely slow); or the pane stops existing, which ends the wait there rather
than in silence until the deadline. It polls rather than watching, because the thing
waited on is mostly not an event — a container exiting is one docker announces, but an
agent going quiet is the *absence* of writes to a file, which nothing does.

Two things the poll loop forced, both bugs in the first version. `Call`'s 30-second
deadline cut a legitimate wait off at 30 seconds and reported it as a transport error,
so the deadline is now the caller's. And refreshing every two seconds saved the
catalog every two seconds — nine hundred identical writes and nine hundred identical
event lines for one half-hour wait — so `SaveIfChanged` fingerprints the catalog
*minus its timestamps*, which is the only definition of "changed" that a poll does not
trip on every time.

Only the daemon looks for conversations (`Transcripts` is nil otherwise, and every
running pane then reports `unknown`, which is exactly what the catalog did before).
It is the one caller that refreshes repeatedly, so the per-pane file reads are
amortised; a one-shot `pane list` reading every agent's transcript store to print a
column would pay that on every invocation.

**`fleet status` is deliberately not wired to this**, against the phase's "should".
Two reasons, and the second is the blocking one. A fleet is unattended, so the state
detection adds over the container's own is mostly about waiting for a human, which
cannot happen there. And correlating a *worktree* run to its conversation is not the
same lookup as correlating a project run — every branch of a fleet shares one
repository — so getting it wrong means showing one branch's state on another's row,
which is the misattribution class `agentctx.ConversationFor` was rewritten to prevent.
Phase 5 moves fleet onto the session, where panes get their state without a second
correlation.

`pane.state` / `pane.exit` events are also not in this phase. They need
`session.events.subscribe` and a connection that stops being request/response, and
`pane.wait` covers the question anyone has today without it.

## Phase 5, as built

`fleet run` spawns panes. One line changed at the launch site —
`r.Session.Start` became `r.start`, which goes through `session.Spawn` when a catalog
can be opened — and four things follow from it.

A fleet container carries `sandbox.pane` and is listed by `pane list`. `fleet status`
reports what the *agent* is doing when a daemon has been watching, from the catalog
rather than from a second correlation — which is exactly why phase 4 left agent state
out of that table: every branch of a fleet shares one repository, so correlating again
here is a chance to show one branch's state on another's row. And a fleet agent gets
the crash safety net, which it had never had: it is a detached run, and detached runs
had none.

The gate classification flipped, which is the part the table existed for. `PaneID`,
`PaneKind` and `PaneSession` were `notYet` — checked exactly like `never` but spelled
differently, because `never` means a decision and that meant a schedule. They are
`fromSpec` now, on the strength of a named test rather than of `fleetOptions` finding
them set: they are filled in by `session.Spawn`, not by `Runner.options`, because a
fleet file cannot ask for a pane id and nothing would be served by letting it.

**Verify stays one container**, against the phase's plan, and this is the decision
worth arguing. The plan asks for a second pane spawned when the agent pane reaches a
terminal state, with the task's success being that pane's exit code. `withVerify`
already wraps the verify around the agent's argv *inside* the one container and makes
its exit code the container's — so the thing that pane reports is the verdict, which is
what `land` reads, and `paneKindFor` calls such a pane `verify` for that reason.
Splitting it needs durable sequencing: something has to notice the agent finished and
then spawn the verify, and an in-memory "now run the verify" step is a verify that
silently never runs after a daemon restart. That is the invisible-gap objection that
kept the snapshot loop out of `studioapi/supervisor.go` for a year, and it would be
reintroduced here to split one exit code into two.

A nil catalog keeps the old path, and `fleet status` does not start a daemon to answer.
A fleet is a git operation on a repository; refusing one because the bookkeeping
directory is unwritable would trade a working feature for a record of it.

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
