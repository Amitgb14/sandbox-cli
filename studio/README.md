# studio/ — Sandbox Studio

A visual control plane for `sandbox-cli`: every agent run, the boundary it ran
inside, and what it changed.

Next.js 15 (App Router) · TypeScript · Tailwind CSS 4 · shadcn/ui (new-york, on
Radix) · TanStack Query + Table · Zustand · Recharts · Lucide. Dark by default.

```sh
cd studio
npm install
npm run dev        # http://localhost:3100
npm run typecheck
npm run lint
npm run build
npm run test:e2e   # Playwright: every route renders with no console errors
```

## In one command

From the repository you want to work in:

```sh
curl -fsSL https://raw.githubusercontent.com/Amitgb14/sandbox-cli/main/studio.sh | sh
```

`studio.sh` installs `sandbox-cli` and `sandbox-studio-api` from the same
release archive, pulls `ghcr.io/amitgb14/sandbox-studio-ui`, starts both halves
and prints the URL. Re-running it is a restart; `studio.sh down`, `status` and
`logs` are the rest of it.

It is the same shape as everything below — UI containerised, API on your host —
with the three things that are easy to get wrong done for you: the project
resolved to the **repository root** (a subdirectory is what makes every
branch-addressed screen answer *not a git repository*), one token generated once
and handed to both halves, and a CORS origin matching the port it just chose.
`--api-in-docker` puts the API in a container too, and says what that costs.

The images are published on every tag and on every push to `main`
(`.github/workflows/images.yml` → `latest`, the tag, and `edge`).

```sh
sh studio.sh status      # what is running, and which repository it manages
sh studio.sh down        # stop the pair, keep everything installed
sh studio.sh uninstall   # remove the containers, the images and Studio's state
```

**Usage gauge (off by default).** The subscription panel in the sidebar footer is
built and working, and hidden until asked for: Settings → *Show subscription
usage* turns it on, per browser. Off also stops the request behind it. Changing
that default needed the persist `version`/`migrate` pair in `src/lib/store.ts` —
a preference already in localStorage outranks a default forever, so without a
migration the new default would have reached only people who had never opened
Studio.

**Editor (parked).** The Files and Changes screens are built but not in the
sidebar: `EDITOR_NAV` in `src/lib/nav.ts` holds the group, and moving it into
`NAV` surfaces both, along with their palette entries and shortcuts. The routes
and endpoints work in the meantime. What they do:

**Files** reads the scoped repository's working tree as it stands on disk,
uncommitted work included — useful for seeing what an agent has actually written
before you diff or land it. It is read-only: the agent edits the workspace from
inside a container, and a control plane that could write to it over HTTP would be
a second editor for the same tree with none of the isolation that makes the first
one safe. A path that resolves outside the repository is refused, so a symlink an
agent wrote pointing at `~/.ssh` reads as "no such file" rather than as a file.
Anything *inside* the repository is readable, `.env` included — gated by the same
loopback binding and token as the rest of the API.

**Add repositories from the browser.** "Add repository…" in the sidebar's
repository picker — or the ＋ beside the repository field on Launch — opens a
folder picker: navigate from your home directory, and the folders holding a
`.git` are marked, as are the ones Studio already manages. The picker runs in the
daemon because a browser cannot produce a host path, and it lists directories
only — never files, and never dot-directories. You can also just type or paste
the absolute path of a git repository on this machine, which is what a repository
on an unusual volume needs. Studio records its *root*, so
any directory inside it will do, and remembers it in
`~/.config/sandbox/studio/projects.json` so it is still there next time.
Removing one forgets it; nothing on disk is touched. The daemon checks each path
itself — absolute, on disk, a git repository, and never your home directory or
an ancestor of it — and shows you its own refusal when it declines.

**Forgetting one takes it off the list and nothing else.** Settings →
Repositories → Forget. The checkout, its branches, its worktrees and any
containers it has run stay where they are; add it again by path and its history
comes back with it.

**The repository it was *started* in is the exception.** `-project` is fixed for
the life of the API process: it is what every screen falls back to and the one
repository you cannot remove. You do not have to go anywhere to change it —
`--project` takes the path:

```sh
sh studio.sh up --project ~/other-project     # from any directory at all
cd ~/other-project && sh studio.sh up         # or the short way, when you are there
```

