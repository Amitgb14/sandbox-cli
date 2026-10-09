"use client";

import { useState } from "react";
import { Area, AreaChart, CartesianGrid, ReferenceLine, XAxis, YAxis } from "recharts";
import { Activity } from "lucide-react";
import { ResourceChips } from "@/components/sandbox/resource-chips";
import { ChartContainer, ChartTooltip, ChartTooltipContent, type ChartConfig } from "@/components/ui/chart";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { useInfo, useMetrics } from "@/lib/api/queries";
import { formatBytes, formatMiB } from "@/lib/format";
import { cn } from "@/lib/utils";
import type { MetricSample, Sandbox } from "@/lib/types";

const chartConfig = {
  cpu: { label: "CPU", color: "var(--status-running)" },
  memory: { label: "Memory", color: "var(--caution)" },
} satisfies ChartConfig;

function clock(iso: string, seconds = false): string {
  return new Date(iso).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", ...(seconds ? { second: "2-digit" } : {}) });
}

/** A counter's rate between the last two samples, per second. */
function rate(samples: MetricSample[], pick: (s: MetricSample) => number): number | null {
  if (samples.length < 2) return null;
  const a = samples[samples.length - 2];
  const b = samples[samples.length - 1];
  const secs = (Date.parse(b.time) - Date.parse(a.time)) / 1000;
  return secs > 0 ? Math.max(0, (pick(b) - pick(a)) / secs) : null;
}

function Tile({ label, value, sub }: { label: string; value: string; sub?: string }) {
  return (
    <div className="flex flex-col gap-1 rounded-lg border bg-card px-3.5 py-2.5">
      <span className="text-xs text-muted-foreground">{label}</span>
      <span className="text-lg font-medium tabular-nums">{value}</span>
      {sub ? <span className="text-[11px] text-muted-foreground tabular-nums">{sub}</span> : null}
    </div>
  );
}

/**
 * Marks for a memory axis that read as sizes: quarters of the limit, which
 * for the sizes a sandbox is given (512 MiB, 1, 2, 4, 8 GiB) are round
 * numbers — 512 MiB, 1 GiB, 1.5 GiB, 2 GiB — where the chart's own picks
 * were 550 and 1100.
 */
export function memoryTicks(limitMiB: number): number[] {
  const top = Math.ceil(limitMiB);
  return [0, top / 4, top / 2, (3 * top) / 4, top];
}

/**
 * The last hour of a sandbox's CPU and memory, as the host measures it: the
 * runtime's counters on macOS, the VMM's process on Linux, never the guest's
 * own account. A sample every interval while it runs; CPU is the share of the
 * vCPUs it was given.
 */
