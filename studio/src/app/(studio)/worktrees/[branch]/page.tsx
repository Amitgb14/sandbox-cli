"use client";

import { use } from "react";
import Link from "next/link";
import {
  ArrowLeft,
  GitBranch,
  GitCommitHorizontal,
  Play,
  Timer,
} from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { EmptyState } from "@/components/common/empty-state";
import { CopyButton } from "@/components/common/copy-button";
import { StatusBadge } from "@/components/common/status-badge";
import {
  useBranchRuns,
  useWorktree,
  useWorktreeCommits,
} from "@/lib/api/queries";
import { DASH, formatDuration, formatRelative, pluralize } from "@/lib/format";
import { runOutcome } from "@/lib/types";
import { cn } from "@/lib/utils";

/**
 * One worktree, and every agent that has worked in it.
 *
 * The worktrees list answers "what branches exist and which need attention";
 * this answers the question that follows — *what happened here*. It is assembled
 * from three places rather than one, because that is where the facts live:
 * git for the branch and its commits, docker for the runs, and the run's own
 * container for how each one ended.
 */
export default function WorktreeDetailPage({
  params,
}: {
  params: Promise<{ branch: string }>;
}) {
  const { branch: raw } = use(params);
  const branch = decodeURIComponent(raw);

  const { data: wt, isPending, isError } = useWorktree(branch);
  const { data: commits } = useWorktreeCommits(branch);
  const { data: runs } = useBranchRuns(branch);

  if (isPending) {
    return <Skeleton className="h-64 w-full rounded-lg" />;
  }
  if (isError || !wt) {
    return (
      <EmptyState
        icon={GitBranch}
        title="No worktree for this branch"
        description="It may have been removed. `fleet clean --worktrees` removes the clean ones; anything with uncommitted work is kept."
      />
    );
  }

  // Newest first, which is the order every listing in this tool uses.
  const history = [...(runs ?? [])].sort((a, b) =>
    b.createdAt.localeCompare(a.createdAt),
  );
  const live = history.find((r) => r.state === "running");

  return (
    <div className="space-y-5">
      <div className="flex flex-wrap items-center gap-2">
        <Button variant="ghost" size="sm" asChild className="-ml-2">
          <Link href="/worktrees">
            <ArrowLeft className="size-4" />
            Worktrees
          </Link>
        </Button>
        <h1 className="font-mono text-lg font-medium">{branch}</h1>
        {/* Verified is a tri-state and null is not false: nothing checked this
            is what `land` refuses on, and it is a different answer from failed. */}
        {wt.verified === true && <Badge variant="outline">verify passed</Badge>}
        {wt.verified === false && (
          <Badge variant="destructive">verify failed</Badge>
        )}
        {wt.verified === null && <Badge variant="outline">unverified</Badge>}
        <div className="ml-auto flex items-center gap-2">
          <Button size="sm" asChild>
            <Link href={`/launch?branch=${encodeURIComponent(branch)}`}>
              <Play className="size-4" />
              Start an agent here
            </Link>
          </Button>
        </div>
      </div>

      <div className="grid gap-4 md:grid-cols-4">
        <Fact label="Head" value={wt.head || DASH} mono />
        <Fact label="Base" value={wt.base ?? DASH} mono />
        <Fact
          label="Ahead / behind"
          value={`${wt.ahead} / ${wt.behind}`}
          hint={wt.behind > 0 ? "landing this will be a merge" : undefined}
        />
        <Fact
          label="Uncommitted"
          value={
            wt.dirty.length === 0 ? "clean" : pluralize(wt.dirty.length, "file")
          }
          hint={wt.dirty.length > 0 ? "exists nowhere else" : undefined}
        />
      </div>

      <Card>
        <CardHeader className="pb-3">
          <CardTitle className="text-sm">
            Agents that worked here
            {live && <Badge className="ml-2">one running</Badge>}
          </CardTitle>
        </CardHeader>
        <CardContent>
          {history.length === 0 ? (
            <p className="text-sm text-muted-foreground">
              No run has worked this branch yet — or their containers have been
              reaped, which is the same thing to anything asking now. Docker is
              the state store.
            </p>
          ) : (
            <ul className="divide-y">
              {history.map((r) => (
                <li
                  key={r.id}
                  className="flex flex-wrap items-center gap-3 py-2 text-sm"
                >
                  <StatusBadge outcome={runOutcome(r)} exitCode={r.exitCode} />
                  <Link
                    href={`/runs/${r.id}`}
                    className="font-mono text-xs hover:underline"
                  >
                    {r.id}
                  </Link>
                  {r.agent && <Badge variant="outline">{r.agent}</Badge>}
                  {r.prompt && (
                    <span
                      className="min-w-0 flex-1 truncate text-muted-foreground"
                      title={r.prompt}
                    >
                      {r.prompt}
                    </span>
                  )}
                  <span className="ml-auto flex items-center gap-1 text-xs text-muted-foreground">
                    <Timer className="size-3.5" />
                    {r.durationMs != null
                      ? formatDuration(r.durationMs)
                      : "running"}
                  </span>
                  <span className="text-xs text-muted-foreground">
                    {formatRelative(r.createdAt)}
                  </span>
                </li>
              ))}
            </ul>
          )}
        </CardContent>
      </Card>

      <Card>
        <CardHeader className="pb-3">
          <CardTitle className="text-sm">
            Commits{" "}
            {wt.base ? (
              <span className="text-muted-foreground">not in {wt.base}</span>
            ) : null}
          </CardTitle>
        </CardHeader>
        <CardContent>
          {!commits || commits.length === 0 ? (
            <p className="text-sm text-muted-foreground">
              Nothing committed beyond the base yet. Uncommitted work still
              counts — `land` commits it before merging.
            </p>
          ) : (
            <ul className="divide-y">
              {commits.map((c) => (
                <li
                  key={c.sha}
                  className="flex flex-wrap items-center gap-3 py-2 text-sm"
                >
                  <GitCommitHorizontal className="size-4 shrink-0 text-muted-foreground" />
                  <span className="font-mono text-xs">{c.shortSha}</span>
                  {/* Text from the repository: rendered, never interpreted. */}
                  <span className="min-w-0 flex-1 truncate">{c.subject}</span>
                  <span className="shrink-0 font-mono text-xs">
                    <span className="text-status-passed">+{c.insertions}</span>{" "}
                    <span className="text-destructive">-{c.deletions}</span>
                  </span>
                  <span className="shrink-0 text-xs text-muted-foreground">
                    {c.author}
                  </span>
                  <span className="shrink-0 text-xs text-muted-foreground">
                    {formatRelative(c.date)}
                  </span>
                  <CopyButton value={c.sha} label="commit" />
                </li>
              ))}
            </ul>
          )}
        </CardContent>
      </Card>

      {wt.dirty.length > 0 && (
        <Card>
          <CardHeader className="pb-3">
            <CardTitle className="text-sm">Uncommitted files</CardTitle>
          </CardHeader>
          <CardContent>
            <ul className="space-y-1 font-mono text-xs">
              {wt.dirty.map((f) => (
                <li key={f} className="truncate text-muted-foreground">
                  {f}
                </li>
              ))}
            </ul>
          </CardContent>
        </Card>
      )}
    </div>
  );
}

function Fact({
  label,
  value,
  hint,
  mono,
}: {
  label: string;
  value: string;
  hint?: string;
  mono?: boolean;
}) {
  return (
    <Card>
      <CardContent className="space-y-1 pt-5">
        <p className="text-xs text-muted-foreground">{label}</p>
        <p className={cn("truncate text-sm", mono && "font-mono")}>{value}</p>
        {hint && <p className="text-xs text-muted-foreground">{hint}</p>}
      </CardContent>
    </Card>
  );
}
