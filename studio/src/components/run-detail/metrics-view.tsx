"use client";

import { Activity, HardDrive, Network } from "lucide-react";
import { Skeleton } from "@/components/ui/skeleton";
import { ChartCard } from "@/components/charts/chart-card";
import { CpuChart, MemoryChart } from "@/components/charts/resource-chart";
import { MetricTile } from "@/components/common/metric-tile";
import { EmptyState } from "@/components/common/empty-state";
import { useRunMetrics } from "@/lib/api/queries";
import { formatBytes, formatBytesShort, formatPercent } from "@/lib/format";
import type { Run } from "@/lib/types";

/**
 * CPU and memory over the run's life, plus the peaks.
 *
 * A run that was never sampled reports **absent**, not zero — a container that
 * exited in a second was not idle, it was never measured, and a gauge reading 0%
 * says the wrong thing about it.
 */
export function MetricsView({ run }: { run: Run }) {
  const { data, isPending } = useRunMetrics(run.id);

  if (isPending) {
    return (
      <div className="space-y-4">
        <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
          {Array.from({ length: 4 }).map((_, i) => (
            <Skeleton key={i} className="h-28 w-full" />
          ))}
        </div>
        <Skeleton className="h-52 w-full" />
        <Skeleton className="h-52 w-full" />
      </div>
    );
  }

  if (!data || data.samples.length === 0) {
    return (
      <EmptyState
        icon={Activity}
        title="This run was never sampled"
        description="The meter takes readings on an interval, so a container that finished inside one interval has none. That is different from a run that used nothing."
      />
    );
  }

  const last = data.samples[data.samples.length - 1];
  const limit = last.memLimitBytes;

  return (
    <div className="space-y-4">
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-4">
        <MetricTile
          label="Peak CPU"
          icon={Activity}
          value={formatPercent(data.peak.cpuPct, 0)}
          hint="Percent of one core. A container allowed four cores can legitimately report 400%, which is why this is never drawn on the same axis as memory."
          footer={`${data.samples.length} samples`}
          spark={data.samples.map((s) => s.cpuPct)}
        />
        <MetricTile
          label="Peak memory"
          icon={HardDrive}
          value={formatBytesShort(data.peak.memBytes)}
          footer={limit ? `of ${formatBytesShort(limit)} limit` : "no limit set"}
          spark={data.samples.map((s) => s.memBytes)}
          sparkTone="var(--chart-2)"
        />
        <MetricTile
          label="Network out"
          icon={Network}
          value={formatBytesShort(last.netTxBytes)}
          hint="What left the container. Under an allowlist every byte here went to a permitted destination — the firewall is default-deny and the proxy decides on the hostname."
          footer={`${formatBytesShort(last.netRxBytes)} in`}
        />
        <MetricTile
          label="Processes"
          icon={Activity}
          value={last.pids}
          footer={`limit ${run.security.pidsLimit}`}
          hint="`--pids-limit` caps this. It defaults to 1024, which is a fork-bomb ceiling rather than a workload one."
        />
      </div>

      {/* Two frames, one scale each. Never two y-axes on one frame: a visual
          crossing between a percentage and a byte count means nothing. */}
      <ChartCard
        title="CPU"
        description="Percent of one core, sampled on the meter's interval."
        aside={
          <span className="font-mono text-xs text-muted-foreground tabular-nums">
            peak {formatPercent(data.peak.cpuPct, 0)}
          </span>
        }
      >
        <CpuChart samples={data.samples} />
      </ChartCard>

      <ChartCard
        title="Memory"
        description={
          limit
            ? `Against the ${formatBytes(limit)} limit this run was given.`
            : "No memory limit was set, so the axis follows the data."
        }
        aside={
          <span className="font-mono text-xs text-muted-foreground tabular-nums">
            peak {formatBytesShort(data.peak.memBytes)}
            {limit ? ` · ${formatPercent((data.peak.memBytes / limit) * 100, 0)} of limit` : ""}
          </span>
        }
      >
        <MemoryChart samples={data.samples} />
      </ChartCard>

      <dl className="grid gap-3 rounded-lg border bg-card/40 p-4 text-xs sm:grid-cols-2 lg:grid-cols-4">
        <Stat label="Block read" value={formatBytes(last.blockReadBytes)} />
        <Stat label="Block written" value={formatBytes(last.blockWriteBytes)} />
        <Stat label="Network received" value={formatBytes(last.netRxBytes)} />
        <Stat label="Network transmitted" value={formatBytes(last.netTxBytes)} />
      </dl>
    </div>
  );
}

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <div className="space-y-0.5">
      <dt className="text-muted-foreground">{label}</dt>
      <dd className="font-mono tabular-nums">{value}</dd>
    </div>
  );
}
