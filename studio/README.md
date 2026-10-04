# studio/ — Sandbox Studio

The browser view of `sandbox-cli`: launch a command or an agent, watch its
output, type into its terminal, read its files and audit events. A sandbox has
no repository, so there is nothing to bring back or review on the host.

```sh
sandbox-cli studio          # prints http://127.0.0.1:7080/#token=…
```

Next.js 15 (App Router) · TypeScript · Tailwind CSS 4 · shadcn/ui (on Radix) ·
TanStack Query · Zustand · xterm.js. Dark by default.

## How it is served

Studio is a **static export embedded in `sandbox-cli`** (`internal/studio`).
`sandbox-cli studio` serves it on a loopback port together with a small API,
from the same origin:

| Path | What answers it |
|---|---|
| `/` and every route | this app, as files (`out/`) |
| `/api/v1/…` | sandboxd's API, proxied to the current context with that sandboxd's token; the browser never holds it |
| `/api/ws/attach` | a WebSocket bridge to a process's terminal |
| `/api/info`, `/api/agents`, `/api/agents/state` | the context and version, the agents and their saved logins, and each agent's state |
| `POST /api/runs` | launching a run, through the same code the CLI uses |

Every `/api` request needs the token `sandbox-cli studio` prints in the URL's
fragment; it is read once, kept in `sessionStorage` for the tab, and wiped from
the address bar. Studio answers loopback `Host` names only and refuses a
cross-origin request. See `internal/studio/server.go`.

Routes are flat, and a detail screen takes its subject as a query parameter
(`/sandbox?id=…`): a static export has no server to answer a path it did not
build.

## Working on it

```sh
cd studio
npm ci
npm run build                 # -> out/
cd .. && make build           # bin/sandbox-cli, bin/sandboxd
bin/sandbox-cli studio --ui-dir studio/out
```

`--ui-dir` serves a build from disk, so a UI change is `npm run build` and a
reload, without rebuilding Go. `make studio` builds the UI into
`internal/studio/ui`, where `sandbox-cli` embeds it; a release does this.

```sh
npm run typecheck
npm run lint
npm run test:e2e              # needs bin/ (make build) and out/ (npm run build)
```

The end-to-end suite (`e2e/`) runs against the real thing: `sandbox-cli studio`
serving `out/`, in front of a `sandboxd` on its in-memory backend, both started
by `e2e/global-setup.ts`.

## Screens

| Route | Screen |
|---|---|
| `/` (also `/sandboxes`), `/sandbox?id=` | Home: every sandbox, searched and filtered by state, with what each was given and a menu to open, copy or terminate it; a first-run panel with code when there are none. One sandbox's overview, terminal, logs, files and events |
| `/launch` | The Playground: a command, an unattended agent or an agent's console, starting in `/sandbox/home`, with the same command run written as CLI, curl, Python and TypeScript beside the form |
| `/snapshots` | Sandboxes captured whole, to start new ones from; delete one |
| `/agents`, `/volumes`, `/settings` | The agents Studio runs (those with a verified headless mode) and their logins; volumes; the context |
