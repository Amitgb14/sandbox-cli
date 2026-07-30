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

Flags: `-addr` (default `127.0.0.1:4319`, loopback), `-project` (default: cwd),
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
| GET | `/health` | Liveness + which engine/project/profile this instance manages |
| GET | `/agents` | Agents this API can launch headlessly (a subset of `internal/agents` — only those with a verified non-interactive mode) |
| GET | `/runs` | List runs (`?all=1`, `?repo=`, `?branch=`, `?agent=`, `?fleet=1`) |
| GET | `/runs/{id}` | One run, by id/name/branch — same three references `sandbox-cli list`/`kill`/`logs` accept |
| POST | `/runs` | Launch a run — always detached (see below) |
| POST | `/runs/{id}/stop` | Stop (or `{"force":true}` to kill) a running run |
| POST | `/runs/{id}/recover` | Restore the crash-recovery snapshot associated with this run's branch |
| GET | `/runs/{id}/logs` | Log stream — WebSocket if the client upgrades, SSE otherwise (`?follow=1` to keep it open) |
| GET | `/runs/{id}/metrics` | One resource sample, or a live SSE stream with `?stream=1` |
| GET | `/stats` | One resource sample per live run, host-wide |
| GET/POST | `/worktrees` | List / create managed git worktrees |
| GET/DELETE | `/worktrees/{branch...}` | Read / remove (`?force=1`) one worktree |

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

### Runs are always detached

`POST /runs` has no foreground/interactive mode. An HTTP request/response
cycle has nowhere to hold a pty, so every run this API starts is what
`sandbox-cli run --detach` or a fleet task would produce — never `-it`. Watch
it with `GET /runs/{id}/logs?follow=1`; there is no `/runs/{id}/attach`.

### Agent selection

`GET /agents` mirrors `internal/agents`, which only registers adapters with a
*verified* headless mode (`claude`, `codex`, `gemini`, `opencode`, `droid` at
the time of writing). That is not an arbitrary subset — a detached container
has no terminal, so an agent that stops to ask permission would simply hang
with nobody able to answer it. `POST /runs` refuses any other agent name.
