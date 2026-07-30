"use client";

import { Area, AreaChart, CartesianGrid, XAxis, YAxis } from "recharts";
import {
  ChartContainer,
  ChartTooltip,
  ChartTooltipContent,
  type ChartConfig,
} from "@/components/ui/chart";
import type { DayBucket } from "@/lib/derive";

/**
 * Runs per day.
 *
 * One series, so there is no legend — the card title names it. Sequential blue,
 * the default magnitude hue. The area fill is a gradient to zero rather than a
 * flat wash, so the baseline stays readable where the value is small.
 */
const config = {
  total: { label: "Runs started", color: "var(--chart-1)" },
} satisfies ChartConfig;

export function RunVolumeChart({ data, height = 200 }: { data: DayBucket[]; height?: number }) {
  return (
    <ChartContainer config={config} className="w-full" style={{ height }}>
      <AreaChart data={data} margin={{ top: 8, right: 8, left: -12, bottom: 0 }}>
        <defs>
          <linearGradient id="run-volume-fill" x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stopColor="var(--color-total)" stopOpacity={0.4} />
            <stop offset="95%" stopColor="var(--color-total)" stopOpacity={0.02} />
          </linearGradient>
        </defs>
        {/* Recessive grid: horizontal only, hairline. Vertical rules on a time
            axis add ink without adding a reading. */}
        <CartesianGrid vertical={false} stroke="var(--viz-grid)" strokeDasharray="2 4" />
        <XAxis
          dataKey="label"
          tickLine={false}
          axisLine={false}
          tickMargin={8}
          minTickGap={24}
          tick={{ fill: "var(--viz-muted)", fontSize: 11 }}
        />
        <YAxis
          allowDecimals={false}
          width={36}
          tickLine={false}
          axisLine={false}
          tick={{ fill: "var(--viz-muted)", fontSize: 11 }}
        />
        <ChartTooltip
          cursor={{ stroke: "var(--viz-axis)", strokeWidth: 1 }}
          content={<ChartTooltipContent indicator="line" />}
        />
        <Area
          dataKey="total"
          type="monotone"
          stroke="var(--color-total)"
          strokeWidth={2}
          fill="url(#run-volume-fill)"
          // ≥ 8px hit target on hover; no dot at rest, so a fourteen-point
          // series does not read as fourteen decorations.
          dot={false}
          activeDot={{ r: 4, strokeWidth: 2, stroke: "var(--viz-surface)" }}
        />
      </AreaChart>
    </ChartContainer>
  );
}
