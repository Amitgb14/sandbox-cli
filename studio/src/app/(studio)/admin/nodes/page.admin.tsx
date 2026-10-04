"use client";

import { useState } from "react";
import { MoreHorizontal, Plus, Server } from "lucide-react";
import { toast } from "sonner";
import { EmptyState } from "@/components/common/empty-state";
import { PageHeader } from "@/components/common/page-header";
import { StatusBadge } from "@/components/common/status-badge";
import { Gate } from "@/components/shell/gate";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Input } from "@/components/ui/input";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { useAddNode, useCordon, useDrain, useNodes, useRemoveNode, type Node, type NodeSpec } from "@/lib/admin/api";
import { DASH, formatMiB, formatRelative } from "@/lib/format";

function used(n: Node): string {
  const s = n.status;
  if (!s) return DASH;
  const cpus = +(s.capacity.cpus - s.free.cpus).toFixed(1);
  return `${cpus}/${s.capacity.cpus} cpus · ${formatMiB(s.capacity.memory_mb - s.free.memory_mb)}/${formatMiB(s.capacity.memory_mb)}`;
}

/**
 * The nodes behind the gateway: whether each answers, what it has given out,
 * and cordon and drain for maintenance. Where a node is — its endpoint — is
 * not shown: the nodes' network is the operator's, and lib/admin drops it.
 */
function Nodes() {
  const { data, isLoading, error } = useNodes();
  const cordon = useCordon();
  const drain = useDrain();
  const remove = useRemoveNode();
  const [adding, setAdding] = useState(false);
  const rows = data ?? [];

  const act = (p: Promise<unknown>, done: string) => p.then(() => toast.success(done)).catch((e: Error) => toast.error(e.message));

  return (
    <div className="flex flex-col gap-5">
      <PageHeader
        title="Nodes"
        description="The sandboxd nodes behind this gateway. A cordoned node takes no new sandboxes while those on it carry on; draining also keeps it cordoned across its restarts."
        actions={
          !adding ? (
            <Button size="sm" onClick={() => setAdding(true)}>
              <Plus className="size-4" />
              Add node
            </Button>
          ) : undefined
        }
      />
      {adding && <AddNode onClose={() => setAdding(false)} />}
      {error ? (
        <p className="text-sm text-destructive">{error.message}</p>
      ) : !isLoading && rows.length === 0 ? (
        <EmptyState icon={Server} title="No nodes" description="Add one here, in the gateway's node file, or with sandbox-gateway nodes add." />
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Node</TableHead>
              <TableHead>State</TableHead>
              <TableHead>Allocated</TableHead>
              <TableHead>Sandboxes</TableHead>
              <TableHead>Version</TableHead>
              <TableHead>Last seen</TableHead>
              <TableHead className="w-10" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {rows.map((n) => {
              const cordoned = !!n.status?.cordoned;
              return (
                <TableRow key={n.name}>
                  <TableCell>
                    <span className="font-mono text-sm font-medium">{n.name}</span>
                    {n.error ? <p className="max-w-80 truncate text-xs text-destructive" title={n.error}>{n.error}</p> : null}
                  </TableCell>
                  <TableCell>
                    <StatusBadge outcome={!n.healthy ? "down" : cordoned ? "cordoned" : "healthy"} size="sm" />
                  </TableCell>
                  <TableCell className="font-mono text-xs text-muted-foreground tabular-nums">{used(n)}</TableCell>
                  <TableCell className="font-mono text-xs tabular-nums">{n.status?.running ?? DASH}</TableCell>
                  <TableCell className="font-mono text-xs text-muted-foreground">{n.status?.version ?? DASH}</TableCell>
                  <TableCell className="text-xs text-muted-foreground">{formatRelative(n.last_seen)}</TableCell>
                  <TableCell className="text-right">
                    <DropdownMenu>
                      <DropdownMenuTrigger asChild>
                        <Button variant="ghost" size="icon" className="size-7" aria-label={`Actions for ${n.name}`}>
                          <MoreHorizontal className="size-4" />
                        </Button>
                      </DropdownMenuTrigger>
                      <DropdownMenuContent align="end">
                        <DropdownMenuItem onClick={() => act(cordon.mutateAsync({ name: n.name, cordoned: !cordoned }), cordoned ? "Uncordoned" : "Cordoned")}>
                          {cordoned ? "Uncordon" : "Cordon"}
                        </DropdownMenuItem>
                        <DropdownMenuItem
                          onClick={() =>
                            confirm(`Drain ${n.name}? It is cordoned and stays so across restarts; its sandboxes carry on.`) &&
                            drain.mutateAsync({ name: n.name, terminate: false }).then((r) => toast.success(`Drained: ${r.remaining} still running`)).catch((e: Error) => toast.error(e.message))
                          }
                        >
                          Drain
                        </DropdownMenuItem>
                        <DropdownMenuItem
                          variant="destructive"
                          onClick={() =>
                            confirm(`Drain ${n.name} and terminate every sandbox on it? Everything in them goes with them.`) &&
                            drain
                              .mutateAsync({ name: n.name, terminate: true })
                              .then((r) => toast.success(`Terminated ${r.terminated.length}${r.failed?.length ? `, ${r.failed.length} failed` : ""}`))
                              .catch((e: Error) => toast.error(e.message))
                          }
                        >
                          Drain and terminate
                        </DropdownMenuItem>
                        <DropdownMenuSeparator />
                        <DropdownMenuItem
                          variant="destructive"
                          onClick={() => confirm(`Remove ${n.name} from the gateway? Its sandboxes' owners are kept, for if it comes back.`) && act(remove.mutateAsync(n.name), "Removed")}
                        >
                          Remove
                        </DropdownMenuItem>
                      </DropdownMenuContent>
                    </DropdownMenu>
                  </TableCell>
                </TableRow>
              );
            })}
          </TableBody>
        </Table>
      )}
    </div>
  );
}

