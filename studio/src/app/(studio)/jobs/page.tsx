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
import { ListChecks, Plus, RefreshCw, Search } from "lucide-react";
import { toast } from "sonner";
import { EmptyState } from "@/components/common/empty-state";
import { PageHeader } from "@/components/common/page-header";
import { StatusBadge } from "@/components/common/status-badge";
import { TagInput } from "@/components/common/tag-input";
import { ColumnHeader } from "@/components/data-table/column-header";
import { FacetedFilter } from "@/components/data-table/faceted-filter";
import { DataTablePagination } from "@/components/data-table/pagination";
import { Gate } from "@/components/shell/gate";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Textarea } from "@/components/ui/textarea";
import { useCancelJob, useJobs, useSubmitJob } from "@/lib/api/gateway";
import { ImagePicker } from "@/components/launch/image-picker";
import { useAgents, useFleetImages, useSandboxes } from "@/lib/api/queries";
import { useCan } from "@/lib/caller";
import { commandArgv, formatArgv, formatRelative, needsShell } from "@/lib/format";
import { labelMap, labelProblem, labelTags, MAX_LABELS } from "@/lib/labels";
import { cn } from "@/lib/utils";
import type { Job, JobSpec } from "@/lib/types";

const list = (s: string) => s.split(",").map((x) => x.trim()).filter(Boolean);

const STATES = [
  { value: "running", label: "Running", tone: "var(--status-running)" },
  { value: "succeeded", label: "Succeeded", tone: "var(--status-good)" },
  { value: "failed", label: "Failed", tone: "var(--status-critical)" },
  { value: "cancelled", label: "Cancelled", tone: "var(--status-idle)" },
];

/** What a job runs, in a few words: its command, or its agent. */
function jobWhat(j: Job): string {
  if (j.spec.agent) return `${j.spec.agent}${j.spec.prompts?.length ? ` · ${j.spec.prompts.length} prompts` : ""}`;
  return formatArgv(j.spec.command ?? []);
}

function matches(j: Job, q: string): boolean {
  return (
    j.id.includes(q) ||
    (j.name ?? "").toLowerCase().includes(q) ||
    jobWhat(j).toLowerCase().includes(q) ||
    labelTags(j.spec.labels).some((t) => t.toLowerCase().includes(q))
  );
}

/**
 * Jobs: work the gateway runs to completion after the request has gone, each
 * run in a sandbox of its own, with what was asked to be kept — output and
 * files — kept after the sandbox is gone.
 *
 * The listing is the gateway's whole list, newest first; search, the State
 * and Labels filters and the pages are the table's, in the browser.
 */
