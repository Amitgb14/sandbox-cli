"use client";

import { useState } from "react";
import { toast } from "sonner";
import { CalendarClock } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { useInfo, useSetSnapshotSchedule, useSnapshots } from "@/lib/api/queries";
import { useCan } from "@/lib/caller";
import { formatBytes, formatRelative } from "@/lib/format";
import type { Sandbox } from "@/lib/types";

const INTERVALS = [300, 900, 1800, 3600, 6 * 3600, 24 * 3600];

/** The intervals a schedule may offer: the usual ones the server's minimum allows, and the minimum itself. */
export function scheduleIntervals(min: number): number[] {
  return [...new Set([...INTERVALS.filter((s) => s >= min), Math.max(min, 1)])].sort((a, b) => a - b);
}

export function every(secs: number): string {
  if (secs % 86400 === 0) return `${secs / 86400} day${secs === 86400 ? "" : "s"}`;
  if (secs % 3600 === 0) return `${secs / 3600} h`;
  if (secs % 60 === 0) return `${secs / 60} min`;
  return `${secs} s`;
}

/**
 * A sandbox's snapshots and its schedule: one every so often while it runs,
 * the newest few kept, taken by sandboxd whether or not Studio is open. The
 * choices are what the server's policy allows (limits in /v1/capabilities);
 * a snapshot taken by hand is listed here too, and no schedule removes it.
 */
export function SandboxSnapshots({ sb }: { sb: Sandbox }) {
  const { data: info } = useInfo();
  const caps = info?.capabilities;
  const can = useCan();
  const takes = !!(caps?.capabilities?.memory_snapshot || caps?.capabilities?.disk_snapshot);
  const { data: all } = useSnapshots(takes);
  const set = useSetSnapshotSchedule();
  const [editing, setEditing] = useState(false);
  const min = caps?.limits.min_snapshot_every_secs ?? 300;
  const maxKeep = caps?.limits.max_snapshot_keep ?? 5;
  const options = scheduleIntervals(min);
  const [everySecs, setEvery] = useState(String(sb.snapshot_every_secs || options[0]));
  const [keep, setKeep] = useState(String(sb.snapshot_keep || 1));

  if (!takes) {
    return <p className="text-sm text-muted-foreground">This endpoint takes no snapshots.</p>;
  }
  const mine = (all ?? []).filter((s) => s.sandbox === sb.id).sort((a, b) => b.created_at.localeCompare(a.created_at));
  const on = !!sb.snapshot_every_secs;
  const save = (e: number, k: number) =>
    set
      .mutateAsync({ id: sb.id, every_secs: e, keep: k })
      .then(() => {
        toast.success(e ? `A snapshot every ${every(e)}, the newest ${k} kept` : "Scheduled snapshots stopped");
        setEditing(false);
      })
      .catch((err: Error) => toast.error(err.message));

  return (
    <div className="flex flex-col gap-3">
      <div className="flex items-center justify-between gap-3 text-sm">
        <span className="flex items-center gap-2">
          <CalendarClock className="size-3.5 text-muted-foreground" />
          {on ? (
            <span>
              Every {every(sb.snapshot_every_secs!)} · keep {sb.snapshot_keep}
            </span>
          ) : (
            <span className="text-muted-foreground">No schedule</span>
          )}
        </span>
        {sb.state === "running" && can("sandbox:create") && !editing ? (
          <Button variant="ghost" size="sm" className="h-7 text-xs" onClick={() => setEditing(true)}>
            {on ? "Change" : "Schedule"}
          </Button>
        ) : null}
      </div>
      {editing ? (
        <div className="flex flex-wrap items-center gap-2 rounded-md border bg-muted/20 p-2.5 text-xs">
          <span className="text-muted-foreground">Every</span>
          <Select value={everySecs} onValueChange={setEvery}>
            <SelectTrigger size="sm" className="w-28" aria-label="Snapshot every">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {options.map((s) => (
                <SelectItem key={s} value={String(s)}>
                  {every(s)}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <span className="text-muted-foreground">keep</span>
          <Select value={keep} onValueChange={setKeep}>
            <SelectTrigger size="sm" className="w-16" aria-label="Snapshots kept">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {Array.from({ length: Math.max(maxKeep, 1) }, (_, i) => i + 1).map((n) => (
                <SelectItem key={n} value={String(n)}>
                  {n}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <div className="ml-auto flex gap-1">
            {on ? (
              <Button variant="ghost" size="sm" className="h-7 text-xs" disabled={set.isPending} onClick={() => save(0, 0)}>
                Stop
              </Button>
            ) : null}
            <Button variant="ghost" size="sm" className="h-7 text-xs" onClick={() => setEditing(false)}>
              Cancel
            </Button>
            <Button size="sm" className="h-7 text-xs" disabled={set.isPending} onClick={() => save(Number(everySecs), Number(keep))}>
              Save
            </Button>
          </div>
        </div>
      ) : null}
      {mine.length ? (
        <div className="flex flex-col divide-y">
          {mine.map((s) => (
            <div key={s.id} className="flex items-center gap-2 py-1.5 text-xs first:pt-0 last:pb-0">
              <span className="min-w-0 flex-1 truncate font-mono">{s.id}</span>
              <span className="rounded border px-1.5 py-0.5 text-[10px] text-muted-foreground">{s.scheduled ? "scheduled" : "manual"}</span>
              <span className="text-muted-foreground" title={s.kind === "disk" ? "Its files; a sandbox from it boots afresh" : "Memory, processes and disk"}>
                {s.kind === "disk" ? "files" : "whole"}
              </span>
              <span className="w-16 text-right text-muted-foreground tabular-nums">{formatBytes(s.bytes)}</span>
              <span className="w-16 text-right text-muted-foreground">{formatRelative(s.created_at)}</span>
            </div>
          ))}
        </div>
      ) : (
        <p className="text-xs text-muted-foreground">None of this sandbox yet. Snapshot, above, takes one now.</p>
      )}
    </div>
  );
}