function MetricsBody({ sb }: { sb: Sandbox }) {
  const { data, error, isLoading } = useMetrics(sb.id);
  const samples = data?.samples ?? [];
  const last = samples[samples.length - 1];
  const rows = samples.map((s) => ({ time: s.time, cpu: +s.cpu_percent.toFixed(2), memory: +(s.memory_bytes / (1 << 20)).toFixed(1) }));
  const limitMiB = last ? last.memory_limit_bytes / (1 << 20) : sb.memory_mb;
  // A sample every few seconds: under ten minutes of them, a minute's marks would
  // all say the same minute.
  const short = samples.length > 1 && Date.parse(last.time) - Date.parse(samples[0].time) < 10 * 60_000;
  const tick = (iso: string) => clock(iso, short);
  const rx = rate(samples, (s) => s.net_rx_bytes);
  const tx = rate(samples, (s) => s.net_tx_bytes);
  const rd = rate(samples, (s) => s.disk_read_bytes);
  const wr = rate(samples, (s) => s.disk_write_bytes);
  const perSec = (n: number | null) => (n === null ? "—" : `${formatBytes(n)}/s`);

  if (error) return <p className="text-sm text-destructive">{error.message}</p>;
  if (isLoading) return <p className="text-sm text-muted-foreground">Reading…</p>;
  if (!samples.length) {
    return (
      <p className="py-10 text-center text-sm text-muted-foreground">
        No samples yet: one is taken every {data?.interval_secs ?? 5} seconds while the sandbox runs.
      </p>
    );
  }
  return (
    <div className="flex flex-col gap-4">
      <div className="grid grid-cols-2 gap-2.5 sm:grid-cols-4">
        <Tile label="CPU" value={`${last.cpu_percent.toFixed(1)}%`} sub={`of ${sb.cpus} vCPU`} />
        <Tile label="Memory" value={formatMiB(Math.round(last.memory_bytes / (1 << 20)))} sub={`of ${formatMiB(Math.round(limitMiB))}`} />
        <Tile label="Network" value={`↓ ${perSec(rx)}`} sub={`↑ ${perSec(tx)}`} />
        <Tile label="Disk" value={`R ${perSec(rd)}`} sub={`W ${perSec(wr)}`} />
      </div>
      <figure className="flex flex-col gap-1">
        <figcaption className="text-xs text-muted-foreground">CPU, % of {sb.cpus} vCPU</figcaption>
        <ChartContainer config={chartConfig} className="aspect-auto h-40 w-full">
          <AreaChart data={rows} margin={{ left: 0, right: 8, top: 6, bottom: 0 }}>
            <CartesianGrid vertical={false} />
            <XAxis dataKey="time" tickFormatter={tick} minTickGap={48} tickLine={false} axisLine={false} />
            <YAxis domain={[0, 100]} width={64} tickLine={false} axisLine={false} tickFormatter={(v) => `${v}%`} />
            <ChartTooltip
              content={<ChartTooltipContent labelFormatter={(_, p) => clock(p?.[0]?.payload?.time ?? "", true)} valueFormatter={(v) => `${v.toFixed(1)}%`} />}
            />
            <Area dataKey="cpu" type="monotone" stroke="var(--color-cpu)" fill="var(--color-cpu)" fillOpacity={0.15} isAnimationActive={false} />
          </AreaChart>
        </ChartContainer>
      </figure>
      <figure className="flex flex-col gap-1">
        <figcaption className="text-xs text-muted-foreground">Memory, of {formatMiB(Math.round(limitMiB))}</figcaption>
        <ChartContainer config={chartConfig} className="aspect-auto h-40 w-full">
          <AreaChart data={rows} margin={{ left: 0, right: 8, top: 6, bottom: 0 }}>
            <CartesianGrid vertical={false} />
            <XAxis dataKey="time" tickFormatter={tick} minTickGap={48} tickLine={false} axisLine={false} />
            <YAxis
              domain={[0, Math.ceil(limitMiB)]}
              ticks={memoryTicks(limitMiB)}
              width={64}
              tickLine={false}
              axisLine={false}
              tickFormatter={(v: number) => (v === 0 ? "0" : formatMiB(Math.round(v)))}
            />
            <ReferenceLine y={limitMiB} stroke="var(--color-memory)" strokeDasharray="4 4" />
            <ChartTooltip
              content={
                <ChartTooltipContent
                  labelFormatter={(_, p) => clock(p?.[0]?.payload?.time ?? "", true)}
                  valueFormatter={(v) => formatMiB(Math.round(v))}
                />
              }
            />
            <Area dataKey="memory" type="monotone" stroke="var(--color-memory)" fill="var(--color-memory)" fillOpacity={0.15} isAnimationActive={false} />
          </AreaChart>
        </ChartContainer>
      </figure>
      <p className="text-[11px] text-muted-foreground">
        Measured on the host every {data?.interval_secs} s. The last hour is kept in sandboxd&apos;s memory on that host, and nowhere else:
        not on disk, so it is gone when sandboxd restarts, or once the sandbox has ended and 100 newer ones have too.
      </p>
    </div>
  );
}

/** A sandbox's metrics in a dialog, polled only while it is open. */
export function MetricsDialog({ sb, open, onOpenChange }: { sb: Sandbox; open: boolean; onOpenChange: (open: boolean) => void }) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      {/* React carries a click in a portal up its component tree, so one in
          here would reach a list row's handler and open the sandbox's panel too. */}
      <DialogContent className="sm:max-w-3xl" onClick={(e) => e.stopPropagation()}>
        <DialogHeader>
          <DialogTitle>Metrics of {sb.name || sb.id}</DialogTitle>
          <DialogDescription>The last hour</DialogDescription>
        </DialogHeader>
        {open ? <MetricsBody sb={sb} /> : null}
      </DialogContent>
    </Dialog>
  );
}

/**
 * A sandbox's resource chips, which open its metrics where the endpoint
 * measures usage, and are plain chips where it does not.
 *
 * With onOpen the caller owns the dialog. A table must: its cells are
 * remounted whenever its column definitions change, which a list polling
 * every few seconds does, and a dialog whose state lived in the cell closed
 * with each poll.
 */
export function ResourcesWithMetrics({ sb, className, onOpen }: { sb: Sandbox; className?: string; onOpen?: () => void }) {
  const { data: info } = useInfo();
  const [open, setOpen] = useState(false);
  const can = !!info?.capabilities?.capabilities?.metrics;
  if (!can) return <ResourceChips sb={sb} className={className} />;
  return (
    <>
      <button
        type="button"
        onClick={() => (onOpen ? onOpen() : setOpen(true))}
        title="CPU and memory over the last hour"
        aria-label={`Metrics of ${sb.name || sb.id}`}
        className={cn("group flex items-center gap-1.5 rounded-md text-left hover:opacity-90", className)}
      >
        <ResourceChips sb={sb} className="flex-nowrap group-hover:[&>span]:border-status-running/50" />
        <Activity className="size-3.5 shrink-0 text-muted-foreground group-hover:text-status-running" />
      </button>
      {onOpen ? null : <MetricsDialog sb={sb} open={open} onOpenChange={setOpen} />}
    </>
  );
}
