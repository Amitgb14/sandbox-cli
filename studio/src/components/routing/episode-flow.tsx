"use client";

import { ArrowRight, Check, SkipForward, X } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { formatDuration, formatRelative } from "@/lib/format";
import type { RouteEpisode } from "@/lib/routing-history";
import { cn } from "@/lib/utils";

/**
 * One failover, drawn as the sequence it was.
 *
 * The list this replaces printed `claude → codex` and an outcome badge, which
 * says what happened to the episode and nothing about what happened to each
 * agent in it. The interesting failures are inside: an agent that was skipped
 * before it ran costs nothing, and one that ran for four minutes and then failed
 * costs four minutes — the arrow between them looks identical either way.
 *
 * Skipped and failed are therefore different marks, read from the record rather
 * than inferred: an attempt with a `routedFrom` and no run of its own never
 * started, while an exit code is a run that did.
 */
export function EpisodeFlow({ episodes }: { episodes: RouteEpisode[] }) {
  return (
    <ul className="divide-y rounded-md border">
      {episodes.map((e) => {
        const hops = attemptsOf(e);
        return (
          <li key={e.id} className="space-y-1.5 p-2.5">
            <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
              <span className="font-mono text-[10px] text-muted-foreground">
                {formatRelative(e.at)}
              </span>
              {hops.map((h, i) => (
                <span key={`${h.agent}-${i}`} className="flex items-center gap-1.5">
                  {i > 0 && <ArrowRight className="size-3 text-muted-foreground" />}
                  <Tooltip>
                    <TooltipTrigger asChild>
                      <span
                        className={cn(
                          "flex items-center gap-1 rounded px-1.5 py-0.5 font-mono text-xs",
                          h.state === "ok" && "bg-status-good/10 text-status-good",
                          h.state === "failed" && "bg-status-critical/10 text-status-critical",
                          h.state === "skipped" && "bg-muted text-muted-foreground",
                        )}
                      >
                        {h.state === "ok" && <Check className="size-3" />}
                        {h.state === "failed" && <X className="size-3" />}
                        {h.state === "skipped" && <SkipForward className="size-3" />}
                        {h.agent}
                      </span>
                    </TooltipTrigger>
                    <TooltipContent className="max-w-xs">
                      {h.state === "skipped"
                        ? `${h.agent} was skipped before it started${h.reason ? ` — ${h.reason}` : ""}. No container was spent and there was no conversation to carry.`
                        : h.state === "ok"
                          ? `${h.agent} ran and finished the work.`
                          : `${h.agent} ran and exited ${h.exitCode}${h.reason ? ` — ${h.reason}` : ""}.`}
                    </TooltipContent>
                  </Tooltip>
                  {h.durationMs > 0 && (
                    <span className="text-[10px] tabular-nums text-muted-foreground">
                      {formatDuration(h.durationMs)}
                    </span>
                  )}
                </span>
              ))}
              <Tooltip>
                <TooltipTrigger asChild>
                  <Badge
                    variant="outline"
                    className={cn(
                      "ml-auto text-[10px]",
                      e.rescued === true && "text-status-good",
                      e.rescued === false && "text-status-critical",
                      e.rescued === null && "text-muted-foreground",
                    )}
                  >
                    {e.rescued === true
                      ? "rescued"
                      : e.rescued === false
                        ? "still failed"
                        : "outcome not recorded"}
                  </Badge>
                </TooltipTrigger>
                <TooltipContent className="max-w-xs">
                  {e.rescued === null
                    ? "The last attempt was detached, and a detached run's audit line is written when the container launches — so the exit code in the log is a placeholder rather than a result. `sandbox-cli list` has its fate."
                    : e.rescued
                      ? "The agent that took over finished the work."
                      : "The chain fired and the work still did not land — a container spent for nothing."}
                </TooltipContent>
              </Tooltip>
            </div>
            {/* The reason, once, below the chain rather than beside it: it is a
                sentence, and putting a sentence inline pushed the outcome badge
                off the row on any window narrower than a desktop. */}
            {hops.find((h) => h.reason) && (
              <p className="truncate text-[11px] text-muted-foreground">
                {hops.find((h) => h.reason)?.reason}
              </p>
            )}
          </li>
        );
      })}
    </ul>
  );
}

interface Hop {
  agent: string;
  state: "ok" | "failed" | "skipped";
  exitCode: number;
  durationMs: number;
  reason?: string;
}

/**
 * The agents of one episode, in order, including the ones that never ran.
 *
 * A skipped agent leaves no record of its own — it has no run to log — so it
 * exists only as the `routedFrom` of the attempt that followed it. Recovering it
 * is what makes the row show a three-agent chain that skipped the first two
 * rather than a two-agent chain that came from nowhere.
 */
function attemptsOf(e: RouteEpisode): Hop[] {
  const hops: Hop[] = [];
  for (const a of e.attempts) {
    const from = a.routedFrom;
    // Only when nothing above already accounts for it: on a supervised retry the
    // predecessor ran and is its own record, and adding it again would draw the
    // same agent twice.
    if (from && !hops.some((h) => h.agent === from)) {
      hops.push({ agent: from, state: "skipped", exitCode: 0, durationMs: 0, reason: a.routeReason });
    }
    if (!a.agent) continue;
    hops.push({
      agent: a.agent,
      state: a.exitCode === 0 ? "ok" : "failed",
      exitCode: a.exitCode,
      durationMs: a.durationMs,
      reason: hops.some((h) => h.reason === a.routeReason) ? undefined : a.routeReason,
    });
  }
  return hops;
}
