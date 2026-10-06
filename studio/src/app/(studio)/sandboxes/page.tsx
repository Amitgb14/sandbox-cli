"use client";

import { useMemo, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { toast } from "sonner";
import {
  type ColumnDef,
  type RowSelectionState,
  type SortingState,
  type VisibilityState,
  flexRender,
  getCoreRowModel,
  getFacetedRowModel,
  getFacetedUniqueValues,
  getFilteredRowModel,
  getPaginationRowModel,
  getSortedRowModel,
  useReactTable,
} from "@tanstack/react-table";
import { CalendarClock, Camera, MoreHorizontal, Plus, RefreshCw, Search, SquareTerminal, Trash2 } from "lucide-react";
import { QuickStart } from "@/components/common/quick-start";
import { ColumnHeader } from "@/components/data-table/column-header";
import { FacetedFilter } from "@/components/data-table/faceted-filter";
import { DataTablePagination } from "@/components/data-table/pagination";
import { ViewOptions } from "@/components/data-table/view-options";
import { SandboxDetails } from "@/components/sandbox/details";
import { ResourcesWithMetrics } from "@/components/sandbox/metrics";
import { AgentActivity, StateDot } from "@/components/sandbox/state-dot";
import { SandboxSummary } from "@/components/sandbox/summary";
import { every } from "@/components/sandbox/snapshots";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Input } from "@/components/ui/input";
import { Sheet, SheetContent, SheetTitle } from "@/components/ui/sheet";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { useAgentStates, useInfo, useKill, useSandboxes, useSnapshots, useSuspend } from "@/lib/api/queries";
import { useCan } from "@/lib/caller";
import { formatDateTime, formatRelative } from "@/lib/format";
import { cn } from "@/lib/utils";
import type { Sandbox } from "@/lib/types";

// What the list shows until the State filter says otherwise: everything alive.
// A terminated sandbox is kept for its record, and a busy endpoint has many,
// which buried the few in use among them.
const LIVE = ["pending", "running", "suspended"];

const STATES = [
  { value: "pending", label: "Starting", tone: "var(--status-running)" },
  { value: "running", label: "Running", tone: "var(--status-running)" },
  { value: "suspended", label: "Suspended", tone: "var(--caution)" },
  { value: "terminated", label: "Terminated", tone: "var(--muted-foreground)" },
];

function matches(s: Sandbox, q: string): boolean {
  return (
    s.id.includes(q) ||
    (s.name ?? "").toLowerCase().includes(q) ||
    s.image.toLowerCase().includes(q) ||
    Object.entries(s.labels ?? {}).some(([k, v]) => `${k}=${v}`.toLowerCase().includes(q))
  );
}

/**
 * Studio's home: every sandbox on this endpoint — started from Studio, the
 * CLI, an SDK or anything else that talks to it — under a strip of what they
 * have been given, and when there are none, how to start one.
 *
 * A click on a row opens it in a panel beside the list, so going through
 * several is up and down, not back and forth; the panel opens as a page of its
 * own for a link to keep. Resources are what each sandbox was given; sandboxd
 * reports no live usage, so the screen shows nothing that would have to be
 * made up.
 */
