"use client";

import { useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { toast } from "sonner";
import { MoreHorizontal, Plus, RefreshCw, Search } from "lucide-react";
import { QuickStart } from "@/components/common/quick-start";
import { StatusBadge } from "@/components/common/status-badge";
import { Labels } from "@/components/sandbox/labels";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { useAgentStates, useKill, useSandboxes } from "@/lib/api/queries";
import { formatRelative } from "@/lib/format";
import { useCan } from "@/lib/caller";
import { cn } from "@/lib/utils";
import type { Sandbox, SandboxState } from "@/lib/types";

const STATES: { id: "all" | SandboxState; label: string }[] = [
  { id: "all", label: "All states" },
  { id: "running", label: "Running" },
  { id: "suspended", label: "Suspended" },
  { id: "terminated", label: "Terminated" },
];

function gib(mb: number): string {
  return mb >= 1024 ? `${+(mb / 1024).toFixed(1)} GiB` : `${mb} MiB`;
}

/**
 * Studio's home: every sandbox on this sandboxd — started from Studio, the
 * CLI, an SDK or anything else that talks to it — in one plain table, and when
 * there are none, how to start one.
 *
 * Resources are what each sandbox was given; sandboxd reports no live usage,
 * so the screen shows nothing that would have to be made up.
 */
export default function SandboxesPage() {
  const router = useRouter();
  const { data, isLoading, isFetching, error, refetch } = useSandboxes();
  const { data: agentStates } = useAgentStates();
  const kill = useKill();
  const can = useCan();
  const agentOf = new Map((agentStates ?? []).map((a) => [a.sandbox, a]));
  const [query, setQuery] = useState("");
  const [state, setState] = useState<"all" | SandboxState>("all");

  const all = data ?? [];
  const q = query.trim().toLowerCase();
  const rows = all
    .filter((s) => state === "all" || s.state === state)
    .filter(
      (s) =>
        !q ||
        s.id.includes(q) ||
        (s.name ?? "").toLowerCase().includes(q) ||
        s.image.toLowerCase().includes(q) ||
        Object.entries(s.labels ?? {}).some(([k, v]) => `${k}=${v}`.toLowerCase().includes(q)),
    )
    .sort((a, b) => b.created_at.localeCompare(a.created_at));

  const terminate = (s: Sandbox) => {
    if (confirm(`Terminate ${s.name || s.id}? Everything in it goes with it.`)) {
      kill.mutate(s.id, { onSuccess: () => toast.success("Terminated"), onError: (e) => toast.error(e.message) });
    }
  };

  return (
    <div className="flex flex-col gap-6">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h1 className="text-[1.75rem] leading-tight font-semibold tracking-tight">Sandboxes</h1>
        {can("sandbox:create") && <div className="flex items-center gap-2">
          <Button asChild variant="ghost" size="sm" className="text-muted-foreground">
            <Link href="/launch">Playground</Link>
          </Button>
          <Button asChild size="sm">
            <Link href="/launch">
              <Plus className="size-4" />
              Create sandbox
            </Link>
          </Button>
        </div>}
      </div>
      {error ? <p className="text-sm text-destructive">{error.message}</p> : null}

      {!isLoading && all.length === 0 ? (
        <QuickStart />
      ) : (
        <div className="flex flex-col gap-3">
          <div className="flex flex-wrap items-center gap-2">
            <div className="relative w-full sm:w-72">
              <Search className="pointer-events-none absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2 text-muted-foreground" />
              <Input placeholder="Search by name, id, image or label" value={query} onChange={(e) => setQuery(e.target.value)} className="h-8 pl-8" />
            </div>
            <Select value={state} onValueChange={(v) => setState(v as "all" | SandboxState)}>
              <SelectTrigger size="sm" className="w-36" aria-label="State">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {STATES.map((s) => (
                  <SelectItem key={s.id} value={s.id}>
                    {s.label}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <Button
              variant="outline"
              size="icon"
              className="ml-auto size-8"
              aria-label="Refresh"
              onClick={() => refetch()}
            >
              <RefreshCw className={cn("size-3.5", isFetching && "animate-spin")} />
            </Button>
          </div>

          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead>State</TableHead>
                <TableHead>Image</TableHead>
                <TableHead>Resources</TableHead>
                <TableHead>Agent</TableHead>
                <TableHead>Created</TableHead>
                <TableHead className="w-10" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {rows.map((s) => {
                const agent = agentOf.get(s.id);
                return (
                  <TableRow
                    key={s.id}
                    className="cursor-pointer"
                    onClick={(e) => {
                      // A click on the row opens it; one on a link or the menu does its own thing.
                      if (!(e.target as HTMLElement).closest("a,button,[role=menu]")) router.push(`/sandbox?id=${s.id}`);
                    }}
                  >
                    <TableCell>
                      <div className="flex flex-col gap-1">
                        <Link href={`/sandbox?id=${s.id}`} className="flex flex-col hover:underline">
                          <span className="text-sm font-medium">{s.name || s.id}</span>
                          {s.name ? <span className="font-mono text-[11px] text-muted-foreground">{s.id}</span> : null}
                        </Link>
                        {s.labels && Object.keys(s.labels).length ? <Labels labels={s.labels} /> : null}
                      </div>
                    </TableCell>
                    <TableCell>
                      <StatusBadge outcome={s.state} size="sm" />
                    </TableCell>
                    <TableCell className="max-w-48 truncate font-mono text-xs text-muted-foreground" title={s.image}>
                      {s.image.replace(/^.*\//, "")}
                    </TableCell>
                    <TableCell className="font-mono text-xs text-muted-foreground tabular-nums">
                      {s.cpus} vCPU · {gib(s.memory_mb)} · {gib(s.disk_mb)}
                    </TableCell>
                    <TableCell title={agent?.why}>
                      {agent ? (
                        <span className="flex items-center gap-1.5">
                          <span className="font-mono text-xs">{agent.agent}</span>
                          {agent.state === "blocked" ? <StatusBadge outcome="blocked" size="sm" /> : null}
                        </span>
                      ) : (
                        <span className="text-xs text-muted-foreground">—</span>
                      )}
                    </TableCell>
                    <TableCell className="text-xs text-muted-foreground" title={s.created_at}>
                      {formatRelative(s.created_at)}
                    </TableCell>
                    <TableCell className="text-right">
                      <DropdownMenu>
                        <DropdownMenuTrigger asChild>
                          <Button variant="ghost" size="icon" className="size-7" aria-label={`Actions for ${s.name || s.id}`}>
                            <MoreHorizontal className="size-4" />
                          </Button>
                        </DropdownMenuTrigger>
                        <DropdownMenuContent align="end">
                          <DropdownMenuItem onClick={() => router.push(`/sandbox?id=${s.id}`)}>Open</DropdownMenuItem>
                          <DropdownMenuItem
                            onClick={() => navigator.clipboard.writeText(s.id).then(() => toast.success("Copied the id"))}
                          >
                            Copy id
                          </DropdownMenuItem>
                          {s.state !== "terminated" && can("sandbox:delete") && (
                            <>
                              <DropdownMenuSeparator />
                              <DropdownMenuItem variant="destructive" onClick={() => terminate(s)}>
                                Terminate
                              </DropdownMenuItem>
                            </>
                          )}
                        </DropdownMenuContent>
                      </DropdownMenu>
                    </TableCell>
                  </TableRow>
                );
              })}
              {rows.length === 0 && (
                <TableRow>
                  <TableCell colSpan={7} className="py-10 text-center text-sm text-muted-foreground">
                    No sandbox matches.
                  </TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
        </div>
      )}
    </div>
  );
}
