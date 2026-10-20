"use client";

import { useState } from "react";
import { Area, AreaChart, CartesianGrid, ReferenceLine, XAxis, YAxis } from "recharts";
import { Activity } from "lucide-react";
import { ResourceChips } from "@/components/sandbox/resource-chips";
import { ChartContainer, ChartTooltip, ChartTooltipContent, type ChartConfig } from "@/components/ui/chart";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { METRICS_REFRESH_MS, useInfo, useMetrics } from "@/lib/api/queries";
import { formatBytes, formatMiB } from "@/lib/format";
import { hasGPU } from "@/lib/gpu";
import { cn } from "@/lib/utils";
import type { GPUSample, MetricSample, Sandbox } from "@/lib/types";

const chartConfig = {
  cpu: { label: "CPU", color: "var(--status-running)" },
  memory: { label: "Memory", color: "var(--caution)" },
} satisfies ChartConfig;

/** A GPU's series colour, by its index: data slots CPU and memory do not use. */
const GPU_COLORS = ["var(--chart-3)", "var(--chart-7)", "var(--chart-5)", "var(--chart-2)"];

/** Every GPU index seen in the samples, in order: a GPU missing from one sample is still a series. */
function gpuIndexes(samples: MetricSample[]): number[] {
  const seen = new Set<number>();
  for (const s of samples) for (const g of s.guest?.gpus ?? []) seen.add(g.index);
  return [...seen].sort((a, b) => a - b);
}

/** The chart config for CPU, memory and each GPU's two series (gpuN busy, vramN its memory). */
function configFor(gpus: number[]): ChartConfig {
  const out: ChartConfig = { ...chartConfig };
  gpus.forEach((i, k) => {
    const color = GPU_COLORS[k % GPU_COLORS.length];
    out[`gpu${i}`] = { label: `GPU ${i}`, color };
    out[`vram${i}`] = { label: `GPU ${i}`, color };
  });
  return out;
}

const MiB = 1 << 20;

/** A GPU's one-line detail under its tile: its memory, temperature and power, as far as the driver said. */
function gpuDetail(g: GPUSample): string {
  const parts: string[] = [];
  if (g.memory_used_bytes !== undefined && g.memory_total_bytes !== undefined) {
    parts.push(`${formatMiB(Math.round(g.memory_used_bytes / MiB))} of ${formatMiB(Math.round(g.memory_total_bytes / MiB))}`);
  }
  if (g.temperature_c !== undefined) parts.push(`${Math.round(g.temperature_c)} °C`);
  if (g.power_watts !== undefined) parts.push(`${Math.round(g.power_watts)} W`);
  return parts.join(" · ") || "no detail from the driver";
}

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
 * runtime's counters on macOS, the VMM's process on Linux. A sample every
 * interval while it runs; CPU is the share of the vCPUs it was given.
 *
 * A GPU sandbox is the exception, and says so on screen. The host holds its
 * memory whole from boot (VFIO pins it), so the host's figure is always the
 * limit and its chart would be a flat line; its memory is drawn from what the
 * guest reports using instead. Its GPUs can only be read by their driver, in
 * the guest. Both are the guest's word, bounded by sandboxd.
 */
