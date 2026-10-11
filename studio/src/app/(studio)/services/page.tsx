"use client";

import { useMemo, useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import {
  type ColumnDef,
  type SortingState,
  flexRender,
  getCoreRowModel,
  getFacetedRowModel,
  getFacetedUniqueValues,
  getFilteredRowModel,
  getPaginationRowModel,
  getSortedRowModel,
  useReactTable,
} from "@tanstack/react-table";
import { Plus, RefreshCw, Search, Workflow } from "lucide-react";
import { toast } from "sonner";
import { EmptyState } from "@/components/common/empty-state";
import { PageHeader } from "@/components/common/page-header";
import { StatusBadge } from "@/components/common/status-badge";
import { ColumnHeader } from "@/components/data-table/column-header";
import { FacetedFilter } from "@/components/data-table/faceted-filter";
import { DataTablePagination } from "@/components/data-table/pagination";
import { Gate } from "@/components/shell/gate";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Textarea } from "@/components/ui/textarea";
import { serviceHref, useDeployService, useServices } from "@/lib/api/gateway";
import { commandProblem } from "@/lib/service-spec";
import { useCaller, useCan } from "@/lib/caller";
import { formatRelative } from "@/lib/format";
import { cn } from "@/lib/utils";
import type { Service, ServiceSpec } from "@/lib/types";

/**
 * Specs to start from. A service runs a command in each replica's sandbox;
 * the code it serves comes either from the image, or from wherever the
 * command fetches it as the replica starts — a Git repository, here, which
 * the base image has git (and python3 and node) to clone and run.
 */
const EXAMPLES: { label: string; spec: string; note: string }[] = [
  {
    label: "From a Git repository",
    note: "Each replica clones the repository as it starts, then serves it. Put your repository's address in place of OWNER/REPO, and your app's start command in place of the server. The network lets it reach the Git host only.",
    spec: `{
  "name": "web",
  "command": ["sh", "-c", "git clone --depth 1 https://github.com/OWNER/REPO /sandbox/home/app && cd /sandbox/home/app && python3 -m http.server 8080"],
  "replicas": 1,
  "port": 8080,
  "health": { "http": "/" },
  "network": { "mode": "allowlist", "allow": ["github.com", "codeload.github.com"] }
}`,
  },
  {
    label: "A private repository",
    note: "Set a secret named GIT_TOKEN first (Secrets), a token that may read the repository; it reaches the replica as an environment variable and is never in the spec.",
    spec: `{
  "name": "api",
  "command": ["sh", "-c", "git clone --depth 1 https://x-access-token:$GIT_TOKEN@github.com/OWNER/REPO /sandbox/home/app && cd /sandbox/home/app && npm ci && npm start"],
  "secrets": ["GIT_TOKEN"],
  "replicas": 1,
  "port": 3000,
  "health": { "http": "/" },
  "network": { "mode": "allowlist", "allow": ["github.com", "codeload.github.com", "registry.npmjs.org"] }
}`,
  },
  {
    label: "An image with the app in it",
    note: "Best for anything real: build your app into an image (FROM the base image), push it to a registry, and name it. Nothing is fetched as a replica starts.",
    spec: `{
  "name": "web",
  "image": "ghcr.io/OWNER/APP:1.0",
  "command": ["./serve", "--port", "8080"],
  "replicas": 2,
  "port": 8080,
  "health": { "http": "/healthz" }
}`,
  },
];

/** in_progress, done, failed — or, with no rollout, nothing to say. */
function rolloutOf(s: Service): string {
  if (!s.rollout || s.rollout.state === "done") return `rev ${s.revision}`;
  return s.rollout.state === "failed" ? `rev ${s.rollout.to} failed` : `rolling ${s.rollout.from} → ${s.rollout.to}`;
}

/** How a service stands, as its Ready badge and the Health filter say it. */
type Health = "healthy" | "starting" | "unhealthy";

function healthOf(s: Service): Health {
  if (s.ready >= s.desired && s.desired > 0) return "healthy";
  return s.ready === 0 && s.desired > 0 ? "unhealthy" : "starting";
}

const HEALTH = [
  { value: "healthy", label: "Healthy", tone: "var(--status-good)" },
  { value: "starting", label: "Starting", tone: "var(--status-running)" },
  { value: "unhealthy", label: "Unhealthy", tone: "var(--status-critical)" },
];

const REACH = [
  { value: "public", label: "Public" },
  { value: "private", label: "Private" },
];

function matches(s: Service, q: string): boolean {
  return [s.spec.name, s.owner, s.tenant ?? "", s.url ?? "", s.spec.image ?? ""].some((v) => v.toLowerCase().includes(q));
}

/**
 * Services: a sandbox spec and a count the gateway keeps true, replacing a
 * replica whose command exits or whose health check fails.
 *
 * Search, the filters and the pages are the table's, over the gateway's
 * whole list.
 */
