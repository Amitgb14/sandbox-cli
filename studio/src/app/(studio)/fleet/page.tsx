"use client";

import { Network } from "lucide-react";
import { toast } from "sonner";
import { PageHeader } from "@/components/common/page-header";
import { EmptyState } from "@/components/common/empty-state";
import { RepoGate } from "@/components/common/repo-gate";
import { StatusBadge } from "@/components/common/status-badge";
import { Button } from "@/components/ui/button";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { useFleet, useLand } from "@/lib/api/queries";
import type { Repo } from "@/lib/types";
import { formatRelative } from "@/lib/format";

/**
 * The last fleet run on this repository, and landing it. A fleet is started
 * from the terminal (`sandbox-cli agent fleet run -f fleet.yaml`), which owns
 * its timing; landing goes through the same refusals the CLI makes — a branch
 * that did not verify is skipped, a base that moved stops it.
 */
function Fleet({ repo }: { repo: Repo }) {
  const { data: st, error } = useFleet(repo.id);
  const land = useLand();
  if (error)
    return <EmptyState icon={Network} title="No fleet run recorded" description="Start one from a terminal: sandbox-cli agent fleet run -f fleet.yaml" />;
  if (!st) return <p className="text-sm text-muted-foreground">Loading…</p>;
  const tasks = Object.values(st.tasks).sort((a, b) => a.branch.localeCompare(b.branch));
  const doLand = (body: { branch?: string; all?: boolean; unverified?: boolean }) =>
    land.mutate(
      { repo: repo.id, ...body },
      {
        onSuccess: (r) => {
          const landed = r.landed ?? [];
          toast.success(landed.length ? `Landed ${landed.join(", ")}` : "Nothing landed");
          for (const s of r.skipped ?? []) toast.message(`Skipped ${s.Branch}`, { description: s.Reason });
        },
        onError: (e) => toast.error(e.message),
      },
    );
  return (
    <div className="flex flex-col gap-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <p className="text-sm text-muted-foreground">
          Started {formatRelative(st.started_at)} from <span className="font-mono">{st.base_branch || st.base_commit.slice(0, 12)}</span>; landing merges into the branch you have checked out.
        </p>
        <Button onClick={() => doLand({ all: true })} disabled={land.isPending}>
          Land everything that verified
        </Button>
      </div>
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Branch</TableHead>
            <TableHead>Agent</TableHead>
            <TableHead>State</TableHead>
            <TableHead>Work</TableHead>
            <TableHead className="text-right" />
          </TableRow>
        </TableHeader>
        <TableBody>
          {tasks.map((t) => (
            <TableRow key={t.branch}>
              <TableCell className="font-mono text-xs">{t.branch}</TableCell>
              <TableCell className="font-mono text-xs">{t.agent}</TableCell>
              <TableCell><StatusBadge outcome={t.state} exitCode={t.exit_code} size="sm" /></TableCell>
              <TableCell className="font-mono text-xs">
                {t.ref ?? (t.checkpoint ? `${t.checkpoint} (checkpoint)` : t.error ?? "—")}
                {!t.ref && t.checkpoint && t.error && (
                  <div className="text-muted-foreground">{t.error}</div>
                )}
              </TableCell>
              <TableCell className="text-right">
                {t.ref && t.state === "verified" && (
                  <Button size="sm" variant="outline" disabled={land.isPending} onClick={() => doLand({ branch: t.branch })}>Land</Button>
                )}
                {t.ref && (t.state === "failed" || t.state === "rejected") && (
                  <Button size="sm" variant="ghost" disabled={land.isPending}
                    onClick={() => confirm(`Land ${t.branch} although it did not verify?`) && doLand({ branch: t.branch, unverified: true })}>
                    Land unverified
                  </Button>
                )}
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  );
}

export default function FleetPage() {
  return (
    <div className="flex flex-col gap-4">
      <PageHeader title="Fleet" description="One agent per branch, each in its own sandbox; land only what its verify accepted." />
      <RepoGate>{(repo) => <Fleet repo={repo} />}</RepoGate>
    </div>
  );
}
