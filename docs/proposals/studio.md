# Proposal: Sandbox Studio — a local control plane for sandbox-cli

**Status:** proposed, not scheduled. This is deliberately **out of roadmap order**:
`docs/roadmap/README.md` scopes Task 2 as "orchestrated from the CLI. **No GUI**"
and lists Task 3 (stronger isolation) as next. Nothing here argues that ordering
was wrong. The document exists because a GUI was started anyway, and an unwritten
decision cannot be reviewed, contradicted, or declined.
**Depends on:** `fleet`, `worktree`, the session layer (`list`/`logs`/`attach`/`kill`),
`internal/audit` — all shipped.
**Relates to:** [`multi-agent-fleet.md`](multi-agent-fleet.md) (which agent runs),
[`autonomous-fleet.md`](autonomous-fleet.md) (whether it succeeded). This one answers
*what you can see and start without typing*, which is a smaller question than either
and a larger risk than both.

## Problem

sandbox-cli's daily loop is now several commands deep. `fleet status --watch` says
what is alive, `fleet logs -f` says what an agent wrote, `worktree git … status`
says what it changed, `audit` records how each run ended, and `stats` says what it
costs. Each is correct and none of them compose: answering "what are my five agents
doing" means four terminals and a `watch`.

That is a real gap, and a visual control plane is a reasonable answer to it. But it
is also the first feature in this project that would make sandbox-cli **listen**,
and that is not a smaller version of what it does today. It is the opposite shape.

### The threat model this inverts

Every privilege decision in this codebase rests on one assumption: there is a
trusted human at a terminal, and reaching a privileged setting requires a
deliberate act.

- `internal/config/trust.go` refuses `image`, `mounts`, `user`, `secrets`,
  `env_allow`, `security.*` and any weakening `network.mode` from a discovered
  `.sandbox.yaml`. The escape hatch is `--config <path>` — and the argument for why
  that is safe is *typing the path is the deliberate act discovery never involves.*
- `fleet.yaml` gets CLI-flag trust on the same reasoning: "running `fleet run` at
  all is the deliberate act."
- `kill` refuses to infer its target where `logs` will, because stopping the wrong
  agent costs its work.

**An HTTP request satisfies none of those tests.** It is not typed, it is not
deliberate, and a browser will issue one because a page told it to. Meanwhile the
process serving it is the one that launches containers with host bind mounts, so
request-controlled input reaching `sandbox.Options` is not sandbox escape — it is
**host compromise**, with the sandbox working exactly as designed around it.

`grep -rln net/http internal/ cmd/` returns nothing today. That is the fact that
makes this proposal load-bearing rather than procedural: there is no existing
listener whose hardening a new one can inherit.

## Design constraints

The four from `multi-agent-fleet.md` hold unchanged — docker is the state store,
one agent per worktree, detached runs are non-interactive, the isolation invariants
do not move. Eight more, all of which are **blockers** rather than preferences:

1. **The API composes, never reimplements.** Every launch goes through
   `config.LoadProfile` → `sandbox.BuildSpec` → `runtime.BuildArgs`. No handler
   assembles a docker argv, sets `HOME`, or resolves a mount. Invariant 1 in
   `CLAUDE.md` says isolation lives in `BuildArgs` and `ResolveWorkspace`; a second
   path to a container is a second place for it to live, and the `--dry-run` golden
   test would no longer cover what actually runs.
2. **Request input is untrusted the way a project `.sandbox.yaml` is untrusted.**
   The refused-key list applies verbatim, and network mode is tighten-only. A
   request may say "run claude on branch X"; it may not say `user: root`,
   `mounts:`, `image:`, or `network.mode: default`. `ValidateProfile` runs on the
   resolved config for every request, not once at startup.
3. **Loopback-only, and authenticated anyway.** Bind `127.0.0.1` with no flag to
   change it in Phase 0–2. Loopback is not an authorization boundary: every other
   process on the machine — including an agent that has broken out of nothing at
   all, just a browser tab — can reach it. A token, generated per server start,
   printed once to the terminal that started it.
4. **Browser-shaped attacks are in scope.** `Origin` checked on every mutating
   request, no cookie auth (token in a header, so a cross-site form cannot carry
   it), no `GET` with side effects. A local server with no CSRF story is a remote
   code execution primitive for any page the user visits.
5. **Every host path goes through `sandbox.RefuseUnsafeHostPath`.** Never `/`,
   never the host home, never an ancestor of it, compared by device+inode rather
   than by string. A request naming a project directory is exactly the input that
   check exists for.
6. **`resolveSession`'s rule survives.** A reference from a request is matched
   against a listing filtered by `sandbox.cli` and is **never** handed to the engine
   to resolve. `POST /runs/postgres/stop` must find nothing rather than somebody's
   database.
