"use client";

import { useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { Plus, Search } from "lucide-react";
import { PageHeader } from "@/components/common/page-header";
import { QuickStart } from "@/components/common/quick-start";
import { StatusBadge } from "@/components/common/status-badge";
import { Labels } from "@/components/sandbox/labels";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { useAgentStates, useSandboxes } from "@/lib/api/queries";
import { formatRelative } from "@/lib/format";
import { cn } from "@/lib/utils";
import type { Sandbox, SandboxState } from "@/lib/types";

const STATES: { id: "all" | SandboxState; label: string }[] = [
  { id: "all", label: "All" },
  { id: "running", label: "Running" },
  { id: "suspended", label: "Suspended" },
  { id: "terminated", label: "Terminated" },
];

/** "Started within": the question a list of sandboxes is usually asked. */
const SINCE: { id: string; label: string; ms: number }[] = [
  { id: "any", label: "Any time", ms: 0 },
  { id: "1h", label: "Last hour", ms: 3_600_000 },
  { id: "6h", label: "Last 6 hours", ms: 6 * 3_600_000 },
  { id: "24h", label: "Last 24 hours", ms: 24 * 3_600_000 },
];

/** What a sandbox was given. The API reports allocations, not live usage. */
function resources(s: Sandbox): string {
  const mem = s.memory_mb >= 1024 ? `${+(s.memory_mb / 1024).toFixed(1)} GiB` : `${s.memory_mb} MiB`;
  const disk = s.disk_mb >= 1024 ? `${+(s.disk_mb / 1024).toFixed(1)} GiB` : `${s.disk_mb} MiB`;
  return `${s.cpus} vCPU · ${mem} · ${disk}`;
}

/**
 * Every sandbox on this sandboxd — started from Studio, the CLI, an SDK or
 * anything else that talks to it — with its state, its agent's state, and
 * what it was given. A row opens the sandbox.
 */
export default function SandboxesPage() {
  const router = useRouter();
  const { data, isLoading, error } = useSandboxes();
  const { data: agentStates } = useAgentStates();
  const agentOf = new Map((agentStates ?? []).map((a) => [a.sandbox, a]));
  const [query, setQuery] = useState("");
  const [state, setState] = useState<"all" | SandboxState>("all");
  const [since, setSince] = useState("any");

  const all = data ?? [];
  const q = query.trim().toLowerCase();
  const within = SINCE.find((x) => x.id === since)?.ms ?? 0;
  const rows = all
    .filter((s) => state === "all" || s.state === state)
    .filter((s) => !within || Date.now() - new Date(s.created_at).getTime() <= within)
    .filter(
      (s) =>
        !q ||
        s.id.includes(q) ||
        (s.name ?? "").toLowerCase().includes(q) ||
        s.image.toLowerCase().includes(q) ||
        Object.entries(s.labels ?? {}).some(([k, v]) => `${k}=${v}`.toLowerCase().includes(q)),
    )
    .sort((a, b) => b.created_at.localeCompare(a.created_at));
  const count = (id: "all" | SandboxState) => (id === "all" ? all.length : all.filter((s) => s.state === id).length);

  return (
    <div className="flex flex-col gap-5">
      <PageHeader
        title="Sandboxes"
        description="Every sandbox on this sandboxd, started from Studio, the CLI, an SDK or anything else that talks to it."
        actions={
          <Button asChild size="sm">
            <Link href="/launch">
              <Plus className="size-4" />
              New sandbox
            </Link>
          </Button>
        }
      />
      {error ? <p className="text-sm text-destructive">{error.message}</p> : null}

      {!isLoading && all.length === 0 ? (
        <QuickStart />
      ) : (
        <>
          <div className="flex flex-wrap items-center gap-2">
            <div role="tablist" aria-label="State" className="flex rounded-lg border bg-card p-0.5">
              {STATES.map((s) => (
                <button
                  key={s.id}
                  role="tab"
                  aria-selected={state === s.id}
                  onClick={() => setState(s.id)}
                  className={cn(
                    "flex items-center gap-1.5 rounded-md px-2.5 py-1 text-xs transition-colors",
                    state === s.id ? "bg-muted font-medium text-foreground" : "text-muted-foreground hover:text-foreground",
                  )}
                >
                  {s.label}
                  <span className="tabular-nums text-muted-foreground">{count(s.id)}</span>
                </button>
              ))}
            </div>
            <Select value={since} onValueChange={setSince}>
              <SelectTrigger size="sm" className="w-40" aria-label="Started within">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {SINCE.map((x) => (
                  <SelectItem key={x.id} value={x.id}>
                    {x.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <div className="relative ml-auto w-full sm:w-72">
              <Search className="pointer-events-none absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2 text-muted-foreground" />
              <Input
                placeholder="Search id, name, image or label"
                value={query}
                onChange={(e) => setQuery(e.target.value)}
                className="h-8 pl-8"
              />
            </div>
          </div>

          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Sandbox</TableHead>
                <TableHead>State</TableHead>
                <TableHead>Agent</TableHead>
                <TableHead>Image</TableHead>
                <TableHead>Resources</TableHead>
                <TableHead>Network</TableHead>
                <TableHead>Labels</TableHead>
                <TableHead className="text-right">Started</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {rows.map((s) => (
                <TableRow
                  key={s.id}
                  className="cursor-pointer"
                  onClick={(e) => {
                    // A click on the row opens it; one on the link already does.
                    if (!(e.target as HTMLElement).closest("a")) router.push(`/sandbox?id=${s.id}`);
                  }}
                >
                  <TableCell>
                    <Link href={`/sandbox?id=${s.id}`} className="flex flex-col hover:underline">
                      <span className="text-sm font-medium">{s.name || s.id}</span>
                      {s.name ? <span className="font-mono text-[11px] text-muted-foreground">{s.id}</span> : null}
                    </Link>
                  </TableCell>
                  <TableCell>
                    <StatusBadge outcome={s.state} size="sm" />
                  </TableCell>
                  <TableCell title={agentOf.get(s.id)?.why}>
                    {agentOf.has(s.id) ? (
                      <div className="flex items-center gap-1.5">
                        <span className="font-mono text-xs">{agentOf.get(s.id)!.agent}</span>
                        <StatusBadge outcome={agentOf.get(s.id)!.state} size="sm" />
                      </div>
                    ) : (
                      <span className="text-xs text-muted-foreground">—</span>
                    )}
                  </TableCell>
                  <TableCell className="max-w-44 truncate font-mono text-xs" title={s.image}>
                    {s.image}
                  </TableCell>
                  <TableCell className="font-mono text-xs text-muted-foreground tabular-nums">{resources(s)}</TableCell>
                  <TableCell className="font-mono text-xs">{s.network.mode}</TableCell>
                  <TableCell>
                    <Labels labels={s.labels} />
                  </TableCell>
                  <TableCell className="text-right text-xs text-muted-foreground" title={s.created_at}>
                    {formatRelative(s.created_at)}
                  </TableCell>
                </TableRow>
              ))}
              {rows.length === 0 && (
                <TableRow>
                  <TableCell colSpan={8} className="py-8 text-center text-sm text-muted-foreground">
                    No sandbox matches these filters.
                  </TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
          <p className="text-xs text-muted-foreground">
            Resources are what each sandbox was given; this sandboxd does not report live usage.
          </p>
        </>
      )}
    </div>
  );
}
