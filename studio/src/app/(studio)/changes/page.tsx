"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import { useSearchParams } from "next/navigation";
import { GitBranch, GitMerge, Play, RotateCw } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { PageHeader } from "@/components/common/page-header";
import { EmptyState } from "@/components/common/empty-state";
import { DiffFiles } from "@/components/run-detail/diff-view";
import { BranchPicker, branchValue } from "@/components/editor/branch-picker";
import { useProjects, useWorktreeDiff, useWorktrees } from "@/lib/api/queries";
import { useUi } from "@/lib/store";
import { cn } from "@/lib/utils";

/**
 * What a branch changed — read from the branch, not from a container.
 *
 * The Runs screen answers this per *run*, and that answer disappears when the
 * container is reaped. A worktree outlives every container that worked in it,
 * and reviewing an agent's work is something you usually do afterwards, so this
 * asks the same question of the branch: what it has beyond its base, plus
 * whatever is still uncommitted in its checkout.
 *
 * Both halves, deliberately. A branch with three commits and an unsaved fourth
 * file is the ordinary state of an agent's worktree, and showing only the
 * commits is how a review misses the part that was still in flight.
 */
export default function ChangesPage() {
  const repoFilter = useUi((s) => s.repoFilter);
  const { data: projects } = useProjects();
  const { data: worktrees } = useWorktrees();
  const search = useSearchParams();
  const [branch, setBranch] = useState("");

  // Seeded from ?branch=, so the worktrees table and the palette can link
  // straight here. Applied once the list has loaded and only when the branch is
  // one this repository actually has — a link carried over from another
  // repository should not leave the picker pointing at nothing.
  const linked = search.get("branch");
  useEffect(() => {
    if (!linked || !worktrees) return;
    if (worktrees.some((w) => !w.primary && w.branch === linked)) setBranch(linked);
  }, [linked, worktrees]);

  const { data, isPending, isFetching, refetch } = useWorktreeDiff(branch || null);
  const repo = projects?.find((p) => (repoFilter ? p.id === repoFilter : p.default));
  const worktree = worktrees?.find((w) => w.branch === branch);

  return (
    <div className="space-y-5">
      <PageHeader
        title="Changes"
        description={
          <>
            What a branch of{" "}
            <span className="font-medium">{repo?.name ?? "the scoped repository"}</span> has that
            its base does not, plus whatever is still uncommitted in its worktree. Read from the
            branch rather than from a run, so it survives the container being reaped.
          </>
        }
        actions={
          <>
            <BranchPicker
              value={branchValue(branch)}
              onChange={setBranch}
              worktreesOnly
              className="w-[18rem]"
            />
            <Button
              variant="outline"
              size="sm"
              onClick={() => refetch()}
              disabled={!branch || isFetching}
            >
              <RotateCw className={cn("size-4", isFetching && "animate-spin")} />
              Refresh
            </Button>
          </>
        }
      />

      {branch && worktree && (
        <div className="flex flex-wrap items-center gap-2 text-sm">
          <Badge variant="outline" className="font-mono text-xs">
            <GitBranch className="size-3" />
            {branch}
          </Badge>
          {worktree.base && (
            <span className="text-muted-foreground">
              against <code className="font-mono text-xs">{worktree.base}</code>
            </span>
          )}
          {/* The two facts `land` refuses on, shown where the work is reviewed
              rather than only on the Worktrees table — this is the screen where
              somebody decides whether it is ready. */}
          {worktree.dirty.length > 0 && (
            <Badge variant="outline" className="text-[10px]">
              {worktree.dirty.length} uncommitted
            </Badge>
          )}
          {worktree.verified === null && (
            <Badge variant="outline" className="text-[10px]">
              never verified
            </Badge>
          )}
          <div className="ml-auto flex gap-2">
            <Button asChild variant="ghost" size="sm" className="h-7 text-xs">
              <Link href={`/files?branch=${encodeURIComponent(branch)}`}>
                Browse these files
              </Link>
            </Button>
            <Button asChild variant="ghost" size="sm" className="h-7 text-xs">
              <Link href={`/worktrees/${encodeURIComponent(branch)}`}>
                <GitMerge className="size-3.5" />
                Worktree
              </Link>
            </Button>
          </div>
        </div>
      )}

      {!branch ? (
        <EmptyState
          icon={GitBranch}
          title="Pick a branch"
          description="Every worktree is its own checkout on disk. Choose one to see what it has that its base does not — including work an agent wrote and never committed."
          action={
            <Button asChild variant="outline" size="sm">
              <Link href="/launch">
                <Play className="size-4" />
                Start a run on a branch
              </Link>
            </Button>
          }
        />
      ) : (
        <DiffFiles
          files={data}
          loading={isPending}
          emptyTitle="Level with its base"
          emptyDescription="This branch has nothing its base does not, and nothing uncommitted in its worktree — so there is nothing here to review yet."
        />
      )}
    </div>
  );
}
