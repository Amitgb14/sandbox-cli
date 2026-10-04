// Builds Studio with NEXT_PUBLIC_STUDIO_ADMIN=off and fails if anything of the
// admin screens is in it: their routes, the admin API paths, their titles.
// A hosted dashboard relies on this build having no operator UI at all, not
// on hiding it, so the check reads the built files rather than the source.
//
// It first checks the default build (out/, from `npm run build`) does have
// those strings, so a probe that matches nothing cannot pass by accident.
//
//   npm run check:admin-off        (part of npm run check)

import { spawnSync } from "node:child_process";
import { existsSync, readdirSync, readFileSync, rmSync, statSync } from "node:fs";
import { join, resolve } from "node:path";

const root = resolve(import.meta.dirname, "..");
const dist = join(root, ".next-admin-off");
// Strings only the admin screens carry.
const PROBES = ["/v1/admin/", "page.admin", "Users & keys", "Lost sandboxes", "Drain and terminate"];

function* files(dir) {
  for (const name of readdirSync(dir)) {
    const p = join(dir, name);
    if (name === "cache") continue; // the compiler's, not served
    if (statSync(p).isDirectory()) yield* files(p);
    else if (/\.(html|js|txt|json)$/.test(name)) yield p;
  }
}

function found(dir) {
  const hits = new Map();
  for (const f of files(dir)) {
    const text = readFileSync(f, "utf8");
    for (const p of PROBES) if (text.includes(p)) hits.set(p, f);
  }
  return hits;
}

const out = join(root, "out");
if (!existsSync(out)) {
  console.error("check-admin-off: no out/; run npm run build first");
  process.exit(1);
}
const inDefault = found(out);
const missing = PROBES.filter((p) => !inDefault.has(p));
if (missing.length) {
  console.error(`check-admin-off: the default build lacks ${missing.join(", ")}; the probes no longer test anything`);
  process.exit(1);
}

rmSync(dist, { recursive: true, force: true });
const r = spawnSync("npx", ["next", "build"], {
  cwd: root,
  stdio: ["ignore", "ignore", "inherit"],
  env: { ...process.env, NEXT_PUBLIC_STUDIO_ADMIN: "off", STUDIO_DIST_DIR: ".next-admin-off" },
});
if (r.status !== 0) {
  console.error("check-admin-off: the admin-off build failed");
  process.exit(1);
}
const hits = found(dist);
rmSync(dist, { recursive: true, force: true });
if (hits.size) {
  for (const [p, f] of hits) console.error(`check-admin-off: "${p}" is in ${f.slice(root.length + 1)}`);
  process.exit(1);
}
console.log(`check-admin-off: a NEXT_PUBLIC_STUDIO_ADMIN=off build has none of ${PROBES.length} admin strings`);
