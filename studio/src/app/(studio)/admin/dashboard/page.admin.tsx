"use client";

import { Boxes, Building2, Cpu, HardDrive, KeyRound, ListChecks, MemoryStick, Server, Users, Workflow } from "lucide-react";
import { MetricTile } from "@/components/common/metric-tile";
import { PageHeader } from "@/components/common/page-header";
import { Gate } from "@/components/shell/gate";
import { Card, CardContent } from "@/components/ui/card";
import { Progress } from "@/components/ui/progress";
import { useStats, type Resources } from "@/lib/admin/api";
import { formatMiB, formatRelative } from "@/lib/format";

/**
 * The super admin's dashboard: the gateway in totals. The super admin runs
 * the gateway and does not see into users' sandboxes — the gateway lists no
 * one else's to them — so this shows how many there are and what they take,
 * never which or whose (GET /v1/admin/stats).
 */
function Dashboard() {
  const { data: s, isLoading, error } = useStats();
  if (error) return <p className="text-sm text-destructive">{error.message}</p>;
  const n = (v: number | undefined) => (isLoading ? null : (v ?? 0));
  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="Dashboard"
        description="The gateway in totals: users, organizations, sandboxes and what the nodes have and give. Counts only — which sandboxes there are, and whose, are their users' to see."
      />
      <section className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-4" aria-label="People">
        <MetricTile label="Users" icon={Users} value={n(s?.users.total)} loading={isLoading}
          footer={s ? `${s.users.accounts} sign in with a password · ${s.users.signed_in_24h} today` : undefined}
          hint="Everyone holding a working key or account of their own: people and the programs an admin issued keys to." />
        <MetricTile label="Organizations" icon={Building2} value={n(s?.orgs)} loading={isLoading}
          footer={s?.users.suspended ? `${s.users.suspended} user${s.users.suspended === 1 ? "" : "s"} suspended` : "each user's own, and those made"} />
        <MetricTile label="API keys" icon={KeyRound} value={n(s?.keys.active)} loading={isLoading}
          footer={s ? `${s.keys.personal} personal key${s.keys.personal === 1 ? "" : "s"} users made` : undefined} />
        <MetricTile label="Nodes" icon={Server} value={n(s?.nodes.total)} loading={isLoading}
          footer={s ? `${s.nodes.healthy} healthy${s.nodes.cordoned ? ` · ${s.nodes.cordoned} cordoned` : ""}` : undefined} />
      </section>
      <section className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-4" aria-label="Work">
        <MetricTile label="Sandboxes running" icon={Boxes} value={n(s?.sandboxes.running)} loading={isLoading}
          footer={s ? `${s.nodes.running} on the nodes' own count` : undefined} />
        <MetricTile label="Sandboxes created" icon={Boxes} value={n(s?.sandboxes.created_24h)} unit="today" loading={isLoading}
          footer={s ? `${s.sandboxes.created_7d} this week · ${s.sandboxes.created} ${s.sandboxes.history_days ? `in ${s.sandboxes.history_days} days` : "in all"}` : undefined}
          hint="Counted from the gateway's records, which keep a terminated sandbox for the history period (--sandbox-history)." />
        <MetricTile label="Jobs running" icon={ListChecks} value={n(s?.jobs.running)} loading={isLoading}
          footer={s ? `${s.jobs.total} kept` : undefined} />
        <MetricTile label="Services" icon={Workflow} value={n(s?.services)} loading={isLoading} />
      </section>
      {s ? (
        <Card className="surface-sheen gap-0 py-0">
          <CardContent className="flex flex-col gap-4 p-4">
            <div className="flex items-baseline justify-between gap-2">
              <h2 className="text-sm font-semibold">Capacity</h2>
              <span className="text-xs text-muted-foreground">what sandboxes are given, of what the nodes have · updated {formatRelative(s.time)}</span>
            </div>
            <Meter icon={MemoryStick} label="Memory" used={s.nodes.used.memory_mb} total={s.nodes.capacity.memory_mb} fmt={formatMiB} />
            <Meter icon={Cpu} label="CPUs" used={s.nodes.used.cpus} total={s.nodes.capacity.cpus} fmt={(v) => `${+v.toFixed(1)}`} />
            <Meter icon={HardDrive} label="Disk" used={s.nodes.used.disk_mb} total={s.nodes.capacity.disk_mb} fmt={formatMiB} />
            <Allocated a={s.allocated} />
          </CardContent>
        </Card>
      ) : null}
    </div>
  );
}

function Meter({ icon: Icon, label, used, total, fmt }: { icon: typeof Cpu; label: string; used: number; total: number; fmt: (v: number) => string }) {
  const pct = total > 0 ? Math.min(100, Math.round((used / total) * 100)) : 0;
  return (
    <div className="flex flex-col gap-1.5" data-testid={`meter-${label.toLowerCase()}`}>
      <div className="flex items-center justify-between gap-2 text-sm">
        <span className="flex items-center gap-2">
          <Icon className="size-4 text-muted-foreground" aria-hidden /> {label}
        </span>
        <span className="tabular-nums text-muted-foreground">
          {total > 0 ? `${fmt(used)} of ${fmt(total)} · ${pct}%` : "no node reports one"}
        </span>
      </div>
      <Progress value={pct} aria-label={`${label} used`} />
    </div>
  );
}

function Allocated({ a }: { a: Resources }) {
  return (
    <p className="text-xs text-muted-foreground">
      Running sandboxes were given {+a.cpus.toFixed(1)} CPUs, {formatMiB(a.memory_mb)} of memory and {formatMiB(a.disk_mb)} of disk, by the
      gateway&apos;s records. These are allocations, not live usage: a sandbox may use less than it was given.
    </p>
  );
}

export default function AdminDashboardPage() {
  return (
    <Gate need="admin">
      <Dashboard />
    </Gate>
  );
}
