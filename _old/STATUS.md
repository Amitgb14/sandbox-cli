# Session server — phases 0 and 1

The slice `PROMPT.md` asks for by default. Phase 2 is not started.

## What landed

**Phase 0 — freeze the boundary.**

| File | Why |
|---|---|
| `docs/architecture/session-server.md` | The track, its phases, the invariants it may not touch, and the five corrections below |
| `docs/roadmap/README.md` | One paragraph placing this beside tasks 1–6 |
| `internal/config/sandboxkind.go` | `SandboxKind` — `docker`/`podman` available, `bwrap`/`none` declared and refused |
| `internal/config/config.go` | The `sandbox:` key, plus validation that separates a typo from an unimplemented kind |
| `internal/config/trust.go` | `sandbox` refused from a project `.sandbox.yaml` |

**Phase 1 — read-only catalog.**

| File | Why |
|---|---|
| `internal/protocol/` | Wire types: envelope, error codes, `Session`/`Workspace`/`Worktree`/`Pane` |
| `internal/session/session.go` | `Open`, `Snapshot`, `Save`, `Adopt` — the catalog and its persistence |
| `internal/session/serve.go` | The NDJSON server and the client half (`Call`) |
| `internal/session/flock_unix.go`, `flock_other.go` | The advisory lock around a save |
| `internal/sandbox/labels.go` | `sandbox.pane`, `sandbox.pane_kind`, `sandbox.pane_session`, `sandbox.sandbox` |
| `internal/cli/serve.go` | `serve`, `serve status`, `serve stop`, `pane list`, `session snapshot` |
| `internal/cli/serve_signal_{unix,other}.go` | SIGTERM where there is one |
| `docs/GUIDE.md` | Command reference rows, and a section saying what this adds over `list` |

Ops implemented: `session.hello`, `session.snapshot`, `pane.list`. Everything else
answers `invalid` — an op that accepts a request and does nothing is worse than one
that refuses, because a client cannot tell the first from success.

## Acceptance

Phase 0: `go test ./internal/runtime/...` and the `--dry-run` golden are unchanged.
No docker argv change.

Phase 1, checked against a real docker daemon (28.0.4) in a throwaway repository:

- `pane list` showed the same container `sandbox-cli list` did, grouped under its
  worktree, with both `main` and the managed `feat-one` worktree in the catalog and
  the real path for each.
- `serve` bound its socket; `serve status` reported `running (pid N)`.
- `kill -9` on the daemon left the container **running**, and `serve status` then
  reported `not running (stale pid N)` — the liveness check is a dial, not the pid
  file, which is why a killed daemon does not read as a live one.
- Restarting `serve` over the stale socket re-adopted the same container.
- `docker rm -f` on the container left `docker ps -a` and `sandbox-cli list` with
  nothing, while `pane list --all` still reported the pane as `stopped`. That is
  the one thing the catalog adds over the engine, demonstrated rather than claimed.
- `serve stop` printed `containers are untouched` and they were.

`go test ./...` is green. `go vet` clean. OneNutri and nutrition-agent were not
touched — confirmed by `docker ps -a` before and after.

## Corrections to the handoff pack

Recorded in `docs/architecture/session-server.md` as decisions rather than quiet
fixes, because each is a place a later reader would reintroduce the duplication.

1. **`runtime.Inspector`/`ContainerInfo` is the engine seam** — the pack asks for an
   `EngineContainer` stub and a `Lister` to inject. Both exist, and `ContainerInfo`
   already carries the exit code a pane's state cannot be decided without.
2. **`sandbox.repo` already is the repo hash** — `worktree.RepoID` is
   `<name>-<8 hex of sha256(abs path)>`. A `sandbox.repo_hash` label would be a
   second identity for one fact.
3. **`sandbox.kind` collides with a user-visible word** — `sandbox-cli list`'s KIND
   column already means interactive-vs-fleet. The pane axis is `sandbox.pane_kind`.
4. **`sandbox.session` is already taken**, and this is the sharp one: it means the
   agent conversation a run reopened, and `recover`'s resume correlation reads it.
   The daemon id is `sandbox.pane_session`.
5. **`sandbox.profile` is already stamped.**

The pack's ten-label set therefore reduces to **three new labels**: the other seven
are already stamped or derivable. The worktree id is derived from the branch rather
than labelled, for the same reason — a stored id is a way for the catalog and the
container to disagree about what a pane is in.

Two smaller departures:

- **Panes store no `argv`.** The pack's security notes say not to copy prompts into
  the catalog while its schema has a plain `argv` array and its example has a prompt
  sitting in one. `audit.SessionMeta` records env vars by name for the same reason.
  `TestPaneCannotHoldAnArgv` pins it from both sides.