7. **No secret values in responses, logs, or state.** `internal/audit` records
   environment variables **by name only**, deliberately — "the credential broker
   exists to keep secret values off the argv and out of config files, and a log is a
   file." An API response is also a file, in a browser's cache. `SessionMeta` has
   nowhere to put a value; neither does any DTO here.
8. **No new state store.** Docker's labels plus `~/.config/sandbox/audit/sessions.jsonl`
   already hold everything the UI needs. A database would be a second answer to
   "what exists", and the listing and the reaper must not be able to disagree —
   which is why `clean` and `stats` were already moved onto the same label filter.

## Phases

### Phase 0 — read-only, and useful alone

`sandbox-cli studio` serves a read-only view: sessions (from labels), fleet status,
logs, worktree diffstat, audit history, live stats. **No endpoint starts, stops, or
lands anything.**

This is the phase worth building first, and not as a stepping stone. It is where
essentially all of the *value* of the complaint above sits — one screen instead of
four terminals — and almost none of the risk, because the worst outcome of a
compromised read-only server is disclosure of what is already on the user's disk.
Constraints 3, 4, 6, 7 and 8 all apply here; 1, 2 and 5 have nothing to bite on yet.

Shipping Phase 0 and stopping is an acceptable outcome for this proposal.

### Phase 1 — launch, through the existing layers

`POST /runs` accepts the same shape a `fleet.yaml` task has (branch, prompt, agent,
args, verify, per-task caps and `allow`) and nothing more. It resolves config
through `config.LoadProfile` with the server's own `--config`/`--profile`, applies
constraint 2 to the request body, and calls the same `fleet` code path the CLI does.

The request cannot select the profile. The server's profile is fixed at startup by
the person who typed the command, for the same reason a project config may raise the
profile and never lower it.

### Phase 2 — streaming

Logs and stats over a WebSocket, so the run detail view is live rather than polled.
Read-only by definition; constraint 4 applies to the upgrade handshake, which
browsers do **not** subject to CORS.

### Phase 3 — the UI

One Next.js app, static-exported, **embedded in the Go binary via `//go:embed`** and
served from the same origin as the API.

This is a decision, not a default. The alternative — a dev server on `:3000` talking
cross-origin to `:8787` — makes CORS, token handling and a build-time base URL the
UI's problem in production, and every one of those is a place constraint 4 gets
weakened for convenience. Same-origin removes the class. It also matches what this
repo already does: `assets/Dockerfile` and the egress proxy's source are both
embedded, and `image.Ref` hashes the latter so a changed proxy produces a new image
tag.

**Studio does not live in `web/`.** That directory is the landing page — static
export, deploying to Pages with no Node server, routes `/` and `/multi-agent`. It
has no server to talk to and should not grow one; `src/app/api/**/route.ts` cannot
work under `output: "export"` at all. Studio is a separate app whose build artifact
is embedded. A `npm run dev` proxy to a locally running `sandbox-cli studio` is fine
as a *development* convenience, and is not a deployment mode.

## Verification

- The refused-key table gets a request-shaped sibling: `TestStudioRefusesPrivilegedFields`
  next to `TestProjectConfigRefusesPrivilegedKeys`, so a new field on the request
  DTO is classified when it is added rather than noticed later. This is the
  `fleet/gates_test.go` pattern, which exists because `persist_auth` was missed
  exactly once and by exactly this mechanism.
- A test that the launch path produces argv **identical** to the CLI's for the same
  inputs — the `--dry-run` golden output is the oracle.
- A test that a session reference from a request cannot name a container without the
  `sandbox.cli` label.
- A test that no response body or log line contains a forwarded secret's value.
- `Origin`-rejection and missing-token tests on every mutating route, including the
  WebSocket upgrade.

## Not chosen

- **A daemon.** `autonomous-fleet.md` constraint 7 already settled this: the
  scheduler is a foreground process, "nothing to install, nothing to
  garbage-collect, and the process is not a place state lives." Studio is a
  foreground command you start and Ctrl-C.
- **Auth-free because it is loopback.** Argued above: loopback is not an
  authorization boundary, and this project's whole premise is that something hostile
  may already be running locally.
- **Remote access, TLS, multi-user, BYOC.** All deferred in the roadmap and all
  outside this document. The moment the bind address is configurable, constraints
  3 and 4 stop being sufficient and this proposal needs rewriting rather than
  extending.
- **Re-implementing docker calls for speed.** Constraint 1. A faster second path to
  a container is the failure this codebase has already been audited for.
- **Putting Studio in `web/`.** Above.

## Non-goals

Studio is a *view and a launcher*. It is not a new boundary, not a new privilege
layer, and not a place any isolation decision gets made. If a question can only be
answered by weakening one of the eight constraints, the answer is that Studio does
not do that thing.
