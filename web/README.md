# web/ — sandbox-cli landing page

Next.js 16 + TypeScript + Tailwind 4 + shadcn/ui. Builds to a **fully static
export**, so it still deploys to GitHub Pages or any bucket with no Node server.

## Design direction — "the containment boundary"

The product *is* a wall between your host and an agent, so the page is built
around that wall rather than around a generic feature grid.

- **Colour is semantic.** Warm coral = *exposed* (host side), cool teal =
  *contained* (sandbox side). The same pair drives the simulator, the threat
  panels, the comparison table and the radar chart, so the palette restates the
  product's thesis wherever it appears. Both are exposed as Tailwind colours
  (`text-exposed`, `bg-contained-soft`, …) via `@theme inline`.
- **Type has three roles** — Bricolage Grotesque (display), IBM Plex Sans (body),
  IBM Plex Mono (the terminal voice, used heavily since the subject is a CLI).
  Loaded through `next/font`, so they self-host with no external request.
- **shadcn/ui token contract** (`--background`, `--card`, `--muted`, `--border`,
  `--radius`) with cool-biased neutrals instead of default grey.
- **Light + dark** via `next-themes`, following the OS by default.
- Respects `prefers-reduced-motion` throughout — the simulator resolves straight
  to its verdict instead of animating.

## The interactive pieces

| Component | What it does |
|---|---|
| `containment-canvas.tsx` | The centrepiece. A `<canvas>` particle system: commands launch from the host side, blocked ones **shatter** against the wall with 30 physics-driven shards, allowed ones pass through into `/workspace`. Ambient motes drift on the exposed side. DPI-aware, theme-aware (reads CSS custom properties each frame). |
| `containment-simulator.tsx` | Wraps the canvas with preset buttons, a **free-text prompt** (type any command and it is classified for real), and a verdict log. Autoplays once on scroll, then hands over to the visitor. |
| `blast-radius.tsx` | One switch flips the host filesystem between `reachable` and `not mounted`, with only the project staying lit. |
| `dry-run-builder.tsx` | Toggle real sandbox flags (`--worktree`, `--allow`, `--no-persist-auth`, `--root`) and watch the actual `docker` argv assemble line by line, animated in and out. |
| `attack-surface-chart.tsx` | Recharts radar over six axes, all "higher is better". Series toggle so the shapes stay comparable. |
| `agent-explorer.tsx` | All fifteen adapters; pick one for its real install route, forwarded env vars and per-agent gotcha. |

## Where the content comes from

Copy and data mirror the repo — `README.md`, `CLAUDE.md` and
`docs/proposals/agent-adapters.md`. The typed data lives in:

- `src/lib/agents.ts` — the fifteen adapters, install routes, forwarded env
- `src/lib/commands.ts` — the boundary classifier (8 rules) and presets
- `src/lib/comparison.ts` — spec-table rows and radar scores

If the CLI's behaviour changes, update those three files and the page follows.

## Develop

```sh
cd web
npm install
npm run dev     # http://localhost:3000
npm run lint
npm run build   # static export -> web/out
```

## Deploy

`npm run build` writes a static site to `web/out`.

- **GitHub Pages** — publish `web/out` (via an action, or push it to `gh-pages`).
  Set `basePath` in `next.config.ts` if serving from a subpath.
- **Netlify / Vercel / Cloudflare Pages** — build command `npm run build`,
  publish directory `web/out`.
- **Any bucket / CDN** — upload `web/out` as-is.

## Notes

- `output: "export"` means no server features (no route handlers, no ISR,
  `images.unoptimized` is on). That is deliberate — the page is a brochure.
- The repo's Go toolchain is untouched; `make test` and friends never look here.
