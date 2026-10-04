"use client";

import { Suspense, useState } from "react";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { toast } from "sonner";
import { PageHeader, SectionHeader } from "@/components/common/page-header";
import { StatusBadge } from "@/components/common/status-badge";
import { Gate } from "@/components/shell/gate";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { useRemoveService, useScaleService, useService } from "@/lib/api/gateway";
import { useCan } from "@/lib/caller";
import { DASH, formatArgv, formatMiB, formatRelative } from "@/lib/format";
import type { Service } from "@/lib/types";

/**
 * One service: its replicas and their health, its rollout, and — with the
 * scopes for it — scale and remove. The spec's environment comes back as
 * names only, as a sandbox's does.
 */
function ServiceDetail() {
  const params = useSearchParams();
  const name = params.get("name") ?? "";
  const tenant = params.get("tenant") ?? undefined;
  const router = useRouter();
  const can = useCan();
  const { data: svc, error } = useService(name, tenant);
  const scale = useScaleService();
  const remove = useRemoveService();
  const [replicas, setReplicas] = useState("");

  if (!name) return <p className="text-sm text-muted-foreground">No service named.</p>;
  if (error) return <p className="text-sm text-destructive">{error.message}</p>;
  if (!svc) return <p className="text-sm text-muted-foreground">Loading…</p>;

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title={svc.spec.name}
        description={
          <span className="flex flex-wrap items-center gap-x-2 gap-y-1">
            <span>
              {svc.owner}
              {svc.tenant ? `@${svc.tenant}` : ""}
            </span>
            <span>· revision {svc.revision}</span>
            <span>· updated {formatRelative(svc.updated_at)}</span>
          </span>
        }
        actions={
          can("sandbox:create") && can("sandbox:delete") ? (
            <Button variant="destructive" size="sm" disabled={remove.isPending}
              onClick={() =>
                confirm(`Remove ${svc.spec.name}? Its replicas are terminated.`) &&
                remove.mutate({ name, tenant }, { onSuccess: () => { toast.success("Removed"); router.push("/services"); }, onError: (e) => toast.error(e.message) })
              }>
              Remove
            </Button>
          ) : undefined
        }
      >
        <div className="mt-2 flex flex-wrap items-center gap-3 text-sm">
          <span className="font-mono text-xs tabular-nums">
            {svc.ready}/{svc.desired} ready
          </span>
          {svc.rollout && svc.rollout.state !== "done" ? (
            <span className={svc.rollout.state === "failed" ? "text-xs text-destructive" : "text-xs text-muted-foreground"}>
              {svc.rollout.state === "failed" ? `rollout to ${svc.rollout.to} failed: ${svc.rollout.reason ?? ""}` : `rolling out ${svc.rollout.from} → ${svc.rollout.to}`}
            </span>
          ) : null}
          {/* A link only to http(s): an href is the one place text from the API could become script. */}
          {svc.url && /^https?:\/\//i.test(svc.url) ? (
            <a href={svc.url} target="_blank" rel="noreferrer noopener" className="font-mono text-xs hover:underline">{svc.url}</a>
          ) : svc.url ? (
            <span className="font-mono text-xs">{svc.url}</span>
          ) : null}
        </div>
        {svc.error ? <p className="text-sm text-destructive">{svc.error}</p> : null}
      </PageHeader>

      {can("sandbox:create") && (
        <form
          className="flex items-end gap-2"
          onSubmit={(e) => {
            e.preventDefault();
            scale.mutate(
              { name, tenant, replicas: Number(replicas) },
              { onSuccess: () => { toast.success(`Scaled to ${replicas}`); setReplicas(""); }, onError: (err) => toast.error(err.message) },
            );
          }}
        >
          <Input aria-label="Replicas" placeholder={String(svc.desired)} value={replicas} onChange={(e) => setReplicas(e.target.value.replace(/\D/g, ""))} className="w-24 font-mono" required />
          <Button type="submit" variant="outline" disabled={scale.isPending || replicas === ""}>Scale</Button>
        </form>
      )}

      <section className="flex flex-col gap-3">
        <SectionHeader title="Replicas" description="Each is a sandbox you own. A replica whose command exits, or that fails its health check enough times in a row, is replaced." />
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Sandbox</TableHead>
              <TableHead>State</TableHead>
              <TableHead>Node</TableHead>
              <TableHead>Revision</TableHead>
              <TableHead>Last check</TableHead>
              <TableHead>Restarts</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {svc.replicas.map((r) => (
              <TableRow key={r.sandbox}>
                <TableCell className="font-mono text-xs">
                  <Link href={`/sandbox?id=${r.sandbox}`} className="hover:underline">{r.sandbox}</Link>
                </TableCell>
                <TableCell>
                  <StatusBadge outcome={r.state} size="sm" />
                </TableCell>
                <TableCell className="font-mono text-xs text-muted-foreground">{r.node}</TableCell>
                <TableCell className="font-mono text-xs tabular-nums">{r.revision}</TableCell>
                <TableCell className="text-xs text-muted-foreground">
                  {formatRelative(r.last_check)}
                  {r.last_error ? <span className="ml-2 text-destructive" title={r.last_error}>{r.last_error}</span> : null}
                </TableCell>
                <TableCell className="font-mono text-xs tabular-nums">{r.restarts}</TableCell>
              </TableRow>
            ))}
            {svc.replicas.length === 0 && (
              <TableRow>
                <TableCell colSpan={6} className="py-8 text-center text-sm text-muted-foreground">
                  No replicas yet.
                </TableCell>
              </TableRow>
            )}
          </TableBody>
        </Table>
      </section>

      <Spec svc={svc} />
    </div>
  );
}

function Spec({ svc }: { svc: Service }) {
  const s = svc.spec;
  const h = s.health;
  const rows: [string, string][] = [
    ["image", s.image || "the gateway's default"],
    ["command", s.command?.length ? formatArgv(s.command) : DASH],
    ["resources", `${s.resources?.cpus || "default"} cpus · ${s.resources?.memory_mb ? formatMiB(s.resources.memory_mb) : "default memory"}`],
    ["port", s.port ? String(s.port) : DASH],
    ["health", h ? (h.http ? `GET ${h.http}` : formatArgv(h.command ?? [])) + ` every ${h.every_secs ?? 10} s, ${h.failures ?? 3} failures` : "while its command runs"],
    ["network", s.network ? `${s.network.mode}${s.network.allow?.length ? `: ${s.network.allow.join(", ")}` : ""}` : "the gateway's default"],
    ["placement", s.placement?.spread ? `spread by ${s.placement.spread}` : DASH],
    ["public", s.public ? "yes, through the router" : "no"],
  ];
  if (svc.env_names?.length) rows.push(["env", svc.env_names.join(", ")]);
  if (s.secrets?.length) rows.push(["secrets", s.secrets.join(", ")]);
  return (
    <section className="flex flex-col gap-3">
      <SectionHeader title="Spec" />
      <dl className="grid max-w-3xl grid-cols-[8rem_1fr] gap-y-1.5 text-sm">
        {rows.map(([k, v]) => (
          <div key={k} className="contents">
            <dt className="text-muted-foreground">{k}</dt>
            <dd className="min-w-0 truncate font-mono text-[13px]">{v}</dd>
          </div>
        ))}
      </dl>
    </section>
  );
}

export default function ServicePage() {
  return (
    <Gate need="gateway">
      <Suspense fallback={<p className="text-sm text-muted-foreground">Loading…</p>}>
        <ServiceDetail />
      </Suspense>
    </Gate>
  );
}
