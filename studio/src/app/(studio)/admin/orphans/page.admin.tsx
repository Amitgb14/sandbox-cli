"use client";

import { UserX } from "lucide-react";
import { toast } from "sonner";
import { EmptyState } from "@/components/common/empty-state";
import { PageHeader } from "@/components/common/page-header";
import { Gate } from "@/components/shell/gate";
import { Button } from "@/components/ui/button";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { useEndOrphan, useOrphans } from "@/lib/admin/api";
import { formatMiB, formatRelative } from "@/lib/format";

/**
 * Sandboxes left running by users who can no longer reach them: suspended,
 * revoked or expired, or removed from the organization the sandbox is in.
 * Nobody else may act on another user's sandbox, so these would run until
 * their lifetime ran out; the super admin, who sees no one else's sandboxes
 * but these, ends them here by id. Nothing inside one is shown.
 */
function Orphans() {
  const { data, isLoading, error } = useOrphans();
  const end = useEndOrphan();
  const rows = data ?? [];
  const endAll = () => {
    if (!confirm(`Terminate all ${rows.length} left-behind sandboxes? Everything in them is discarded.`)) return;
    Promise.allSettled(rows.map((s) => end.mutateAsync(s.id))).then((rs) => {
      const failed = rs.filter((r) => r.status === "rejected").length;
      if (failed) toast.error(`${failed} could not be terminated`);
      else toast.success(`Terminated ${rs.length}`);
    });
  };
  return (
    <div className="flex flex-col gap-5">
      <PageHeader
        title="Left behind"
        description="Sandboxes still running whose owner can no longer reach them: suspended, revoked or expired, or removed from the organization the sandbox is in. Nobody else may act on them, so they run until their lifetime ends; terminate them here. Restoring the user, or adding them back, gives them back instead."
      />
      {error ? (
        <p className="text-sm text-destructive">{error.message}</p>
      ) : !isLoading && rows.length === 0 ? (
        <EmptyState icon={UserX} title="Nothing left behind" description="Every running sandbox's owner can still reach it." />
      ) : (
        <>
          {rows.length > 1 ? (
            <div>
              <Button variant="outline" size="sm" disabled={end.isPending} onClick={endAll}>
                Terminate all {rows.length}
              </Button>
            </div>
          ) : null}
          <Table data-testid="orphans">
            <TableHeader>
              <TableRow>
                <TableHead>Sandbox</TableHead>
                <TableHead>Owner</TableHead>
                <TableHead>Why</TableHead>
                <TableHead>Given</TableHead>
                <TableHead>Ends</TableHead>
                <TableHead className="text-right" />
              </TableRow>
            </TableHeader>
            <TableBody>
              {rows.map((s) => (
                <TableRow key={s.id}>
                  <TableCell className="text-sm">
                    {s.name || <span className="text-muted-foreground">unnamed</span>}
                    <div className="font-mono text-[11px] text-muted-foreground">{s.id} · {s.node}</div>
                  </TableCell>
                  <TableCell className="text-sm">
                    {s.user}
                    <div className="font-mono text-[11px] text-muted-foreground">in {s.org}</div>
                  </TableCell>
                  <TableCell className="max-w-64 text-xs text-muted-foreground">{s.reason}</TableCell>
                  <TableCell className="font-mono text-xs text-muted-foreground">
                    {s.cpus} cpus · {formatMiB(s.memory_mb)}
                  </TableCell>
                  <TableCell className="text-xs text-muted-foreground" title={s.expires_at}>
                    {s.expires_at ? formatRelative(s.expires_at) : "never"}
                  </TableCell>
                  <TableCell className="text-right">
                    <Button
                      size="sm"
                      variant="ghost"
                      disabled={end.isPending}
                      onClick={() =>
                        confirm(`Terminate ${s.name || s.id}, ${s.user}'s? Everything in it is discarded.`) &&
                        end.mutate(s.id, { onError: (e) => toast.error(e.message), onSuccess: () => toast.success(`Terminated ${s.name || s.id}`) })
                      }
                    >
                      Terminate
                    </Button>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </>
      )}
    </div>
  );
}

export default function OrphansPage() {
  return (
    <Gate need="admin">
      <Orphans />
    </Gate>
  );
}
