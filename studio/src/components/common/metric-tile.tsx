"use client";

import { useEffect, useId, useState } from "react";
import { Area, AreaChart, ResponsiveContainer } from "recharts";
import { Card, CardContent } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { cn } from "@/lib/utils";
import { DASH } from "@/lib/format";
import type { LucideIcon } from "lucide-react";

/**
 * A stat tile: one number, its unit, and just enough context to know whether it
 * is good news.
 *
 * Three rules it enforces so the row reads as one system:
 *
 *   - The value is the loudest thing, in proportional figures (a lone number has
 *     nothing to align with, so tabular figures would only widen the digits).
 *   - `null` renders an em dash with a reason, never `0`. "Nothing finished yet"
 *     and "everything failed" must not look the same.
 *   - The sparkline is context, not a second chart: no axes, no grid, no
 *     tooltip. If it needs a tooltip it wants to be a real chart.
 */
export function MetricTile({
  label,
  value,
  unit,
  hint,
  absentReason,
  icon: Icon,
  spark,
  sparkTone = "var(--chart-1)",
  footer,
  className,
  loading,
}: {
  label: string;
  /** `null` means absent — the tile says so rather than showing zero. */
  value: string | number | null;
  unit?: string;
  hint?: string;
  absentReason?: string;
  icon?: LucideIcon;
  spark?: number[];
  sparkTone?: string;
  footer?: React.ReactNode;
  className?: string;
  loading?: boolean;
}) {
  const absent = value === null || value === undefined;

  return (
    <Card className={cn("surface-sheen relative gap-0 overflow-hidden py-4", className)}>
      <CardContent className="px-4">
        <div className="flex items-start justify-between gap-2">
          <div className="flex items-center gap-1.5 text-xs font-medium text-muted-foreground">
            {Icon && <Icon className="size-3.5" aria-hidden />}
            <span>{label}</span>
          </div>
          {hint && (
            <Tooltip>
              <TooltipTrigger asChild>
                <span
                  className="cursor-help text-[11px] text-muted-foreground/60"
                  aria-label={hint}
                >
                  ?
                </span>
              </TooltipTrigger>
              <TooltipContent className="max-w-xs">{hint}</TooltipContent>
            </Tooltip>
          )}
        </div>

        <div className="mt-2 flex items-baseline gap-1.5">
          {loading ? (
            <Skeleton className="h-8 w-20" />
          ) : (
            <>
              <span
                className={cn(
                  "text-3xl leading-none font-semibold tracking-tight",
                  absent && "text-muted-foreground",
                )}
              >
                {absent ? DASH : value}
              </span>
              {!absent && unit && (
                <span className="text-sm text-muted-foreground">{unit}</span>
              )}
            </>
          )}
        </div>

        <div className="mt-1.5 min-h-4 text-xs text-muted-foreground">
          {absent && !loading ? (absentReason ?? "Nothing to report yet") : footer}
        </div>
      </CardContent>

      {spark && spark.length > 1 && !absent && (
        <Sparkline values={spark} tone={sparkTone} />
      )}
    </Card>
  );
}

/**
 * The tile's sparkline.
 *
 * Client-only, and deliberately so: a `ResponsiveContainer` has nothing to
 * measure during a server render, so it prerenders at -1×-1 and warns. The
 * sparkline is context rather than content — nothing is lost by it appearing a
 * frame after the number it sits under, and the reserved height stops the tile
 * from shifting when it does.
 */
function Sparkline({ values, tone }: { values: number[]; tone: string }) {
  const [mounted, setMounted] = useState(false);
  const gradientId = `spark-${useId().replace(/:/g, "")}`;

  useEffect(() => setMounted(true), []);

  return (
    <div className="pointer-events-none h-10 w-full opacity-70">
      {mounted && (
        <ResponsiveContainer width="100%" height="100%" minWidth={0}>
          <AreaChart
            data={values.map((v, i) => ({ i, v }))}
            margin={{ top: 2, right: 0, bottom: 0, left: 0 }}
          >
            <defs>
              <linearGradient id={gradientId} x1="0" y1="0" x2="0" y2="1">
                <stop offset="0%" stopColor={tone} stopOpacity={0.35} />
                <stop offset="100%" stopColor={tone} stopOpacity={0} />
              </linearGradient>
            </defs>
            <Area
              type="monotone"
              dataKey="v"
              stroke={tone}
              strokeWidth={2}
              fill={`url(#${gradientId})`}
              isAnimationActive={false}
            />
          </AreaChart>
        </ResponsiveContainer>
      )}
    </div>
  );
}
