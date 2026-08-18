"use client";

import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import type { AgentWorkload } from "@/lib/routing-history";
import { cn } from "@/lib/utils";

/**
 * Who was asked for, and who did the work.
 *
 * Two bars per agent rather than one, because the interesting number is the
 * *gap*. An agent asked for ten times that ran twice is a provider worth
 * looking at; an agent that ran eight times having been asked for none is
 * carrying work under a login nobody chose deliberately — which matters here in
 * a way it would not on a hosted gateway, since each agent has its own
 * credential, its own subscription and its own bill.
 *
 * Drawn as plain divs rather than a chart: it is two numbers per row, and a
 * charting library would add a legend, an axis and a tooltip layer to say what
 * a bar of width n/max already says.
 */
export function Workload({ rows }: { rows: AgentWorkload[] }) {
  if (rows.length === 0) return null;
  const max = Math.max(1, ...rows.map((r) => Math.max(r.asked, r.ran)));

  return (
    <div className="space-y-2">
      {rows.map((r) => (
        <div key={r.agent} className="flex items-center gap-3 text-xs">
          <span className="w-20 shrink-0 font-mono">{r.agent}</span>
          <div className="min-w-0 flex-1 space-y-1">
            <Bar
              label="asked for"
              value={r.asked}
              max={max}
              className="bg-muted-foreground/40"
              hint={`${r.agent} was the agent requested in ${r.asked} routing ${r.asked === 1 ? "episode" : "episodes"}.`}
            />
            <Bar
              label="ran"
              value={r.ran}
              max={max}
              className={cn(r.ran > r.asked ? "bg-status-serious" : "bg-status-good")}
              hint={
                r.ran > r.asked
                  ? `${r.agent} finished more episodes than it was asked for — it is picking up work routed away from something else, under its own login and its own bill.`
                  : `${r.agent} finished ${r.ran} of the episodes it was asked for.`
              }
            />
          </div>
        </div>
      ))}
      <p className="text-[11px] text-muted-foreground">
        Counted per episode, not per run: a chain that fired twice is one episode with one
        agent asked and one that finished it.
      </p>
    </div>
  );
}

function Bar({
  label,
  value,
  max,
  className,
  hint,
}: {
  label: string;
  value: number;
  max: number;
  className: string;
  hint: string;
}) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <div className="flex items-center gap-2">
          <span className="w-14 shrink-0 text-right text-[10px] text-muted-foreground">
            {label}
          </span>
          <div className="h-2 min-w-0 flex-1 overflow-hidden rounded-sm bg-muted">
            <div
              className={cn("h-full rounded-sm", className)}
              // A zero-width bar and an absent one look the same, so zero keeps a
              // hairline: "none" is a reading, not a missing row — and the row it
              // matters most on is the one this panel exists for, an agent that
              // ran work it was never asked for.
              style={{ width: value === 0 ? "2px" : `${Math.max(4, (value / max) * 100)}%` }}
            />
          </div>
          <span className="w-6 shrink-0 tabular-nums text-[10px] text-muted-foreground">
            {value}
          </span>
        </div>
      </TooltipTrigger>
      <TooltipContent className="max-w-xs">{hint}</TooltipContent>
    </Tooltip>
  );
}
