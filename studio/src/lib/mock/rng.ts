/**
 * A seeded PRNG, so the fixtures are the same on every reload.
 *
 * Fixtures that move on their own are worse than no fixtures: a chart whose
 * shape changes each refresh makes it impossible to tell a rendering bug from
 * new data. Only `NOW` is allowed to vary, and it is read once at module load.
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
 * The clock the fixtures are anchored to, read once. Everything derived from it
 * is rendered client-side only (the pages fetch through TanStack Query, so the
 * first paint is a skeleton) — a timestamp formatted on the server and again in
 * the browser is a hydration mismatch waiting to happen.
 */
export const NOW = Date.now();

export function ago(ms: number): string {
  return new Date(NOW - ms).toISOString();
}

export function ahead(ms: number): string {
  return new Date(NOW + ms).toISOString();
}

export const MINUTE = 60_000;
export const HOUR = 60 * MINUTE;
export const DAY = 24 * HOUR;
