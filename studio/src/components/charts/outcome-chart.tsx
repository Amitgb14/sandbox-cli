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
import type { DayBucket } from "@/lib/derive";

/**
 * How runs ended, by day.
 *
 * These four are *states*, so they wear the reserved status palette rather than
 * categorical slots — and in the same order everywhere else in the app reads
 * them: passed, verify-failed, failed, stopped. Four series, so a legend is
 * present; the tooltip carries the numbers, since labelling every segment of a
 * fourteen-day stack would bury the shape.
 *
 * "verify failed" is deliberately its own colour and not folded into "failed".
 * A run whose verify said no did its work and was judged; a run that crashed did
 * not get that far, and the two lead to different next actions.
 */
const config = {
  passed: { label: "Passed", color: "var(--status-good)" },
  verifyFailed: { label: "Verify failed", color: "var(--status-serious)" },
  failed: { label: "Failed", color: "var(--status-critical)" },
  stopped: { label: "Stopped", color: "var(--status-idle)" },
} satisfies ChartConfig;

export function OutcomeChart({ data, height = 200 }: { data: DayBucket[]; height?: number }) {
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
          width={36}
          tickLine={false}
          axisLine={false}
          tick={{ fill: "var(--viz-muted)", fontSize: 11 }}
        />
        <ChartTooltip
          cursor={{ fill: "var(--muted)", fillOpacity: 0.4 }}
          content={<ChartTooltipContent indicator="dot" />}
        />
        <ChartLegend content={<ChartLegendContent />} />
        {/* A 1.5px stroke in the surface colour is the 2px spacer between
            stacked segments — without it two adjacent statuses touch and read as
            one band. The topmost series carries the rounded data-end; the stack
            stays anchored square to the baseline. */}
        <Bar
          dataKey="passed"
          stackId="o"
          fill="var(--color-passed)"
          stroke="var(--viz-surface)"
          strokeWidth={1.5}
        />
        <Bar
          dataKey="verifyFailed"
          stackId="o"
          fill="var(--color-verifyFailed)"
          stroke="var(--viz-surface)"
          strokeWidth={1.5}
        />
        <Bar
          dataKey="failed"
          stackId="o"
          fill="var(--color-failed)"
          stroke="var(--viz-surface)"
          strokeWidth={1.5}
        />
        <Bar
          dataKey="stopped"
          stackId="o"
          fill="var(--color-stopped)"
          stroke="var(--viz-surface)"
          strokeWidth={1.5}
          radius={[3, 3, 0, 0]}
        />
      </BarChart>
    </ChartContainer>
  );
}
