"use client";

import { useState } from "react";
import Link from "next/link";
import {
  ArrowLeft,
  Copy,
  GitBranch,
  Skull,
  Square,
  MessagesSquare,
  Shuffle,
  Timer,
} from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { StatusBadge } from "@/components/common/status-badge";
import { KindBadge, NetworkBadge, ProfileBadge } from "@/components/common/posture-badges";
import { useKillRun, useStopRun } from "@/lib/api/queries";
import { runOutcome, VERIFY_FAILED_EXIT, type Run } from "@/lib/types";
import { formatDateTime, formatDuration, formatRelative, shortId, tildify } from "@/lib/format";

export function RunHeader({ run }: { run: Run }) {
  const [killOpen, setKillOpen] = useState(false);
  const stop = useStopRun();
  const kill = useKillRun();
  const live = run.state === "running";
  const outcome = runOutcome(run);

  return (
    <div className="space-y-4">
      <div className="flex items-center gap-2">
        <Button asChild variant="ghost" size="sm" className="h-7 -ml-2 text-xs text-muted-foreground">
          <Link href="/runs">
            <ArrowLeft className="size-3.5" />
            Runs
          </Link>
        </Button>
      </div>

      <div className="flex flex-wrap items-start justify-between gap-4">
        <div className="min-w-0 space-y-2">
          <div className="flex min-w-0 flex-wrap items-center gap-2">
            <GitBranch className="size-4 shrink-0 text-muted-foreground" />
            <h1 className="truncate font-mono text-xl font-semibold tracking-tight">
              {run.branch ?? run.name}
            </h1>
            <StatusBadge outcome={outcome} exitCode={run.exitCode} />
            <KindBadge kind={run.kind} />
            <ProfileBadge profile={run.profile} />
            <NetworkBadge network={run.network} />
          </div>

          <div className="flex flex-wrap items-center gap-x-4 gap-y-1 text-xs text-muted-foreground">
            <span>
              {run.repoName}
              {run.base && <span> → {run.base}</span>}
            </span>
            <span className="font-mono">{run.agent ?? "plain run"}</span>
            {/* A routed run used a different agent than was asked for, which
                moves the login, the bill and the transcript. Shown beside the
                agent rather than tucked into a details tab: this is the first
                thing somebody wonders when the name is not the one they picked. */}
            {run.routedFrom && (
              <Tooltip>
                <TooltipTrigger asChild>
                  <Badge variant="outline" className="gap-1 text-[10px]">
                    <Shuffle className="size-3" />
                    routed from {run.routedFrom}
                    {(run.routeAttempt ?? 0) > 1 && ` · attempt ${run.routeAttempt}`}
                  </Badge>
                </TooltipTrigger>
                <TooltipContent className="max-w-xs">
                  {/* Two different events wear the same badge, and saying the
                      wrong one is worse than saying nothing: a preflight skip
                      means the named agent never ran, while a later attempt
                      means it ran, failed without writing anything, and its
                      briefing was carried across. */}
                  {(run.routeAttempt ?? 0) > 1
                    ? `${run.routedFrom} ${run.routeReason || "failed"}, so this agent took over — with a briefing of that conversation mounted read-only, not the conversation itself. It runs under its own login and writes its own transcript.`
                    : run.routeReason
                      ? `${run.routedFrom} was skipped before it started — ${run.routeReason}. This agent ran with its own login and its own transcript; there was no conversation to inherit.`
                      : `${run.routedFrom} was asked for and this agent ran instead.`}
                </TooltipContent>
              </Tooltip>
            )}
            {/* A handoff is not a failover, and the two must not wear one badge:
                routing says a provider stopped answering, this says a person
                read a conversation and chose who should carry it on. A run can
                carry both — handed over, then routed when the target was down —
                so they render side by side rather than as one line. */}
            {run.handoffFrom && (
              <Tooltip>
                <TooltipTrigger asChild>
                  <Badge variant="outline" className="gap-1 text-[10px]">
                    <MessagesSquare className="size-3" />
                    briefed from {run.handoffFrom}
                  </Badge>
                </TooltipTrigger>
                <TooltipContent className="max-w-xs">
                  Started from {run.handoffFrom}&apos;s conversation
                  {run.handoffSession ? ` ${run.handoffSession.slice(0, 8)}` : ""} — a briefing
                  mounted read-only at /sandbox/context, not the conversation itself. This agent
                  began a new one, under its own login and its own transcript.
                </TooltipContent>
              </Tooltip>
            )}
            <span className="flex items-center gap-1 tabular-nums">
              <Timer className="size-3.5" />
              {formatDuration(run.durationMs)}
              {live && " so far"}
            </span>
            <Tooltip>
              <TooltipTrigger asChild>
                <span className="cursor-help">
                  {live
                    ? `started ${formatRelative(run.startedAt)}`
                    : `finished ${formatRelative(run.finishedAt)}`}
                </span>
              </TooltipTrigger>
              <TooltipContent>
                Created {formatDateTime(run.createdAt)}
                {run.startedAt && <> · started {formatDateTime(run.startedAt)}</>}
                {run.finishedAt && <> · finished {formatDateTime(run.finishedAt)}</>}
              </TooltipContent>
            </Tooltip>
            <Tooltip>
              <TooltipTrigger asChild>
                <button
                  className="flex items-center gap-1 font-mono hover:text-foreground"
                  onClick={() => {
                    navigator.clipboard.writeText(run.id);
                    toast.success("Container id copied");
                  }}
                >
                  {shortId(run.id, 12)}
                  <Copy className="size-3" />
                </button>
              </TooltipTrigger>
              <TooltipContent>
                {/* Ids are abbreviated for display, never for addressing. */}
                Full container id: <span className="font-mono">{run.id}</span>
              </TooltipContent>
            </Tooltip>
          </div>
        </div>

        {live && (
          <div className="flex shrink-0 items-center gap-2">
            <Button
              variant="outline"
              size="sm"
              disabled={stop.isPending}
              onClick={() =>
                stop.mutate(run.id, {
                  onSuccess: () =>
                    toast.success("Asked it to exit", {
                      description: "The guest gets a grace period to finish writing.",
                    }),
                })
              }
            >
              <Square className="size-4" />
              Stop
            </Button>
            <Button variant="destructive" size="sm" onClick={() => setKillOpen(true)}>
              <Skull className="size-4" />
              Kill
            </Button>
          </div>
        )}
      </div>

      {run.prompt && (
        <div className="rounded-lg border bg-card/40 p-3">
          <p className="text-[11px] font-medium tracking-wide text-muted-foreground uppercase">
            Prompt
          </p>
          <p className="mt-1 text-sm">{run.prompt}</p>
        </div>
      )}

      {/* `verify` is what makes a run autonomous rather than merely headless, and
          its *presence* is what tells "no check" from "passed its check". */}
      {run.verify && (
        <div className="flex flex-wrap items-center gap-2 rounded-lg border bg-card/40 px-3 py-2 text-xs">
          <Badge variant="outline" className="shrink-0 text-[10px]">
            verify
          </Badge>
          <code className="min-w-0 flex-1 truncate font-mono">{run.verify}</code>
          {outcome === "verify-failed" ? (
            <span className="text-status-serious">
              ran and said no · exit {VERIFY_FAILED_EXIT}
            </span>
          ) : outcome === "passed" ? (
            <span className="text-status-good">passed</span>
          ) : live ? (
            <span className="text-muted-foreground">not decided yet</span>
          ) : (
            <span className="text-muted-foreground">did not get that far</span>
          )}
        </div>
      )}

      <p className="truncate font-mono text-xs text-muted-foreground" title={run.workspace}>
        {tildify(run.workspace)} → {run.workdir}
      </p>

      <Dialog open={killOpen} onOpenChange={setKillOpen}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>Kill this sandbox?</DialogTitle>
            <DialogDescription asChild>
              <div className="space-y-2 text-sm">
                <p>
                  <span className="font-mono">{run.branch ?? run.name}</span> is terminated
                  immediately, with no chance to finish what it was writing.
                </p>
                <p>
                  The workspace is a bind mount, so whatever is already saved stays on disk.
                  Anything mid-write does not.
                </p>
              </div>
            </DialogDescription>
          </DialogHeader>
          <DialogFooter className="gap-2 sm:justify-between">
            <Button
              variant="outline"
              onClick={() => {
                setKillOpen(false);
                stop.mutate(run.id, { onSuccess: () => toast.success("Asked it to exit instead") });
              }}
            >
              <Square className="size-3.5" />
              Stop gracefully
            </Button>
            <Button
              variant="destructive"
              disabled={kill.isPending}
              onClick={() =>
                kill.mutate(run.id, {
                  onSuccess: () => {
                    setKillOpen(false);
                    toast.warning("Killed");
                  },
                })
              }
            >
              <Skull className="size-3.5" />
              Kill now
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
