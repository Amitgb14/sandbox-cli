"use client";

import { Bar, BarChart, CartesianGrid, XAxis, YAxis } from "recharts";
import {
  ChartContainer,
  ChartLegend,
  ChartLegendContent,
  ChartTooltip,
  ChartTooltipContent,
  type ChartConfig,
} from "@/components/ui/chart";
import type { FailoverDay } from "@/lib/routing-history";

/**
 * Failovers per day: is this getting worse.
 *
 * The counters above answer "how often has routing helped" over all of history,
 * which is the wrong shape for the question people bring to an outage — a week
 * where every run fell through and a year with one bad afternoon can produce the
 * same rescue rate.
 *
 * Two series and the same status palette the rest of the app uses, in the same
 * order: rescued, then still-failed. Note what is *not* here — a bar for runs
 * that did not route. This chart counts episodes, and an ordinary run is not one;
 * including them would make every real signal a rounding error against normal
 * traffic.
 */
const config = {
  rescued: { label: "Rescued", color: "var(--status-good)" },
  failed: { label: "Still failed", color: "var(--status-critical)" },
  // Its own band rather than folded into either: a detached run's audit line is
  // written at launch, so its exit code is a placeholder. Colouring that as a
  // rescue is how this chart read 100% success by construction.
  unknown: { label: "Not recorded", color: "var(--status-idle)" },
} satisfies ChartConfig;

export function FailoverTrend({ data, height = 160 }: { data: FailoverDay[]; height?: number }) {
  return (
    <ChartContainer config={config} className="w-full" style={{ height }}>
      <BarChart data={data} margin={{ top: 8, right: 8, left: -12, bottom: 0 }} barCategoryGap={4}>
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
          width={28}
          tickLine={false}
          axisLine={false}
          tick={{ fill: "var(--viz-muted)", fontSize: 11 }}
        />
        <ChartTooltip
          cursor={{ fill: "var(--muted)", fillOpacity: 0.4 }}
          content={<ChartTooltipContent indicator="dot" />}
        />
        <ChartLegend content={<ChartLegendContent />} />
        <Bar
          dataKey="rescued"
          stackId="f"
          fill="var(--color-rescued)"
          stroke="var(--viz-surface)"
          strokeWidth={1.5}
        />
        <Bar
          dataKey="failed"
          stackId="f"
          fill="var(--color-failed)"
          stroke="var(--viz-surface)"
          strokeWidth={1.5}
        />
        <Bar
          dataKey="unknown"
          stackId="f"
          fill="var(--color-unknown)"
          stroke="var(--viz-surface)"
          strokeWidth={1.5}
          radius={[3, 3, 0, 0]}
        />
      </BarChart>
    </ChartContainer>
  );
}
