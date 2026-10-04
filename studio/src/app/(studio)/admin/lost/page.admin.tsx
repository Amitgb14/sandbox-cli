"use client";

import { Unplug } from "lucide-react";
import { EmptyState } from "@/components/common/empty-state";
import { PageHeader } from "@/components/common/page-header";
import { Gate } from "@/components/shell/gate";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { useLost } from "@/lib/admin/api";
import { DASH, formatMiB, formatRelative } from "@/lib/format";

/**
 * Sandboxes recorded on nodes that have not answered for longer than the
 * gateway's grace period. Their state is unknown — the node may come back
 * with them running — and they no longer count against a tenant's quota.
 */
function Lost() {
  const { data, isLoading, error } = useLost();
  const rows = data ?? [];
  return (
    <div className="flex flex-col gap-5">
      <PageHeader title="Lost sandboxes" description="On nodes that stopped answering. Their state is unknown until the node answers again; they no longer count against their tenant's quota." />
      {error ? (
        <p className="text-sm text-destructive">{error.message}</p>
      ) : !isLoading && rows.length === 0 ? (
        <EmptyState icon={Unplug} title="Nothing lost" description="Every node holding a sandbox is answering." />
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Sandbox</TableHead>
              <TableHead>Owner</TableHead>
              <TableHead>Node</TableHead>
              <TableHead>Given</TableHead>
              <TableHead>Node down since</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {rows.map((s) => (
              <TableRow key={s.id}>
                <TableCell className="font-mono text-xs">{s.id}</TableCell>
                <TableCell className="text-sm">
                  {s.user}
                  {s.tenant ? <span className="text-muted-foreground">@{s.tenant}</span> : null}
                </TableCell>
                <TableCell className="font-mono text-xs">{s.node}</TableCell>
                <TableCell className="font-mono text-xs text-muted-foreground">
                  {s.cpus ? `${s.cpus} cpus · ${formatMiB(s.memory_mb)}` : DASH}
                </TableCell>
                <TableCell className="text-xs text-muted-foreground">{s.node_down_since ? formatRelative(s.node_down_since) : "node removed"}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </div>
  );
}

export default function LostPage() {
  return (
    <Gate need="admin">
      <Lost />
    </Gate>
  );
}
