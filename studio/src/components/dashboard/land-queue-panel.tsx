"use client";

import Link from "next/link";
import { GitMerge, Inbox, ShieldAlert, ShieldCheck, ShieldQuestion } from "lucide-react";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { EmptyState } from "@/components/common/empty-state";
import { landQueue } from "@/lib/derive";
import { pluralize } from "@/lib/format";
import type { Worktree } from "@/lib/types";

/**
 * Branches with work and no agent on them — what `land` would consider.
 *
 * The verify state is shown as three values, not two: passed, rejected, and
 * *never checked*. `land` refuses a branch that was never verified, so a UI that
 * folded "no check" into "not passed" would explain the wrong refusal.
 */
export function LandQueuePanel({
  worktrees,
  loading,
}: {
  worktrees: Worktree[];
  loading?: boolean;
}) {
  const queue = landQueue(worktrees);

  return (
    <Card className="surface-sheen gap-3">
      <CardHeader className="gap-1">
        <div className="flex items-center justify-between gap-2">
          <CardTitle className="flex items-center gap-2 text-sm font-medium">
            <GitMerge className="size-4 text-muted-foreground" />
            Waiting to land
            <span className="text-muted-foreground tabular-nums">{queue.length}</span>
          </CardTitle>
          <Button asChild variant="ghost" size="sm" className="h-7 text-xs">
            <Link href="/worktrees">Worktrees</Link>
          </Button>
        </div>
      </CardHeader>
      <CardContent className="space-y-1.5">
        {loading ? (
          <>
            <Skeleton className="h-10 w-full" />
            <Skeleton className="h-10 w-full" />
          </>
        ) : queue.length === 0 ? (
          <EmptyState
            icon={Inbox}
            title="Nothing waiting"
            description="Every branch is either empty, already landed, or still has an agent working it."
            className="border-0 py-6"
          />
        ) : (
          queue.slice(0, 6).map((w) => (
            <div
              key={w.branch}
              className="flex items-center justify-between gap-3 rounded-md border bg-card/40 px-2.5 py-2"
            >
              <div className="min-w-0">
                <p className="truncate font-mono text-xs font-medium">{w.branch}</p>
                <p className="text-[11px] text-muted-foreground">
                  <span className="tabular-nums">{pluralize(w.ahead, "commit")}</span>
                  {w.dirty.length > 0 && (
                    <span className="text-caution">
                      {" "}
                      · {pluralize(w.dirty.length, "uncommitted file")}
                    </span>
                  )}
                  {w.base && <span> → {w.base}</span>}
                </p>
              </div>
              <VerifyMark verified={w.verified} />
            </div>
          ))
        )}
      </CardContent>
    </Card>
  );
}

function VerifyMark({ verified }: { verified: boolean | null }) {
  if (verified === true) {
    return (
      <Tooltip>
        <TooltipTrigger asChild>
          <ShieldCheck className="size-4 shrink-0 text-status-good" aria-label="Verify passed" />
        </TooltipTrigger>
        <TooltipContent>Its verify ran and passed. `land` will take it.</TooltipContent>
      </Tooltip>
    );
  }
  if (verified === false) {
    return (
      <Tooltip>
        <TooltipTrigger asChild>
          <ShieldAlert
            className="size-4 shrink-0 text-status-serious"
            aria-label="Verify failed"
          />
        </TooltipTrigger>
        <TooltipContent>
          Its verify ran and said no. `land` refuses this branch, and `--all` skips it and carries
          on — the refusal is about the branch, not the base.
        </TooltipContent>
      </Tooltip>
    );
  }
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <ShieldQuestion
          className="size-4 shrink-0 text-muted-foreground"
          aria-label="Never verified"
        />
      </TooltipTrigger>
      <TooltipContent className="max-w-xs">
        No verify was ever declared for this branch, which is a different thing from one that
        failed. `land` refuses it too, and says which.
      </TooltipContent>
    </Tooltip>
  );
}
