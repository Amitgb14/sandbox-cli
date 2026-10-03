"use client";

import { useState } from "react";
import Link from "next/link";
import { Boxes } from "lucide-react";
import { PageHeader } from "@/components/common/page-header";
import { EmptyState } from "@/components/common/empty-state";
import { StatusBadge } from "@/components/common/status-badge";
import { Labels } from "@/components/sandbox/labels";
import { Input } from "@/components/ui/input";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { useSandboxes } from "@/lib/api/queries";
import { formatRelative } from "@/lib/format";

/** Every sandbox on this sandboxd, terminated ones included until it forgets them. */
export default function SandboxesPage() {
  const { data, isLoading, error } = useSandboxes();
  const [filter, setFilter] = useState("");
  const q = filter.trim().toLowerCase();
  const rows = (data ?? []).filter(
    (s) =>
      !q ||
      s.id.includes(q) ||
      (s.name ?? "").toLowerCase().includes(q) ||
      Object.entries(s.labels ?? {}).some(([k, v]) => `${k}=${v}`.toLowerCase().includes(q)),
  );
  return (
    <div className="flex flex-col gap-4">
      <PageHeader
        title="Sandboxes"
        description="Every sandbox on this sandboxd — started from Studio, the CLI, an SDK or anything else that talks to it."
        actions={<Input placeholder="Filter by id, name or label" value={filter} onChange={(e) => setFilter(e.target.value)} className="w-64" />}
      />
      {error ? <p className="text-sm text-destructive">{error.message}</p> : null}
      {!isLoading && rows.length === 0 ? (
        <EmptyState icon={Boxes} title="No sandboxes" description={q ? "Nothing matches that filter." : "Launch one, or run sandbox-cli run."} />
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Sandbox</TableHead>
              <TableHead>State</TableHead>
              <TableHead>Image</TableHead>
              <TableHead>Network</TableHead>
              <TableHead>Labels</TableHead>
              <TableHead className="text-right">Created</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {rows.map((s) => (
              <TableRow key={s.id}>
                <TableCell className="font-mono text-xs">
                  <Link href={`/sandbox?id=${s.id}`} className="hover:underline">{s.id}</Link>
                  {s.name ? <span className="ml-2 text-muted-foreground">{s.name}</span> : null}
                </TableCell>
                <TableCell><StatusBadge outcome={s.state} size="sm" /></TableCell>
                <TableCell className="max-w-48 truncate font-mono text-xs" title={s.image}>{s.image}</TableCell>
                <TableCell className="font-mono text-xs">{s.network.mode}</TableCell>
                <TableCell><Labels labels={s.labels} /></TableCell>
                <TableCell className="text-right text-xs text-muted-foreground">{formatRelative(s.created_at)}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </div>
  );
}
