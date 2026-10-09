// Checks the hosted build (out-hosted/, from `npm run build:hosted`), which
// `sandbox-cli studio host` serves to many users. It must carry none of the
// operator's admin screens, none of local Studio's token handling — a
// hosted page has no Studio token to read, store or ask for — and none of the
// editors of the host user's own settings; and it must
// carry the invite-link sign-in. The check reads the built files, not the
// source, because what matters is what is served.
//
// It first checks the default build (out/, from `npm run build`) does have
// the local strings and lacks the hosted ones, so a probe that matches
// nothing cannot pass by accident.
//
//   npm run check:hosted        (part of npm run check)

import { existsSync, readdirSync, readFileSync, statSync } from "node:fs";
import { join, resolve } from "node:path";

const root = resolve(import.meta.dirname, "..");
// Strings only the admin screens carry (the same as check-admin-off.mjs).
const ADMIN = ["/v1/admin/", "/v1/admin/orgs", "/admin/orgs", "page.admin", "Users & keys", "Lost sandboxes", "Drain and terminate", "All organizations"];
// Strings only local Studio carries: its token handling, and the editors of
// this machine's user's settings (agents' saved keys, templates, egress rules).
const LOCAL = [
  "sandbox-studio-token",
  "Studio needs its token",
  "#token=",
  "This agent reads no API key from the environment.",
  "New template",
  "Deny rules saved",
  "Create group",
];
// Strings only the hosted sign-in carries.
const HOSTED = ["Your session has ended", "This invite link did not sign you in", "Sign out"];

function* files(dir) {
  for (const name of readdirSync(dir)) {
    const p = join(dir, name);
    if (name === "cache") continue; // the compiler's, not served
    if (statSync(p).isDirectory()) yield* files(p);
    else if (/\.(html|js|txt|json)$/.test(name)) yield p;
  }
}

function found(dir, probes) {
  const hits = new Map();
  for (const f of files(dir)) {
    const text = readFileSync(f, "utf8");
    for (const p of probes) if (text.includes(p)) hits.set(p, f);
  }
  return hits;
}

function fail(msg) {
  console.error(`check-hosted: ${msg}`);
  process.exitCode = 1;
}

const out = join(root, "out");
const hosted = join(root, "out-hosted");
for (const [dir, how] of [[out, "npm run build"], [hosted, "npm run build:hosted"]]) {
  if (!existsSync(dir)) {
    console.error(`check-hosted: no ${dir.slice(root.length + 1)}/; run ${how} first`);
    process.exit(1);
  }
}

const inDefault = found(out, [...ADMIN, ...LOCAL, ...HOSTED]);
for (const p of [...ADMIN, ...LOCAL]) if (!inDefault.has(p)) fail(`the default build lacks "${p}"; the probe no longer tests anything`);
for (const p of HOSTED) if (inDefault.has(p)) fail(`the default build carries "${p}", a hosted-only string`);

const inHosted = found(hosted, [...ADMIN, ...LOCAL, ...HOSTED]);
for (const p of [...ADMIN, ...LOCAL]) if (inHosted.has(p)) fail(`"${p}" is in ${inHosted.get(p).slice(root.length + 1)}`);
for (const p of HOSTED) if (!inHosted.has(p)) fail(`the hosted build lacks "${p}"`);

if (!process.exitCode) {
  console.log(`check-hosted: the hosted build has none of ${ADMIN.length + LOCAL.length} admin, token and settings strings, and its ${HOSTED.length} sign-in strings`);
}
