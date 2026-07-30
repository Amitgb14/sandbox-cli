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

## Why it is a frontend that already works

The daemon does not exist yet. Studio is built against its contract and ships a
fixture for every endpoint, so the whole UI is reviewable today — but the rule
that keeps that from becoming a lie is that **the UI always knows which one it
got**. The header carries a live/fixture badge, `/settings` names the endpoint,
and nothing presents a fixture as a real reading. It is the same bargain the CLI
makes when it prints the age of a cached usage figure instead of the figure alone.

`src/lib/api/client.ts` probes `GET /v1/health` once, caches the answer, and
routes every later call. `reconnect()` is the explicit retry, wired to the header
badge and the ⌘K palette.

## The backend contract

Point Studio at a different daemon with `NEXT_PUBLIC_SANDBOX_API`; it defaults to
`http://localhost:8787`.

| Method | Path | Returns |
|---|---|---|
| `GET` | `/v1/health` | `DaemonInfo` |
| `GET` | `/v1/runs` | `Run[]` |
| `POST` | `/v1/runs` | `{ id }` — launch |
| `POST` | `/v1/runs/preview` | `LaunchPreview` — the real `BuildSpec` dry run |
| `GET` | `/v1/runs/:id` | `Run` |
| `GET` | `/v1/runs/:id/metrics` | `MetricSeries` |
| `GET` | `/v1/runs/:id/logs` | `LogLine[]` |
| `GET` | `/v1/runs/:id/diff` | `DiffFile[]` |
| `GET` | `/v1/runs/:id/config` | `ResolvedConfig` |
| `POST` | `/v1/runs/:id/stop` | `204` |
| `POST` | `/v1/runs/:id/kill` | `204` |
| `GET` | `/v1/agents` | `Agent[]` |
| `GET` | `/v1/worktrees` | `Worktree[]` |
| `POST` | `/v1/worktrees/:branch/land` | `{ merged, message }` |
| `DELETE` | `/v1/worktrees/:branch` | `204` |
| `GET` | `/v1/usage` · `POST /v1/usage/refresh` | `UsageSnapshot` |
| `GET` | `/v1/doctor` | `DoctorCheck[]` |
| `GET` | `/v1/audit` | `AuditRecord[]` |

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
| `/agents` | The fifteen adapters, their logins, what crosses the boundary, and which five are fleet-eligible |
| `/worktrees` | One branch per agent, with the two facts `land` refuses on |
| `/settings` · `/settings/doctor` | What Studio remembers, what the CLI decides, and whether this host can deliver it |

## What the UI refuses to do

These are design decisions, not omissions.

- **Stop and kill are never one control.** Stop asks the guest to exit and waits;
  kill does not. The difference is whether the agent closed the file it was
  editing, so it has to be chosen by name — and only kill confirms, because
  reading the wrong session costs a second and stopping the wrong agent costs its
  work.
- **The terminal is read-only and says so.** A detached run has neither `-i` nor
  `-t`, so it is not listening on stdin. An input that silently went nowhere would
  be worse than none; interactive sessions get a pointer to `sandbox-cli attach`.
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
