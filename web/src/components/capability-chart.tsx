"use client";

import {
  Bar,
  BarChart,
  CartesianGrid,
  Cell,
  LabelList,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";
import { SCORE_AXES, SCORES } from "@/lib/comparison";
import { cn } from "@/lib/utils";

/**
 * Emphasis, not a rainbow: the series that is the point wears the accent, the
 * two context series wear grays. Scores come straight from the table above, so
 * the two cannot disagree.
 *
 * Palette validated for the light surface (#ffffff) — every adjacent pair clears
 * the CVD and normal-vision separation floors, and all three clear 3:1 contrast.
 */
const SERIES = [
  { id: "sandbox", name: "sandbox-cli", color: "#059669", emphasis: true },
  { id: "builtin", name: "Built-in agent sandboxes", color: "#3f3f46", emphasis: false },
  { id: "cloud", name: "Cloud microVMs", color: "#8e8e96", emphasis: false },
] as const;

const DATA = SCORE_AXES.map((axis) => {
  const row: Record<string, string | number> = { axis };
  for (const s of SCORES) row[s.id] = s.values[axis];
  return row;
});

export function CapabilityChart({ className }: { className?: string }) {
  return (
    <figure className={cn("overflow-hidden rounded-2xl border bg-card", className)}>
      <figcaption className="flex flex-col gap-2.5 border-b px-4 py-3.5 sm:px-5">
        <span className="text-sm font-medium">
          Six axes, all &ldquo;higher is better&rdquo;, scored 0&ndash;5
        </span>
        <ul className="flex flex-wrap gap-x-4 gap-y-1.5">
          {SERIES.map((s) => (
            <li key={s.id} className="flex items-center gap-1.5 text-xs text-muted-foreground">
              <span
                aria-hidden="true"
                className="size-2 rounded-[2px]"
                style={{ backgroundColor: s.color }}
              />
              {s.name}
            </li>
          ))}
        </ul>
      </figcaption>

      <div className="h-[420px] w-full px-2 py-4 sm:px-4">
        <ResponsiveContainer width="100%" height="100%">
          <BarChart
            data={DATA}
            layout="vertical"
            margin={{ top: 4, right: 34, bottom: 4, left: 8 }}
            barCategoryGap="26%"
            barGap={2}
          >
            <CartesianGrid
              horizontal={false}
              stroke="var(--grid-line)"
              strokeWidth={1}
            />
            <XAxis
              type="number"
              domain={[0, 5]}
              ticks={[0, 1, 2, 3, 4, 5]}
              tickLine={false}
              axisLine={false}
              tick={{ fill: "var(--muted-foreground)", fontSize: 11 }}
            />
            <YAxis
              type="category"
              dataKey="axis"
              width={128}
              tickLine={false}
              axisLine={false}
              tick={{ fill: "var(--foreground)", fontSize: 12 }}
            />
            <Tooltip
              cursor={{ fill: "var(--muted)", opacity: 0.6 }}
              contentStyle={{
                borderRadius: 10,
                border: "1px solid var(--border)",
                background: "var(--card)",
                fontSize: 12,
                boxShadow: "0 12px 32px -20px rgba(9,9,11,0.4)",
              }}
              labelStyle={{ color: "var(--foreground)", fontWeight: 600 }}
              formatter={(value, _name, item) => [
                `${value} / 5`,
                SERIES.find((s) => s.id === item?.dataKey)?.name ?? "",
              ]}
            />
            {SERIES.map((s) => (
              <Bar key={s.id} dataKey={s.id} barSize={9} radius={[0, 4, 4, 0]} isAnimationActive={false}>
                {DATA.map((_, i) => (
                  <Cell key={i} fill={s.color} />
                ))}
                {s.emphasis ? (
                  <LabelList
                    dataKey={s.id}
                    position="right"
                    offset={7}
                    fill="var(--muted-foreground)"
                    fontSize={11}
                  />
                ) : null}
              </Bar>
            ))}
          </BarChart>
        </ResponsiveContainer>
      </div>

      <p className="border-t bg-surface px-4 py-3 text-xs leading-relaxed text-muted-foreground sm:px-5">
        Only the emphasised series is labelled; hover any bar for its score, and the table above is
        the full, readable version of the same judgement. Isolation is the axis where sandbox-cli
        deliberately does not win — a shared-kernel container is not a microVM, which is why{" "}
        <code className="font-mono">--runtime</code> exists.
      </p>
    </figure>
  );
}
