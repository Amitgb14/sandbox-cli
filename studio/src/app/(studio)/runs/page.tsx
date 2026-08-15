"use client";

import { useMemo } from "react";
import Link from "next/link";
import { Play, RotateCw } from "lucide-react";
import { Button } from "@/components/ui/button";
import { PageHeader } from "@/components/common/page-header";
import { RunsTable } from "@/components/runs/runs-table";
import { useAudit, useProjects, useRuns } from "@/lib/api/queries";
import { useUi } from "@/lib/store";
import { scopeToRepo } from "@/lib/derive";
import { cn } from "@/lib/utils";

export default function RunsPage() {
  const repoFilter = useUi((s) => s.repoFilter);
  const { data, isPending, isFetching, refetch } = useRuns();
  // Memoized because RunsTable feeds it to useReactTable, which requires a
  // stable reference: a new array every render makes the table think the data
  // changed, so its auto-reset fires through the microtask queue and calls
  // setState — sometimes before the component has finished mounting, which
  // React reports as "a side-effect in your render function".
  const runs = useMemo(() => scopeToRepo(data ?? [], repoFilter), [data, repoFilter]);
  const { data: projects } = useProjects();
  const repoName = projects?.find((r) => r.id === repoFilter)?.name;

  // Only when the table would otherwise be empty, and sharing the dashboard's
  // query key so visiting both is one fetch. This exists to answer the question
  // an empty Runs screen raises and cannot answer for itself: a container is
  // reaped, the run log is not, so "no runs here" and "nothing ever ran here"
  // are different statements.
  const { data: history } = useAudit(undefined, 5000, {
    enabled: !isPending && runs.length === 0,
  });

  return (
    <div className="space-y-5">
      <PageHeader
        title="Runs"
        description={
          <>
            Every container carrying the <code className="font-mono text-xs">sandbox.cli</code>{" "}
            label{repoName ? ` for ${repoName}` : ""}, running or finished. A run stays here after
            it exits — how it ended is the point.
          </>
        }
        actions={
          <>
            <Button
              variant="outline"
              size="sm"
              onClick={() => refetch()}
              disabled={isFetching}
              aria-label="Refresh"
            >
              <RotateCw className={cn("size-4", isFetching && "animate-spin")} />
              Refresh
            </Button>
            <Button asChild size="sm">
              <Link href="/launch">
                <Play className="size-4" />
                New run
              </Link>
            </Button>
          </>
        }
      />
      <RunsTable
        runs={runs}
        loading={isPending}
        history={{ count: history?.length ?? 0, label: repoName }}
      />
    </div>
  );
}
