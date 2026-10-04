"use client";

import { Camera } from "lucide-react";
import { toast } from "sonner";
import { CodeBlock } from "@/components/common/code-block";
import { EmptyState } from "@/components/common/empty-state";
import { PageHeader } from "@/components/common/page-header";
import { Button } from "@/components/ui/button";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { useDeleteSnapshot, useInfo, useSnapshots } from "@/lib/api/queries";
import { formatBytes, formatRelative } from "@/lib/format";
import { useCan } from "@/lib/caller";

/**
 * Snapshots: a sandbox's memory, processes and disk, captured so new
 * sandboxes start from that moment instead of from the image — the template
 * every hosted sandbox product has under some name. Taken from a running
 * sandbox's page; started from with --from-snapshot.
 */
export default function SnapshotsPage() {
  const { data: info } = useInfo();
  const can = info?.capabilities?.capabilities?.memory_snapshot;
  const { data, isLoading, error } = useSnapshots(can !== false);
  const del = useDeleteSnapshot();
  const allows = useCan();
  const rows = [...(data ?? [])].sort((a, b) => b.created_at.localeCompare(a.created_at));

  return (
    <div className="flex flex-col gap-5">
      <PageHeader
        title="Snapshots"
        description="A running sandbox captured whole — memory, processes and disk — so new sandboxes start from that moment. Take one from a sandbox's page."
      />
      {can === false ? (
        <EmptyState
          icon={Camera}
          title="This sandboxd cannot take snapshots"
          description="Its backend does not offer memory snapshots (capability memory_snapshot), so a request for one is refused rather than served without it."
        />
      ) : error ? (
        <p className="text-sm text-destructive">{error.message}</p>
      ) : !isLoading && rows.length === 0 ? (
        <EmptyState
          icon={Camera}
          title="No snapshots yet"
          description="Open a running sandbox and take one; it appears here, and any number of new sandboxes can start from it."
        />
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Snapshot</TableHead>
              <TableHead>From sandbox</TableHead>
              <TableHead>Image</TableHead>
              <TableHead className="text-right">Size</TableHead>
              <TableHead className="text-right">Taken</TableHead>
              <TableHead className="text-right" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {rows.map((s) => (
              <TableRow key={s.id}>
                <TableCell className="font-mono text-xs">{s.id}</TableCell>
                <TableCell className="font-mono text-xs text-muted-foreground">{s.sandbox}</TableCell>
                <TableCell className="max-w-48 truncate font-mono text-xs" title={s.image}>
                  {s.image}
                </TableCell>
                <TableCell className="text-right text-xs tabular-nums">{formatBytes(s.bytes)}</TableCell>
                <TableCell className="text-right text-xs text-muted-foreground">{formatRelative(s.created_at)}</TableCell>
                <TableCell className="text-right">
                  {allows("sandbox:delete") && <Button
                    size="sm"
                    variant="ghost"
                    disabled={del.isPending}
                    onClick={() => {
                      if (confirm(`Delete ${s.id}? New sandboxes can no longer start from it.`)) {
                        del.mutate(s.id, { onError: (e) => toast.error(e.message), onSuccess: () => toast.success("Deleted") });
                      }
                    }}
                  >
                    Delete
                  </Button>}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
      {can !== false && (
        <div className="max-w-2xl">
          <p className="mb-2 text-sm font-medium">Start a sandbox from one</p>
          <CodeBlock value="sandbox-cli run --from-snapshot SNAPSHOT_ID -- COMMAND" title="CLI">
            sandbox-cli run --from-snapshot SNAPSHOT_ID -- COMMAND
          </CodeBlock>
        </div>
      )}
    </div>
  );
}
