"use client";

import { Suspense, useEffect, useMemo, useRef, useState } from "react";
import Link from "next/link";
import { useSearchParams } from "next/navigation";
import {
  ArrowDown,
  ArrowUp,
  GitBranch,
  GitMerge,
  Play,
  Search,
  ShieldAlert,
  ShieldCheck,
  ShieldQuestion,
  Trash2,
} from "lucide-react";
import { toast } from "sonner";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip";
import { MoreHorizontal } from "lucide-react";
import { StatusBadge } from "@/components/common/status-badge";
import { auditOutcome } from "@/lib/derive";
import { PageHeader } from "@/components/common/page-header";
import { EmptyState } from "@/components/common/empty-state";
import { LiveDot } from "@/components/common/status-badge";
import {
  useLandWorktree,
  useRemoveWorktree,
  useAudit,
  useWorktrees,
} from "@/lib/api/queries";
import { useUi } from "@/lib/store";
import { scopeToRepo } from "@/lib/derive";
import { DASH, formatRelative, pluralize, tildify } from "@/lib/format";
import { cn } from "@/lib/utils";
import type { AuditRecord, Worktree } from "@/lib/types";

/**
 * One branch per agent.
 *
 * The two facts every row has to carry are the two `land` refuses on: whether an
 * agent is still working the branch, and whether its verify was ever decided. A
 * worktree is addressed by **branch** throughout — never by the directory name
 * derived from one, because an agent that runs `git checkout -b` inside its
 * worktree puts the two out of sync.
 */
export default function WorktreesPage() {
  // useSearchParams needs a boundary: a branch can be pointed at by link
  // (?branch=feat/x) from the command palette.
  return (
    <Suspense fallback={<Skeleton className="h-[28rem] w-full" />}>
      <WorktreesContent />
    </Suspense>
  );
}

