"use client";

import Link from "next/link";
import type { ColumnDef } from "@tanstack/react-table";
import { FileDiff, GitBranch } from "lucide-react";
import { Checkbox } from "@/components/ui/checkbox";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { ColumnHeader } from "@/components/data-table/column-header";
import { StatusBadge } from "@/components/common/status-badge";
import { KindBadge, NetworkBadge, ProfileBadge } from "@/components/common/posture-badges";
import { RunRowActions } from "@/components/runs/row-actions";
import { runOutcome, type Run } from "@/lib/types";
import {
  DASH,
  formatBytesShort,
  formatDuration,
  formatRelative,
  shortId,
} from "@/lib/format";

/**
 * The runs table.
 *
 * Two things it is careful about, both inherited from the CLI's own listing:
 *
 *   - **Values that came from the repository are text, not markup.** A branch
 *     name is written by whoever pushed it, so it renders inside a `<span>` with
 *     no interpretation — the old text-parsing `ps` was built around exactly this
 *     regression, and a tab-separated table should not be forgeable by a branch
 *     name.
 *   - **KIND is a column, not a detail.** Whether a container belongs to a fleet
 *     decides what `fleet stop --all` reaches and what `max_parallel` counts, and
 *     the listing is where somebody decides what to kill.
 */
