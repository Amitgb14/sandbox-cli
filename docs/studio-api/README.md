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
`-token` (or `$SANDBOX_STUDIO_TOKEN`), `-cors-origin` (repeatable).

## Trust model

This is an HTTP server that can start containers and stop/kill them, on
request, with no confirmation prompt — that is a new local attack surface the
CLI itself does not have, and it deserves the same care as everything else in
`CLAUDE.md`'s trust-boundary section. Three defaults hold the line:

- **Binds to loopback by default** (`127.0.0.1:8787`). Nothing off the machine
  can reach it unless you deliberately rebind `-addr`.
- **No CORS headers unless you ask for them.** A browser tab open on some
  other site cannot read this API's responses cross-origin — the classic
  "malicious webpage drives your local admin API" class of attack (the same
  one Ollama, various print/scan services, and dev servers have all shipped
  at one point) is closed by default. Studio's own frontend, if served from a
  different origin (a Next dev server on another port, say), needs an
  explicit `-cors-origin http://localhost:3000`.
- **Optional bearer token** (`-token` / `$SANDBOX_STUDIO_TOKEN`), required on
  every request but `/health`. A second, cheap layer on top of the two above:
  it does not stop another local process that can already reach 127.0.0.1,
  but it stops one that happens to guess the port.

None of this replaces the container boundary — it is scoped to "who may ask
this process to start or stop containers," which is a question the CLI never
had to answer because a terminal already answers it.

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
| POST | `/v1/runs` | Launch a run — always detached; `console:true` keeps a terminal to attach to (see below) |
| POST | `/v1/runs/{id}/stop` | Stop (or `{"force":true}` to kill) a running run |
| POST | `/v1/runs/{id}/recover` | Restore the crash-recovery snapshot associated with this run's branch |
| GET | `/v1/runs/{id}/logs` | Server-Sent Events log stream (`?follow=1` to keep it open) |
| GET | `/v1/runs/{id}/metrics` | One resource sample, or a live stream with `?stream=1` |
| GET | `/v1/stats` | One resource sample per live run, host-wide |
| GET/POST | `/v1/worktrees` | List / create managed git worktrees |
| GET/DELETE | `/v1/worktrees/{branch}` | Read / remove (`?force=1`) one worktree |

### Why SSE, not WebSocket

This repository's stated convention (`CLAUDE.md`) is standard library plus
`cobra` and `yaml.v3` — nothing else. Go's `net/http` has no server-side
WebSocket support, and every stream this API offers (`docker logs -f`,
resource samples) is one-directional: server to client, nothing sent back
over the same connection. That is exactly what Server-Sent Events are for,
and it costs no new dependency. If a future need turns up for the client to
push messages over the same connection, that is the point to revisit this
choice — not before.

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

### Agent selection

`GET /agents` mirrors `internal/agents`, which only registers adapters with a
*verified* headless mode (`claude`, `codex`, `gemini`, `opencode`, `droid` at
the time of writing). That is not an arbitrary subset — a detached container
has no terminal, so an agent that stops to ask permission would simply hang
with nobody able to answer it. `POST /runs` refuses any other agent name.
