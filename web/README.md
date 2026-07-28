# web/ — the sandbox-cli landing page

Next.js 16 (App Router, Turbopack) · TypeScript · Tailwind CSS 4 · shadcn/ui
(`base-nova`, on Base UI). Builds to a **fully static export**, so it deploys to
GitHub Pages or any bucket with no Node server.

```sh
cd web
npm install
npm run dev      # http://localhost:3000
npm run lint
npm run build    # static export -> web/out
```

## Design direction

Light, near-monochrome, in the spirit of a modern security-tooling page: white
paper, one very dark ink for type and primary actions, hairline neutral rules,
`Inter` for text and `Geist Mono` for everything the terminal says. Colour is
never decoration — there are exactly three semantic hues and each means one
thing:

| Token | Colour | Means |
|---|---|---|
| `contained` | emerald `#059669` | inside the boundary — allowed, safe, mounted |
| `exposed` | rose `#e11d48` | outside it — reachable, dangerous |
| `caution` | amber `#b45309` | opt-in reach that deliberately widens the boundary |

Every graphic restates the same pair, so the palette carries the argument. All
tokens live in `src/app/globals.css` under the shadcn contract (`--background`,
`--card`, `--muted`, `--border`, `--radius`) plus the three above, exposed to
Tailwind through `@theme inline` as `text-contained`, `bg-exposed-soft`, and so
on. The page is light-only by design and respects `prefers-reduced-motion`
throughout.

## The interactive pieces

| Component | What it does |
|---|---|
| `containment-canvas.tsx` | The centrepiece. A `<canvas>` particle system: commands launch from the host side, ones that reach past the workspace **shatter** against the wall in 22 physics-driven shards, ordinary work passes through the one opening into `/workspace`. DPI-aware; every colour is read from CSS custom properties each frame. |
| `containment-simulator.tsx` | Wraps the canvas with presets, a **free-text prompt** (type any command and it is classified for real), and a verdict log naming the mechanism that decided it. Autoplays once on scroll, then hands over. |
| `blast-radius.tsx` | One switch flips twelve host locations between *reachable* and *not a path at all*, with a live count and the stake behind each one. |
| `dry-run-builder.tsx` | Toggle real sandbox flags and watch the actual `docker` argv assemble, line by line, in the order `runtime.BuildArgs` emits them. Each flag is marked as widening or tightening the boundary, and a counter tracks host paths in reach. |
| `egress-visualizer.tsx` | Requests fly at the firewall: registries and agent APIs sail through, exfiltration stops dead. One switch turns the allowlist off to show the difference. |
| `parallel-agents.tsx` | Three branches, three containers, one repo — the `--worktree` story as a diagram. |
| `live-gauge.tsx` | The three places sandbox-cli reports usage (footer gauge, Claude status line, peak summary), with numbers that walk client-side only. |
| `agent-explorer.tsx` | All fifteen adapters: install route, forwarded env, `--allow` domains, and the per-agent gotcha. |
| `deploy-guide.tsx` | Local development and production as two step-by-step paths, over a matrix of what `--profile` changes — the selected column stays lit while you read, because the section is about the difference. The prod path ends with the invariants re-checked on the fully-merged config. |
| `capability-chart.tsx` | Recharts. An **emphasis** bar chart (accent + two grays), not a rainbow — palette validated for the light surface: every adjacent pair clears the CVD and normal-vision separation floors and all three clear 3:1 contrast. |

## Where the content comes from

Copy and data mirror the repository — `README.md`, `CLAUDE.md`,
`docs/AGENTS.md`, `docs/GUIDE.md`. The typed data lives in `src/lib`:

- `site.ts` — version, install routes, repo and doc URLs
- `agents.ts` — the fifteen adapters, delivery, sizes, forwarded env, baseline domains
- `features.ts` — every shipped capability, grouped
- `setup.ts` — per-platform setup, every path ending in `doctor`
- `deploy.ts` — the dev and prod paths, and what `--profile` changes between
  them; mirrors `internal/config/profile.go`
- `comparison.ts` — the landscape table, the 0–5 scores behind the chart, the platform matrix
- `argv.ts` — the model of `runtime.BuildArgs` behind the command builder
- `reach.ts` — host paths and what is at stake in each
- `egress.ts` — destinations and their verdicts
- `classify.ts` — the boundary classifier behind the simulator

If the CLI's behaviour changes, update those files and the page follows.
**`VERSION` in `src/lib/site.ts` is the only place the release number appears.**

## Deploy

`npm run build` writes a static site to `web/out`.

- **GitHub Pages** — publish `web/out`. Set `basePath` in `next.config.ts` if
  serving from a subpath.
- **Netlify / Vercel / Cloudflare Pages** — build command `npm run build`,
  publish directory `web/out`.
- **Any bucket / CDN** — upload `web/out` as-is.

`output: "export"` means no server features (no route handlers, no ISR,
`images.unoptimized` is on). That is deliberate — the page is a brochure. The
repo's Go toolchain is untouched; `make test` and friends never look here.
