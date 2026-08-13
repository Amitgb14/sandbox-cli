"use client";

import { ChevronDown, RefreshCw } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { Skeleton } from "@/components/ui/skeleton";
import { useRefreshUsage, useUsage } from "@/lib/api/queries";
import { useUi } from "@/lib/store";
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
 *
 * A third rule arrived with #47, and it is the one the first two produced
 * between them. The agent marks exactly one window as in force
 * (`limits[].is_active`), and that window is the *first* to expire — so
 * dropping expired readings silently left the panel showing only allowances
 * that are not running, with no sign that the number you came for was missing.
 * An in-force window whose reading has expired now says so and points at the
 * refresh, and a window that is not in force is dimmed rather than given equal
 * weight. Absent still means absent; it is now visibly absent.
 */
export function UsageGauge() {
  const { data, isPending } = useUsage();
  const refresh = useRefreshUsage();
  const collapsed = useUi((s) => s.usageCollapsed);
  const setCollapsed = useUi((s) => s.setUsageCollapsed);

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

  // Note what is deliberately NOT a condition here: whether the agent is
  // installed. Being able to *read* these numbers and being able to *refresh*
  // them are different questions, and an earlier version of this panel gated
  // the whole thing on the second — which hid it entirely under
  // `docker compose --profile api`, where the daemon is a container with no
  // claude binary while the cache it serves is mounted, real and current.
  // Refusing to show a true number because this process cannot improve it is
  // the wrong trade; saying so underneath it is the right one.

  const now = Date.now();
  // One ordered list, not two concatenated. The previous version rendered the
  // readable windows and then the expired ones, which is a partition by
  // implementation detail leaking into visual priority: it put the 5-hour
  // window — the one in force, and the first to expire because it is the short
  // one — *below* an idle weekly sitting at 0%. Reading is_active to stop idle
  // allowances getting equal weight, and then giving them better placement, is
  // the same bug wearing a different hat.
  const rows = [...data.windows].sort(byPriority);

  const ageMs = data.fetchedAt ? now - new Date(data.fetchedAt).getTime() : null;

  return (
    <div className="space-y-2 rounded-md border bg-card/50 p-2.5 group-data-[collapsible=icon]:hidden">
      <div className="flex items-center justify-between">
        <button
          type="button"
          onClick={() => setCollapsed(!collapsed)}
          aria-expanded={!collapsed}
          className="flex items-center gap-1 text-[11px] font-medium tracking-wide text-muted-foreground uppercase transition-colors hover:text-foreground"
        >
          <ChevronDown
            className={cn("size-3 transition-transform", collapsed && "-rotate-90")}
          />
          {data.agent} usage
        </button>
        {/* Only where a refresh could actually happen. These numbers are readable
            on a machine that never had Claude Code installed — the cache travels
            in the sandbox-owned agent HOME, and the daemon may itself be in a
            container with no claude binary — so the control is hidden rather
            than offered and then failed. */}
        {data.canRefresh && !data.abandoned ? (
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
        ) : null}
      </div>

      {collapsed ? null : (
        <>
          {rows.map((w, i) => (
            <WindowMeter
              key={`${w.kind}-${w.scope ?? "account"}-${i}`}
              window={w}
              expired={!showable(w, now)}
              canRefresh={data.canRefresh}
            />
          ))}

          <p className="text-[10px] leading-tight text-muted-foreground">
            {ageMs === null
              ? "Age unknown — the agent did not record when it last refreshed."
              : `Read ${formatDurationTight(ageMs)} ago from ${sourceLabel(data.path)}.`}
            {data.abandoned ? (
              <>
                {" "}
                That file is still being written, but this reading is not — the agent has stopped
                recording usage there, so it cannot be made current and the refresh is not offered.
                The figure was true when it was taken.
              </>
            ) : data.canRefresh ? null : (
              <>
                {" "}
                Only Claude Code can advance it, and this server has none on its PATH — so this
                figure will not change here.
              </>
            )}
          </p>
        </>
      )}
    </div>
  );
}