Either restarts the pair against that repository on the same ports and token, so
an open tab follows. `status` prints the repository the daemon reports and flags
the mismatch when your shell is somewhere else.

One limit: `--api-in-docker` stays single-repository. That container is started
with only its one project mounted, so a repository added afterwards is a path it
cannot see — the default, where the API is an ordinary host process, has no such
limit.

## Agents on another machine, browser on this one

The daemon and its containers can live on a Linux box while the browser stays in
front of you. "Remote" has to mean the **whole of sandbox-cli is remote**: every
safety refusal is evaluated against the filesystem it runs on — `RefuseUnsafeHostPath`
compares device and inode, `GitCommonDir` demands a real git directory, worktree
paths resolve symlinks on local disk — so a local daemon pointed at a remote
docker would validate paths here and mount paths there. `DOCKER_HOST` is a
reserved environment name for the neighbouring reason.

```sh
# on the remote machine — daemon and agents, no UI
sh studio.sh up --api-only

# reachable on the network
sh studio.sh up --api-only --bind 10.0.0.5
```

It prints the two values the other machine needs:

```
  Daemon URL   http://127.0.0.1:8787
  Token        <generated once>
```

**Leave it on loopback and tunnel.** The daemon keeps binding `127.0.0.1`, so the
`Host` it sees is still a loopback name, the DNS-rebinding defence still holds,
and the token still governs — nothing in `guard.go` is relaxed. The transport is
SSH's, which is a better answer than any TLS this repository would grow.

```sh
ssh -N -L 8787:127.0.0.1:8787 you@box     # on your machine
sh studio.sh up --api-url http://localhost:8787
```

`--bind 10.0.0.5` exposes the daemon directly instead, passing `-allow-host` for
that address because the daemon answers to loopback names only. `--bind 0.0.0.0`
works too and allows this machine's own reachable names — a wildcard is not an
address any browser dials, so it is not one the guard can be given. Use either on
a private network you trust: **there is no TLS**, so the token and everything it
protects cross the network in cleartext. The daemon *refuses* to bind a routable
address with no token at all — it holds the docker socket, so an unauthenticated
routable port is root on that machine for whoever reaches it.

Two things bite in that order, and both look like the daemon being broken:

```sh
# 1. the port. A server distribution denies inbound by default.
sudo firewall-cmd --permanent --add-port=8787/tcp && sudo firewall-cmd --reload
sudo ufw allow from 10.0.0.0/24 to any port 8787 proto tcp   # Debian/Ubuntu

# check from the machine with the browser — /health needs no token, so a JSON
# reply means bind, firewall and routing are all correct
curl http://10.0.0.5:8787/v1/health
```

**2. the origin.** `--port` on the *daemon* is the port your browser's Studio
runs on, not a UI on that machine: the daemon builds its allowed CORS origins
from it. Start it with `--port 3199` if that is where your Studio is, or the
network will work while every request is refused on the origin check — Studio
falls back to fixtures and the header badge says so, which reads like an outage.
The daemon logs the refusal, so `~/.config/sandbox/studio/api.log` names it.

**Or set it in the browser.** Settings → Connection takes a Daemon URL and a
Token, kept in the browser, which is what lets one Studio reach several boxes. A
value typed there outranks the one the UI was started with — the opposite of the
token's precedence, and deliberately: an injected token is *this* server saying
what it is running with, while an injected URL is only a default location.

Connections you use more than once can be **saved**: the list keeps a label, the URL and that daemon's token, and connecting to one applies both halves at once — a token belongs to a single machine, so switching the URL alone is the mistake the list removes. They are stored in the browser, where the active token already is.

Every path on screen is then the remote machine's — the repository picker, the
file browser, the worktree paths, the clone target — and a repository added by
path names a directory on *that* machine. Which is also why the folder picker
runs in the daemon.

So is the configuration. The egress posture, the profile and the resolved
allowlist belong to the daemon: a launch may add domains and may never widen the
posture, so the Launch screen *reports* what that machine will do rather than
offering a control it does not have. Change it in that machine's own
`~/.config/sandbox/config.yaml` (or the `--config` file it was started with) and
restart the daemon. The engine and version in the header badge are the quickest
way to be sure which machine you are looking at.

**Uninstalling needs no copy of `studio.sh`.** The installer that put it there
takes it away, including the halves that are *running*:

```sh
curl -fsSL https://raw.githubusercontent.com/Amitgb14/sandbox-cli/main/install.sh | sh -s -- --uninstall
```

