"use client";

import Link from "next/link";
import { Boxes, Camera, Hand, HardDrive, Plus } from "lucide-react";
import { PageHeader, SectionHeader } from "@/components/common/page-header";
import { MetricTile } from "@/components/common/metric-tile";
import { QuickStart } from "@/components/common/quick-start";
import { Button } from "@/components/ui/button";
import { StatusBadge } from "@/components/common/status-badge";
import { Labels } from "@/components/sandbox/labels";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { useAgentStates, useInfo, useSandboxes, useSnapshots, useVolumes } from "@/lib/api/queries";
import { formatRelative } from "@/lib/format";

/** What is running, which agents are waiting for you, and what this sandboxd can do. */
export default function OverviewPage() {
  const { data: info } = useInfo();
  const { data: sandboxes, isLoading } = useSandboxes();
  const { data: snapshots } = useSnapshots(info?.capabilities?.capabilities?.memory_snapshot === true);
  const { data: volumes } = useVolumes(info?.capabilities?.capabilities?.volumes === true);
  const { data: agentStates } = useAgentStates();
  const agentOf = new Map((agentStates ?? []).map((a) => [a.sandbox, a]));
  const waiting = (agentStates ?? []).filter((a) => a.state === "blocked");
  const live = (sandboxes ?? []).filter((s) => s.state !== "terminated");
  const caps = info?.capabilities;

  return (
    <div className="flex flex-col gap-8">
      <PageHeader
        title="Overview"
        description={info ? `Context ${info.context} · sandbox-cli ${info.version}` : "Connecting to sandboxd…"}
        actions={
          <Button asChild size="sm">
            <Link href="/launch">
              <Plus className="size-4" />
              New sandbox
            </Link>
          </Button>
        }
      />
      <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
        <MetricTile
          label="Running"
          icon={Boxes}
          loading={isLoading}
          value={live.filter((s) => s.state === "running").length}
          footer={`${live.filter((s) => s.state === "suspended").length} suspended`}
          hint="sandboxes on this sandboxd"
        />
        <MetricTile label="Waiting for you" icon={Hand} value={agentStates ? waiting.length : null} hint="agents quiet at a terminal" absentReason="Agent states unavailable" />
        <MetricTile
          label="Snapshots"
          icon={Camera}
          value={caps?.capabilities.memory_snapshot ? (snapshots?.length ?? null) : null}
          absentReason={caps && !caps.capabilities.memory_snapshot ? "Not offered by this backend" : undefined}
          hint="sandboxes captured to start from"
        />
        <MetricTile
          label="Volumes"
          icon={HardDrive}
          value={caps?.capabilities.volumes ? (volumes?.length ?? null) : null}
          footer={volumes ? `${volumes.filter((v) => v.attached_to).length} mounted` : undefined}
          absentReason={caps && !caps.capabilities.volumes ? "Not offered by this backend" : undefined}
          hint="named filesystems that outlive sandboxes"
        />
      </div>

      {!isLoading && (sandboxes ?? []).length === 0 ? <QuickStart title="Start your first sandbox" /> : null}

      {(sandboxes ?? []).length > 0 && (
        <section className="flex flex-col gap-3">
          <SectionHeader title="Live sandboxes" actions={<Link href="/sandboxes" className="text-sm text-muted-foreground hover:text-foreground">all →</Link>} />
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Sandbox</TableHead>
                <TableHead>State</TableHead>
                <TableHead>Agent</TableHead>
                <TableHead>Labels</TableHead>
                <TableHead className="text-right">Created</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {live.slice(0, 8).map((s) => (
                <TableRow key={s.id}>
                  <TableCell className="font-mono text-xs">
                    <Link href={`/sandbox?id=${s.id}`} className="hover:underline">
                      {s.name || s.id}
                    </Link>
                  </TableCell>
                  <TableCell><StatusBadge outcome={s.state} size="sm" /></TableCell>
                  <TableCell title={agentOf.get(s.id)?.why}>
                    {agentOf.has(s.id) ? <StatusBadge outcome={agentOf.get(s.id)!.state} size="sm" /> : <span className="text-xs text-muted-foreground">—</span>}
                  </TableCell>
                  <TableCell><Labels labels={s.labels} /></TableCell>
                  <TableCell className="text-right text-xs text-muted-foreground">{formatRelative(s.created_at)}</TableCell>
                </TableRow>
              ))}
              {live.length === 0 && !isLoading && (
                <TableRow>
                  <TableCell colSpan={5} className="text-sm text-muted-foreground">
                    Nothing running. <Link href="/launch" className="underline">Launch something</Link>.
                  </TableCell>
                </TableRow>
          )}
          </TableBody>
        </Table>
      </section>
      )}

      {caps && (
        <section className="flex flex-col gap-3">
          <SectionHeader title="What this sandboxd can do" description="A request for anything it lacks is refused, never served weaker." />
          <Card className="surface-sheen gap-0 py-0">
            <CardContent className="flex flex-col gap-4 p-4">
              <div className="flex flex-wrap gap-1.5">
                {Object.entries(caps.capabilities)
                  .sort()
                  .map(([k, v]) => (
                    <Badge key={k} variant="outline" className={v ? "border-contained/40 bg-contained/5 text-contained" : "text-muted-foreground line-through"}>
                      {k}
                    </Badge>
                  ))}
              </div>
              <dl className="grid grid-cols-2 gap-4 border-t pt-4 text-sm sm:grid-cols-5">
                {(
                  [
                    ["Network default", caps.network.default.mode],
                    ["Network ceiling", caps.network.ceiling],
                    ["CPUs", caps.limits.max_cpus],
                    ["Memory", `${caps.limits.max_memory_mb} MiB`],
                    ["Disk", `${caps.limits.max_disk_mb} MiB`],
                  ] as const
                ).map(([k, v]) => (
                  <div key={k} className="flex flex-col gap-0.5">
                    <dt className="text-xs text-muted-foreground">{k}</dt>
                    <dd className="font-mono text-[13px] tabular-nums">{v}</dd>
                  </div>
                ))}
              </dl>
            </CardContent>
          </Card>
        </section>
      )}
    </div>
  );
}
