"use client";

import { useEffect, useState } from "react";
import { Check, Loader2 } from "lucide-react";
import { formatBytes, formatDuration } from "@/lib/format";
import { cn } from "@/lib/utils";
import type { SnapshotProgress } from "@/lib/types";

/** A percentage of the read, or null where it has not begun or has no estimate. */
export function capturePercent(p: SnapshotProgress): number | null {
  if (p.phase !== "capture") return 100;
  if (!p.bytes || !p.estimated_bytes || p.estimated_bytes < 0) return null;
  // The estimate is the guest's: the bar stops short of full rather than
  // claiming done while the read goes on.
  return Math.max(0, Math.min(99, Math.floor((p.bytes / p.estimated_bytes) * 100)));
}

/** Which of the three steps a snapshot is in: 0 preparing, 1 reading, 2 storing. */
export function snapshotStep(p: SnapshotProgress): 0 | 1 | 2 {
  if (p.phase === "store") return 2;
  return p.bytes > 0 ? 1 : 0;
}

export const STEP_NAMES = ["Preparing", "Reading files", "Storing"] as const;

function useElapsed(since: string): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(t);
  }, []);
  return Math.max(0, now - Date.parse(since));
}

/**
 * A snapshot being taken, from what sandboxd reports on the sandbox, as three
 * steps: preparing (the runtime readies the disk and reads nothing yet),
 * reading its files (counted, against an estimate where there is one) and
 * storing them where a fork starts from (which reports nothing as it goes).
 * Only the reading has a percentage; the others show time, rather than a bar
 * that would have to be made up. Any view of the sandbox shows the same, so it
 * survives the panel being closed or the page reloaded.
 */
export function SnapshotProgressBar({ p, className }: { p: SnapshotProgress; className?: string }) {
  const elapsed = useElapsed(p.started_at);
  const step = snapshotStep(p);
  const pct = capturePercent(p);
  const label = p.scheduled ? "Taking a scheduled snapshot" : "Taking a snapshot";
  const reading = step === 1 && pct !== null;
  const detail =
    step === 0
      ? "the runtime is getting the disk ready"
      : step === 1
        ? p.estimated_bytes
          ? `${formatBytes(p.bytes)} of about ${formatBytes(p.estimated_bytes)}`
          : `${formatBytes(p.bytes)} read`
        : `${formatBytes(p.bytes)} read; keeping it for forks`;
  return (
    <div className={cn("flex min-w-64 flex-col gap-2 rounded-md border border-status-running/30 bg-status-running/10 px-3 py-2", className)}>
      <div className="flex items-center gap-2 text-xs">
        <Loader2 className="size-3.5 animate-spin text-status-running" />
        <span className="font-medium">{label}</span>
        <span className="ml-auto tabular-nums text-muted-foreground">{formatDuration(elapsed)}</span>
      </div>
      <ol className="grid grid-cols-3 gap-1.5" aria-label="Snapshot steps">
        {STEP_NAMES.map((name, i) => {
          const done = i < step;
          const now = i === step;
          return (
            <li key={name} aria-current={now ? "step" : undefined} className="flex flex-col gap-1">
              <div
                role={now ? "progressbar" : undefined}
                aria-label={now ? name : undefined}
                aria-valuemin={now ? 0 : undefined}
                aria-valuemax={now ? 100 : undefined}
                aria-valuenow={now && reading ? pct! : undefined}
                className="relative h-1.5 overflow-hidden rounded-full bg-status-running/20"
              >
                {done ? <div className="absolute inset-0 bg-status-running" /> : null}
                {now && reading ? <div className="h-full bg-status-running transition-[width] duration-500" style={{ width: `${pct}%` }} /> : null}
                {now && !reading ? <div className="absolute inset-0 animate-pulse bg-status-running/60" /> : null}
              </div>
              <span className={cn("flex items-center gap-1 text-[11px]", now ? "font-medium text-foreground" : "text-muted-foreground")}>
                {done ? <Check className="size-3 text-status-running" /> : null}
                {name}
                {now && reading ? <span className="ml-auto tabular-nums">{pct}%</span> : null}
              </span>
            </li>
          );
        })}
      </ol>
      <span className="text-[11px] tabular-nums text-muted-foreground">{detail}</span>
    </div>
  );
}
