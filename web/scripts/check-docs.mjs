#!/usr/bin/env node
// Checks every internal link in the exported site (out/), and so every link
// the rendered docs make: a link to a page that was not built, or to an
// anchor that page does not have, fails.
//
// It reads the export rather than re-rendering the Markdown itself, so what
// it checks is exactly what is deployed — the docs' links after they were
// rewritten to /docs addresses (src/lib/markdown.ts), the sidebar, the
// header, the footer, and the other pages' links into the docs. A relative
// link in a doc to a file the repository does not have fails earlier, in the
// build.
//
// Run after `next build` (npm run build does both): npm run check:docs.

import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const OUT = path.join(path.dirname(fileURLToPath(import.meta.url)), "..", "out");

if (!fs.existsSync(path.join(OUT, "docs", "index.html"))) {
  console.error(`check-docs: ${path.relative(process.cwd(), OUT)}/docs/index.html is missing: run \`npm run build\` first`);
  process.exit(1);
}

/** Every .html file under dir. */
function htmlFiles(dir) {
  return fs.readdirSync(dir, { withFileTypes: true }).flatMap((e) => {
    const p = path.join(dir, e.name);
    if (e.isDirectory()) return e.name === "_next" ? [] : htmlFiles(p);
    return e.name.endsWith(".html") ? [p] : [];
  });
}

/** The address a file is served at, as trailingSlash: true exports it. */
function routeOf(file) {
  const rel = path.relative(OUT, file).split(path.sep).join("/");
  if (rel === "index.html") return "/";
  if (rel.endsWith("/index.html")) return `/${rel.slice(0, -"index.html".length)}`;
  return `/${rel}`;
}

const decode = (s) =>
  s.replace(/&amp;/g, "&").replace(/&quot;/g, '"').replace(/&#x27;|&#39;/g, "'").replace(/&lt;/g, "<").replace(/&gt;/g, ">");

const pages = new Map(); // route -> { ids, hrefs }
for (const file of htmlFiles(OUT)) {
  const html = fs.readFileSync(file, "utf8");
  // Only the document itself: the inlined RSC payload in <script> repeats
  // hrefs in its own encoding, and is checked by being the same page.
  const body = html.replace(/<script[\s\S]*?<\/script>/g, "");
  const ids = new Set([...body.matchAll(/\sid="([^"]*)"/g)].map((m) => decode(m[1])));
  const hrefs = [...body.matchAll(/<a\s[^>]*?href="([^"]*)"/g)].map((m) => decode(m[1]));
  pages.set(routeOf(file), { ids, hrefs });
}

const problems = [];
let checked = 0;
for (const [route, { hrefs }] of pages) {
  if (route === "/404.html" || route === "/_not-found/") continue;
  for (const href of hrefs) {
    if (/^[a-z][a-z0-9+.-]*:/i.test(href) || href.startsWith("//")) continue; // external
    checked++;
    const hashAt = href.indexOf("#");
    let target = hashAt < 0 ? href : href.slice(0, hashAt);
    const anchor = hashAt < 0 ? "" : decodeURIComponent(href.slice(hashAt + 1));
    if (target === "") target = route;
    if (!target.startsWith("/")) {
      problems.push(`${route}: relative link ${href}`);
      continue;
    }
    // A file (/favicon.ico) is checked for existence; a page by its route.
    if (path.extname(target) && !target.endsWith("/")) {
      if (!fs.existsSync(path.join(OUT, target))) problems.push(`${route}: ${href} — no such file`);
      continue;
    }
    if (!target.endsWith("/")) target += "/";
    const page = pages.get(target);
    if (!page) {
      problems.push(`${route}: ${href} — no page ${target}`);
      continue;
    }
    if (anchor && !page.ids.has(anchor)) {
      problems.push(`${route}: ${href} — ${target} has no #${anchor}`);
    }
  }
}

const docs = [...pages.keys()].filter((r) => r.startsWith("/docs/"));
if (problems.length) {
  console.error(`check-docs: ${problems.length} broken internal link(s):`);
  for (const p of problems) console.error(`  ${p}`);
  process.exit(1);
}
console.log(`check-docs: ${checked} internal links across ${pages.size} pages (${docs.length} under /docs) all resolve`);