export const runColumns: ColumnDef<Run>[] = [
  {
    id: "select",
    header: ({ table }) => (
      <Checkbox
        checked={
          table.getIsAllPageRowsSelected() ||
          (table.getIsSomePageRowsSelected() && "indeterminate")
        }
        onCheckedChange={(v) => table.toggleAllPageRowsSelected(!!v)}
        aria-label="Select all rows on this page"
        className="translate-y-[2px]"
      />
    ),
    cell: ({ row }) => (
      <Checkbox
        checked={row.getIsSelected()}
        onCheckedChange={(v) => row.toggleSelected(!!v)}
        aria-label="Select row"
        className="translate-y-[2px]"
      />
    ),
    enableSorting: false,
    enableHiding: false,
    size: 32,
  },
  {
    id: "outcome",
    accessorFn: (run) => runOutcome(run),
    header: ({ column }) => <ColumnHeader column={column} title="Status" />,
    cell: ({ row }) => (
      <StatusBadge outcome={runOutcome(row.original)} exitCode={row.original.exitCode} />
    ),
    filterFn: (row, id, value: string[]) => value.includes(row.getValue(id)),
    meta: { label: "Status" },
    size: 130,
  },
  {
    id: "target",
    accessorFn: (run) => `${run.branch ?? run.name} ${run.repoName} ${run.id}`,
    header: ({ column }) => <ColumnHeader column={column} title="Branch" />,
    cell: ({ row }) => {
      const run = row.original;
      return (
        <div className="min-w-0 max-w-[22rem]">
          <Link
            href={`/runs/${run.id}`}
            className="flex min-w-0 items-center gap-1.5 font-mono text-sm hover:underline"
          >
            <GitBranch className="size-3.5 shrink-0 text-muted-foreground" />
            {/* Text from the repository, rendered as text. */}
            <span className="truncate">{run.branch ?? run.name}</span>
          </Link>
          <p className="mt-0.5 truncate text-xs text-muted-foreground">
            {run.repoName}
            {run.base && <span> → {run.base}</span>}
            <span className="ml-1.5 font-mono opacity-60">{shortId(run.id, 8)}</span>
          </p>
        </div>
      );
    },
    meta: { label: "Branch" },
  },
  {
    id: "agent",
    accessorFn: (run) => run.agent ?? "plain run",
    header: ({ column }) => <ColumnHeader column={column} title="Agent" />,
    cell: ({ row }) => {
      const run = row.original;
      return (
        <div className="space-y-0.5">
          <p className="font-mono text-xs">{run.agent ?? "plain run"}</p>
          {run.prompt ? (
            <Tooltip>
              <TooltipTrigger asChild>
                <p className="max-w-[16rem] cursor-help truncate text-xs text-muted-foreground">
                  {run.prompt}
                </p>
              </TooltipTrigger>
              <TooltipContent className="max-w-md">{run.prompt}</TooltipContent>
            </Tooltip>
          ) : (
            <p className="max-w-[16rem] truncate font-mono text-xs text-muted-foreground">
              {run.command.join(" ")}
            </p>
          )}
        </div>
      );
    },
    filterFn: (row, id, value: string[]) => value.includes(row.getValue(id)),
    meta: { label: "Agent" },
  },
  {
    id: "kind",
    accessorFn: (run) => run.kind,
    header: ({ column }) => <ColumnHeader column={column} title="Kind" />,
    cell: ({ row }) => <KindBadge kind={row.original.kind} />,
    filterFn: (row, id, value: string[]) => value.includes(row.getValue(id)),
    meta: { label: "Kind" },
    size: 96,
  },
  {
    id: "profile",
    accessorFn: (run) => run.profile,
    header: ({ column }) => <ColumnHeader column={column} title="Profile" />,
    cell: ({ row }) => <ProfileBadge profile={row.original.profile} />,
    filterFn: (row, id, value: string[]) => value.includes(row.getValue(id)),
    meta: { label: "Profile" },
    size: 96,
  },
  {
    id: "network",
    accessorFn: (run) => run.network.mode,
    header: ({ column }) => <ColumnHeader column={column} title="Egress" />,
    cell: ({ row }) => <NetworkBadge network={row.original.network} />,
    filterFn: (row, id, value: string[]) => value.includes(row.getValue(id)),
    meta: { label: "Egress" },
    size: 90,
  },
  {
    id: "diff",
    accessorFn: (run) => (run.diffStat?.insertions ?? 0) + (run.diffStat?.deletions ?? 0),
    header: ({ column }) => <ColumnHeader column={column} title="Changes" />,
    cell: ({ row }) => {
      const d = row.original.diffStat;
      if (!d || d.files === 0) {
        return <span className="text-xs text-muted-foreground">{DASH}</span>;
      }
      return (
        <Tooltip>
          <TooltipTrigger asChild>
            <span className="flex cursor-help items-center gap-1.5 font-mono text-xs tabular-nums">
              <FileDiff className="size-3.5 text-muted-foreground" />
              <span className="text-status-good">+{d.insertions}</span>
              <span className="text-status-critical">-{d.deletions}</span>
            </span>
          </TooltipTrigger>
          <TooltipContent>
            {d.files} {d.files === 1 ? "file" : "files"} changed in the workspace
          </TooltipContent>
        </Tooltip>
      );
    },
    meta: { label: "Changes" },
    size: 110,
  },
  {
    id: "memory",
    accessorFn: (run) => run.latestMetrics?.memBytes ?? -1,
    header: ({ column }) => <ColumnHeader column={column} title="Memory" />,
    cell: ({ row }) => {
      const m = row.original.latestMetrics;
      // A finished run has no current reading, and a live one that was never
      // sampled has none either. Neither is zero.
      if (!m) return <span className="text-xs text-muted-foreground">{DASH}</span>;
      return (
        <span className="font-mono text-xs tabular-nums">{formatBytesShort(m.memBytes)}</span>
      );
    },
    meta: { label: "Memory" },
    size: 90,
  },
  {
    id: "duration",
    accessorFn: (run) => run.durationMs ?? 0,
    header: ({ column }) => <ColumnHeader column={column} title="Duration" />,
    cell: ({ row }) => (
      <span className="font-mono text-xs tabular-nums">
        {formatDuration(row.original.durationMs)}
        {row.original.state === "running" && (
          <span className="ml-1 text-muted-foreground">so far</span>
        )}
      </span>
    ),
    meta: { label: "Duration" },
    size: 110,
  },
  {
    id: "started",
    accessorFn: (run) => new Date(run.startedAt ?? run.createdAt).getTime(),
    header: ({ column }) => <ColumnHeader column={column} title="Started" />,
    cell: ({ row }) => (
      <span className="text-xs whitespace-nowrap text-muted-foreground">
        {formatRelative(row.original.startedAt ?? row.original.createdAt)}
      </span>
    ),
    meta: { label: "Started" },
    size: 100,
  },
  {
    id: "actions",
    cell: ({ row }) => <RunRowActions run={row.original} />,
    enableSorting: false,
    enableHiding: false,
    size: 44,
  },
];
