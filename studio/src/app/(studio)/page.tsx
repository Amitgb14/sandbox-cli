"use client";

import Link from "next/link";
import { Boxes, History, PauseCircle, ShieldCheck } from "lucide-react";
import { PageHeader, SectionHeader } from "@/components/common/page-header";
import { MetricTile } from "@/components/common/metric-tile";
import { StatusBadge } from "@/components/common/status-badge";
import { Labels } from "@/components/sandbox/labels";
import { Badge } from "@/components/ui/badge";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { useInfo, useRuns, useSandboxes } from "@/lib/api/queries";
import { formatRelative } from "@/lib/format";

/** What is running, what came back, and what this sandboxd can do. */
export default function DashboardPage() {
  const { data: info } = useInfo();
  const { data: sandboxes, isLoading } = useSandboxes();
  const { data: runs } = useRuns();
  const live = (sandboxes ?? []).filter((s) => s.state !== "terminated");
  const caps = info?.capabilities;

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="Dashboard"
        description={info ? `Context ${info.context} · sandbox-cli ${info.version}` : "Connecting to sandboxd…"}
      />
      <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
        <MetricTile label="Running" icon={Boxes} loading={isLoading} value={live.filter((s) => s.state === "running").length} hint="sandboxes on this sandboxd" />
        <MetricTile label="Suspended" icon={PauseCircle} loading={isLoading} value={live.filter((s) => s.state === "suspended").length} hint="memory kept, nothing running" />
        <MetricTile label="Work not back" icon={History} value={runs?.filter((r) => !r.done).length ?? null} hint="runs whose commits have not been brought back" />
        <MetricTile label="Backend" icon={ShieldCheck} value={caps?.backend ?? null} hint={caps ? `API ${caps.api_version}` : undefined} />
      </div>

      <section className="flex flex-col gap-3">
        <SectionHeader title="Sandboxes" actions={<Link href="/sandboxes" className="text-sm text-muted-foreground hover:text-foreground">all →</Link>} />
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Sandbox</TableHead>
              <TableHead>State</TableHead>
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
                <TableCell><Labels labels={s.labels} /></TableCell>
                <TableCell className="text-right text-xs text-muted-foreground">{formatRelative(s.created_at)}</TableCell>
              </TableRow>
            ))}
            {live.length === 0 && !isLoading && (
              <TableRow>
                <TableCell colSpan={4} className="text-sm text-muted-foreground">
                  Nothing running. <Link href="/launch" className="underline">Launch something</Link>.
                </TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
      </section>

      {caps && (
        <section className="flex flex-col gap-3">
          <SectionHeader title="What this sandboxd can do" description="A request for anything it lacks is refused, never served weaker." />
          <div className="flex flex-wrap gap-1.5">
            {Object.entries(caps.capabilities)
              .sort()
              .map(([k, v]) => (
                <Badge key={k} variant="outline" className={v ? "border-contained/40 text-contained" : "text-muted-foreground line-through"}>
                  {k}
                </Badge>
              ))}
          </div>
          <p className="text-sm text-muted-foreground">
            Network: default <span className="font-mono">{caps.network.default.mode}</span>, ceiling{" "}
            <span className="font-mono">{caps.network.ceiling}</span>. Limits: {caps.limits.max_cpus} CPUs,{" "}
            {caps.limits.max_memory_mb} MiB memory, {caps.limits.max_disk_mb} MiB disk.
          </p>
        </section>
      )}
    </div>
  );
}