function Jobs() {
  const can = useCan();
  const { data, isLoading, isFetching, error, refetch } = useJobs();
  const cancel = useCancelJob();
  const router = useRouter();
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [sorting, setSorting] = useState<SortingState>([]);
  const rows = useMemo(() => data ?? [], [data]);

  // Every label on the listed jobs, for the Labels filter to offer.
  const labelOptions = useMemo(
    () => [...new Set(rows.flatMap((j) => labelTags(j.spec.labels)))].sort().map((t) => ({ value: t, label: t })),
    [rows],
  );

  const columns = useMemo<ColumnDef<Job>[]>(
    () => [
      {
        id: "job",
        accessorFn: (j) => j.name || j.id,
        header: ({ column }) => <ColumnHeader column={column} title="Job" />,
        cell: ({ row: { original: j } }) => (
          <Link href={`/job?id=${j.id}`} className="flex flex-col hover:underline">
            <span className="text-sm font-medium">{j.name || j.id}</span>
            {j.name ? <span className="font-mono text-[11px] text-muted-foreground">{j.id}</span> : null}
          </Link>
        ),
      },
      {
        id: "state",
        accessorKey: "state",
        header: "State",
        cell: ({ row }) => <StatusBadge outcome={row.original.state} size="sm" />,
        filterFn: (row, id, value: string[]) => !value?.length || value.includes(row.getValue(id)),
      },
      {
        id: "runs",
        header: "Runs",
        cell: ({ row: { original: j } }) => (
          <span className="font-mono text-xs text-muted-foreground tabular-nums">
            {j.succeeded} ok · {j.failed} failed · {j.running} running · {j.queued} queued
          </span>
        ),
      },
      {
        id: "what",
        header: "What",
        cell: ({ row }) => <span className="block max-w-64 truncate font-mono text-xs text-muted-foreground">{jobWhat(row.original)}</span>,
      },
      {
        id: "labels",
        accessorFn: (j) => labelTags(j.spec.labels),
        // The facet counts are per label, not per set of them.
        getUniqueValues: (j) => labelTags(j.spec.labels),
        header: "Labels",
        // A job shown has every label chosen: picking more narrows, as a
        // second filter would.
        filterFn: (row, id, value: string[]) => !value?.length || value.every((t) => (row.getValue(id) as string[]).includes(t)),
        cell: ({ row }) => {
          const tags = labelTags(row.original.spec.labels);
          if (!tags.length) return <span className="text-xs text-muted-foreground">—</span>;
          return (
            <div className="flex max-w-56 flex-wrap items-center gap-1" title={tags.join("\n")}>
              {tags.slice(0, 2).map((t) => (
                <Badge key={t} variant="outline" className="block max-w-28 truncate font-mono text-[10px] font-normal">
                  {t}
                </Badge>
              ))}
              {tags.length > 2 ? <span className="text-[11px] whitespace-nowrap text-muted-foreground">+{tags.length - 2}</span> : null}
            </div>
          );
        },
      },
      {
        id: "created",
        accessorFn: (j) => j.created_at,
        header: ({ column }) => <ColumnHeader column={column} title="Created" />,
        cell: ({ row }) => <span className="text-xs text-muted-foreground">{formatRelative(row.original.created_at)}</span>,
      },
      {
        id: "actions",
        header: "",
        cell: ({ row: { original: j } }) =>
          j.state === "running" && can("sandbox:create") ? (
            <div className="text-right">
              <Button size="sm" variant="ghost" disabled={cancel.isPending}
                onClick={() => confirm(`Cancel ${j.name || j.id}? Its running sandboxes are terminated.`) && cancel.mutate(j.id, { onError: (e) => toast.error(e.message), onSuccess: () => toast.success("Cancelled") })}>
                Cancel
              </Button>
            </div>
          ) : null,
      },
    ],
    [can, cancel],
  );

  const table = useReactTable({
    data: rows,
    columns,
    state: { sorting, globalFilter: query },
    getRowId: (j) => j.id,
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
        title="Jobs"
        description="Commands and agent runs the gateway runs to completion, each in a sandbox of its own. Output and kept files stay until the job expires."
        actions={
          can("sandbox:create") && !open ? (
            <Button size="sm" onClick={() => setOpen(true)}>
              <Plus className="size-4" />
              Submit job
            </Button>
          ) : undefined
        }
      />
      {open && can("sandbox:create") && <SubmitJob onClose={() => setOpen(false)} />}
      {error ? (
        <p className="text-sm text-destructive">{error.message}</p>
      ) : !isLoading && rows.length === 0 ? (
        <EmptyState icon={ListChecks} title="No jobs" description="Submit one here, or with sandbox-cli job run." />
      ) : (
        <div className="flex flex-col gap-3">
          <div className="flex flex-wrap items-center gap-2">
            <div className="relative w-full sm:w-72">
              <Search className="pointer-events-none absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2 text-muted-foreground" />
              <Input placeholder="Search by name, id, command or label" aria-label="Search jobs" value={query} onChange={(e) => setQuery(e.target.value)} className="h-8 pl-8" />
            </div>
            <FacetedFilter column={table.getColumn("state")} title="State" options={STATES} />
            {labelOptions.length ? <FacetedFilter column={table.getColumn("labels")} title="Labels" options={labelOptions} /> : null}
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
                <TableRow key={row.id} className="cursor-pointer" onClick={(e) => !(e.target as HTMLElement).closest("a,button") && router.push(`/job?id=${row.id}`)}>
                  {row.getVisibleCells().map((cell) => (
                    <TableCell key={cell.id}>{flexRender(cell.column.columnDef.cell, cell.getContext())}</TableCell>
                  ))}
                </TableRow>
              ))}
              {table.getRowModel().rows.length === 0 && (
                <TableRow>
                  <TableCell colSpan={columns.length} className="py-10 text-center text-sm text-muted-foreground">
                    No job matches.
                  </TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
          <DataTablePagination table={table} noun="job" />
        </div>
      )}
    </div>
  );
}

