"use client";

import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { useEvents } from "@/lib/api/queries";
import type { AuditEvent } from "@/lib/types";
import { formatDateTime, formatDuration } from "@/lib/format";

/**
 * A sandbox's audit events: what it was asked to do and how it ended, as
 * sandboxd recorded them. Environment variables appear by name only — the log
 * has nowhere to put a value.
 */
function detail(e: AuditEvent): string {
  const parts: string[] = [];
  switch (e.type) {
    case "sandbox.created":
      parts.push(`image ${e.image}`);
      if (e.network) parts.push(`network ${e.network.mode}`);
      if (e.env_names?.length) parts.push(`env ${e.env_names.join(", ")}`);
      if (e.labels) parts.push(Object.entries(e.labels).map(([k, v]) => `${k}=${v}`).join(", "));
      if (e.volumes?.length) parts.push(`volumes ${e.volumes.map((v) => `${v.name}:${v.path}`).join(", ")}`);
      break;
    case "process.started":
      parts.push(`pid ${e.pid}`, (e.argv ?? []).join(" "));
      break;
    case "process.exited":
      parts.push(`pid ${e.pid} exit ${e.exit_code} after ${formatDuration(e.duration_ms ?? 0)}`);
      break;
    case "file.read":
    case "file.written":
    case "file.removed":
      parts.push(e.path ?? "");
      break;
    case "sandbox.network_updated":
      if (e.network) parts.push(`${e.network.mode} ${(e.network.allow ?? []).join(", ")}`);
      break;
    case "tunnel.opened":
      parts.push(`port ${e.port}`);
      break;
  }
  if (e.reason) parts.push(e.reason);
  return parts.filter(Boolean).join(" · ");
}

export function SandboxEvents({ sandbox }: { sandbox: string }) {
  const { data, error, isLoading } = useEvents(sandbox);
  if (error) return <p className="text-sm text-muted-foreground">No events: {error.message}</p>;
  if (isLoading) return <p className="text-sm text-muted-foreground">reading…</p>;
  return (
    <div className="flex flex-col gap-2">
      {data?.truncated ? <p className="text-xs text-muted-foreground">(older events omitted)</p> : null}
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead className="w-44">Time</TableHead>
            <TableHead className="w-48">Event</TableHead>
            <TableHead>Detail</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {(data?.events ?? []).map((e, i) => (
            <TableRow key={i}>
              <TableCell className="font-mono text-xs">{formatDateTime(e.time)}</TableCell>
              <TableCell className="font-mono text-xs">{e.type}</TableCell>
              <TableCell className="max-w-0 truncate font-mono text-xs" title={detail(e)}>
                {detail(e)}
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  );
}