/** Files are named, not uploaded: they must be under the gateway's --node-files-dir. */
function AddNode({ onClose }: { onClose: () => void }) {
  const add = useAddNode();
  const [spec, setSpec] = useState<NodeSpec>({ name: "", endpoint: "" });
  const field = (k: keyof NodeSpec, placeholder: string, mono = true) => (
    <Input aria-label={k} placeholder={placeholder} value={spec[k] ?? ""} onChange={(e) => setSpec({ ...spec, [k]: e.target.value })} className={mono ? "font-mono" : ""} />
  );
  return (
    <Card className="surface-sheen gap-0 py-0">
      <CardContent className="p-4">
        <form
          className="grid gap-2 md:grid-cols-2"
          onSubmit={(e) => {
            e.preventDefault();
            const clean = Object.fromEntries(Object.entries(spec).filter(([, v]) => v)) as unknown as NodeSpec;
            add.mutate(clean, {
              onSuccess: (n) => {
                toast.success(`Added ${n.name}${n.healthy ? "" : ", not answering yet"}`);
                onClose();
              },
              onError: (err) => toast.error(err.message),
            });
          }}
        >
          {field("name", "name (its --node-id)")}
          {field("endpoint", "https://10.0.0.17:7443")}
          {field("token_file", "token file, under the node files directory")}
          {field("ca_file", "CA file (optional)")}
          {field("cert_file", "client certificate (optional)")}
          {field("key_file", "client key (optional)")}
          <div className="flex gap-2 md:col-span-2">
            <Button type="submit" disabled={add.isPending || !spec.name || !spec.endpoint}>Add</Button>
            <Button type="button" variant="ghost" onClick={onClose}>Close</Button>
          </div>
        </form>
      </CardContent>
    </Card>
  );
}

export default function NodesPage() {
  return (
    <Gate need="admin">
      <Nodes />
    </Gate>
  );
}
