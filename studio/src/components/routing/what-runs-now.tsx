"use client";

import { ArrowRight, CircleSlash, SkipForward } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import type { ChainOutcome } from "@/lib/routing-history";
import { cn } from "@/lib/utils";

/**
 * If you pressed Launch now, what would run.
 *
 * Everything else on this screen is history or configuration. This is the
 * present tense, and it is the question the page is actually opened with — which
 * until now had to be answered by reading two panels and doing the join in your
 * head: a provider list saying claude is not answering, and a chain list saying
 * claude falls back to codex.
 *
 * The whole row is the answer, including the part that is boring: an agent whose
 * provider is up shows itself, unchanged, because "nothing would happen
 * differently" is the reading people need most of the time and a screen that
 * only rendered the exceptions would leave them inferring it from absence.
 */
export function WhatRunsNow({ outcomes }: { outcomes: ChainOutcome[] }) {
  if (outcomes.length === 0) return null;

  return (
    <ul className="divide-y rounded-md border">
      {outcomes.map((o) => (
        <li key={o.primary} className="flex flex-wrap items-center gap-2 p-2.5 text-xs">
          <span className="w-20 shrink-0 font-mono text-muted-foreground">{o.primary}</span>

          {o.skipped.map((s) => (
            <span key={s.agent} className="flex items-center gap-1.5">
              <Tooltip>
                <TooltipTrigger asChild>
                  <span className="flex items-center gap-1 rounded bg-muted px-1.5 py-0.5 font-mono text-muted-foreground line-through">
                    <SkipForward className="size-3" />
                    {s.agent}
                  </span>
                </TooltipTrigger>
                <TooltipContent className="max-w-xs">
                  {s.agent} would be skipped before a container exists — {s.reason}. Nothing is
                  half-done and no turn is spent, which is the difference between skipping and
                  failing over.
                </TooltipContent>
              </Tooltip>
              <ArrowRight className="size-3 text-muted-foreground" />
            </span>
          ))}

          {o.running ? (
            <span
              className={cn(
                "rounded px-1.5 py-0.5 font-mono",
                o.skipped.length > 0
                  ? "bg-status-serious/10 text-status-serious"
                  : "bg-status-good/10 text-status-good",
              )}
            >
              {o.running}
            </span>
          ) : (
            <Tooltip>
              <TooltipTrigger asChild>
                <span className="flex items-center gap-1 rounded bg-status-critical/10 px-1.5 py-0.5 font-mono text-status-critical">
                  <CircleSlash className="size-3" />
                  refused
                </span>
              </TooltipTrigger>
              <TooltipContent className="max-w-xs">
                Every agent in this chain was asked and none answered, so a launch is refused
                rather than started into an outage — the run would fail slowly, having spent a
                container, with the reason buried in its logs.
              </TooltipContent>
            </Tooltip>
          )}

          {o.unprobed && o.running && (
            <Tooltip>
              <TooltipTrigger asChild>
                <Badge variant="outline" className="text-[10px]">
                  not probed
                </Badge>
              </TooltipTrigger>
              <TooltipContent className="max-w-xs">
                Nothing was asked about {o.running}: it has no probeable host, so it is taken
                rather than skipped. Unknown is not down — skipping it would be acting on a
                measurement nobody made.
              </TooltipContent>
            </Tooltip>
          )}

          {o.skipped.length === 0 && o.running === o.primary && (
            <span className="ml-auto text-[10px] text-muted-foreground">
              nothing would change
            </span>
          )}
        </li>
      ))}
    </ul>
  );
}
