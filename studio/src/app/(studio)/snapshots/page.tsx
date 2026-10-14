"use client";

import { useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { Camera, Rocket } from "lucide-react";
import { toast } from "sonner";
import { CodeBlock } from "@/components/common/code-block";
import { EmptyState } from "@/components/common/empty-state";
import { PageHeader } from "@/components/common/page-header";
import { Button } from "@/components/ui/button";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { useDeleteSnapshot, useInfo, useSandboxes, useSnapshot, useSnapshots } from "@/lib/api/queries";
import { formatBytes, formatRelative } from "@/lib/format";
import { useCan } from "@/lib/caller";

/**
 * Snapshots: a sandbox captured so new sandboxes start from it instead of from
 * the image — whole (memory, processes and disk) where the backend can, its
 * files only where it can capture no more (the macOS backend). Taken from a
 * running sandbox's panel or here; started from with Launch, which opens the
 * launch page with the snapshot chosen, or with --from-snapshot.
 */
const KIND = { memory: "Whole machine", disk: "Files only" } as const;
export default function SnapshotsPage() {
  const { data: info } = useInfo();
  const caps = info?.capabilities?.capabilities;
  const can = caps ? !!(caps.memory_snapshot || caps.disk_snapshot) : undefined;
  const { data, isLoading, error } = useSnapshots(can !== false);
  const del = useDeleteSnapshot();
  const allows = useCan();
  const rows = [...(data ?? [])].sort((a, b) => b.created_at.localeCompare(a.created_at));
  const router = useRouter();
  const launch = (id: string) => `/launch/?snapshot=${encodeURIComponent(id)}`;

  return (
    <div className="flex flex-col gap-5">
      <PageHeader
        title="Snapshots"
        description={
          caps?.memory_snapshot
            ? "A running sandbox captured whole — memory, processes and disk — so new sandboxes start from that moment. Take one from a sandbox's panel."
            : "A running sandbox's files, captured so new sandboxes start from them. What was running is not kept: a sandbox started from one boots afresh. Take one from a sandbox's panel."
        }
        actions={can && allows("sandbox:create") ? <TakeSnapshot onTaken={(id) => router.push(launch(id))} /> : null}
      />
      {can === false ? (
        <EmptyState
          icon={Camera}
          title="This sandboxd cannot take snapshots"
          description="Its backend captures neither a whole sandbox (memory_snapshot) nor its files (disk_snapshot), so a request for a snapshot is refused rather than served without it."
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
              <TableHead>Kind</TableHead>
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
                <TableCell className="text-xs whitespace-nowrap">{KIND[s.kind ?? "memory"]}</TableCell>
                <TableCell className="max-w-48 truncate font-mono text-xs" title={s.image}>
                  {s.image}
                </TableCell>
                <TableCell className="text-right text-xs tabular-nums">{formatBytes(s.bytes)}</TableCell>
                <TableCell className="text-right text-xs text-muted-foreground">{formatRelative(s.created_at)}</TableCell>
                <TableCell className="text-right whitespace-nowrap">
                  {allows("sandbox:create") && (
                    <Button size="sm" variant="ghost" asChild>
                      <Link href={launch(s.id)} title="Start a new sandbox from this snapshot">
                        <Rocket className="size-3.5" />
                        Launch
                      </Link>
                    </Button>
                  )}
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
          <p className="mb-2 text-sm font-medium">Start a sandbox from one from the command line</p>
          <CodeBlock value="sandbox-cli run --from-snapshot SNAPSHOT_ID -- COMMAND" title="CLI">
            sandbox-cli run --from-snapshot SNAPSHOT_ID -- COMMAND
          </CodeBlock>
        </div>
      )}
    </div>
  );
}

/**
 * Take a snapshot of one of the caller's running sandboxes from here, rather
 * than from its panel. Once taken, the toast offers to launch from it.
 */
function TakeSnapshot({ onTaken }: { onTaken: (id: string) => void }) {
  const { data: sandboxes } = useSandboxes();
  const take = useSnapshot();
  const [picked, setPicked] = useState("");
  const running = (sandboxes ?? []).filter((sb) => sb.state === "running" && !sb.snapshotting);
  const ref = running.some((sb) => sb.id === picked) ? picked : "";
  return (
    <div className="flex items-center gap-2">
      <Select value={ref} onValueChange={setPicked}>
        <SelectTrigger size="sm" className="w-56" aria-label="Sandbox to snapshot">
          <SelectValue placeholder={running.length ? "Choose a running sandbox" : "No running sandbox"} />
        </SelectTrigger>
        <SelectContent>
          {running.map((sb) => (
            <SelectItem key={sb.id} value={sb.id}>
              <span className="font-mono text-xs">{sb.name || sb.id}</span>
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
      <Button
        size="sm"
        disabled={!ref || take.isPending}
        onClick={() =>
          take.mutate(ref, {
            onError: (e) => toast.error(e.message),
            onSuccess: (snap) => {
              setPicked("");
              toast.success(`Snapshot ${snap.id} taken`, { action: { label: "Launch from it", onClick: () => onTaken(snap.id) } });
            },
          })
        }
      >
        <Camera className="size-3.5" />
        {take.isPending ? "Taking…" : "Take snapshot"}
      </Button>
    </div>
  );
}
