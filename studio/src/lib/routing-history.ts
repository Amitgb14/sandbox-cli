import { LOCALE } from "@/lib/format";
import type { AuditRecord } from "@/lib/types";

/**
 * Reading routing out of the run log.
 *
 * No new collection: every run already records which agent was asked for, which
 * one ran, why, and the episode id that ties a chain's attempts together. This
 * is the derivation over records that exist — which is also why it can answer
 * for history written before anyone looked at this screen.
 */

/** One routing episode: the attempts of a single chain, in order. */
export interface RouteEpisode {
  id: string;
  /** Newest-first ordering key — when the episode's first attempt started. */
  at: string;
  attempts: AuditRecord[];
  /** The agent that ended up running, i.e. the last attempt. */
  finalAgent: string | null;
  /** True when the last attempt exited 0 — routing got the work done. */
  rescued: boolean;
  /**
   * True when the chain fired at all. A single-attempt episode means the
   * primary was reachable and worked; it is recorded, but it is not a switch.
   */
  switched: boolean;
}

export interface RouteStats {
  /** Episodes where the chain actually moved to another agent. */
  switched: number;
  /** Of those, how many ended with a run that exited 0. */
  rescued: number;
  /** Of those, how many still failed — routing cost a container and gained nothing. */
  wasted: number;
  /** Why the switches happened, commonest first. */
  reasons: Array<{ reason: string; count: number }>;
  /** Which agents were routed away from, commonest first. */
  from: Array<{ agent: string; count: number }>;
}

/**
 * Group audit records into episodes.
 *
 * Records with no `routeId` are ignored rather than treated as one-attempt
 * episodes: an ordinary run is not a routing event, and counting every run as a
 * successful one would make the rescue rate meaningless.
 */
export function episodesFrom(records: AuditRecord[]): RouteEpisode[] {
  const byId = new Map<string, AuditRecord[]>();
  for (const r of records) {
    if (!r.routeId) continue;
    byId.set(r.routeId, [...(byId.get(r.routeId) ?? []), r]);
  }

  const out: RouteEpisode[] = [];
  for (const [id, group] of byId) {
    // By attempt number rather than by timestamp: the log's timestamps are
    // second-resolution, and two attempts of one episode can land in the same
    // second when the first fails immediately — which is exactly the outage case.
    const attempts = [...group].sort(
      (a, b) => (a.routeAttempt ?? 0) - (b.routeAttempt ?? 0),
    );
    const last = attempts[attempts.length - 1];
    out.push({
      id,
      at: attempts[0].time,
      attempts,
      finalAgent: last.agent,
      rescued: last.exitCode === 0,
      switched: attempts.length > 1 || !!last.routedFrom,
    });
  }
  return out.sort((a, b) => b.at.localeCompare(a.at));
}

export function routeStats(episodes: RouteEpisode[]): RouteStats {
  const switched = episodes.filter((e) => e.switched);
  const reasons = new Map<string, number>();
  const from = new Map<string, number>();
  for (const e of switched) {
    for (const a of e.attempts) {
      if (a.routeReason) reasons.set(a.routeReason, (reasons.get(a.routeReason) ?? 0) + 1);
      if (a.routedFrom) from.set(a.routedFrom, (from.get(a.routedFrom) ?? 0) + 1);
    }
  }
  const rank = <T extends string>(m: Map<T, number>) =>
    [...m.entries()].sort((a, b) => b[1] - a[1]);

  return {
    switched: switched.length,
    rescued: switched.filter((e) => e.rescued).length,
    // Not "failed": the chain did its job and the work still did not land, which
    // is a different thing from routing being broken and is worth its own word.
    wasted: switched.filter((e) => !e.rescued).length,
    reasons: rank(reasons).map(([reason, count]) => ({ reason, count })),
    from: rank(from).map(([agent, count]) => ({ agent, count })),
  };
}

/** One hop between two agents: configured, observed, or both. */
export interface ChainEdge {
  from: string;
  to: string;
  /** A chain currently says this hop should happen. */
  configured: boolean;
  /** How many episodes actually took it. */
  fired: number;
  /** Of those, how many ended with work that finished. */
  rescued: number;
}

/**
 * The hops between agents: what is configured, and what has actually happened.
 *
 * Both, deliberately, and the difference is the interesting part. A configured
 * hop that has never fired is untested; a hop that fired but is configured
 * nowhere is history from before somebody changed the chain, and hiding it would
 * make the picture agree with the settings rather than with the record.
 *
 * A hop is read off `routedFrom`, which every routed run carries, rather than by
 * pairing consecutive attempts: a preflight skip is a single attempt with a
 * routedFrom and no predecessor to pair it with, and it is a hop like any other.
 */
export function chainEdges(
  chains: Record<string, string[]>,
  episodes: RouteEpisode[],
): ChainEdge[] {
  const edges = new Map<string, ChainEdge>();
  const edge = (from: string, to: string) => {
    const k = `${from} ${to}`;
    let e = edges.get(k);
    if (!e) {
      e = { from, to, configured: false, fired: 0, rescued: 0 };
      edges.set(k, e);
    }
    return e;
  };

  for (const [from, chain] of Object.entries(chains)) {
    // Only the first link: a chain of [codex, gemini] means claude falls back to
    // codex, and codex to gemini only if codex's own chain says so. Drawing the
    // second link as claude's would put an arrow on the graph that no run
    // launched from here can take.
    if (chain[0]) edge(from, chain[0]).configured = true;
  }

  for (const e of episodes) {
    for (const a of e.attempts) {
      if (!a.routedFrom || !a.agent || a.routedFrom === a.agent) continue;
      const hop = edge(a.routedFrom, a.agent);
      hop.fired++;
      // Credited to the hop that ended the episode, since that is the one whose
      // agent did the work: an intermediate hop in a three-agent chain rescued
      // nothing by itself.
      if (e.rescued && a.agent === e.finalAgent) hop.rescued++;
    }
  }
  return [...edges.values()].sort((a, b) => b.fired - a.fired);
}

/** Failovers on one local day. */
export interface FailoverDay {
  date: string;
  label: string;
  rescued: number;
  failed: number;
}

/**
 * Episodes bucketed by day, oldest first.
 *
 * Every day in the window is present, including the empty ones: a chart that
 * skips them draws a bad week and a quiet one at the same width, which is the
 * one distinction a volume chart exists to make. Only episodes that *switched*
 * are counted, because a chain that never fired is not a data point about
 * routing.
 */
export function failoverDays(
  episodes: RouteEpisode[],
  days = 14,
  now = Date.now(),
): FailoverDay[] {
  const dayMs = 86_400_000;
  const key = (d: Date) =>
    `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(
      d.getDate(),
    ).padStart(2, "0")}`;

  const buckets = new Map<string, FailoverDay>();
  const start = new Date(now - (days - 1) * dayMs);
  start.setHours(0, 0, 0, 0);
  for (let i = 0; i < days; i++) {
    const d = new Date(start.getTime() + i * dayMs);
    buckets.set(key(d), {
      date: key(d),
      label: d.toLocaleDateString(LOCALE, { month: "short", day: "numeric" }),
      rescued: 0,
      failed: 0,
    });
  }

  for (const e of episodes) {
    if (!e.switched) continue;
    const bucket = buckets.get(key(new Date(e.at)));
    if (!bucket) continue;
    if (e.rescued) bucket.rescued++;
    else bucket.failed++;
  }
  return [...buckets.values()];
}
