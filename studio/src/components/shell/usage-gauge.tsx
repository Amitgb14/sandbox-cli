"use client";

import { RefreshCw } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { Skeleton } from "@/components/ui/skeleton";
import { useRefreshUsage, useUsage } from "@/lib/api/queries";
import { formatDurationTight, formatRelative } from "@/lib/format";
import { cn } from "@/lib/utils";
import type { UsageWindow } from "@/lib/types";

/**
 * The subscription windows, in the sidebar footer.
 *
 * Two rules from the CLI travel with this number and both are visible here:
 *
 *   - **Absent means absent.** No windows, no percentage, a window past its
 *     reset — each renders nothing or a dash, never a placeholder zero. A
 *     cached figure for a window that has already reset measures the period
 *     *before* the reset, so showing it as the current one would be a lie.
 *   - **Always aged.** These refresh only when the agent talks to the server, so
 *     an unlabelled percentage can be hours stale. The age is printed next to it
 *     and `--refresh` is the only way to make it current.
 */
export function UsageGauge() {
  const { data, isPending } = useUsage();
  const refresh = useRefreshUsage();

  if (isPending) {
    return (
      <div className="space-y-2 px-2 py-1 group-data-[collapsible=icon]:hidden">
        <Skeleton className="h-3 w-full" />
        <Skeleton className="h-3 w-2/3" />
      </div>
    );
  }

  // A shape the parser no longer recognises yields no windows rather than a
  // zero — so there is nothing honest to draw.
  if (!data || data.windows.length === 0) return null;

  const now = Date.now();
  const shown = data.windows.filter((w) => showable(w, now));
  if (shown.length === 0) return null;

  const ageMs = data.fetchedAt ? now - new Date(data.fetchedAt).getTime() : null;

  return (
    <div className="space-y-2 rounded-md border bg-card/50 p-2.5 group-data-[collapsible=icon]:hidden">
      <div className="flex items-center justify-between">
        <span className="text-[11px] font-medium tracking-wide text-muted-foreground uppercase">
          {data.agent} usage
        </span>
        <Tooltip>
          <TooltipTrigger asChild>
            <Button
              variant="ghost"
              size="icon"
              className="size-5 text-muted-foreground"
              onClick={() => refresh.mutate()}
              disabled={refresh.isPending}
              aria-label="Refresh usage"
            >
              <RefreshCw className={cn("size-3", refresh.isPending && "animate-spin")} />
            </Button>
          </TooltipTrigger>
          <TooltipContent className="max-w-xs">
            Runs one throwaway turn to make the reading current — the request is spent from the
            window being measured, which is why it is opt-in.
          </TooltipContent>
        </Tooltip>
      </div>

      {shown.map((w, i) => (
        <WindowMeter key={`${w.kind}-${w.scope ?? "account"}-${i}`} window={w} />
      ))}

      <p className="text-[10px] leading-tight text-muted-foreground">
        {ageMs === null
          ? "Age unknown — the agent did not record when it last refreshed."
          : `Read ${formatDurationTight(ageMs)} ago, from the agent's own cache.`}
      </p>
    </div>
  );
}

/** A window with no percentage, or one past its reset, has nothing to show. */
function showable(w: UsageWindow, now: number): boolean {
  if (w.utilization === null) return false;
  if (w.resetsAt && new Date(w.resetsAt).getTime() < now) return false;
  return true;
}

function WindowMeter({ window: w }: { window: UsageWindow }) {
  const pct = w.utilization ?? 0;
  // One hue, not a red/amber/green traffic light: this is a magnitude, and
  // recolouring it at thresholds would imply a judgement the number does not
  // carry. High usage gets a warning tint only once it is genuinely near the cap.
  const tone = pct >= 90 ? "bg-status-critical" : pct >= 75 ? "bg-status-warning" : "bg-chart-1";
  return (
    <div className="space-y-1">
      <div className="flex items-baseline justify-between gap-2 text-[11px]">
        <span className="text-muted-foreground">
          {w.label}
          {w.scope && <span className="ml-1 font-mono opacity-70">{w.scope}</span>}
        </span>
        <span className="font-medium tabular-nums">{pct}%</span>
      </div>
      <div className="h-1 overflow-hidden rounded-full bg-muted">
        <div
          className={cn("h-full rounded-full transition-[width]", tone)}
          style={{ width: `${Math.min(100, Math.max(2, pct))}%` }}
        />
      </div>
      {w.resetsAt && (
        <p className="text-[10px] text-muted-foreground">resets {formatRelative(w.resetsAt)}</p>
      )}
    </div>
  );
}