That stops the UI container and the API process on your host, removes both
binaries, and lists what it deliberately left — `~/.config/sandbox` with the
agent logins in it, the base image, the cache volumes, the Studio images. Add
`--purge` to delete those too.

Stopping is not optional the way deleting an image is: Studio leaves a container
and a host process holding the docker socket and a port, and removing the
binaries while those keep running is the worst of both states.

`sh studio.sh uninstall` does the Studio-scoped half if you did have the script.
By hand it is three commands:

```sh
docker rm -f sandbox-studio-ui sandbox-studio-api
docker rmi $(docker images -q 'ghcr.io/amitgb14/sandbox-studio-*')
rm -rf ~/.config/sandbox/studio          # token, ports, api log
```

## Without a Node toolchain

`docker-compose.yml` at the repository root runs the UI in a container:

```sh
docker compose up            # http://localhost:3100
```

The API stays a host process, which is the recommended shape — it launches
containers, so in a container it would need the host's docker socket, and
anything holding that socket is root on the host:

```sh
go run ./cmd/sandbox-studio-api -cors-origin http://localhost:3100
```

If you want both in containers anyway, the API is behind an opt-in profile and
the compose file explains what you are agreeing to:

```sh
docker compose --profile api up
```

Two things that file gets right and are easy to get wrong yourself. The API and
the daemon must agree on absolute paths, because a bind mount the API asks for
is resolved by the daemon on the *host* — so the project and
`~/.config/sandbox` are mounted at their own paths, not at `/app` or `/root`.
And `NEXT_PUBLIC_SANDBOX_API` is read by the **browser**, so it has to be a URL
you could type: `http://api:8787` resolves inside the compose network and
nowhere else.

This is a **separate app from `web/`** and deliberately so. `web/` is the landing
page: a static export, light-only, with no server. Studio talks to a local daemon,
needs a served Next app, and is designed dark-first. Sharing one Next config
between a brochure and a control plane would have meant fighting `output:
"export"` on every route.

## Why it works with or without a daemon

The daemon is `cmd/sandbox-studio-api` (`internal/studioapi`), and it ships. The
fixtures stayed anyway: Studio carries one for every endpoint, so the whole UI
runs with nothing behind it — useful for working on the frontend, and the reason
this app was reviewable before the backend existed.

The rule that keeps that from becoming a lie is that **the UI always knows which
one it got**. The header carries a live/fixture badge, `/settings` names the
endpoint, and nothing presents a fixture as a real reading. It is the same
bargain the CLI makes when it prints the age of a cached usage figure instead of
the figure alone.

`src/lib/api/client.ts` probes `GET /v1/health`, caches the answer, and routes
every later call. The cache is one-directional: while the daemon is *there* it is
kept, because a failed fetch in front of every panel is what caching prevents —
but once the probe has said no it is rechecked every fifteen seconds, so a tab
opened while the API was restarting heals itself instead of showing fixtures
forever. Flipping to live invalidates every query, or the badge would change
while the tables underneath kept their fabricated rows. `reconnect()` is the
explicit retry, wired to the header badge and the ⌘K palette, and a write
(adding a repository, say) re-probes before refusing rather than trusting a
cached "offline".

## The backend contract

Point Studio at a different daemon two ways, and which one you want depends on
whether you are building the bundle or running someone else's:

- `NEXT_PUBLIC_SANDBOX_API` — read at **build** time and inlined, so it is the
  one for `npm run dev` and for a deployment that compiles its own bundle.
- `SANDBOX_API_URL` — read per request by the server and handed to the page as
  `window.__SANDBOX_API__`, so it is the one for the **published image**, whose
  build could not have known which port your daemon would end up on. It wins
  when both are set. `SANDBOX_STUDIO_TOKEN` rides the same channel, which is how
  `studio.sh` gets a token into the browser without anybody copying one.

Both default to `http://localhost:8787`. The root layout is `force-dynamic` for
this reason: prerendered, it would read the environment once at build time and
the runtime value would silently do nothing.

