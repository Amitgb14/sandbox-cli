import { LOCALE } from "@/lib/format";
import {
  runOutcome,
  VERIFY_FAILED_EXIT,
  type AuditRecord,
  type Run,
  type RunOutcome,
  type Worktree,
} from "@/lib/types";

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

// ---------------------------------------------------------------------------
// The durable history
// ---------------------------------------------------------------------------

/**
 * The same derivations, over the audit log instead of over containers.
 *
 * They exist because the two records answer different questions. A container is
 * the state store — it carries a run's logs, exit code and labels until it is
 * reaped, and `fleet clean` reaps it. The audit log is written when a run *ends*
 * and never removed, so it is the only thing that can answer "how have runs gone
 * here" after a tidy-up.
 *
 * The dashboard reads containers for what is in flight, and these for
 * everything historical. Computing a fourteen-day chart from containers gave a
 * chart of whatever had not been cleaned up yet.
 */

/**
 * How a recorded run ended.
 *
 * An audit line carries no verify command, so exit 90 is read as a failed verify
 * on the strength of the code alone. That is sound rather than convenient:
 * `VerifyFailedExit` was chosen to sit above the usual application range and
 * below the shell's reserved 126/127/128+n precisely so it could not be confused
 * with something an agent produced.
 */
export function auditOutcome(r: AuditRecord): RunOutcome {
  if (r.exitCode === 0) return "passed";
  if (r.exitCode === VERIFY_FAILED_EXIT) return "verify-failed";
  // 137 is SIGKILL, 143 SIGTERM: somebody stopped this, it did not decide.
  if (r.exitCode === 137 || r.exitCode === 143) return "stopped";
  return "failed";
}

/** Recorded runs bucketed by local day, oldest first, every day present. */
export function bucketAuditByDay(
  records: AuditRecord[],
  days = 14,
  now = Date.now(),
): DayBucket[] {
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

  for (const r of records) {
    const t = new Date(r.time);
    if (!Number.isFinite(t.getTime())) continue;
    const bucket = buckets.get(dayKey(t));
    if (!bucket) continue; // outside the window
    bucket.total++;
    switch (auditOutcome(r)) {
      case "passed":
        bucket.passed++;
        break;
      case "verify-failed":
        bucket.verifyFailed++;
        break;
      case "stopped":
        bucket.stopped++;
        break;
      default:
        bucket.failed++;
    }
  }
  return [...buckets.values()];
}

/** What the audit log can say about outcomes, over its whole window. */
export interface HistoryStats {
  finishedToday: number;
  /** `null` when nothing has been decided — 0% and "nothing yet" differ. */
  passRate: number | null;
  decided: number;
  medianDurationMs: number | null;
  total: number;
}

export function historyStats(records: AuditRecord[], now = Date.now()): HistoryStats {
  const todayStart = new Date(now);
  todayStart.setHours(0, 0, 0, 0);

  const decidedOutcomes: RunOutcome[] = ["passed", "failed", "verify-failed"];
  const decided = records.filter((r) => decidedOutcomes.includes(auditOutcome(r)));
  const passed = decided.filter((r) => auditOutcome(r) === "passed").length;

  const durations = records
    .map((r) => r.durationMs)
    .filter((d): d is number => typeof d === "number" && d > 0)
    .sort((a, b) => a - b);

  return {
    total: records.length,
    finishedToday: records.filter(
      (r) => new Date(r.time).getTime() >= todayStart.getTime(),
    ).length,
    decided: decided.length,
    // Percent units, not a fraction — the same as runStats, because both feed
    // formatPercent, which appends a sign and does not convert. Returning 0.93
    // here rendered a 93% pass rate as "1%".
    passRate: decided.length === 0 ? null : (passed / decided.length) * 100,
    medianDurationMs: durations.length === 0 ? null : durations[Math.floor(durations.length / 2)],
  };
}

/** Recorded runs per agent, busiest first. */
export function byAgentAudit(records: AuditRecord[]): AgentActivity[] {
  const map = new Map<string, AgentActivity>();
  for (const r of records) {
    const key = r.agent ?? "plain run";
    const entry = map.get(key) ?? { agent: key, runs: 0, passed: 0, failed: 0 };
    entry.runs++;
    const o = auditOutcome(r);
    if (o === "passed") entry.passed++;
    if (o === "failed" || o === "verify-failed") entry.failed++;
    map.set(key, entry);
  }
  return [...map.values()].sort((a, b) => b.runs - a.runs);
}