function SubmitJob({ onClose }: { onClose: () => void }) {
  const router = useRouter();
  const { data: agents } = useAgents();
  const submit = useSubmitJob();
  const [kind, setKind] = useState<"command" | "agent">("command");
  const [name, setName] = useState("");
  const [image, setImage] = useState("");
  const [command, setCommand] = useState("");
  const [agent, setAgent] = useState("claude");
  const [prompt, setPrompt] = useState("");
  const [retries, setRetries] = useState("");
  const [timeout, setTimeoutSecs] = useState("");
  const [secrets, setSecrets] = useState("");
  const [files, setFiles] = useState("");
  const [labels, setLabels] = useState<string[]>([]);
  // What the nodes have built, and what the user's sandboxes run: what a
  // job can start from, as the create form offers.
  const { data: built } = useFleetImages();
  const { data: sandboxes } = useSandboxes();
  const inUse = (sandboxes ?? []).map((sb) => sb.image);

  function send(e: React.FormEvent) {
    e.preventDefault();
    const spec: JobSpec = { name: name || undefined, image: image || undefined };
    if (kind === "command") spec.command = commandArgv(command);
    else {
      spec.agent = agent;
      spec.prompt = prompt;
    }
    if (retries) spec.retries = Number(retries);
    if (timeout) spec.timeout_secs = Number(timeout);
    if (list(secrets).length) spec.secrets = list(secrets);
    if (list(files).length) spec.keep = { files: list(files) };
    if (labels.some((l) => labelProblem(l))) {
      toast.error("A label will be refused; hover it to see why.");
      return;
    }
    if (labels.length > MAX_LABELS) {
      toast.error(`At most ${MAX_LABELS} labels.`);
      return;
    }
    if (labels.length) spec.labels = labelMap(labels);
    submit.mutate(spec, {
      onSuccess: (j) => {
        toast.success(`Submitted ${j.id}`);
        router.push(`/job?id=${j.id}`);
      },
      onError: (err) => toast.error(err.message),
    });
  }

  return (
    <Card className="surface-sheen gap-0 py-0">
      <CardContent className="p-4">
        <form onSubmit={send} className="grid gap-4 md:grid-cols-2">
          <div className="flex flex-col gap-1.5">
            <Label>Runs</Label>
            <Select value={kind} onValueChange={(v) => setKind(v as "command" | "agent")}>
              <SelectTrigger aria-label="Runs">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="command">A command</SelectItem>
                <SelectItem value="agent">An agent, unattended</SelectItem>
              </SelectContent>
            </Select>
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="job-name">Name</Label>
            <Input id="job-name" placeholder="optional" value={name} onChange={(e) => setName(e.target.value)} />
          </div>
          {kind === "command" ? (
            <div className="flex flex-col gap-1.5 md:col-span-2">
              <Label htmlFor="job-command">Command</Label>
              <Input id="job-command" placeholder="make test" value={command} onChange={(e) => setCommand(e.target.value)} className="font-mono" required />
              {needsShell(command) ? (
                <p className="text-xs text-muted-foreground" data-testid="runs-in-shell">
                  Runs in a shell: <span className="font-mono">sh -c &apos;…&apos;</span>, for the loops, pipes and variables in it.
                </p>
              ) : null}
            </div>
          ) : (
            <>
              <div className="flex flex-col gap-1.5">
                <Label>Agent</Label>
                <Select value={agent} onValueChange={setAgent}>
                  <SelectTrigger aria-label="Agent">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {(agents ?? []).map((a) => (
                      <SelectItem key={a.name} value={a.name}>
                        {a.name}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </div>
              <div className="flex flex-col gap-1.5 md:col-span-2">
                <Label htmlFor="job-prompt">Prompt</Label>
                <Textarea id="job-prompt" value={prompt} onChange={(e) => setPrompt(e.target.value)} required />
              </div>
            </>
          )}
          <div className="flex flex-col gap-1.5">
            <Label>Image</Label>
            <ImagePicker value={image} onChange={setImage} built={built ?? []} inUse={inUse} builtWhere="Built on the gateway's nodes" />
          </div>
          <div className="grid grid-cols-2 gap-2">
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="job-retries">Retries</Label>
              <Input id="job-retries" placeholder="0" value={retries} onChange={(e) => setRetries(e.target.value.replace(/\D/g, ""))} />
            </div>
            <div className="flex flex-col gap-1.5">
              <Label htmlFor="job-timeout">Timeout, seconds</Label>
              <Input id="job-timeout" placeholder="3600" value={timeout} onChange={(e) => setTimeoutSecs(e.target.value.replace(/\D/g, ""))} />
            </div>
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="job-secrets">Secrets</Label>
            <Input id="job-secrets" placeholder="NAME, OTHER (set in the environment)" value={secrets} onChange={(e) => setSecrets(e.target.value)} className="font-mono" />
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="job-files">Keep files</Label>
            <Input id="job-files" placeholder="/sandbox/home/report.md, …" value={files} onChange={(e) => setFiles(e.target.value)} className="font-mono" />
          </div>
          <div className="flex flex-col gap-1.5 md:col-span-2">
            <Label htmlFor="job-labels">Labels</Label>
            <TagInput id="job-labels" value={labels} onChange={setLabels} invalid={labelProblem} placeholder="team=infra, nightly — Enter or comma adds one" />
            <p className="text-xs text-muted-foreground">Your own tags for the job, <span className="font-mono">key=value</span> or a bare <span className="font-mono">key</span>; the list filters by them.</p>
          </div>
          <div className="flex gap-2 md:col-span-2">
            <Button type="submit" disabled={submit.isPending}>Submit</Button>
            <Button type="button" variant="ghost" onClick={onClose}>Close</Button>
          </div>
        </form>
      </CardContent>
    </Card>
  );
}

export default function JobsPage() {
  return (
    <Gate need="gateway">
      <Jobs />
    </Gate>
  );
}
