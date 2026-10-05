# studio/ — Sandbox Studio

The browser view of `sandbox-cli`: launch a command or an agent, watch its
output, type into its terminal, read its files and audit events. A sandbox has
no repository, so there is nothing to bring back or review on the host.

```sh
sandbox-cli studio          # prints http://127.0.0.1:7080/#token=…
```

Next.js 15 (App Router) · TypeScript · Tailwind CSS 4 · shadcn/ui (on Radix) ·
TanStack Query · Zustand · xterm.js. Dark by default.

This is the developer's page. What each screen does for a user, how scopes
decide them on a gateway, organizations and the hosted build are in
[docs/studio.md](../docs/studio.md), which the website renders too.

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
serving `out/`, in front of a `sandboxd` on its in-memory backend, and four more
in front of a real `sandbox-gateway` (one node, on the same backend), one per
API key — admin, a tenant's that may create organizations, a read-only one, and
one that may not create them (`e2e/state.ts`) — all started
by `e2e/global-setup.ts`. `npm run check` also runs `check:admin-off`, which
builds with `NEXT_PUBLIC_STUDIO_ADMIN=off` and fails if anything of the admin
screens is in that build.

A pull request that touches only `studio/` runs only Studio's CI job, not the
Go matrix; one that changes Go under `cmd/` or `internal/` runs it too, since
the suite runs those binaries. Note what changed on the screens in
[CHANGELOG.md](CHANGELOG.md) here; a change that also touches the CLI or the
API goes in the main one.

## Screens

| Route | Screen |
|---|---|
| `/` (also `/sandboxes`), `/sandbox?id=` | Home: a strip of what sandboxes were given (`GET /v1/node` where offered), then every sandbox, searched, filtered by state, sorted, paged and selectable, with what each was given; a row opens its details in a panel (`components/sandbox/details.tsx`), which is also the page at `/sandbox?id=` — overview, terminal (Terminal opens a shell in it, as `sandbox-cli shell` does), logs, files and events, two panes when wide; a first-run panel with code when there are none |
| `/launch` | The Playground: a command, an unattended agent or an agent's console, starting in `/sandbox/home`, with the same command run written as CLI, curl, Python and TypeScript beside the form |
| `/snapshots` | Sandboxes captured whole, to start new ones from; delete one |
| `/agents`, `/volumes`, `/settings` | The agents Studio runs (those with a verified headless mode) and their logins; volumes; the context |

### Through a gateway

On load Studio asks `GET /v1/whoami` (`src/lib/caller.ts`). A plain sandboxd
answers 404 and gets the screens above and nothing else. A gateway answers with
the key's scopes, which decide the rest ([docs/studio.md](../docs/studio.md#scopes-and-screens)):

| Route | Screen | Shown to |
|---|---|---|
| `/jobs`, `/job?id=` | Jobs: submit, cancel; a job's runs, each run's kept output and files | any key |
| `/services`, `/service?name=` | Services: deploy from a JSON spec, replicas and health, scale, remove | any key |
| `/secrets` | The tenant's secrets by name; set and remove with `secrets:write` | any key |
| `/ssh` | Where to connect; your SSH keys and a short-lived access token with `sandbox:ssh` | any key |
| `/members` | The current organization's members; owners (and admin) add, remove and change roles, members read | any key |
| `/account` | User, tenant, the current organization, key id and scopes | any key |
| `/admin/nodes`, `/admin/lost`, `/admin/keys`, `/admin/audit`, `/admin/orgs` | Nodes (cordon, drain, add, remove), lost sandboxes, API keys and any user's SSH keys, the audit record, every organization | `admin` |

On a gateway the top of the sidebar is the organization switcher
(`src/components/shell/org-switcher.tsx`, `src/lib/org.ts`): the key's own
tenant and every organization its user belongs to, *Create organization* with
`org:create`, and *Members*. The choice is kept per browser in localStorage and
sent as `X-Sandbox-Org` on every call (`?org=` on the attach WebSocket, which
cannot carry headers); the gateway checks it. Switching clears the query cache
and remounts the screens, so nothing of the previous organization is shown. The
detection call, whoami, is made without it, so a refused selection never reads
as a plain sandboxd; a refused one falls back to the key's own tenant, with a
notice. With nothing chosen in this browser, Studio starts in the context's
organization (`sandbox-cli org use`).

Actions a key's scopes do not allow are hidden, everywhere (`useCan`); a screen
the caller may not have renders *Not available* inside `<Gate>`, which mounts
nothing — and so requests nothing — until whoami allows it. Hiding is
cosmetic; the gateway's 403 is the control. The admin API lives in
`src/lib/admin` and is imported only by the admin pages, which are named
`page.admin.tsx`: `NEXT_PUBLIC_STUDIO_ADMIN=off` at build time drops
`admin.tsx` from the page extensions (`next.config.ts`), so those pages are
not routes and none of their code is bundled — for a dashboard hosted for many
tenants. `NEXT_PUBLIC_STUDIO_ADMIN=off make studio build` builds a sandbox-cli
with that Studio.