function WorktreesContent() {
  const repoFilter = useUi((s) => s.repoFilter);
  const { data, isPending } = useWorktrees();

  /**
   * One audit fetch for the whole table, grouped by branch here.
   *
   * Not one request per row: a listing of twenty branches would be twenty
   * requests to answer a column, and the log is a single file the daemon reads
   * either way. The limit bounds it — a branch whose last run is older than
   * this many records shows a dash, which is the same thing the column says for
   * a branch that has never run and is honest about both.
   */
  const { data: audit } = useAudit(undefined, 2000);
  const lastRuns = useMemo(() => {
    const newest = new Map<string, AuditRecord>();
    for (const r of audit ?? []) {
      if (!r.branch) continue;
      const seen = newest.get(r.branch);
      if (!seen || r.time > seen.time) newest.set(r.branch, r);
    }
    return newest;
  }, [audit]);
  const search = useSearchParams();
  const [query, setQuery] = useState(() => search.get("branch") ?? "");
  const [landing, setLanding] = useState<Worktree | null>(null);
  const [removing, setRemoving] = useState<Worktree | null>(null);

  // Seeded from the link on mount above; this catches the case where the
  // palette points at another branch while the page is already open. It follows
  // the param rather than the box, so typing afterwards is never overwritten.
  const linked = search.get("branch");
  const lastLinked = useRef(linked);
  useEffect(() => {
    if (linked === lastLinked.current) return;
    lastLinked.current = linked;
    setQuery(linked ?? "");
  }, [linked]);

  const land = useLandWorktree();
  const remove = useRemoveWorktree();

  const worktrees = scopeToRepo(data ?? [], repoFilter)
    .filter((w) => !w.primary)
    .filter(
      (w) => !query || w.branch.toLowerCase().includes(query.toLowerCase()),
    );

  const busy = worktrees.filter((w) => w.runId).length;
  const dirty = worktrees.filter((w) => w.dirty.length > 0).length;

  return (
    <div className="space-y-5">
      <PageHeader
        title="Worktrees"
        description="Each agent works its own branch in its own directory, mounted at its own host path so git cannot prune it away mid-session."
        actions={
          <Button asChild size="sm">
            <Link href="/launch">
              <Play className="size-4" />
              New run
            </Link>
          </Button>
        }
      />

      <div className="flex flex-wrap items-center gap-3">
        <div className="relative">
          <Search className="absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2 text-muted-foreground" />
          <Input
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="Search branches…"
            className="h-8 w-full pl-8 sm:w-72"
          />
        </div>
        <div className="flex items-center gap-2 text-xs text-muted-foreground">
          <Badge variant="outline" className="tabular-nums">
            {worktrees.length} worktrees
          </Badge>
          {busy > 0 && (
            <Badge
              variant="outline"
              className="border-status-running/40 text-status-running tabular-nums"
            >
              {busy} with an agent
            </Badge>
          )}
          {dirty > 0 && (
            <Badge
              variant="outline"
              className="border-caution/40 text-caution tabular-nums"
            >
              {dirty} dirty
            </Badge>
          )}
        </div>
      </div>

      <div className="overflow-hidden rounded-lg border">
        <Table>
          <TableHeader className="bg-muted/40">
            <TableRow className="hover:bg-transparent">
              <TableHead className="h-9">Branch</TableHead>
              <TableHead className="h-9">Base</TableHead>
              <TableHead className="h-9">Commits</TableHead>
              <TableHead className="h-9">Working tree</TableHead>
              <TableHead className="h-9">Verify</TableHead>
              <TableHead className="h-9">Agent</TableHead>
              <TableHead className="h-9">Last run</TableHead>
              <TableHead className="h-9">Created</TableHead>
              <TableHead className="h-9 w-10" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {isPending ? (
              Array.from({ length: 6 }).map((_, i) => (
                <TableRow key={i}>
                  {Array.from({ length: 9 }).map((__, j) => (
                    <TableCell key={j}>
                      <Skeleton className="h-5 w-full" />
                    </TableCell>
                  ))}
                </TableRow>
              ))
            ) : worktrees.length === 0 ? (
              <TableRow className="hover:bg-transparent">
                <TableCell colSpan={9} className="p-0">
                  <EmptyState
                    icon={GitBranch}
                    title={query ? "No branch matches" : "No worktrees"}
                    description={
                      query
                        ? "Try a shorter search."
                        : "A run started with --worktree creates one. Until then every run works the main checkout, and git allows only one agent there at a time."
                    }
                    className="border-0"
                  />
                </TableCell>
              </TableRow>
            ) : (
              worktrees.map((w) => (
                <TableRow key={`${w.repoId}-${w.branch}`}>
                  <TableCell>
                    <div className="flex items-center gap-2">
                      {w.runId && <LiveDot />}
                      <div className="min-w-0">
                        {/* The branch name is the link, not the whole row: the
                            row also carries a menu whose items are their own
                            actions, and a row-level click would swallow them. */}
                        <Link
                          href={`/worktrees/${encodeURIComponent(w.branch)}?repo=${encodeURIComponent(w.repoId)}`}
                          className="truncate font-mono text-sm hover:underline"
                        >
                          {w.branch}
                        </Link>
                        <p
                          className="truncate font-mono text-[11px] text-muted-foreground"
                          title={w.path}
                        >
                          {tildify(w.path)}
                        </p>
                      </div>
                    </div>
                  </TableCell>
                  <TableCell className="font-mono text-xs text-muted-foreground">
                    {w.base ?? "—"}
                  </TableCell>
                  <TableCell>
                    <div className="flex items-center gap-2 font-mono text-xs tabular-nums">
                      {w.ahead > 0 && (
                        <span className="flex items-center gap-0.5 text-status-good">
                          <ArrowUp className="size-3" />
                          {w.ahead}
                        </span>
                      )}
                      {w.behind > 0 && (
                        <Tooltip>
                          <TooltipTrigger asChild>
                            <span className="flex cursor-help items-center gap-0.5 text-caution">
                              <ArrowDown className="size-3" />
                              {w.behind}
                            </span>
                          </TooltipTrigger>
                          <TooltipContent>
                            Behind {w.base} by {pluralize(w.behind, "commit")}.
                          </TooltipContent>
                        </Tooltip>
                      )}
                      {w.ahead === 0 && w.behind === 0 && (
                        <span className="text-muted-foreground">even</span>
                      )}
                    </div>
                  </TableCell>
                  <TableCell>
                    {w.dirty.length === 0 ? (
                      <span className="text-xs text-muted-foreground">
                        clean
                      </span>
                    ) : (
                      <Tooltip>
                        <TooltipTrigger asChild>
                          <span className="cursor-help text-xs text-caution tabular-nums">
                            {pluralize(w.dirty.length, "file")}
                          </span>
                        </TooltipTrigger>
                        <TooltipContent className="max-w-sm">
                          <p className="font-medium">Uncommitted</p>
                          <ul className="mt-1 space-y-0.5 font-mono text-[11px]">
                            {[...new Set(w.dirty)].slice(0, 6).map((f) => (
                              <li key={f}>{f}</li>
                            ))}
                          </ul>
                        </TooltipContent>
                      </Tooltip>
                    )}
                  </TableCell>
                  <TableCell>
                    <VerifyCell verified={w.verified} live={!!w.runId} />
                  </TableCell>
                  <TableCell>
                    {w.runId ? (
                      <Link
                        href={`/runs/${w.runId}`}
                        className="font-mono text-xs text-status-running hover:underline"
                      >
                        working
                      </Link>
                    ) : (
                      <span className="text-xs text-muted-foreground">
                        idle
                      </span>
                    )}
                  </TableCell>
                  <TableCell className="whitespace-nowrap">
                    <LastRunCell record={lastRuns.get(w.branch)} />
                  </TableCell>
                  <TableCell className="text-xs whitespace-nowrap text-muted-foreground">
                    {formatRelative(w.createdAt)}
                  </TableCell>
                  <TableCell>
                    <DropdownMenu>
                      <DropdownMenuTrigger asChild>
                        <Button
                          variant="ghost"
                          size="icon"
                          className="size-7 text-muted-foreground"
                          aria-label="Worktree actions"
                        >
                          <MoreHorizontal className="size-4" />
                        </Button>
                      </DropdownMenuTrigger>
                      <DropdownMenuContent align="end" className="w-56">
                        <DropdownMenuItem asChild>
                          <Link
                            href={`/launch?branch=${encodeURIComponent(w.branch)}`}
                          >
                            <Play className="size-3.5" />
                            Start an agent here
                          </Link>
                        </DropdownMenuItem>
                        <DropdownMenuSeparator />
                        <DropdownMenuItem
                          disabled={!!w.runId || w.ahead === 0}
                          onClick={() => setLanding(w)}
                        >
                          <GitMerge className="size-3.5" />
                          Land onto {w.base ?? "its base"}…
                        </DropdownMenuItem>
                        <DropdownMenuItem
                          variant="destructive"
                          onClick={() => setRemoving(w)}
                        >
                          <Trash2 className="size-3.5" />
                          Remove worktree…
                        </DropdownMenuItem>
                      </DropdownMenuContent>
                    </DropdownMenu>
                  </TableCell>
                </TableRow>
              ))
            )}
          </TableBody>
        </Table>
      </div>

      {/* `land` is the only operation that writes to the base branch, and it
          refuses on every ambiguity. The dialog states which refusals apply
          before it is attempted rather than after. */}
      <Dialog open={!!landing} onOpenChange={(o) => !o && setLanding(null)}>
        <DialogContent className="sm:max-w-lg">
          <DialogHeader>
            <DialogTitle>
              Land <span className="font-mono">{landing?.branch}</span>?
            </DialogTitle>
            <DialogDescription asChild>
              <div className="space-y-2 text-sm">
                <p>
                  This merges into{" "}
                  <span className="font-mono">{landing?.base}</span> — the only
                  operation in Studio that writes to a base branch.
                </p>
                {landing?.verified !== true && (
                  <p className="text-caution">
                    {landing?.verified === false
                      ? "Its verify ran and said no. land refuses a branch that did not pass."
                      : "No verify was ever decided for this branch. land refuses that too, and the two refusals are different — one was judged, one was never checked."}
                  </p>
                )}
                {landing && landing.dirty.length > 0 && (
                  <p className="text-caution">
                    {pluralize(landing.dirty.length, "file")} uncommitted.
                    Landing commits the worktree as it stands, which is only
                    correct if the worktree is still on the branch being landed.
                  </p>
                )}
              </div>
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" onClick={() => setLanding(null)}>
              Cancel
            </Button>
            <Button
              disabled={land.isPending}
              onClick={() => {
                if (!landing) return;
                land.mutate(
                  { branch: landing.branch, onto: landing.base ?? undefined },
                  {
                    onSuccess: (res) => {
                      setLanding(null);
                      toast.success("Landed", { description: res.message });
                    },
                    onError: (e) =>
                      toast.error("Refused", {
                        description: e instanceof Error ? e.message : String(e),
                      }),
                  },
                );
              }}
            >
              <GitMerge className="size-3.5" />
              Land it
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={!!removing} onOpenChange={(o) => !o && setRemoving(null)}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>
              Remove <span className="font-mono">{removing?.branch}</span>?
            </DialogTitle>
            <DialogDescription asChild>
              <div className="space-y-2 text-sm">
                <p>
                  The worktree directory goes away. The branch and its commits
                  stay in the repository.
                </p>
                {removing && removing.dirty.length > 0 && (
                  <p className="text-destructive">
                    {pluralize(removing.dirty.length, "file")} uncommitted —
                    those changes are only here, and removing the worktree
                    discards them.
                  </p>
                )}
                {removing?.runId && (
                  <p className="text-destructive">
                    An agent is working this branch right now. Stop it first.
                  </p>
                )}
              </div>
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" onClick={() => setRemoving(null)}>
              Cancel
            </Button>
            <Button
              variant="destructive"
              disabled={remove.isPending || !!removing?.runId}
              onClick={() => {
                if (!removing) return;
                remove.mutate({ branch: removing.branch, repoId: removing.repoId }, {
                  onSuccess: () => {
                    setRemoving(null);
                    toast.success(`Removed ${removing.branch}`);
                  },
                });
              }}
            >
              <Trash2 className="size-3.5" />
              Remove
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}

function VerifyCell({
  verified,
  live,
}: {
  verified: boolean | null;
  live: boolean;
}) {
  if (live) {
    return <span className="text-xs text-muted-foreground">deciding</span>;
  }
  const map = {
    passed: { Icon: ShieldCheck, tone: "text-status-good", label: "passed" },
    failed: {
      Icon: ShieldAlert,
      tone: "text-status-serious",
      label: "said no",
    },
    none: {
      Icon: ShieldQuestion,
      tone: "text-muted-foreground",
      label: "never checked",
    },
  } as const;
  const key =
    verified === true ? "passed" : verified === false ? "failed" : "none";
  const { Icon, tone, label } = map[key];
  return (
    <span className={cn("flex items-center gap-1.5 text-xs", tone)}>
      <Icon className="size-3.5" />
      {label}
    </span>
  );
}

/**
 * How the last finished run on this branch ended, and when.
 *
 * From the audit log rather than from docker, and that is the point: a
 * container is reaped and the branch then looks like nothing ever ran on it,
 * while the log is what survives `fleet clean`. The Agent column already says
 * whether something is working *now*; this says what happened last, which is
 * the question the listing could not answer without opening every branch in
 * turn.
 */
function LastRunCell({ record }: { record?: AuditRecord }) {
  if (!record) {
    return <span className="text-xs text-muted-foreground">{DASH}</span>;
  }
  return (
    <div className="flex items-center gap-2">
      <StatusBadge outcome={auditOutcome(record)} exitCode={record.exitCode} />
      <span className="text-xs whitespace-nowrap text-muted-foreground">
        {formatRelative(record.time)}
      </span>
    </div>
  );
}
