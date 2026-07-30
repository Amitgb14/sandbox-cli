"use client";

import { Area, AreaChart, CartesianGrid, Line, LineChart, XAxis, YAxis } from "recharts";
import {
  ChartContainer,
  ChartTooltip,
  ChartTooltipContent,
  type ChartConfig,
} from "@/components/ui/chart";
import { formatBytesShort } from "@/lib/format";
import type { MetricSample } from "@/lib/types";

/**
 * CPU and memory over a run's life.
 *
 * **Two charts, never two y-axes.** CPU is a percentage that routinely passes
 * 100 (a container with four cores can report 400%), memory is a byte count with
 * a ceiling; putting them on one frame with two scales would let any visual
 * crossing be read as a relationship that is not there. So: two stacked frames,
 * one scale each, sharing an x-axis.
 */

const cpuConfig = {
  cpuPct: { label: "CPU", color: "var(--chart-1)" },
} satisfies ChartConfig;

const memConfig = {
  memBytes: { label: "Memory", color: "var(--chart-2)" },
} satisfies ChartConfig;

function timeTick(iso: string): string {
  return new Date(iso).toLocaleTimeString([], { hour: "2-digit", minute: "2-digit" });
}

export function CpuChart({ samples, height = 160 }: { samples: MetricSample[]; height?: number }) {
  const data = samples.map((s) => ({ ...s, t: timeTick(s.t) }));
  return (
    <ChartContainer config={cpuConfig} className="w-full" style={{ height }}>
      <LineChart data={data} margin={{ top: 8, right: 8, left: -12, bottom: 0 }}>
        <CartesianGrid vertical={false} stroke="var(--viz-grid)" strokeDasharray="2 4" />
        <XAxis
          dataKey="t"
          tickLine={false}
          axisLine={false}
          tickMargin={8}
          minTickGap={40}
          tick={{ fill: "var(--viz-muted)", fontSize: 11 }}
        />
        <YAxis
          width={44}
          tickLine={false}
          axisLine={false}
          tickFormatter={(v: number) => `${Math.round(v)}%`}
          tick={{ fill: "var(--viz-muted)", fontSize: 11 }}
        />
        <ChartTooltip
          cursor={{ stroke: "var(--viz-axis)", strokeWidth: 1 }}
          content={
            <ChartTooltipContent
              indicator="line"
              formatter={(value) => `${Number(value).toFixed(1)}%`}
            />
          }
        />
        <Line
          dataKey="cpuPct"
          type="monotone"
          stroke="var(--color-cpuPct)"
          strokeWidth={2}
          dot={false}
          activeDot={{ r: 4, strokeWidth: 2, stroke: "var(--viz-surface)" }}
        />
      </LineChart>
    </ChartContainer>
  );
}

export function MemoryChart({
  samples,
  height = 160,
}: {
  samples: MetricSample[];
  height?: number;
}) {
  const data = samples.map((s) => ({ ...s, t: timeTick(s.t) }));
  // The limit is a reference line, not a series: it does not vary and drawing it
  // as one would imply it might.
  const limit = samples[0]?.memLimitBytes ?? 0;
  return (
    <ChartContainer config={memConfig} className="w-full" style={{ height }}>
      <AreaChart data={data} margin={{ top: 8, right: 8, left: -12, bottom: 0 }}>
        <defs>
          <linearGradient id="mem-fill" x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stopColor="var(--color-memBytes)" stopOpacity={0.35} />
            <stop offset="95%" stopColor="var(--color-memBytes)" stopOpacity={0.02} />
          </linearGradient>
        </defs>
        <CartesianGrid vertical={false} stroke="var(--viz-grid)" strokeDasharray="2 4" />
        <XAxis
          dataKey="t"
          tickLine={false}
          axisLine={false}
          tickMargin={8}
          minTickGap={40}
          tick={{ fill: "var(--viz-muted)", fontSize: 11 }}
        />
        <YAxis
          width={44}
          domain={[0, limit || "auto"]}
          tickLine={false}
          axisLine={false}
          tickFormatter={(v: number) => formatBytesShort(v)}
          tick={{ fill: "var(--viz-muted)", fontSize: 11 }}
        />
        <ChartTooltip
          cursor={{ stroke: "var(--viz-axis)", strokeWidth: 1 }}
          content={
            <ChartTooltipContent
              indicator="line"
              formatter={(value) => formatBytesShort(Number(value))}
            />
          }
        />
        <Area
          dataKey="memBytes"
          type="monotone"
          stroke="var(--color-memBytes)"
          strokeWidth={2}
          fill="url(#mem-fill)"
          dot={false}
          activeDot={{ r: 4, strokeWidth: 2, stroke: "var(--viz-surface)" }}
        />
      </AreaChart>
    </ChartContainer>
  );
}