function Services() {
  const can = useCan();
  const router = useRouter();
  const { data, isLoading, isFetching, error, refetch } = useServices();
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [sorting, setSorting] = useState<SortingState>([]);
  const rows = useMemo(() => data ?? [], [data]);
  const anyOf = (value: string[] | undefined, v: string) => !value?.length || value.includes(v);

  const columns = useMemo<ColumnDef<Service>[]>(
    () => [
      {
        id: "service",
        accessorFn: (s) => s.spec.name,
        header: ({ column }) => <ColumnHeader column={column} title="Service" />,
        cell: ({ row: { original: s } }) => (
          <>
            <Link href={serviceHref(s)} className="flex flex-col hover:underline">
              <span className="text-sm font-medium">{s.spec.name}</span>
              <span className="text-[11px] text-muted-foreground">
                {s.owner}
                {s.tenant ? `@${s.tenant}` : ""}
              </span>
            </Link>
            {s.error ? <p className="mt-1 max-w-80 truncate text-xs text-destructive" title={s.error}>{s.error}</p> : null}
          </>
        ),
      },
      {
        id: "health",
        accessorFn: healthOf,
        header: "Ready",
        filterFn: (row, id, value: string[]) => anyOf(value, row.getValue(id)),
        cell: ({ row: { original: s } }) => (
          <span className="flex items-center gap-2">
            <StatusBadge outcome={healthOf(s)} size="sm" />
            <span className="font-mono text-xs tabular-nums">
              {s.ready}/{s.desired}
            </span>
          </span>
        ),
      },
      {
        id: "revision",
        header: "Revision",
        cell: ({ row }) => <span className="font-mono text-xs text-muted-foreground">{rolloutOf(row.original)}</span>,
      },
      {
        id: "reach",
        accessorFn: (s) => (s.url ? "public" : "private"),
        header: "URL",
        filterFn: (row, id, value: string[]) => anyOf(value, row.getValue(id)),
        cell: ({ row: { original: s } }) => (
          <span className="block max-w-56 truncate font-mono text-xs">{s.url ?? <span className="text-muted-foreground">private</span>}</span>
        ),
      },
      {
        id: "updated",
        accessorFn: (s) => s.updated_at,
        header: ({ column }) => <ColumnHeader column={column} title="Updated" />,
        cell: ({ row }) => <span className="text-xs text-muted-foreground">{formatRelative(row.original.updated_at)}</span>,
      },
    ],
    [],
  );

  const table = useReactTable({
    data: rows,
    columns,
    state: { sorting, globalFilter: query },
    getRowId: (s) => `${s.tenant ?? ""}/${s.spec.name}`,
    onSortingChange: setSorting,
    onGlobalFilterChange: setQuery,
    globalFilterFn: (row, _id, q: string) => !q.trim() || matches(row.original, q.trim().toLowerCase()),
    getCoreRowModel: getCoreRowModel(),
    getFilteredRowModel: getFilteredRowModel(),
    getSortedRowModel: getSortedRowModel(),
    getPaginationRowModel: getPaginationRowModel(),
    getFacetedRowModel: getFacetedRowModel(),
    getFacetedUniqueValues: getFacetedUniqueValues(),
    initialState: { pagination: { pageSize: 20 } },
    // The list refreshes every few seconds; that must not send the reader
    // back to the first page.
    autoResetPageIndex: false,
  });
  const filtered = query.trim() !== "" || table.getState().columnFilters.length > 0;

  return (
    <div className="flex flex-col gap-5">
      <PageHeader
        title="Services"
        description="Sandbox specs the gateway keeps a count of: each replica is one of your sandboxes, replaced when its command exits or its health check fails. A changed spec rolls out one replica at a time."
        actions={
          can("sandbox:create") && !open ? (
            <Button size="sm" onClick={() => setOpen(true)}>
              <Plus className="size-4" />
              Deploy service
            </Button>
          ) : undefined
        }
      />
      {open && can("sandbox:create") && <Deploy existing={rows} onClose={() => setOpen(false)} />}
      {error ? (
        <p className="text-sm text-destructive">{error.message}</p>
      ) : !isLoading && rows.length === 0 ? (
        <EmptyState icon={Workflow} title="No services" description="Deploy one from a spec here, or with sandbox-cli service deploy -f service.yaml." />
      ) : (
        <div className="flex flex-col gap-3">
          <div className="flex flex-wrap items-center gap-2">
            <div className="relative w-full sm:w-72">
              <Search className="pointer-events-none absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2 text-muted-foreground" />
              <Input placeholder="Search by name, owner, image or URL" aria-label="Search services" value={query} onChange={(e) => setQuery(e.target.value)} className="h-8 pl-8" />
            </div>
            <FacetedFilter column={table.getColumn("health")} title="Health" options={HEALTH} />
            <FacetedFilter column={table.getColumn("reach")} title="Reach" options={REACH} />
            {filtered ? (
              <Button variant="ghost" size="sm" className="h-8" onClick={() => { setQuery(""); table.resetColumnFilters(); }}>
                Reset
              </Button>
            ) : null}
            <div className="ml-auto">
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
                    <TableHead key={h.id}>{h.isPlaceholder ? null : flexRender(h.column.columnDef.header, h.getContext())}</TableHead>
                  ))}
                </TableRow>
              ))}
            </TableHeader>
            <TableBody>
              {table.getRowModel().rows.map((row) => (
                <TableRow key={row.id} className="cursor-pointer" onClick={(e) => !(e.target as HTMLElement).closest("a,button") && router.push(serviceHref(row.original))}>
                  {row.getVisibleCells().map((cell) => (
                    <TableCell key={cell.id}>{flexRender(cell.column.columnDef.cell, cell.getContext())}</TableCell>
                  ))}
                </TableRow>
              ))}
              {table.getRowModel().rows.length === 0 && (
                <TableRow>
                  <TableCell colSpan={columns.length} className="py-10 text-center text-sm text-muted-foreground">
                    No service matches.
                  </TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
          <DataTablePagination table={table} noun="service" />
        </div>
      )}
    </div>
  );
}