| Method | Path | Returns |
|---|---|---|
| `GET` | `/v1/health` | `DaemonInfo` |
| `GET` | `/v1/runs` | `{ runs }` — live only; `?all=1` includes finished |
| `POST` | `/v1/runs` | `Run` — launch, always detached |
| `GET` | `/v1/runs/:id` | `Run` |
| `DELETE` | `/v1/runs/:id` | `204` — reap a finished container; refuses a live one |
| `GET` | `/v1/runs/:id/metrics` | `MetricSeries`; `?stream=1` for SSE |
| `GET` | `/v1/runs/:id/logs` | `LogLine[]`; `?follow=1` for SSE |
| `GET` | `/v1/runs/:id/diff` | `DiffFile[]`, with hunks |
| `GET` | `/v1/runs/:id/config` | `ResolvedConfig` |
| `POST` | `/v1/runs/:id/stop` | `Run` — `{"force":true}` to kill |
| `POST` | `/v1/runs/:id/recover` | restore this branch's crash snapshot |
| `GET` | `/v1/runs/:id/conversation` | the agent's transcript for this run |
| `GET` | `/v1/runs/:id/console` | live console output (WebSocket, or SSE by default) |
| `POST` | `/v1/runs/:id/console/input` | type at a running agent — **requires `-token`** |
| `POST` | `/v1/runs/:id/console/resize` | tell the container its terminal size |
| `GET` | `/v1/agents` | `{ agents }` |
| `GET` | `/v1/agents/:agent/sessions` | that agent's past conversations, for resume |
| `GET` · `POST` | `/v1/worktrees` | `{ worktrees }` · create |
| `GET` · `DELETE` | `/v1/worktrees/:branch` | `Worktree` · `204` (`?force=1`) |
| `GET` | `/v1/worktrees/:branch/commits` | what the agent committed on that branch |
| `GET` | `/v1/commits/:sha/diff` | one commit's diff |
| `GET` | `/v1/stats` | one sample per live run, host-wide |
| `GET` | `/v1/stats/history` | the same, over time |
| `GET` · `POST` | `/v1/usage` · `/v1/usage/refresh` | `UsageSnapshot` |
| `GET` | `/v1/doctor` | `{ profile, checks }` |
| `GET` | `/v1/audit` | `{ records }` — env by name only |

`:branch` is a trailing wildcard on the two worktree routes, because a branch
name usually contains a slash (`feat/studio-api`) and a single-segment parameter
makes exactly those branches unaddressable. `/commits` keeps a single segment: a
trailing wildcard has to be the last thing in the pattern, and it is the more
specific route, so the mux prefers it.

Two are deliberately absent. **Kill is not its own route** — it is `stop` with
`{"force":true}`, because the difference is a flag on one act and a second path
to "end this run" is a second place for the two to disagree about what they
reach. And **there is no `land`**: it merges into the base branch, and this API
has no authentication unless a token is set, so a write endpoint that can move
someone's `main` is a decision for whoever runs the daemon rather than a gap to
fill quietly.

The launch form's dry run is computed in the browser (`localPreview`) rather
than by the daemon. It models a documented subset of the refusals, and says so.

`src/lib/types.ts` is the whole schema, and every type names the Go struct it
mirrors — `runtime.ContainerInfo`, `audit.SessionMeta`, `agents.Descriptor`,
`worktree.Info`, `agentusage.Snapshot`, `config.Config`. Where the Go side draws a
distinction the types draw the same one, because collapsing it here is how a UI
ends up asserting something the daemon never claimed:

- **An id is not a path.** `Run.repoId` is `worktree.RepoID`; `repoName` is for
  humans and two clones of a same-named repo share it.
- **A request is not an outcome.** `EgressEnforcement` is what was *asked for*.
  The container takes the by-name proxy path only if `sandbox-egress-proxy` is on
  its PATH, and the host cannot observe that.
- **Absent is not zero.** `latestMetrics` is nullable, `UsageWindow.utilization`
  is nullable, `passRate` is `null` when nothing has finished. A container that
  exited in a second was not idle — it was never measured.

Logs and metrics will arrive as newline-delimited JSON; `streamNdjson` already
reads that shape and falls back to replaying a fixture on a timer, append-only, so
nothing in the live views can come to depend on having the whole transcript up
front.

## Screens

