"use client";

import { useEffect, useMemo, useState } from "react";
import { useRouter } from "next/navigation";
import {
  flexRender,
  getCoreRowModel,
  getFacetedRowModel,
  getFacetedUniqueValues,
  getFilteredRowModel,
  getPaginationRowModel,
  getSortedRowModel,
  useReactTable,
  type ColumnFiltersState,
  type SortingState,
  type VisibilityState,
} from "@tanstack/react-table";
import { Activity, Search, Skull, Square, X } from "lucide-react";
import { toast } from "sonner";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { EmptyState } from "@/components/common/empty-state";
import { FacetedFilter } from "@/components/data-table/faceted-filter";
import { DataTablePagination } from "@/components/data-table/pagination";
import { ViewOptions } from "@/components/data-table/view-options";
import { runColumns } from "@/components/runs/columns";
import { useKillRun, useStopRun } from "@/lib/api/queries";
import { pluralize } from "@/lib/format";
import { cn } from "@/lib/utils";
import type { Run } from "@/lib/types";
import {
  AGENT_FACETS,
  EGRESS_FACETS,
  KIND_FACETS,
  OUTCOME_FACETS,
  PROFILE_FACETS,
} from "@/components/runs/facets";

export function RunsTable({ runs, loading }: { runs: Run[]; loading?: boolean }) {
  const router = useRouter();
  const [sorting, setSorting] = useState<SortingState>([{ id: "started", desc: true }]);
  const [columnFilters, setColumnFilters] = useState<ColumnFiltersState>([]);
  const [columnVisibility, setColumnVisibility] = useState<VisibilityState>({
    memory: false,
    profile: false,
  });
  const [rowSelection, setRowSelection] = useState({});
  const [query, setQuery] = useState("");

  const stop = useStopRun();
  const kill = useKillRun();

  const table = useReactTable({
    data: runs,
    columns: runColumns,
    state: { sorting, columnFilters, columnVisibility, rowSelection, globalFilter: query },
    onSortingChange: setSorting,
    onColumnFiltersChange: setColumnFilters,
    onColumnVisibilityChange: setColumnVisibility,
    onRowSelectionChange: setRowSelection,
    onGlobalFilterChange: setQuery,
    getRowId: (run) => run.id,
    // One free-text field searching branch, repo, agent, prompt, command and id.
    // Five separate search boxes is five decisions before the first result.
    globalFilterFn: (row, _columnId, value: string) => {
      const run = row.original;
      const haystack = [
        run.branch,
        run.name,
        run.repoName,
        run.agent,
        run.prompt,
        run.command.join(" "),
        run.id,
        run.base,
      ]
        .filter(Boolean)
        .join(" ")
        .toLowerCase();
      return haystack.includes(value.toLowerCase());
    },
    getCoreRowModel: getCoreRowModel(),
    getFilteredRowModel: getFilteredRowModel(),
    getSortedRowModel: getSortedRowModel(),
    getPaginationRowModel: getPaginationRowModel(),
    getFacetedRowModel: getFacetedRowModel(),
    getFacetedUniqueValues: getFacetedUniqueValues(),
    initialState: { pagination: { pageSize: 20 } },
    // Off, and replaced by the effect below. Auto-reset schedules its page-index
    // update through the table's own microtask queue, which on the first pass
    // runs before this component has finished mounting — React reports that as
    // "a side-effect in your render function". The behaviour is worth keeping
    // though: filtering down to five rows while you are on page three should
    // not show you an empty page.
    autoResetPageIndex: false,
  });

  // What auto-reset did, from where React asks for it. Deliberately not keyed on
  // the row count: rows change as runs start and finish, and a table that jumped
  // you back to page one every time an agent exited would be unusable.
  useEffect(() => {
    table.setPageIndex(0);
  }, [table, columnFilters, query]);

  const filtered = columnFilters.length > 0 || query.length > 0;
  const selectedRuns = table.getFilteredSelectedRowModel().rows.map((r) => r.original);
  const selectedLive = useMemo(
    () => selectedRuns.filter((r) => r.state === "running"),
    [selectedRuns],
  );

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-2">
        <div className="relative">
          <Search className="absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2 text-muted-foreground" />
          <Input
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="Branch, agent, prompt, container id…"
            className="h-8 w-full pl-8 sm:w-[19rem]"
          />
        </div>

        <FacetedFilter column={table.getColumn("outcome")} title="Status" options={OUTCOME_FACETS} />
        <FacetedFilter column={table.getColumn("agent")} title="Agent" options={AGENT_FACETS} />
        <FacetedFilter column={table.getColumn("kind")} title="Kind" options={KIND_FACETS} />
        <FacetedFilter column={table.getColumn("network")} title="Egress" options={EGRESS_FACETS} />
        <FacetedFilter
          column={table.getColumn("profile")}
          title="Profile"
          options={PROFILE_FACETS}
        />

        {filtered && (
          <Button
            variant="ghost"
            size="sm"
            className="h-8 px-2 text-xs"
            onClick={() => {
              setColumnFilters([]);
              setQuery("");
            }}
          >
            Reset
            <X className="size-3.5" />
          </Button>
        )}

        <ViewOptions table={table} />
      </div>

      {/* Bulk actions appear only when something is selected, and only offer
          what applies: a finished run has nothing to stop. */}
      {selectedLive.length > 0 && (
        <div className="flex flex-wrap items-center gap-2 rounded-lg border bg-card/60 px-3 py-2">
          <span className="text-xs text-muted-foreground">
            {pluralize(selectedLive.length, "live sandbox", "live sandboxes")} selected
          </span>
          <div className="ml-auto flex items-center gap-2">
            <Button
              variant="outline"
              size="sm"
              className="h-7 text-xs"
              onClick={() => {
                selectedLive.forEach((r) => stop.mutate(r.id));
                toast.success(`Asked ${pluralize(selectedLive.length, "sandbox", "sandboxes")} to exit`);
                setRowSelection({});
              }}
            >
              <Square className="size-3.5" />
              Stop all
            </Button>
            <Button
              variant="outline"
              size="sm"
              className="h-7 border-destructive/40 text-xs text-destructive hover:bg-destructive/10 hover:text-destructive"
              onClick={() => {
                selectedLive.forEach((r) => kill.mutate(r.id));
                toast.warning(`Killed ${pluralize(selectedLive.length, "sandbox", "sandboxes")}`, {
                  description: "None of them got to finish what they were writing.",
                });
                setRowSelection({});
              }}
            >
              <Skull className="size-3.5" />
              Kill all
            </Button>
          </div>
        </div>
      )}

      <div className="overflow-hidden rounded-lg border">
        <Table>
          <TableHeader className="bg-muted/40">
            {table.getHeaderGroups().map((group) => (
              <TableRow key={group.id} className="hover:bg-transparent">
                {group.headers.map((header) => (
                  <TableHead
                    key={header.id}
                    style={{ width: header.getSize() !== 150 ? header.getSize() : undefined }}
                    className="h-9 whitespace-nowrap"
                  >
                    {header.isPlaceholder
                      ? null
                      : flexRender(header.column.columnDef.header, header.getContext())}
                  </TableHead>
                ))}
              </TableRow>
            ))}
          </TableHeader>
          <TableBody>
            {loading ? (
              Array.from({ length: 8 }).map((_, i) => (
                <TableRow key={i}>
                  {table.getVisibleLeafColumns().map((col) => (
                    <TableCell key={col.id}>
                      <Skeleton className="h-5 w-full" />
                    </TableCell>
                  ))}
                </TableRow>
              ))
            ) : table.getRowModel().rows.length === 0 ? (
              <TableRow className="hover:bg-transparent">
                {/* Visible leaf columns, not every declared column: `memory` and
                    `profile` start hidden, so the declared count would span more
                    cells than the row has and skew the empty state. */}
                <TableCell colSpan={table.getVisibleLeafColumns().length} className="p-0">
                  <EmptyState
                    icon={Activity}
                    title={filtered ? "No runs match these filters" : "No runs recorded yet"}
                    description={
                      filtered
                        ? "Clear a filter, or widen the search. The counts inside each filter already account for the others."
                        : "Runs appear here as soon as one starts — and stay after it exits, because how a run ended is the point."
                    }
                    className="border-0"
                    action={
                      filtered ? (
                        <Button
                          variant="outline"
                          size="sm"
                          onClick={() => {
                            setColumnFilters([]);
                            setQuery("");
                          }}
                        >
                          Reset filters
                        </Button>
                      ) : undefined
                    }
                  />
                </TableCell>
              </TableRow>
            ) : (
              table.getRowModel().rows.map((row) => (
                <TableRow
                  key={row.id}
                  data-state={row.getIsSelected() && "selected"}
                  className={cn(
                    "cursor-pointer",
                    row.original.state === "running" && "bg-status-running/[0.04]",
                  )}
                  onClick={(e) => {
                    // Clicks that land on a control belong to that control.
                    const el = e.target as HTMLElement;
                    if (el.closest("button,a,input,[role=checkbox],[role=menuitem]")) return;
                    router.push(`/runs/${row.original.id}`);
                  }}
                >
                  {row.getVisibleCells().map((cell) => (
                    <TableCell key={cell.id} className="py-2">
                      {flexRender(cell.column.columnDef.cell, cell.getContext())}
                    </TableCell>
                  ))}
                </TableRow>
              ))
            )}
          </TableBody>
        </Table>
      </div>

      <DataTablePagination table={table} noun="run" />
    </div>
  );
}
