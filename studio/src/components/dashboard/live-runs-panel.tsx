"use client";

import Link from "next/link";
import { ArrowUpRight, MoonStar, Skull, Square } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip";
import { EmptyState } from "@/components/common/empty-state";
import { LiveDot } from "@/components/common/status-badge";
import { KindBadge, NetworkBadge } from "@/components/common/posture-badges";
import { useKillRun, useStopRun } from "@/lib/api/queries";
import {
  formatBytesShort,
  formatDurationTight,
  formatPercent,
} from "@/lib/format";
import { cn } from "@/lib/utils";
import type { Run } from "@/lib/types";

/**
 * What is working right now.
 *
 * The two controls are separate on purpose, and that is inherited rather than
 * invented: **stop** asks the guest to exit and gives it a grace period, **kill**
 * does not. The difference is whether the agent closed the file it was editing,
 * so neither hides behind a shared "end run".
 */
export function LiveRunsPanel({
  runs,
  loading,
}: {
  runs: Run[];
  loading?: boolean;
}) {
  const stop = useStopRun();
  const kill = useKillRun();

  return (
    <Card className="surface-sheen gap-3">
      <CardHeader className="gap-1">
        <div className="flex items-center justify-between gap-2">
          <CardTitle className="flex items-center gap-2 text-sm font-medium">
            {runs.length > 0 && <LiveDot />}
            In flight
            <span className="text-muted-foreground tabular-nums">
              {runs.length}
            </span>
          </CardTitle>
          <Button asChild variant="ghost" size="sm" className="h-7 text-xs">
            <Link href="/runs">
              All runs
              <ArrowUpRight className="size-3.5" />
            </Link>
          </Button>
        </div>
      </CardHeader>
      <CardContent className="space-y-2">
        {loading ? (
          <>
            <Skeleton className="h-16 w-full" />
            <Skeleton className="h-16 w-full" />
          </>
        ) : runs.length === 0 ? (
          <EmptyState
            icon={MoonStar}
            title="Nothing running"
            description="Nothing is running right now. Finished runs are kept and listed under Runs; one you start from Launch shows up here within a few seconds."
            action={
              <Button asChild size="sm" variant="outline">
                <Link href="/launch">Launch a run</Link>
              </Button>
            }
            className="border-0 py-8"
          />
        ) : (
          runs.map((run) => (
            <LiveRunRow key={run.id} run={run} stop={stop} kill={kill} />
          ))
        )}
      </CardContent>
    </Card>
  );
}

function LiveRunRow({
  run,
  stop,
  kill,
}: {
  run: Run;
  stop: ReturnType<typeof useStopRun>;
  kill: ReturnType<typeof useKillRun>;
}) {
  const m = run.latestMetrics;
  const memFrac =
    m && m.memLimitBytes > 0 ? m.memBytes / m.memLimitBytes : null;

  return (
    <div className="group rounded-lg border bg-card/40 p-3 transition-colors hover:bg-accent/40">
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0 flex-1">
          <div className="flex min-w-0 items-center gap-2">
            <Link
              href={`/runs/${run.id}`}
              className="truncate font-mono text-sm font-medium hover:underline"
            >
              {run.branch ?? run.name}
            </Link>
            <KindBadge kind={run.kind} />
          </div>
          <div className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted-foreground">
            <span className="font-mono">{run.agent ?? "plain run"}</span>
            <span>{run.repoName}</span>
            <span className="tabular-nums">
              {formatDurationTight(run.durationMs)}
            </span>
            <NetworkBadge network={run.network} />
            {run.verify && (
              <Tooltip>
                <TooltipTrigger asChild>
                  <span className="truncate font-mono text-[11px] text-caution">
                    verify: {run.verify}
                  </span>
                </TooltipTrigger>
                <TooltipContent className="max-w-sm">
                  This run&apos;s definition of done. Its exit code becomes the
                  container&apos;s, which is what makes the run autonomous
                  rather than merely headless.
                </TooltipContent>
              </Tooltip>
            )}
          </div>
        </div>

        <div className="flex shrink-0 items-center gap-1 opacity-0 transition-opacity group-hover:opacity-100 focus-within:opacity-100">
          <Tooltip>
            <TooltipTrigger asChild>
              <Button
                variant="ghost"
                size="icon"
                className="size-7"
                disabled={stop.isPending}
                onClick={() =>
                  stop.mutate(run.id, {
                    onSuccess: () =>
                      toast.success(`Asked ${run.branch ?? run.name} to exit`, {
                        description:
                          "The guest gets a grace period to finish writing.",
                      }),
                  })
                }
                aria-label="Stop"
              >
                <Square className="size-3.5" />
              </Button>
            </TooltipTrigger>
            <TooltipContent>
              Stop — asks the guest to exit, then waits
            </TooltipContent>
          </Tooltip>
          <Tooltip>
            <TooltipTrigger asChild>
              <Button
                variant="ghost"
                size="icon"
                className="size-7 text-destructive hover:text-destructive"
                disabled={kill.isPending}
                onClick={() =>
                  kill.mutate(run.id, {
                    onSuccess: () =>
                      toast.warning(`Killed ${run.branch ?? run.name}`, {
                        description:
                          "Terminated immediately — no chance to finish what it was writing.",
                      }),
                  })
                }
                aria-label="Kill"
              >
                <Skull className="size-3.5" />
              </Button>
            </TooltipTrigger>
            <TooltipContent>Kill — immediate, mid-write</TooltipContent>
          </Tooltip>
        </div>
      </div>

      {/* Two meters, not a chart: this is a current reading, and a row of
          thirty sparklines would be thirty things nobody reads. */}
      <div className="mt-2.5 grid grid-cols-2 gap-3">
        <Meter
          label="CPU"
          value={m ? formatPercent(m.cpuPct, 0) : null}
          frac={m ? Math.min(1, m.cpuPct / 400) : null}
          tone="bg-chart-1"
        />
        <Meter
          label="Memory"
          value={
            m
              ? `${formatBytesShort(m.memBytes)}${
                  m.memLimitBytes
                    ? ` / ${formatBytesShort(m.memLimitBytes)}`
                    : ""
                }`
              : null
          }
          frac={memFrac}
          tone={
            memFrac !== null && memFrac > 0.9
              ? "bg-status-warning"
              : "bg-chart-2"
          }
        />
      </div>
    </div>
  );
}

function Meter({
  label,
  value,
  frac,
  tone,
}: {
  label: string;
  /** `null` when the container was never sampled — absent, not zero. */
  value: string | null;
  frac: number | null;
  tone: string;
}) {
  return (
    <div className="space-y-1">
      <div className="flex items-baseline justify-between gap-2 text-[11px]">
        <span className="text-muted-foreground">{label}</span>
        <span className="font-mono tabular-nums">{value ?? "not sampled"}</span>
      </div>
      <div className="h-1 overflow-hidden rounded-full bg-muted">
        {frac !== null && (
          <div
            className={cn("h-full rounded-full transition-[width]", tone)}
            style={{ width: `${Math.max(2, Math.min(100, frac * 100))}%` }}
          />
        )}
      </div>
    </div>
  );
}