/** A spec as JSON, deployed as `service deploy` does: created, or updated when the name exists. */
function Deploy({ existing, onClose }: { existing: Service[]; onClose: () => void }) {
  const router = useRouter();
  const deploy = useDeployService();
  const caller = useCaller();
  const tenant = caller.kind === "gateway" ? caller.who.tenant : "";
  const [example, setExample] = useState(0);
  const [text, setText] = useState(EXAMPLES[0].spec);
  const [problem, setProblem] = useState<string | null>(null);

  function send(e: React.FormEvent) {
    e.preventDefault();
    let spec: ServiceSpec;
    try {
      spec = JSON.parse(text);
    } catch (err) {
      setProblem(`Not JSON: ${(err as Error).message}`);
      return;
    }
    if (!spec || typeof spec !== "object" || typeof spec.name !== "string") {
      setProblem("A spec is an object with a name.");
      return;
    }
    if (spec.image === "") delete spec.image;
    const bad = commandProblem(spec);
    if (bad) {
      setProblem(bad);
      return;
    }
    setProblem(null);
    // Names are unique per tenant, and PUT names one in the caller's own.
    const update = existing.some((s) => s.spec.name === spec.name && (s.tenant ?? "") === tenant);
    deploy.mutate(
      { spec, update },
      {
        onSuccess: (s) => {
          toast.success(`${update ? "Updated" : "Deployed"} ${s.spec.name}`);
          router.push(serviceHref(s));
        },
        onError: (err) => setProblem(err.message),
      },
    );
  }

  return (
    <Card className="surface-sheen gap-0 py-0">
      <CardContent className="p-4">
        <form onSubmit={send} className="flex flex-col gap-3">
          <p className="text-sm text-muted-foreground">
            A service is a command the gateway keeps running in <span className="font-mono">replicas</span> sandboxes, each restarted when it exits or
            fails its <span className="font-mono">health</span> check, and reached on <span className="font-mono">port</span>. The code comes from its image,
            or from what the command fetches as it starts.
          </p>
          <div className="flex flex-wrap gap-2" role="group" aria-label="Start from">
            {EXAMPLES.map((ex, i) => (
              <Button key={ex.label} type="button" size="sm" variant={example === i ? "secondary" : "ghost"} aria-pressed={example === i}
                onClick={() => { setExample(i); setText(ex.spec); setProblem(null); }}>
                {ex.label}
              </Button>
            ))}
          </div>
          <p className="text-xs text-muted-foreground">{EXAMPLES[example].note}</p>
          <Textarea aria-label="Service spec" value={text} onChange={(e) => setText(e.target.value)} className="min-h-56 font-mono text-xs" spellCheck={false} />
          <p className="text-xs text-muted-foreground">
            The spec as JSON (docs/services.md). One with the name of an existing service updates it, one replica at a time. Environment values are kept
            by the gateway; a value that must stay secret belongs in Secrets, named under <code className="font-mono">secrets</code>. A service is
            reached from outside only where the gateway runs its router (<span className="font-mono">&quot;public&quot;: true</span>); otherwise, with{" "}
            <span className="font-mono">sandbox-cli tunnel</span> to a replica.
          </p>
          {problem ? <p className="text-sm text-destructive">{problem}</p> : null}
          <div className="flex gap-2">
            <Button type="submit" disabled={deploy.isPending}>Deploy</Button>
            <Button type="button" variant="ghost" onClick={onClose}>Close</Button>
          </div>
        </form>
      </CardContent>
    </Card>
  );
}

export default function ServicesPage() {
  return (
    <Gate need="gateway">
      <Services />
    </Gate>
  );
}
