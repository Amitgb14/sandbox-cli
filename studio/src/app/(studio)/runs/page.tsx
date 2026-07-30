"use client";

import Link from "next/link";
import { Play, RotateCw } from "lucide-react";
import { Button } from "@/components/ui/button";
import { PageHeader } from "@/components/common/page-header";
import { RunsTable } from "@/components/runs/runs-table";
import { useRuns } from "@/lib/api/queries";
import { useUi } from "@/lib/store";
import { scopeToRepo } from "@/lib/derive";
import { REPOS } from "@/lib/mock/data";
import { cn } from "@/lib/utils";

export default function RunsPage() {
  const repoFilter = useUi((s) => s.repoFilter);
  const { data, isPending, isFetching, refetch } = useRuns();
  const runs = scopeToRepo(data ?? [], repoFilter);
  const repoName = REPOS.find((r) => r.id === repoFilter)?.name;

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
      <RunsTable runs={runs} loading={isPending} />
    </div>
  );
}
