/**
 * A seeded PRNG, so the fixtures are the same on every reload.
 *
 * Fixtures that move on their own are worse than no fixtures: a chart whose
 * shape changes each refresh makes it impossible to tell a rendering bug from
 * new data. Only `NOW` is allowed to vary — it is read once at module load and
 * rounded to the hour, so the *shape* of everything is identical between reloads
 * while the ages stay honest. See its own comment for why neither a fixed epoch
 * nor a wall-clock read is right.
 */
export function makeRng(seed: number) {
  let s = seed >>> 0;
  return function next(): number {
    // mulberry32
    s = (s + 0x6d2b79f5) >>> 0;
    let t = s;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

export interface Rng {
  (): number;
  int(min: number, max: number): number;
  pick<T>(items: readonly T[]): T;
  chance(p: number): boolean;
  float(min: number, max: number): number;
}

export function rngFor(seed: number): Rng {
  const next = makeRng(seed);
  const r = (() => next()) as Rng;
  r.int = (min, max) => Math.floor(next() * (max - min + 1)) + min;
  r.pick = (items) => items[Math.floor(next() * items.length)];
  r.chance = (p) => next() < p;
  r.float = (min, max) => next() * (max - min) + min;
  return r;
}

/**
 * The clock the fixtures are anchored to: the top of the current hour.
 *
 * Two failures to avoid at once, and the first one is why this is not simply
 * `Date.now()`.
 *
 * **Not wall-clock.** Read once at module load means once on the server and
 * again in the browser — two different values, so every timestamp derived from
 * it differs between the server-rendered HTML and the client's first render.
 * That is a hydration mismatch by construction, and it does not depend on the
 * pages fetching through TanStack Query: a "use client" component is still
 * rendered on the server in the App Router.
 *
 * **And not a fixed epoch either**, which is what this was and what the mismatch
 * was traded for. A frozen anchor does not stay a neutral choice: it *rots*. Six
 * fixture runs stamped minutes before 2026-01-01 and marked `running` read, eight
 * months later, as agents that had been running for 226 days — and the fixtures
 * are plausible enough to be believed, because they were authored from a real
 * repository with its real repo id. That cost two separate debugging sessions,
 * both of which concluded "these are fixtures" only after checking docker. The
 * fourteen-day charts had the same problem from the other side: every record sat
 * outside the window, so the dashboard's history was empty on a machine with
 * hundreds of real runs behind it.
 *
 * Rounding to the hour gets both. Server and client compute the same number for
 * an hour at a time, so hydration is stable unless a single page load straddles
 * the boundary — and even then it would only matter if fixture timestamps
 * reached server-rendered markup, which they do not: nothing imports the fixtures
 * outside the transport layer, and that resolves in the browser. Ages stay
 * believable ("41m ago" is 41 minutes ago, ±the hour), day buckets land on real
 * days, and the seeded RNG still makes every other detail identical between two
 * reloads.
 */
const HOUR_MS = 3_600_000;
export const NOW = Math.floor(Date.now() / HOUR_MS) * HOUR_MS;

export function ago(ms: number): string {
  return new Date(NOW - ms).toISOString();
}

export function ahead(ms: number): string {
  return new Date(NOW + ms).toISOString();
}

export const MINUTE = 60_000;
export const HOUR = 60 * MINUTE;
export const DAY = 24 * HOUR;
