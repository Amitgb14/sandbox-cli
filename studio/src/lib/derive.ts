import { LOCALE } from "@/lib/format";
import { runOutcome, type Run, type RunOutcome, type Worktree } from "@/lib/types";

/**
 * Aggregations, kept out of the components.
 *
 * Every number the dashboard shows is derived here, so the tiles and the charts
 * cannot disagree about what "today" or "passed" means. `runOutcome` is the one
 * classifier; nothing in this file re-implements it.
 */

const DAY_MS = 86_400_000;

export interface DayBucket {
  /** ISO date, `YYYY-MM-DD`. */
  date: string;
  /** Short label for the axis: `Jul 22`. */
  label: string;
  total: number;
  passed: number;
  failed: number;
  verifyFailed: number;
  stopped: number;
}

/**
 * Runs bucketed by local day, oldest first, with **every** day in the window
 * present. A chart that skips empty days draws a busy week and a quiet one the
 * same width, which is the one thing a volume chart exists to distinguish.
 */
export function bucketByDay(runs: Run[], days = 14, now = Date.now()): DayBucket[] {
  const buckets = new Map<string, DayBucket>();
  const start = new Date(now - (days - 1) * DAY_MS);
  start.setHours(0, 0, 0, 0);

  for (let i = 0; i < days; i++) {
    const d = new Date(start.getTime() + i * DAY_MS);
    const key = dayKey(d);
    buckets.set(key, {
      date: key,
      label: d.toLocaleDateString(LOCALE, { month: "short", day: "numeric" }),
      total: 0,
      passed: 0,
      failed: 0,
      verifyFailed: 0,
      stopped: 0,
    });
  }

  for (const run of runs) {
    const at = new Date(run.startedAt ?? run.createdAt);
    const bucket = buckets.get(dayKey(at));
    if (!bucket) continue;
    bucket.total++;
    switch (runOutcome(run)) {
      case "passed":
        bucket.passed++;
        break;
      case "failed":
        bucket.failed++;
        break;
      case "verify-failed":
        bucket.verifyFailed++;
        break;
      case "stopped":
        bucket.stopped++;
        break;
      default:
        break;
    }
  }

  return [...buckets.values()];
}

function dayKey(d: Date): string {
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(
    d.getDate(),
  ).padStart(2, "0")}`;
}

export interface RunStats {
  live: number;
  liveFleet: number;
  finishedToday: number;
  /**
   * Share of *decided* runs that passed, over the window. `null` when nothing
   * finished — a pass rate of 0% and "nothing to rate yet" are different facts.
   */
  passRate: number | null;
  decided: number;
  medianDurationMs: number | null;
  /** Memory currently committed across live containers, and their limits. */
  memInFlightBytes: number;
  memLimitBytes: number;
  cpuInFlightPct: number;
  /** Live runs whose verify has not yet been decided. */
  awaitingVerify: number;
}

export function runStats(runs: Run[], now = Date.now()): RunStats {
  const live = runs.filter((r) => r.state === "running");
  const todayStart = new Date(now);
  todayStart.setHours(0, 0, 0, 0);

  const finished = runs.filter((r) => r.state === "exited");
  const finishedToday = finished.filter(
    (r) => new Date(r.finishedAt ?? r.createdAt).getTime() >= todayStart.getTime(),
  ).length;

  const decidedOutcomes: RunOutcome[] = ["passed", "failed", "verify-failed"];
  const decided = finished.filter((r) => decidedOutcomes.includes(runOutcome(r)));
  const passed = decided.filter((r) => runOutcome(r) === "passed").length;

  const durations = finished
    .map((r) => r.durationMs)
    .filter((d): d is number => d !== null)
    .sort((a, b) => a - b);

  return {
    live: live.length,
    liveFleet: live.filter((r) => r.kind === "fleet").length,
    finishedToday,
    passRate: decided.length === 0 ? null : (passed / decided.length) * 100,
    decided: decided.length,
    medianDurationMs: durations.length ? durations[Math.floor(durations.length / 2)] : null,
    memInFlightBytes: live.reduce((sum, r) => sum + (r.latestMetrics?.memBytes ?? 0), 0),
    memLimitBytes: live.reduce((sum, r) => sum + (r.latestMetrics?.memLimitBytes ?? 0), 0),
    cpuInFlightPct: live.reduce((sum, r) => sum + (r.latestMetrics?.cpuPct ?? 0), 0),
    awaitingVerify: live.filter((r) => r.verify).length,
  };
}

export interface AgentActivity {
  agent: string;
  runs: number;
  passed: number;
  failed: number;
}

/** Runs per agent over the whole window, busiest first. */
export function byAgent(runs: Run[]): AgentActivity[] {
  const map = new Map<string, AgentActivity>();
  for (const run of runs) {
    const key = run.agent ?? "plain run";
    const entry = map.get(key) ?? { agent: key, runs: 0, passed: 0, failed: 0 };
    entry.runs++;
    const o = runOutcome(run);
    if (o === "passed") entry.passed++;
    if (o === "failed" || o === "verify-failed") entry.failed++;
    map.set(key, entry);
  }
  return [...map.values()].sort((a, b) => b.runs - a.runs);
}

/** The egress posture across a set of runs, for the dashboard's one-line summary. */
export function egressSummary(runs: Run[]) {
  const byMode = { allowlist: 0, none: 0, default: 0 };
  let byName = 0;
  let byAddress = 0;
  const domains = new Set<string>();
  for (const run of runs) {
    byMode[run.network.mode]++;
    if (run.network.enforcement === "name") byName++;
    if (run.network.enforcement === "address") byAddress++;
    run.network.allow.forEach((d) => domains.add(d));
  }
  return { byMode, byName, byAddress, distinctDomains: domains.size };
}

/** Branches that have work and no live agent — the `land` queue. */
export function landQueue(worktrees: Worktree[]): Worktree[] {
  return worktrees
    .filter((w) => !w.primary && !w.runId && w.ahead > 0)
    .sort((a, b) => Number(b.verified) - Number(a.verified) || b.ahead - a.ahead);
}

export function scopeToRepo<T extends { repoId: string }>(items: T[], repoId: string | null): T[] {
  return repoId ? items.filter((i) => i.repoId === repoId) : items;
}