export default function SandboxesPage() {
  const router = useRouter();
  const { data, isLoading, isFetching, error, refetch } = useSandboxes();
  const { data: agentStates } = useAgentStates();
  const { data: info } = useInfo();
  const caps = info?.capabilities?.capabilities ?? {};
  const kill = useKill();
  const suspend = useSuspend();
  const can = useCan();

  const [query, setQuery] = useState("");
  const [sorting, setSorting] = useState<SortingState>([{ id: "created", desc: true }]);
  const [selection, setSelection] = useState<RowSelectionState>({});
  const [visibility, setVisibility] = useState<VisibilityState>({});
  const [open, setOpen] = useState<{ id: string; shell: boolean } | null>(null);
  const [wide, setWide] = useState(false);

  const all = useMemo(() => data ?? [], [data]);
  const agentOf = useMemo(() => new Map((agentStates ?? []).map((a) => [a.sandbox, a])), [agentStates]);
  const takesSnapshots = !!(caps.memory_snapshot || caps.disk_snapshot);
  const { data: snapshots } = useSnapshots(takesSnapshots);
  const snapshotsOf = useMemo(() => {
    const m = new Map<string, number>();
    for (const s of snapshots ?? []) m.set(s.sandbox, (m.get(s.sandbox) ?? 0) + 1);
    return m;
  }, [snapshots]);

  const terminate = (list: Sandbox[]) => {
    const live = list.filter((s) => s.state !== "terminated");
    if (!live.length) return;
    const what = live.length === 1 ? live[0].name || live[0].id : `${live.length} sandboxes`;
    if (!confirm(`Terminate ${what}? Everything in ${live.length === 1 ? "it" : "them"} goes with ${live.length === 1 ? "it" : "them"}.`)) return;
    Promise.allSettled(live.map((s) => kill.mutateAsync(s.id))).then((rs) => {
      const failed = rs.filter((r) => r.status === "rejected") as PromiseRejectedResult[];
      if (failed.length) toast.error(`${failed.length} not terminated: ${(failed[0].reason as Error).message}`);
      else toast.success(live.length === 1 ? "Terminated" : `Terminated ${live.length}`);
      setSelection({});
      if (open && live.some((s) => s.id === open.id)) setOpen(null);
    });
  };

  const columns = useMemo<ColumnDef<Sandbox>[]>(
    () => [
      {
        id: "select",
        header: ({ table }) => (
          <Checkbox
            aria-label="Select all on this page"
            checked={table.getIsAllPageRowsSelected() || (table.getIsSomePageRowsSelected() && "indeterminate")}
            onCheckedChange={(v) => table.toggleAllPageRowsSelected(!!v)}
          />
        ),
        cell: ({ row }) => (
          <Checkbox aria-label={`Select ${row.original.name || row.original.id}`} checked={row.getIsSelected()} onCheckedChange={(v) => row.toggleSelected(!!v)} />
        ),
        enableSorting: false,
        enableHiding: false,
      },
      {
        id: "name",
        accessorFn: (s) => s.name || s.id,
        header: ({ column }) => <ColumnHeader column={column} title="Name" />,
        cell: ({ row: { original: s } }) => (
          <div className="flex min-w-0 flex-col">
            <span className="truncate text-sm font-medium">{s.name || <span className="font-mono">{s.id}</span>}</span>
            {s.name ? <span className="font-mono text-[11px] text-muted-foreground">{s.id}</span> : null}
          </div>
        ),
        enableHiding: false,
      },
      {
        id: "state",
        accessorKey: "state",
        header: "State",
        cell: ({ row }) => {
          const a = row.original.state === "running" ? agentOf.get(row.original.id) : undefined;
          return (
            <div className="flex flex-col gap-0.5">
              <StateDot state={row.original.state} />
              {a ? <AgentActivity agent={a} className="pl-4" /> : null}
            </div>
          );
        },
        filterFn: (row, id, value: string[]) => !value?.length || value.includes(row.getValue(id)),
        meta: { label: "State" },
      },
      {
        id: "image",
        accessorKey: "image",
        header: "Image",
        cell: ({ row }) => (
          <span className="block max-w-44 truncate font-mono text-xs text-muted-foreground" title={row.original.image}>
            {row.original.image.replace(/^.*\//, "")}
          </span>
        ),
        meta: { label: "Image" },
      },
      {
        id: "resources",
        header: "Resources",
        cell: ({ row }) => <ResourcesWithMetrics sb={row.original} />,
        meta: { label: "Resources" },
      },
      {
        id: "snapshots",
        header: "Snapshots",
        // Inactive is no schedule and nothing taken; otherwise how many it
        // has, with the schedule's clock when one runs.
        cell: ({ row: { original: s } }) => {
          const n = snapshotsOf.get(s.id) ?? 0;
          if (!s.snapshot_every_secs && n === 0) return <span className="text-xs text-muted-foreground">Inactive</span>;
          return (
            <span
              className="inline-flex items-center gap-1.5 text-xs tabular-nums"
              title={s.snapshot_every_secs ? `every ${every(s.snapshot_every_secs)}, the newest ${s.snapshot_keep} kept` : "taken by hand; no schedule"}
            >
              {s.snapshot_every_secs ? <CalendarClock className="size-3.5 text-status-running" /> : <Camera className="size-3.5 text-muted-foreground" />}
              {n}
            </span>
          );
        },
        meta: { label: "Snapshots" },
      },
      {
        id: "labels",
        header: "Labels",
        cell: ({ row }) => {
          const entries = Object.entries(row.original.labels ?? {});
          if (!entries.length) return <span className="text-xs text-muted-foreground">—</span>;
          return (
            <div className="flex max-w-44 items-center gap-1" title={entries.map(([k, v]) => `${k}=${v}`).join("\n")}>
              {entries.slice(0, 1).map(([k, v]) => (
                <Badge key={k} variant="outline" className="block max-w-28 truncate font-mono text-[10px] font-normal">
                  {k}={v}
                </Badge>
              ))}
              {entries.length > 1 ? <span className="text-[11px] whitespace-nowrap text-muted-foreground">+{entries.length - 1}</span> : null}
            </div>
          );
        },
        meta: { label: "Labels" },
      },
      {
        id: "created",
        accessorKey: "created_at",
        header: ({ column }) => <ColumnHeader column={column} title="Created" />,
        cell: ({ row }) => (
          <span className="text-xs whitespace-nowrap text-muted-foreground" title={formatDateTime(row.original.created_at)}>
            {formatRelative(row.original.created_at)}
          </span>
        ),
        meta: { label: "Created" },
      },
      {
        id: "actions",
        enableSorting: false,
        enableHiding: false,
        cell: ({ row: { original: s } }) => {
          const label = s.name || s.id;
          return (
            <div className="flex items-center justify-end gap-0.5">
              {s.state === "running" && can("sandbox:create") ? (
                <Tooltip>
                  <TooltipTrigger asChild>
                    <Button variant="ghost" size="icon" className="size-7 text-muted-foreground" aria-label={`Open a terminal in ${label}`} onClick={() => setOpen({ id: s.id, shell: true })}>
                      <SquareTerminal className="size-4" />
                    </Button>
                  </TooltipTrigger>
                  <TooltipContent>Open a terminal</TooltipContent>
                </Tooltip>
              ) : null}
              <DropdownMenu>
                <DropdownMenuTrigger asChild>
                  <Button variant="ghost" size="icon" className="size-7" aria-label={`Actions for ${label}`}>
                    <MoreHorizontal className="size-4" />
                  </Button>
                </DropdownMenuTrigger>
                <DropdownMenuContent align="end">
                  <DropdownMenuItem onClick={() => setOpen({ id: s.id, shell: false })}>Open</DropdownMenuItem>
                  <DropdownMenuItem onClick={() => router.push(`/sandbox?id=${encodeURIComponent(s.id)}`)}>Open as a page</DropdownMenuItem>
                  <DropdownMenuItem onClick={() => navigator.clipboard.writeText(s.id).then(() => toast.success("Copied the id"))}>Copy id</DropdownMenuItem>
                  {s.state !== "terminated" && caps.suspend && can("sandbox:create") ? (
                    <DropdownMenuItem
                      onClick={() =>
                        suspend
                          .mutateAsync({ id: s.id, resume: s.state === "suspended" })
                          .then(() => toast.success(s.state === "suspended" ? "Resumed" : "Suspended"))
                          .catch((e: Error) => toast.error(e.message))
                      }
                    >
                      {s.state === "suspended" ? "Resume" : "Suspend"}
                    </DropdownMenuItem>
                  ) : null}
                  {s.state !== "terminated" && can("sandbox:delete") && (
                    <>
                      <DropdownMenuSeparator />
                      <DropdownMenuItem variant="destructive" onClick={() => terminate([s])}>
                        Terminate
                      </DropdownMenuItem>
                    </>
                  )}
                </DropdownMenuContent>
              </DropdownMenu>
            </div>
          );
        },
      },
    ],
    // terminate and suspend close over state that changes every render; the
    // columns only need rebuilding when what they show does.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [agentOf, snapshotsOf, caps.suspend, can, open],
  );

  const table = useReactTable({
    data: all,
    columns,
    // The Snapshots column only where the endpoint takes snapshots.
    state: { sorting, rowSelection: selection, columnVisibility: { snapshots: takesSnapshots, ...visibility }, globalFilter: query },
    getRowId: (s) => s.id,
    onSortingChange: setSorting,
    onRowSelectionChange: setSelection,
    onColumnVisibilityChange: setVisibility,
    onGlobalFilterChange: setQuery,
    globalFilterFn: (row, _id, q: string) => !q.trim() || matches(row.original, q.trim().toLowerCase()),
    getCoreRowModel: getCoreRowModel(),
    getFilteredRowModel: getFilteredRowModel(),
    getSortedRowModel: getSortedRowModel(),
    getPaginationRowModel: getPaginationRowModel(),
    getFacetedRowModel: getFacetedRowModel(),
    getFacetedUniqueValues: getFacetedUniqueValues(),
    initialState: { pagination: { pageSize: 20 }, columnFilters: [{ id: "state", value: LIVE }] },
    autoResetPageIndex: false,
  });

  // Up and down in the panel walk the list as it is filtered and sorted.
  const order = table.getPrePaginationRowModel().rows.map((r) => r.original.id);
  const at = open ? order.indexOf(open.id) : -1;
  const step = (d: number) => (at >= 0 && order[at + d] ? () => setOpen({ id: order[at + d], shell: false }) : undefined);
  const chosen = table.getFilteredSelectedRowModel().rows.map((r) => r.original);
  const chosenLive = chosen.filter((s) => s.state !== "terminated");

  return (
    <div className="flex flex-col gap-6">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div className="flex flex-col gap-1">
          <h1 className="text-[1.75rem] leading-tight font-semibold tracking-tight">Sandboxes</h1>
          {info?.context ? <p className="text-sm text-muted-foreground">On {info.context}</p> : null}
        </div>
        {can("sandbox:create") && (
          <Button asChild size="sm">
            <Link href="/launch">
              <Plus className="size-4" />
              Create sandbox
            </Link>
          </Button>
        )}
      </div>
      {error ? <p className="text-sm text-destructive">{error.message}</p> : null}

      {!isLoading && all.length === 0 ? (
        <QuickStart />
      ) : (
        <>
          <SandboxSummary sandboxes={all} />

          <div className="flex flex-col gap-3">
            <div className="flex flex-wrap items-center gap-2">
              <div className="relative w-full sm:w-72">
                <Search className="pointer-events-none absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2 text-muted-foreground" />
                <Input placeholder="Search by name, id, image or label" value={query} onChange={(e) => setQuery(e.target.value)} className="h-8 pl-8" />
              </div>
              <FacetedFilter column={table.getColumn("state")} title="State" options={STATES} />
              {chosen.length ? (
                <div className="flex items-center gap-2 rounded-md border bg-muted/40 py-0.5 pr-0.5 pl-3 text-xs">
                  <span className="tabular-nums">{chosen.length} selected</span>
                  {chosenLive.length && can("sandbox:delete") ? (
                    <Button variant="ghost" size="sm" className="h-7 text-destructive hover:text-destructive" onClick={() => terminate(chosen)}>
                      <Trash2 className="size-3.5" />
                      Terminate {chosenLive.length}
                    </Button>
                  ) : null}
                  <Button variant="ghost" size="sm" className="h-7" onClick={() => setSelection({})}>
                    Clear
                  </Button>
                </div>
              ) : null}
              <div className="ml-auto flex items-center gap-2">
                <ViewOptions table={table} />
                <Button variant="outline" size="icon" className="size-8" aria-label="Refresh" onClick={() => refetch()}>
                  <RefreshCw className={cn("size-3.5", isFetching && "animate-spin")} />
                </Button>
              </div>
            </div>

            <Table>
                <TableHeader className="bg-muted/30">
                  {table.getHeaderGroups().map((g) => (
                    <TableRow key={g.id} className="hover:bg-transparent">
                      {g.headers.map((h) => (
                        <TableHead key={h.id} className={cn(h.column.id === "select" && "w-10 pl-4", h.column.id === "actions" && "w-20")}>
                          {h.isPlaceholder ? null : flexRender(h.column.columnDef.header, h.getContext())}
                        </TableHead>
                      ))}
                    </TableRow>
                  ))}
                </TableHeader>
                <TableBody>
                  {table.getRowModel().rows.map((row) => (
                    <TableRow
                      key={row.id}
                      data-state={open?.id === row.id ? "selected" : undefined}
                      className={cn("cursor-pointer", row.original.state === "terminated" && "text-muted-foreground")}
                      onClick={(e) => {
                        // A click on the row opens it; one on a control does its own thing.
                        if (!(e.target as HTMLElement).closest("a,button,[role=checkbox],[role=menu]")) setOpen({ id: row.id, shell: false });
                      }}
                    >
                      {row.getVisibleCells().map((cell) => (
                        <TableCell key={cell.id} className={cn(cell.column.id === "select" && "pl-4")}>
                          {flexRender(cell.column.columnDef.cell, cell.getContext())}
                        </TableCell>
                      ))}
                    </TableRow>
                  ))}
                  {table.getRowModel().rows.length === 0 && (
                    <TableRow>
                      <TableCell colSpan={columns.length} className="py-10 text-center text-sm text-muted-foreground">
                        {(() => {
                          const stateFilter = (table.getColumn("state")?.getFilterValue() as string[] | undefined) ?? [];
                          const hidden = all.filter((s) => !stateFilter.includes(s.state)).length;
                          return stateFilter.length && hidden && !query.trim() ? (
                            <span>
                              None {stateFilter.length === LIVE.length && LIVE.every((s) => stateFilter.includes(s)) ? "running" : "in these states"};{" "}
                              <button type="button" className="underline underline-offset-2 hover:text-foreground" onClick={() => table.getColumn("state")?.setFilterValue(undefined)}>
                                show all {hidden}
                              </button>
                            </span>
                          ) : (
                            "No sandbox matches."
                          );
                        })()}
                      </TableCell>
                    </TableRow>
                  )}
                </TableBody>
            </Table>
            <DataTablePagination table={table} noun="sandbox" nouns="sandboxes" />
          </div>
        </>
      )}

      <Sheet open={!!open} onOpenChange={(o) => !o && setOpen(null)}>
        <SheetContent
          side="right"
          showCloseButton={false}
          aria-describedby={undefined}
          className={cn("w-full gap-0 p-0 sm:max-w-xl", wide && "sm:max-w-[min(1280px,94vw)]")}
        >
          <SheetTitle className="sr-only">Sandbox details</SheetTitle>
          {open ? (
            <SandboxDetails
              id={open.id}
              variant="panel"
              expanded={wide}
              startShell={open.shell}
              onClose={() => setOpen(null)}
              onPrev={step(-1)}
              onNext={step(1)}
              onToggleExpand={() => setWide(!wide)}
              onGone={() => setOpen(null)}
            />
          ) : null}
        </SheetContent>
      </Sheet>
    </div>
  );
}
