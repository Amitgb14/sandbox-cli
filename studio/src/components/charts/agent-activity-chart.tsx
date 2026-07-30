"use client";

import { Bar, BarChart, CartesianGrid, LabelList, XAxis, YAxis } from "recharts";
import {
  ChartContainer,
  ChartTooltip,
  ChartTooltipContent,
  type ChartConfig,
} from "@/components/ui/chart";
import type { AgentActivity } from "@/lib/derive";

/**
 * Runs per agent — a horizontal bar chart, because the categories are names and
 * names read left to right.
 *
 * One series, directly labelled, so no legend and no y-axis ticks fighting the
 * bars for the same space. Ordered by magnitude, which is what makes a bar chart
 * worth drawing at all.
 */
const config = {
  runs: { label: "Runs", color: "var(--chart-1)" },
} satisfies ChartConfig;

export function AgentActivityChart({
  data,
  height = 220,
}: {
  data: AgentActivity[];
  height?: number;
}) {
  return (
    <ChartContainer config={config} className="w-full" style={{ height }}>
      <BarChart
        data={data}
        layout="vertical"
        margin={{ top: 0, right: 28, left: 4, bottom: 0 }}
        barCategoryGap={6}
      >
        <CartesianGrid horizontal={false} stroke="var(--viz-grid)" strokeDasharray="2 4" />
        <XAxis type="number" hide allowDecimals={false} />
        <YAxis
          type="category"
          dataKey="agent"
          tickLine={false}
          axisLine={false}
          width={78}
          tick={{ fill: "var(--viz-muted)", fontSize: 11 }}
        />
        <ChartTooltip
          cursor={{ fill: "var(--muted)", fillOpacity: 0.4 }}
          content={<ChartTooltipContent indicator="dot" />}
        />
        <Bar dataKey="runs" fill="var(--color-runs)" radius={[0, 4, 4, 0]} barSize={14}>
          {/* Direct labels: the value belongs next to its bar, and text wears a
              text token rather than the series colour. */}
          <LabelList
            dataKey="runs"
            position="right"
            offset={8}
            className="fill-muted-foreground"
            fontSize={11}
          />
        </Bar>
      </BarChart>
    </ChartContainer>
  );
}