function MetricsBody({ sb }: { sb: Sandbox }) {
  const { data, error, isLoading } = useMetrics(sb.id);
  const samples = data?.samples ?? [];
  const last = samples[samples.length - 1];
  const gpu = hasGPU(sb);
  const gpus = gpuIndexes(samples);
  const config = configFor(gpus);
  const rows = samples.map((s) => {
    const row: Record<string, string | number | null> = {
      time: s.time,
      cpu: +s.cpu_percent.toFixed(2),
      // A gap where the guest did not answer, never the host's flat limit drawn as use.
      memory: gpu
        ? s.guest?.memory_used_bytes !== undefined
          ? +(s.guest.memory_used_bytes / MiB).toFixed(1)
          : null
        : +(s.memory_bytes / MiB).toFixed(1),
    };
    for (const i of gpus) {
      const g = s.guest?.gpus?.find((x) => x.index === i);
      row[`gpu${i}`] = g?.utilization_percent ?? null;
      row[`vram${i}`] = g?.memory_used_bytes !== undefined ? +(g.memory_used_bytes / MiB).toFixed(1) : null;
    }
    return row;
  });
  const limitMiB = last ? last.memory_limit_bytes / MiB : sb.memory_mb;
  const lastGPUs = last?.guest?.gpus ?? [];
  const vramMiB = Math.max(0, ...samples.flatMap((s) => (s.guest?.gpus ?? []).map((g) => (g.memory_total_bytes ?? 0) / MiB)));
  const memoryNow = gpu ? last?.guest?.memory_used_bytes : last?.memory_bytes;
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
        <Tile
          label={gpu ? "Memory in use" : "Memory"}
          value={memoryNow === undefined ? "—" : formatMiB(Math.round(memoryNow / MiB))}
          sub={gpu ? `of ${formatMiB(Math.round(limitMiB))}, all held on the host` : `of ${formatMiB(Math.round(limitMiB))}`}
        />
        <Tile label="Network" value={`↓ ${perSec(rx)}`} sub={`↑ ${perSec(tx)}`} />
        <Tile label="Disk" value={`R ${perSec(rd)}`} sub={`W ${perSec(wr)}`} />
      </div>
      {gpu ? (
        <div className="grid grid-cols-2 gap-2.5 sm:grid-cols-4">
          {lastGPUs.length ? (
            lastGPUs.map((g) => (
              <Tile
                key={g.index}
                label={`GPU ${g.index}`}
                value={g.utilization_percent === undefined ? "—" : `${Math.round(g.utilization_percent)}%`}
                sub={gpuDetail(g)}
              />
            ))
          ) : (
            <Tile label="GPU" value="—" sub="the guest's driver did not answer" />
          )}
        </div>
      ) : null}
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
        <figcaption className="text-xs text-muted-foreground">
          {gpu
            ? `Memory in use, as the guest reports it, of ${formatMiB(Math.round(limitMiB))} — the host holds all ${formatMiB(Math.round(limitMiB))} for a GPU sandbox from boot`
            : `Memory, of ${formatMiB(Math.round(limitMiB))}`}
        </figcaption>
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
      {gpus.length ? (
        <>
          <figure className="flex flex-col gap-1">
            <figcaption className="text-xs text-muted-foreground">GPU, % busy, from its driver in the guest</figcaption>
            <ChartContainer config={config} className="aspect-auto h-40 w-full">
              <AreaChart data={rows} margin={{ left: 0, right: 8, top: 6, bottom: 0 }}>
                <CartesianGrid vertical={false} />
                <XAxis dataKey="time" tickFormatter={tick} minTickGap={48} tickLine={false} axisLine={false} />
                <YAxis domain={[0, 100]} width={64} tickLine={false} axisLine={false} tickFormatter={(v) => `${v}%`} />
                <ChartTooltip
                  content={<ChartTooltipContent labelFormatter={(_, p) => clock(p?.[0]?.payload?.time ?? "", true)} valueFormatter={(v) => `${v.toFixed(0)}%`} />}
                />
                {gpus.map((i) => (
                  <Area
                    key={i}
                    dataKey={`gpu${i}`}
                    type="monotone"
                    stroke={`var(--color-gpu${i})`}
                    fill={`var(--color-gpu${i})`}
                    fillOpacity={0.12}
                    isAnimationActive={false}
                  />
                ))}
              </AreaChart>
            </ChartContainer>
          </figure>
          {vramMiB > 0 ? (
            <figure className="flex flex-col gap-1">
              <figcaption className="text-xs text-muted-foreground">GPU memory, of {formatMiB(Math.round(vramMiB))}</figcaption>
              <ChartContainer config={config} className="aspect-auto h-40 w-full">
                <AreaChart data={rows} margin={{ left: 0, right: 8, top: 6, bottom: 0 }}>
                  <CartesianGrid vertical={false} />
                  <XAxis dataKey="time" tickFormatter={tick} minTickGap={48} tickLine={false} axisLine={false} />
                  <YAxis
                    domain={[0, Math.ceil(vramMiB)]}
                    ticks={memoryTicks(vramMiB)}
                    width={64}
                    tickLine={false}
                    axisLine={false}
                    tickFormatter={(v: number) => (v === 0 ? "0" : formatMiB(Math.round(v)))}
                  />
                  <ChartTooltip
                    content={
                      <ChartTooltipContent
                        labelFormatter={(_, p) => clock(p?.[0]?.payload?.time ?? "", true)}
                        valueFormatter={(v) => formatMiB(Math.round(v))}
                      />
                    }
                  />
                  {gpus.map((i) => (
                    <Area
                      key={i}
                      dataKey={`vram${i}`}
                      type="monotone"
                      stroke={`var(--color-vram${i})`}
                      fill={`var(--color-vram${i})`}
                      fillOpacity={0.12}
                      isAnimationActive={false}
                    />
                  ))}
                </AreaChart>
              </ChartContainer>
            </figure>
          ) : null}
        </>
      ) : null}
      <p className="text-[11px] text-muted-foreground">Refreshes every {METRICS_REFRESH_MS / 1000} s.</p>
    </div>
  );
}

/** A sandbox's metrics in a dialog, polled only while it is open. */
export function MetricsDialog({ sb, open, onOpenChange }: { sb: Sandbox; open: boolean; onOpenChange: (open: boolean) => void }) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      {/* React carries a click in a portal up its component tree, so one in
          here would reach a list row's handler and open the sandbox's panel too. */}
      <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-3xl" onClick={(e) => e.stopPropagation()}>
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
        title={hasGPU(sb) ? "CPU, memory and GPU over the last hour" : "CPU and memory over the last hour"}
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
