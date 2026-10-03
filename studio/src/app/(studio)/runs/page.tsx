"use client";

import Link from "next/link";
import { History } from "lucide-react";
import { toast } from "sonner";
import { PageHeader } from "@/components/common/page-header";
import { EmptyState } from "@/components/common/empty-state";
import { RepoGate } from "@/components/common/repo-gate";
import { StatusBadge } from "@/components/common/status-badge";
import { Button } from "@/components/ui/button";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { useBringBack, useForgetRun, useRuns } from "@/lib/api/queries";
import type { Repo, Run } from "@/lib/types";
import { formatRelative } from "@/lib/format";

/**
 * Where each run's work is — the same answer `sandbox-cli recover` gives:
 * home already, in a sandbox that is still alive, in a checkpoint, or lost.
 */
function whereIs(r: Run): React.ReactNode {
  if (r.done) return r.brought_back ? <span className="font-mono text-xs">{r.brought_back}</span> : "nothing new came back";
  if (r.state === "running" || r.state === "pending") return "in the sandbox — bring it back";
  if (r.state === "suspended") return "in a suspended sandbox — resume it, then bring it back";
  if (r.checkpoint)
    return (
      <span>
        sandbox gone; last checkpoint{" "}
        <Link href={`/review?ref=${encodeURIComponent(r.checkpoint)}`} className="font-mono text-xs underline">
          {r.checkpoint}
        </Link>
      </span>
    );
  return <span className="text-status-critical">sandbox gone and no checkpoint was taken: lost</span>;
}

function RunsTable({ repo }: { repo: Repo }) {
  const { data, isLoading } = useRuns();
  const bring = useBringBack();
  const forget = useForgetRun();
  const runs = (data ?? []).filter((r) => r.repo_id === repo.id);
  if (!isLoading && runs.length === 0)
    return <EmptyState icon={History} title="No runs on this repository" description="Launch one, or run sandbox-cli run from the checkout." />;
  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>Sandbox</TableHead>
          <TableHead>Agent</TableHead>
          <TableHead>Started</TableHead>
          <TableHead>Sandbox state</TableHead>
          <TableHead>The work</TableHead>
          <TableHead className="text-right" />
        </TableRow>
      </TableHeader>
      <TableBody>
        {runs.map((r) => (
          <TableRow key={r.sandbox}>
            <TableCell className="font-mono text-xs">
              {r.state === "gone" ? r.sandbox : <Link href={`/sandbox?id=${r.sandbox}`} className="hover:underline">{r.sandbox}</Link>}
            </TableCell>
            <TableCell className="font-mono text-xs">{r.agent ?? "—"}</TableCell>
            <TableCell className="text-xs text-muted-foreground">{formatRelative(r.started)}</TableCell>
            <TableCell><StatusBadge outcome={r.state} size="sm" /></TableCell>
            <TableCell className="text-sm">{whereIs(r)}</TableCell>
            <TableCell className="text-right">
              {r.state === "running" && (
                <Button size="sm" variant="outline" disabled={bring.isPending}
                  onClick={() => bring.mutate(r.sandbox, {
                    onSuccess: (res) => {
                      toast.success(res.ref ? `Brought back to ${res.ref}` : "No new commits");
                      // The work is home either way; a mirror that failed is said, not hidden.
                      if (res.mirrored) toast.success(res.mirrored);
                      if (res.mirror_error) toast.error(`Not mirrored: ${res.mirror_error}`);
                    },
                    onError: (e) => toast.error(e.message),
                  })}>
                  Bring back
                </Button>
              )}
              {(r.done || r.state === "gone") && (
                <Button size="sm" variant="ghost" onClick={() => forget.mutate(r.sandbox)}>
                  Forget
                </Button>
              )}
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}

export default function RunsPage() {
  return (
    <div className="flex flex-col gap-4">
      <PageHeader
        title="Runs"
        description="Runs that cloned this repository, from Studio or the CLI. A detached run's work stays in its sandbox until you bring it back; Forget drops the record, never a ref."
      />
      <RepoGate>{(repo) => <RunsTable repo={repo} />}</RepoGate>
    </div>
  );
}
