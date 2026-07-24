"use client";

import { useState } from "react";
import {
  PolarAngleAxis,
  PolarGrid,
  PolarRadiusAxis,
  Radar,
  RadarChart,
  ResponsiveContainer,
} from "recharts";
import { RADAR, SERIES, type Series } from "@/lib/comparison";
import { cn } from "@/lib/utils";

/**
 * Six axes, all "higher is better", so the enclosed area reads directly as
 * how well contained *and* how practical each option is. Series can be toggled
 * so the shapes stay comparable instead of turning into a hairball.
 */
export function AttackSurfaceChart() {
  const [active, setActive] = useState<Set<Series["key"]>>(
    () => new Set<Series["key"]>(["sandbox", "host"]),
  );

  const toggle = (key: Series["key"]) =>
    setActive((prev) => {
      const next = new Set(prev);
      if (next.has(key)) {
        if (next.size > 1) next.delete(key); // never leave the chart empty
      } else {
        next.add(key);
      }
      return next;
    });

  return (
    <div className="flex flex-col gap-4 rounded-xl border bg-card p-5 shadow-sm">
      <div className="flex flex-wrap items-center gap-2">
        {SERIES.map((s) => {
          const on = active.has(s.key);
          return (
            <button
              key={s.key}
              onClick={() => toggle(s.key)}
              aria-pressed={on}
              className={cn(
                "flex items-center gap-2 rounded-md border px-2.5 py-1.5 font-mono text-xs transition-all",
                on ? "bg-background" : "opacity-45 hover:opacity-80",
              )}
              style={on ? { borderColor: s.color, color: s.color } : undefined}
            >
              <span className="size-2 rounded-full" style={{ background: s.color }} />
              {s.label}
            </button>
          );
        })}
      </div>

      <div className="h-[360px] w-full">
        <ResponsiveContainer width="100%" height="100%">
          <RadarChart data={RADAR as unknown as Record<string, unknown>[]} outerRadius="72%">
            <PolarGrid stroke="var(--border)" />
            <PolarAngleAxis
              dataKey="axis"
              tick={{ fill: "var(--muted-foreground)", fontSize: 11, fontFamily: "var(--font-plex-mono)" }}
            />
            <PolarRadiusAxis domain={[0, 10]} tick={false} axisLine={false} />
            {SERIES.filter((s) => active.has(s.key)).map((s) => (
              <Radar
                key={s.key}
                name={s.label}
                dataKey={s.key}
                stroke={s.color}
                fill={s.color}
                fillOpacity={0.16}
                strokeWidth={2}
                isAnimationActive
                animationDuration={550}
              />
            ))}
          </RadarChart>
        </ResponsiveContainer>
      </div>

      <p className="text-center text-xs text-muted-foreground">
        Every axis is scored so higher is better — a larger enclosed shape is a better trade overall.
        Scores are the maintainers&apos; assessment, not a benchmark.
      </p>
    </div>
  );
}