/**
 * Which file the reading came from, shortened for a sidebar.
 *
 * The API returns the path it actually read, and there are two candidates — the
 * sandbox-owned agent HOME and your own home — resolved by whichever was
 * refreshed last. The old text said "the agent's own cache" whatever happened,
 * which named the wrong one every time the user's own file won.
 */
function sourceLabel(path: string | null): string {
  if (!path) return "the agent's own cache";
  return path.includes("/.config/sandbox/agents/") ? "the sandbox agent's cache" : "your own cache";
}

/**
 * Whether this window has a percentage that describes *now*.
 *
 * False for a window past its reset: the cached figure then measures the period
 * before it, so showing it as current would be a number that is wrong rather
 * than one that is missing. The caller renders those as expired rows — it no
 * longer drops them, which is the half this predicate used to be blamed for.
 */
function showable(w: UsageWindow, now: number): boolean {
  if (w.utilization === null) return false;
  if (w.resetsAt && new Date(w.resetsAt).getTime() < now) return false;
  return true;
}

/**
 * Shortest period first, account before per-model.
 *
 * Deliberately *not* sorted by whether a window is in force. is_active decides
 * emphasis — dimming, the idle marker — and letting it decide position too
 * would make the list reorder itself as windows roll over, so the row you were
 * reading moves while you read it. A fixed order that matches the CLI status
 * line (`5h … · wk …`) is the one a reader can learn once.
 */
function byPriority(a: UsageWindow, b: UsageWindow): number {
  const period = (w: UsageWindow) => (w.kind === "five_hour" ? 0 : 1);
  if (period(a) !== period(b)) return period(a) - period(b);
  return (a.scope ? 1 : 0) - (b.scope ? 1 : 0);
}

/**
 * One window's row.
 *
 * `expired` is the window that is in force but whose cached reading describes a
 * period that has already ended. It gets a row rather than being dropped,
 * because the reader came here to see both windows and a panel showing only the
 * weekly answers a question nobody asked — but it gets a dash rather than a
 * percentage, because after a reset the true figure is *unknown*, not the old
 * number and not zero. Printing either would be inventing a reading.
 */
function WindowMeter({
  window: w,
  expired = false,
  canRefresh = true,
}: {
  window: UsageWindow;
  expired?: boolean;
  canRefresh?: boolean;
}) {
  const pct = w.utilization ?? 0;
  // Explicitly false, not merely falsy: null means the agent said nothing about
  // this window, which is not the same claim as "this allowance is idle".
  const idle = w.active === false;
  // One hue, not a red/amber/green traffic light: this is a magnitude, and
  // recolouring it at thresholds would imply a judgement the number does not
  // carry. High usage gets a warning tint only once it is genuinely near the cap.
  const tone = pct >= 90 ? "bg-status-critical" : pct >= 75 ? "bg-status-warning" : "bg-chart-1";
  return (
    <div className={cn("space-y-1", idle && "opacity-60")}>
      <div className="flex items-baseline justify-between gap-2 text-[11px]">
        <span className="text-muted-foreground">
          {w.label}
          {w.scope && <span className="ml-1 font-mono opacity-70">{w.scope}</span>}
          {idle && <span className="ml-1 opacity-70" title="not the window currently in force">idle</span>}
        </span>
        <span className="font-medium tabular-nums">{expired ? "—" : `${pct}%`}</span>
      </div>
      <div className="h-1 overflow-hidden rounded-full bg-muted">
        {expired ? null : (
          <div
            className={cn("h-full rounded-full transition-[width]", tone)}
            style={{ width: `${Math.min(100, Math.max(2, pct))}%` }}
          />
        )}
      </div>
      {expired ? (
        <p className="text-[10px] text-muted-foreground">
          reading expired {w.resetsAt ? formatRelative(w.resetsAt) : "already"}
          {canRefresh ? " — refresh to see it" : ""}
        </p>
      ) : (
        w.resetsAt && (
          <p className="text-[10px] text-muted-foreground">resets {formatRelative(w.resetsAt)}</p>
        )
      )}
    </div>
  );
}