| Route | What it is |
|---|---|
| `/` | Dashboard — tiles, run volume, outcomes, what is in flight, the boundary, the land queue |
| `/runs` | Every run. TanStack Table: one free-text field, five faceted filters with live counts, column visibility, pagination, row selection, bulk stop/kill |
| `/runs/[id]` | Terminal · Metrics · Changes · Logs · Config. The tab lives in the URL so a link can point at one |
| `/launch` | Start a run, with the boundary spelled out and a dry-run preview that recomputes as you type |
| `/agents` | The thirteen adapters, their logins, what crosses the boundary, and which are fleet-eligible |
| `/worktrees` | One branch per agent, with the two facts `land` refuses on |
| `/settings` · `/settings/doctor` | What Studio remembers, what the CLI decides, and whether this host can deliver it |

## What the UI refuses to do

These are design decisions, not omissions.

- **Stop and kill are never one control.** Stop asks the guest to exit and waits;
  kill does not. The difference is whether the agent closed the file it was
  editing, so it has to be chosen by name — and only kill confirms, because
  reading the wrong session costs a second and stopping the wrong agent costs its
  work.
- **A terminal is read-only unless the run has a console.** An ordinary detached
  run has neither `-i` nor `-t`, so nothing is listening on stdin and an input
  that silently went nowhere would be worse than none. A run launched with
  `console: true` is created `-dit` for exactly this, and then the terminal
  attaches for real over `console/input`. Which one you have is stated rather
  than inferred: typing at a run without a console is refused with the reason,
  since stdin is fixed at create time and nothing later can open it.
- **Typing always needs a token.** Every other endpoint is read-only or launches
  a container you could have launched anyway; a keyboard on a session that is
  *already running* — holding a workspace, and under dev's defaults an OAuth
  refresh token in the agent's HOME — is not. `console/input` refuses outright
  when the daemon was started without `-token`, whatever the rest of the server
  is doing.
- **Security settings are read-only in `/settings`.** The profile, the egress
  posture and the mount rules come out of the config layers. A UI that wrote into
  the middle of that stack would become a layer nobody could see in the file.
- **The launch form models a documented subset of the refusals.** The daemon runs
  the real `BuildSpec`; a form that reimplemented the whole rule set would
  eventually disagree with the thing that actually enforces it. What it does model,
  it explains — every refusal carries its reason.
- **Values from the repository are text.** A branch name is written by whoever
  pushed it and is rendered as text, never interpreted. A table should not be
  forgeable by a branch name.

## Design

Dark is the designed mode; light is stepped for its own surface and validated
against it rather than being an automatic flip. Two colour families live in
`src/app/globals.css` and must not be confused:

- **UI chrome** (`--background` … `--primary`). `--primary` is the brand violet
  and never appears as a data series.
- **Data-visualisation slots** (`--chart-1` … `--chart-8`, `--status-*`), assigned
  in fixed order and never cycled.

The categorical palette was validated against Studio's own surfaces rather than
assumed: on dark (`#121214`) all eight slots pass the lightness band, chroma
floor, adjacent-pair CVD separation (worst ΔE 8.4), normal-vision floor (worst ΔE
19.3) and 3:1 contrast. On light (`#ffffff`) slots 3–5 fall below 3:1, which is
why every chart ships a legend, direct labels and a table view.

Chart rules that are load-bearing rather than stylistic:

- **Never two y-axes.** CPU is a percentage that routinely passes 100%; memory is
  a byte count with a ceiling. They get two frames sharing an x-axis, because a
  visual crossing between them would mean nothing.
- **Status colours are reserved.** `--status-good` is not "series 9". "Verify
  failed" is its own colour and not folded into "failed" — a run whose verify said
  no did its work and was judged; one that crashed never got that far.
- **Every day appears in a volume chart, including the empty ones.** Skipping them
  draws a busy week and a quiet one the same width.

## Development notes

**`node_modules` lives outside this directory.** `/workspace` is a bind mount of
the host worktree, and this container is linux/arm64 while the host is
darwin/arm64 — so installing here would fill the shared tree with Linux native
binaries and break the host's toolchain. `studio/node_modules` is a symlink to a
container-local path.

On the host that symlink is dangling and harmless (it is gitignored, so it is
never committed). `npm install` replaces it with a real directory on its own; if
you would rather be explicit, `rm studio/node_modules && npm install`.

Fixtures are generated from a seeded PRNG (`src/lib/mock/rng.ts`), so they are the
same on every reload — a chart whose shape changes each refresh makes it
impossible to tell a rendering bug from new data. Only `NOW` varies, read once at
module load, and every fixture-derived timestamp renders client-side, which is
what keeps relative times out of hydration mismatches.
