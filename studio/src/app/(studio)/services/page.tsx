"use client";

import { useState } from "react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { Plus, Workflow } from "lucide-react";
import { toast } from "sonner";
import { EmptyState } from "@/components/common/empty-state";
import { PageHeader } from "@/components/common/page-header";
import { StatusBadge } from "@/components/common/status-badge";
import { Gate } from "@/components/shell/gate";
import { Button } from "@/components/ui/button";
import { Card, CardContent } from "@/components/ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Textarea } from "@/components/ui/textarea";
import { serviceHref, useDeployService, useServices } from "@/lib/api/gateway";
import { useCaller, useCan } from "@/lib/caller";
import { formatRelative } from "@/lib/format";
import type { Service, ServiceSpec } from "@/lib/types";

const EXAMPLE = `{
  "name": "web",
  "image": "",
  "command": ["./serve", "--port", "8080"],
  "replicas": 2,
  "port": 8080,
  "health": { "http": "/healthz" }
}`;

/** in_progress, done, failed — or, with no rollout, nothing to say. */
function rolloutOf(s: Service): string {
  if (!s.rollout || s.rollout.state === "done") return `rev ${s.revision}`;
  return s.rollout.state === "failed" ? `rev ${s.rollout.to} failed` : `rolling ${s.rollout.from} → ${s.rollout.to}`;
}

/**
 * Services: a sandbox spec and a count the gateway keeps true, replacing a
 * replica whose command exits or whose health check fails.
 */
function Services() {
  const can = useCan();
  const router = useRouter();
  const { data, isLoading, error } = useServices();
  const [open, setOpen] = useState(false);
  const rows = data ?? [];

  return (
    <div className="flex flex-col gap-5">
      <PageHeader
        title="Services"
        description="Sandbox specs the gateway keeps a count of: each replica is one of your sandboxes, replaced when its command exits or its health check fails. A changed spec rolls out one replica at a time."
        actions={
          can("sandbox:create") && !open ? (
            <Button size="sm" onClick={() => setOpen(true)}>
              <Plus className="size-4" />
              Deploy service
            </Button>
          ) : undefined
        }
      />
      {open && can("sandbox:create") && <Deploy existing={rows} onClose={() => setOpen(false)} />}
      {error ? (
        <p className="text-sm text-destructive">{error.message}</p>
      ) : !isLoading && rows.length === 0 ? (
        <EmptyState icon={Workflow} title="No services" description="Deploy one from a spec here, or with sandbox-cli service deploy -f service.yaml." />
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Service</TableHead>
              <TableHead>Ready</TableHead>
              <TableHead>Revision</TableHead>
              <TableHead>URL</TableHead>
              <TableHead>Updated</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {rows.map((s) => (
              <TableRow key={`${s.tenant}/${s.spec.name}`} className="cursor-pointer" onClick={(e) => !(e.target as HTMLElement).closest("a,button") && router.push(serviceHref(s))}>
                <TableCell>
                  <Link href={serviceHref(s)} className="flex flex-col hover:underline">
                    <span className="text-sm font-medium">{s.spec.name}</span>
                    <span className="text-[11px] text-muted-foreground">
                      {s.owner}
                      {s.tenant ? `@${s.tenant}` : ""}
                    </span>
                  </Link>
                  {s.error ? <p className="mt-1 max-w-80 truncate text-xs text-destructive" title={s.error}>{s.error}</p> : null}
                </TableCell>
                <TableCell>
                  <span className="flex items-center gap-2">
                    <StatusBadge outcome={s.ready >= s.desired && s.desired > 0 ? "healthy" : s.ready === 0 && s.desired > 0 ? "unhealthy" : "starting"} size="sm" />
                    <span className="font-mono text-xs tabular-nums">
                      {s.ready}/{s.desired}
                    </span>
                  </span>
                </TableCell>
                <TableCell className="font-mono text-xs text-muted-foreground">{rolloutOf(s)}</TableCell>
                <TableCell className="max-w-56 truncate font-mono text-xs">{s.url ?? <span className="text-muted-foreground">private</span>}</TableCell>
                <TableCell className="text-xs text-muted-foreground">{formatRelative(s.updated_at)}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </div>
  );
}

/** A spec as JSON, deployed as `service deploy` does: created, or updated when the name exists. */
function Deploy({ existing, onClose }: { existing: Service[]; onClose: () => void }) {
  const router = useRouter();
  const deploy = useDeployService();
  const caller = useCaller();
  const tenant = caller.kind === "gateway" ? caller.who.tenant : "";
  const [text, setText] = useState(EXAMPLE);
  const [problem, setProblem] = useState<string | null>(null);

  function send(e: React.FormEvent) {
    e.preventDefault();
    let spec: ServiceSpec;
    try {
      spec = JSON.parse(text);
    } catch (err) {
      setProblem(`Not JSON: ${(err as Error).message}`);
      return;
    }
    if (!spec || typeof spec !== "object" || typeof spec.name !== "string") {
      setProblem("A spec is an object with a name.");
      return;
    }
    if (spec.image === "") delete spec.image;
    setProblem(null);
    // Names are unique per tenant, and PUT names one in the caller's own.
    const update = existing.some((s) => s.spec.name === spec.name && (s.tenant ?? "") === tenant);
    deploy.mutate(
      { spec, update },
      {
        onSuccess: (s) => {
          toast.success(`${update ? "Updated" : "Deployed"} ${s.spec.name}`);
          router.push(serviceHref(s));
        },
        onError: (err) => setProblem(err.message),
      },
    );
  }

  return (
    <Card className="surface-sheen gap-0 py-0">
      <CardContent className="p-4">
        <form onSubmit={send} className="flex flex-col gap-3">
          <p className="text-xs text-muted-foreground">
            A ServiceSpec as JSON (docs/fleet.md, Services). One with the name of an existing service updates it. Environment values are kept by the gateway; a value
            that must stay secret belongs in Secrets, named under <code className="font-mono">secrets</code>.
          </p>
          <Textarea aria-label="Service spec" value={text} onChange={(e) => setText(e.target.value)} className="min-h-56 font-mono text-xs" spellCheck={false} />
          {problem ? <p className="text-sm text-destructive">{problem}</p> : null}
          <div className="flex gap-2">
            <Button type="submit" disabled={deploy.isPending}>Deploy</Button>
            <Button type="button" variant="ghost" onClick={onClose}>Close</Button>
          </div>
        </form>
      </CardContent>
    </Card>
  );
}

export default function ServicesPage() {
  return (
    <Gate need="gateway">
      <Services />
    </Gate>
  );
}
