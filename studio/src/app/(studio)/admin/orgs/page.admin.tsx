"use client";

import { Building2 } from "lucide-react";
import { EmptyState } from "@/components/common/empty-state";
import { PageHeader } from "@/components/common/page-header";
import { Gate } from "@/components/shell/gate";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { useAdminOrgs } from "@/lib/admin/api";
import { formatRelative } from "@/lib/format";

/**
 * Every organisation on the gateway: who made it and how many belong to it.
 * Users make them (org:create); the operator sees them all here, and each is a
 * tenant with its own quota.
 */
function Orgs() {
  const { data, isLoading, error } = useAdminOrgs();
  const rows = data ?? [];
  return (
    <div className="flex flex-col gap-5">
      <PageHeader
        title="All organizations"
        description="Tenants users made with org:create. Each has its own sandboxes, secrets, jobs, services and quota; how many one user may make is --max-orgs-per-user."
      />
      {error ? (
        <p className="text-sm text-destructive">{error.message}</p>
      ) : !isLoading && rows.length === 0 ? (
        <EmptyState icon={Building2} title="No organizations" description="None has been created on this gateway." />
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Organization</TableHead>
              <TableHead>Created by</TableHead>
              <TableHead>Members</TableHead>
              <TableHead>Owners</TableHead>
              <TableHead>Created</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {rows.map((o) => (
              <TableRow key={o.name}>
                <TableCell className="font-mono text-sm">{o.name}</TableCell>
                <TableCell className="text-sm">
                  {o.created_by}
                  {o.created_by_tenant ? <span className="text-muted-foreground">@{o.created_by_tenant}</span> : null}
                </TableCell>
                <TableCell className="tabular-nums">{o.members}</TableCell>
                <TableCell className="tabular-nums">{o.owners}</TableCell>
                <TableCell className="text-xs text-muted-foreground">{formatRelative(o.created)}</TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </div>
  );
}

export default function AdminOrgsPage() {
  return (
    <Gate need="admin">
      <Orgs />
    </Gate>
  );
}