- **The catalog is scoped to one repository.** The pack left this open ("or all
  managed, document which"). The session id derives from `worktree.RepoID`, so two
  repositories cannot share a socket, and `pane list` shows the set `list` shows.

## Decisions taken where the pack left a choice

- **`sandbox: none` is refused under every profile, not just prod.** The pack says
  `none` is "dev-only", which reads like the existing profile asymmetry and is not
  the same thing — dev is the *default* profile, so dev-only would make an
  unimplemented no-isolation mode reachable by the ordinary path. The profile split
  is about controls that could not be *applied*; this is a control nobody wrote.
- **`pane list` does not autostart `serve`.** The pack prefers autostart. A
  read-only listing that forks a long-lived daemon is a surprise, and `serve` is a
  foreground process in this phase, so there is nothing to fork it into. Instead
  `pane list` answers from the engine directly and says which source it used.
- **`serve stop --kill-panes` is refused rather than implemented.** Phase 1 starts
  and stops no containers, so the flag would be the one place this reaches past what
  the phase owns. The error names `kill` and `clean`.

## Two things found while building

- **Unix socket paths are capped at ~104 bytes** (`sun_path`), and the session path
  can exceed it — Go's own `t.TempDir()` on macOS produces one that does. Bind then
  fails with "invalid argument", which names nothing. `session.CheckSockPath` refuses
  up front with the remedy, `serve status` says "cannot run here" rather than "not
  running", and the session directory dropped the `-default` suffix to buy back eight
  bytes of a hundred.
- **Liveness has to be the socket.** A pid file outlives a killed daemon and a live
  pid is not yet a bound socket — there is a window on the way up and another on the
  way down. `servePID` dials; the pid is read only so that "stale pid 4711" can be
  said instead of "not running".

## Review remediation

Eight findings from `/code-review high 166`; six major, all reproduced before
fixing and all now covered by a test verified to fail against the original code.

| Where | What was wrong |
|---|---|
| `config/load.go` | **`mergeInto` never copied `Sandbox`.** The key was inert: `sandbox: podman` in a user config was dropped, and `sandbox: none` was therefore never refused — `Validate` runs on the merged config, where the field was always `""`. Documented, announced, and doing nothing. |
| `session/session.go` | **`Open` used `RepoRoot`, the id used `RepoID`.** `rev-parse --show-toplevel` answers with a *linked worktree's own* directory; `RepoID` follows the pointer to the main checkout. From inside a managed worktree, one session file recorded the worktree as the repository, flagged it `Main` (the one worktree `worktree rm` must never touch), lost the real checkout, and found no managed worktrees at all. Now `worktree.MainRepo`, the same function `RepoID` uses. |
| `session/session.go` | **The name fallback bound a pane to another pane's container.** Container names are deterministic and therefore reused by the next run on a branch, so a finished `p_one` was reported running against `p_two`'s container — and `p_two` never entered the catalog. A phase-2 mutation by pane id would have acted on the wrong container. Now the fallback requires the container to carry no pane label or the same one, and consults `claimed`. |
| `session/serve.go` | **A second `serve` stole the first's socket.** Unlinking was unconditional; the second's own cleanup then removed the socket *and* pid file, leaving the first running, invisible to `status`, unreachable, and still writing `session.json` — the two-daemon case `flock`'s comment describes. Now it dials first and refuses. |
| `session/serve.go` | **Shutdown hung on an idle client.** Cancelling closed the listener but not accepted connections, so `handleConn` blocked in a read and `wg.Wait()` never returned — and since `signal.NotifyContext` keeps the handler registered, later SIGTERMs were swallowed, so `serve stop` and Ctrl-C both looked dead and only SIGKILL worked, leaving the socket behind. Connections are tracked and closed on cancel. |
| `cli/serve.go` | **`serve stop` could SIGTERM the caller's process group.** `servePID` returns `0` when the pid file is missing, and `kill(0, SIGTERM)` signals every process in the caller's group — the user's shell job and its siblings — while leaving the daemon running. Guarded on `pid > 0` with a message naming `lsof`. |
| `cli/serve.go` | *minor* — the no-daemon listing honoured only `All`, silently dropping the workspace and worktree filters, so the two sources answered different questions. One `session.FilterPanes` now serves both. |
| `cli/serve.go` | *minor* — `openSession` swallowed a `LoadProfile` error and recorded `profile: dev`. A repo whose config trips `ErrRestrictedProjectKeys` would be **catalogued as dev**: wrong in the direction that matters, and stamped rather than assumed. The error is returned. |

Two follow-ons found while verifying the fixes, both in the same area:

- `serve status` printed `running (pid 0)` when the pid file was unreadable — a lie
  in the shape of a fact, and `0` is exactly the value that made `serve stop`
  dangerous. It now says `pid unknown` and names the file.
- The second `serve` printed its banner *and* `stopped; containers are untouched`
  before the refusal, because the banner preceded the bind and the stop message was
  unconditional. The check moved ahead of the banner, and "stopped" is printed only
  on a clean stop.

A structural guard came out of the first finding.
`TestEveryConfigFieldSurvivesTheMerge` sets every `Config` field in a real user
config, loads it, and fails if it arrives zero — because `mergeInto` is
hand-written, so a field added to the struct and not to that function parses,
validates and is silently discarded. `TestEveryConfigFieldIsClassified` already
made a new field declare its trust; nothing made it declare that it works. Verified
by reverting the one-line fix: the guard fails, naming the field.

## Not done

- **`web/src` is deliberately not updated.** The repo's rule is that a user-facing
  change updates the site in the same PR, and these are three new commands. The
  judgement is that phase 2 changes what `pane` *is* — it grows a spawn path — so
  documenting it on the marketing site now would publish something known to be in
  flux. `docs/GUIDE.md` and the architecture note carry it instead. Worth revisiting
  when phase 2 or 3 lands; flagging it rather than deciding it silently.
- Phases 2–7. Nothing in this slice calls `BuildArgs`, changes fleet, or touches
  Studio's write path.
