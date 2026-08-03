# Sandbox Studio API

`sandbox-studio-api` (`cmd/sandbox-studio-api`, implemented in
`internal/studioapi`) is a local HTTP control plane for sandbox-cli. A frontend
("Sandbox Studio") talks to it instead of shelling out to the `sandbox-cli`
binary itself.

It adds no container logic of its own. Every handler resolves to the exact
machinery the CLI commands use — `sandbox.Session`,
`runtime.Runtime`/`Inspector`/`Controller`, `internal/worktree`,
`internal/agents`, `internal/rescue` — so a run launched through this API is
isolated exactly as one launched from a terminal, and every isolation
invariant documented in the top-level `CLAUDE.md` holds here unchanged.

## Running it

```sh
make build-studio-api
./bin/sandbox-studio-api -project /path/to/repo
```

Flags: `-addr` (default `127.0.0.1:8787`, loopback), `-project` (default: cwd),
`-history-db` (a SQLite index over the audit log; without it every history
question is answered by scanning the log, which is the default and always
correct), `-history-retain` (drop indexed runs older than this on start; the log
itself is never touched),
`-config`, `-profile` (`dev`/`prod`, same meaning as the CLI's `--profile`),
`-token` (or `$SANDBOX_STUDIO_TOKEN`), `-cors-origin` (repeatable),
`-allow-host` (repeatable).

## Trust model

This is an HTTP server that can start containers and stop/kill them, on
request, with no confirmation prompt — that is a new local attack surface the
CLI itself does not have, and it deserves the same care as everything else in
`CLAUDE.md`'s trust-boundary section. The question it has to answer is one a
terminal answered for free: **who may ask this process to start a container?**

The first version answered it with CORS, and that was not enough. Refusing to
*reflect* an unlisted origin stops a page reading the response; the request
still arrives, and the container still starts. A cross-origin `POST` carrying a
`text/plain` body is a CORS "simple request" — no preflight ever happens — and
`POST /runs/{id}/stop` needs no body at all. So a page you merely visited could
drive this control plane and simply not see the replies.

Four checks now hold the line, in the order `internal/studioapi/guard.go`
applies them:

- **The `Host` header must name a loopback address** (or one you named with
  `-allow-host`). This is what catches DNS rebinding: a page on
  `attacker.example` whose DNS answer is `127.0.0.1` satisfies the browser's
  same-origin policy — as far as the browser is concerned the origin *is*
  `attacker.example` — so its `Origin` looks legitimate. What gives it away is
  the name it dialled. `-allow-host` **adds to** loopback rather than replacing
  it.
- **An unlisted `Origin` is refused outright**, not merely denied a CORS
  header. This is the check that actually stops the attack above. A browser
  attaches `Origin` to every cross-origin request, including WebSocket
  handshakes — which matters, because a WebSocket upgrade is exempt from CORS
  entirely, so reflecting origins would have protected the log stream not at
  all. Non-browser clients (curl, your own backend) send no `Origin` and are
  unaffected; the token governs those.
- **Optional bearer token** (`-token` / `$SANDBOX_STUDIO_TOKEN`), required on
  every request but `/health`, compared in constant time. It does not stop
  another local process that can already reach 127.0.0.1, but it stops one that
  happens to guess the port. The single exception to "in a header" is the
  WebSocket log stream, where the browser API cannot set headers at all:
  `?token=` is accepted on the upgrade handshake and nowhere else.
- **A JSON content type is required on any body**, and bodies are capped at
  1 MiB. Defense in depth behind the origin check — a body this server parses
  as JSON should be labelled as JSON, and insisting on that also means a
  cross-origin `POST` cannot stay "simple" enough to skip its preflight.

**Binding off loopback** (`-addr 0.0.0.0:4319`) drops the first check's
protection to whatever your network provides, and the server says so once at
startup. Set `-token` if you do it.

None of this replaces the container boundary — it is scoped to "who may ask
this process to start or stop containers." Everything *inside* that boundary is
unchanged: `POST /runs` builds the same `sandbox.Options` a `--worktree` run
does, so `sandbox.ResolveWorkspace` still refuses to mount `/`, the host home,
or an ancestor of it, `config.IsReservedEnv` still refuses the control
variables, and `persist_auth` is re-checked here for the same reason
`internal/fleet` re-checks it.

## Contract

`internal/studioapi/types.go` is the source of truth for every request/response
shape; `types.ts` in this directory is a hand-maintained TypeScript mirror for
a frontend to import directly. Keep the two in sync when the Go types change —
there is no code generation step (yet) tying them together.

### Endpoints

| Method | Path | What it does |
|---|---|---|
| GET | `/v1/health` | Liveness + which engine/project/profile this instance manages |
| GET | `/v1/agents` | Agents this API can launch headlessly (a subset of `internal/agents` — only those with a verified non-interactive mode) |
| GET | `/v1/runs` | List runs (`?all=1`, `?repo=`, `?branch=`, `?agent=`, `?fleet=1`) |
| GET | `/v1/runs/{id}` | One run, by id/name/branch — same three references `sandbox-cli list`/`kill`/`logs` accept |
| GET | `/v1/agents/{agent}/sessions` | Conversations a run can be resumed from (sandbox-owned store only) |
| POST | `/v1/runs` | Launch a run — always detached; `console:true` keeps a terminal to attach to (see below) |
| GET | `/v1/runs/{id}/conversation` | What the agent said, and whether it can be answered |
| GET | `/v1/runs/{id}/console` | Raw pty output as SSE (base64 frames), for a terminal view |
| POST | `/v1/runs/{id}/console/input` | Send keystrokes to a running agent's stdin — **always needs a token** |
| POST | `/v1/runs/{id}/console/resize` | Tell the container its terminal size — **always needs a token** |
| POST | `/v1/runs/{id}/stop` | Stop (or `{"force":true}` to kill) a running run |
| POST | `/v1/runs/{id}/recover` | Restore the crash-recovery snapshot associated with this run's branch |
| GET | `/v1/runs/{id}/logs` | Server-Sent Events log stream (`?follow=1` to keep it open) |
| GET | `/v1/runs/{id}/metrics` | One resource sample, or a live stream with `?stream=1` |
| GET | `/v1/stats` | One resource sample per live run, host-wide |
| GET/POST | `/v1/worktrees` | List / create managed git worktrees |
| GET/DELETE | `/v1/worktrees/{branch}` | Read / remove (`?force=1`) one worktree |

A run is addressed by short id, container name, or branch — the same three
references `sandbox-cli list`/`kill`/`logs` accept, resolved the same way (matched
against this tool's own containers, never handed to the engine, ambiguity refused
with the candidates listed). The worktree routes take a *whole* branch name,
slashes included, so `GET /worktrees/feat/studio-api` works. Run paths are single
segment, so address a slash-bearing branch by id or name there — `GET
/runs?branch=feat/studio-api` finds it.

### Log streaming: WebSocket and SSE

`GET /runs/{id}/logs` speaks both, and they carry the **identical** payload — a
`LogEvent` per WebSocket text frame, or per SSE `data:` line with `event:`
repeating its `type` — so picking a transport does not change how a client reads
a stream.

```ts
const ws = new WebSocket(`ws://127.0.0.1:4319/runs/${id}/logs?follow=1`);
ws.onmessage = (e) => {
  const ev: LogEvent = JSON.parse(e.data);
  if (ev.type === "log") append(ev.stream, ev.data);
  if (ev.type === "end") markFinished();
};
```

```sh
curl -N 'http://127.0.0.1:4319/runs/<id>/logs?follow=1'   # SSE, no handshake needed
```

Use the WebSocket from a browser. `EventSource` cannot carry an `Authorization`
header, so a token-protected server cannot serve a browser log viewer over SSE at
all; the WebSocket takes `?token=` for exactly that reason. Use SSE from curl or
any plain HTTP client.

The WebSocket is hand-rolled against RFC 6455 (`internal/studioapi/websocket.go`)
rather than pulled in as a dependency, because this repository's stated
convention (`CLAUDE.md`) is the standard library plus `cobra` and `yaml.v3`, and
what a one-directional log stream needs is a small, fully specified subset: the
handshake, unmasked server text frames, and enough of the client direction to
answer a ping and notice a close. Deliberately absent, since nothing here uses
them: `permessage-deflate`, subprotocol negotiation, and reassembly of
fragmented client messages. Anything that needs those is the point to argue for
a real library.

`/runs/{id}/metrics?stream=1` stays SSE-only: it is a sampler on a timer with
nothing to negotiate, and a chart does not need a socket.

Both transports end with an explicit event — `{"type":"end"}` on success,
`{"type":"error","error":"…"}` otherwise — because a client cannot otherwise
tell a stream that finished from a connection that dropped, and an incomplete
log rendered as a complete one is how a half-finished agent run reads as a
finished one.

### Usage figures are a cache, and in compose they are the wrong cache

`GET /v1/usage` reports what Claude Code last wrote to its own
`cachedUsageUtilization`, with the `fetchedAt` it came with. Two things follow,
and both are by design rather than gaps:

- **Always aged.** The cache refreshes only when the agent talks to the server,
  so a reading can be hours or days old. Every response carries `fetchedAt` and
  every client must show it; `POST /v1/usage/refresh` is the only way to make it
  current, and it spends a request from the window being measured.
- **A window past its reset reports no percentage.** The cached figure then
  measures the period *before* the reset, which is not a stale amount of the
  current one — it is a different number entirely.

Running the API from `docker compose` adds a third, and it is the one that looks
like a bug: `${HOME}/.claude.json` is **not** mounted, so the server can only
reach the sandbox agent's copy under `~/.config/sandbox/agents/claude`. That one
is refreshed only when a *sandbox* run talks to the server, so it lags the host's
by days. Uncomment the read-only mount in `docker-compose.yml`, or run the API on
the host, where both candidates are visible and the fresher one wins.

### Runs are always detached — and `console` is what you attach to

`POST /runs` has no foreground mode. An HTTP request/response cycle has
nowhere to hold a pty, so every run this API starts is what
`sandbox-cli run --detach` or a fleet task would produce. Watch it with
`GET /runs/{id}/logs?follow=1`; there is no `/runs/{id}/attach`, and there
will not be — a terminal belongs to a terminal.

That left a gap worth naming: a run could be watched and never *answered*.
`"console": true` closes it. The container is created `-dit` — still detached,
but keeping a pty and stdin — so `sandbox-cli attach <branch>` from any
terminal has a keyboard, and the agent can stop and ask something.

It changes two things together, because neither is useful alone:

- the container keeps a console (`-dit` rather than `-d`), and
- the agent runs its **interactive** argv rather than its headless one.

A console wired to `claude -p` is a keyboard for an agent that never asks;
the interactive argv without a console is an agent waiting on stdin that was
never created. So it is one field, not two. `prompt` then *seeds the first
turn* instead of being the whole run.

Refused with `verify`: verify's exit code is the container's, which is how
`land` decides the work is done, and an interactive session's exit code is
whenever somebody quit. Refused without an `agent`, too — a plain `command`
is already whatever argv you wrote.

Nothing about it widens the boundary. A pty and an open stdin change what the
container *listens to*, never what it can reach.

### Talking to a console run over HTTP

> Running the API from `docker compose`? The console needs a **rebuilt image**
> (`docker compose --profile api up -d --build api`) and a
> `SANDBOX_STUDIO_TOKEN`. A stale image accepts `"console": true`, ignores it,
> and launches the run headless — which looks exactly like the feature not
> working. `GET /v1/stats/history` answering `404` rather than `501` is the
> quickest way to spot a binary that predates both.


Two endpoints, deliberately different mechanisms.

**Reading** is `GET /runs/{id}/conversation`, and it comes from the agent's
*transcript*, not the container's output. A console run draws a full-screen
TUI: its stdout is cursor moves and repaints, and text scraped out of one
mid-redraw looks like an answer without being one. The transcript is the same
exchange as structured data, and it is written per turn while the run is in
flight — measured at 121KB → 150KB across a run whose stdout stayed empty.

The reply is `{messages: [{role, text, at}], writable}`. `writable` is the
daemon's answer to "can this be typed at right now", which needs two facts that
both live here: the container is running, and it was created with stdin.

The run→transcript correlation has two filters and both are load-bearing. Only
the **sandbox-owned** agent HOME is searched, because that directory is the
container's whole HOME and a transcript anywhere else was written by something
else — the claude wrapper really does have two verified stores, and the other
one is your own `~/.claude`. And the window is matched on when a session
*started*, not when it was last modified: a session still being appended to has
a recent mtime that says nothing about which run owns it. Skipping either put a
two-day-old conversation, from the developer's own live Claude Code session, on
the screen of a sandbox run three minutes old. Observed, then fixed, then
pinned by test. When nothing survives both filters the answer is *no
conversation* rather than the closest candidate.

**Answering** is `POST /runs/{id}/console/input` with `{"data": "...",
"enter": true}`. `enter` appends `\r`, not `\n` — the container's stdin is a
pty in raw mode, where a line feed is not a submit and the text would sit in
the agent's input box. Without `enter` the bytes go exactly as given, so a
client can send a control character or a partial line.

This is the one endpoint that **requires a token even when the rest of the
server does not**. Everything else here is read-only or launches a container
the caller could have launched anyway — `POST /runs` already takes an arbitrary
argv, so a console is not a new class of reach. What is new is a keyboard on a
session that is *already running*, holding a workspace and, under dev's
defaults, an OAuth refresh token in the agent's HOME. Without `-token` it
answers `403` and says so.

The reply also carries `sessionId` and `resume` — the exact line to type on the
host to carry the conversation on after the container is gone. It is built by
the daemon rather than assembled by a client from the id, because the flags are
not guessable: a Studio session lives in the **sandbox-owned** agent HOME under
the pooled `-workspace` bucket, and the claude wrapper's default history mount
puts the *host's* per-project bucket over exactly that path. Without `--no-sync`
the agent answers `No conversation found with session ID` for an id that is
perfectly real. Measured both ways.

### Two more things a console run may ask for

`"skipPermissions": true` adds the agent's skip-permissions flag. A *headless*
run always has it — an agent that stops to ask does not fail, it hangs — but a
console run is one somebody is attached to, where being asked is the point, so
here it is opt-in. It changes what the agent asks, never what it can reach; the
container is the blast-radius boundary either way. Only agents whose
non-interactive mode is a flag rather than a subcommand have one, which
`GET /v1/agents` reports as `canSkipPermissions`.

`"resume": "<session-id>"` carries on an existing conversation instead of
starting one, using the agent's own resume flag from the verified descriptor.
Refused without `console`: a headless resume would replay one prompt into an old
conversation and exit, which is not what "carry this on" means.

A resumed run is also the one case where the transcript belonging to a container
is *known* rather than inferred, so it is stamped as `sandbox.session`. That is
load-bearing: every correlation filter assumes a session began around the time
its container did, and a resumed one began before — without the label a resumed
run reports no conversation at all.

**Resizing is not cosmetic.** A full-screen agent renders *nothing* until it
knows its terminal size, so `POST /console/resize` is what turns an attached
console from a blank rectangle into the agent's interface. Measured: a console
container that had written zero bytes to stdout in ten minutes painted its
whole UI, 1333 bytes of it, within a second of the first resize. `docker
attach` sends one from the client terminal's dimensions, which is why attaching
from a real terminal always worked and the first browser console did not.

Two consequences a client has to handle. The signal only fires when the
dimensions actually *differ*, so re-attaching at the same size paints nothing —
Studio sends one column narrower and back. And keystrokes must be **serialized**:
one HTTP request per keypress races, and `What is 12 times 12?` reached the
agent as `rtWha is21 t ime1 2s?`. Studio coalesces into one in-flight write.

`GET /v1/health` reports `authRequired`, because health is the only endpoint
that answers without a token — so it is the only way a client lacking one can
learn it needs one, rather than failing every other request with a 401 it
cannot explain.

### Agent selection

`GET /agents` mirrors `internal/agents`, which only registers adapters with a
*verified* headless mode (`claude`, `codex`, `gemini`, `opencode`, `droid` at
the time of writing). That is not an arbitrary subset — a detached container
has no terminal, so an agent that stops to ask permission would simply hang
with nobody able to answer it. `POST /runs` refuses any other agent name.
