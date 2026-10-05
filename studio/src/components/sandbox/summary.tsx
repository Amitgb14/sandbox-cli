"use client";

import { useNode } from "@/lib/api/queries";
import { useCaller } from "@/lib/caller";
import { formatMiB } from "@/lib/format";
import { cn } from "@/lib/utils";
import type { Sandbox } from "@/lib/types";

function Tile({ label, value, of, used, children }: { label: string; value: string; of?: string; used?: number; children?: React.ReactNode }) {
  return (
    <div className="flex flex-col gap-2 rounded-xl border bg-card px-4 py-3">
      <span className="text-xs text-muted-foreground">{label}</span>
      <div className="flex flex-wrap items-baseline gap-x-1.5">
        <span className="text-xl font-medium whitespace-nowrap tabular-nums">{value}</span>
        {of ? <span className="text-xs text-muted-foreground tabular-nums">of {of}</span> : null}
      </div>
      {used !== undefined ? (
        <div className="h-1 overflow-hidden rounded-full bg-muted" role="meter" aria-label={label} aria-valuenow={Math.round(used * 100)} aria-valuemin={0} aria-valuemax={100}>
          <div className={cn("h-full rounded-full", used > 0.85 ? "bg-caution" : "bg-status-running")} style={{ width: `${Math.min(100, used * 100)}%` }} />
        </div>
      ) : (
        children
      )}
    </div>
  );
}

/**
 * The few numbers worth a glance before the list: how many sandboxes are up,
 * and how much of the machine they have been given. Given, not used: these are
 * allocations, from a plain sandboxd's GET /v1/node, else — on a gateway —
 * summed from the list, without a capacity to measure them against.
 */
export function SandboxSummary({ sandboxes }: { sandboxes: Sandbox[] }) {
  // A gateway is never asked for a node's status: it has none to give.
  const { data: node } = useNode(useCaller().kind === "sandboxd");
  const live = sandboxes.filter((s) => s.state !== "terminated");
  const running = live.filter((s) => s.state === "running").length;
  const suspended = live.length - running;

  const summed = live.reduce(
    (a, s) => ({ cpus: a.cpus + s.cpus, memory_mb: a.memory_mb + s.memory_mb, disk_mb: a.disk_mb + s.disk_mb }),
    { cpus: 0, memory_mb: 0, disk_mb: 0 },
  );
  // A capacity of 0 is one sandboxd could not measure, not a full machine:
  // its Free is 0 too, so what was given is the list's sum, not 0 - 0.
  const from = (k: "cpus" | "memory_mb" | "disk_mb") => (node?.capacity[k] ? node.capacity[k] - node.free[k] : summed[k]);
  const given = { cpus: from("cpus"), memory_mb: from("memory_mb"), disk_mb: from("disk_mb") };
  const share = (g: number, cap?: number) => (cap ? g / cap : undefined);
  const cpus = (n: number) => `${+n.toFixed(1)}`;

  return (
    <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
      <Tile label="Running" value={String(running)}>
        <span className="text-xs text-muted-foreground">
          {suspended ? `${suspended} suspended` : "none suspended"}
          {node?.cordoned ? " · cordoned" : ""}
        </span>
      </Tile>
      <Tile label="vCPU given" value={cpus(given.cpus)} of={node?.capacity.cpus ? cpus(node.capacity.cpus) : undefined} used={share(given.cpus, node?.capacity.cpus)}>
        <span className="text-xs text-muted-foreground">to running and suspended</span>
      </Tile>
      <Tile label="Memory given" value={formatMiB(given.memory_mb)} of={node?.capacity.memory_mb ? formatMiB(node.capacity.memory_mb) : undefined} used={share(given.memory_mb, node?.capacity.memory_mb)}>
        <span className="text-xs text-muted-foreground">to running and suspended</span>
      </Tile>
      <Tile label="Disk given" value={formatMiB(given.disk_mb)} of={node?.capacity.disk_mb ? formatMiB(node.capacity.disk_mb) : undefined} used={share(given.disk_mb, node?.capacity.disk_mb)}>
        <span className="text-xs text-muted-foreground">to running and suspended</span>
      </Tile>
    </div>
  );
}
