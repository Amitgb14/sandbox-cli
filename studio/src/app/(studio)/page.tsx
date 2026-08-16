"use client";

import Link from "next/link";
import {
  Activity,
  Cpu,
  GitMerge,
  Play,
  ShieldCheck,
  Timer,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { PageHeader } from "@/components/common/page-header";
import { MetricTile } from "@/components/common/metric-tile";
import { ChartCard } from "@/components/charts/chart-card";
import { RunVolumeChart } from "@/components/charts/run-volume-chart";
import { OutcomeChart } from "@/components/charts/outcome-chart";
import { AgentActivityChart } from "@/components/charts/agent-activity-chart";
import { LiveRunsPanel } from "@/components/dashboard/live-runs-panel";
import { BoundaryPanel } from "@/components/dashboard/boundary-panel";
import { LandQueuePanel } from "@/components/dashboard/land-queue-panel";
import {
  useAudit,
  useHistoryStats,
  useProjects,
  useRuns,
  useWorktrees,
} from "@/lib/api/queries";
import { useUi } from "@/lib/store";
import {
  bucketAuditByDay,
  byAgentAudit,
  historyStats,
  runStats,
  scopeToRepo,
  toDayBuckets,
} from "@/lib/derive";
import {
  formatBytesShort,
  formatDuration,
  formatPercent,
  pluralize,
} from "@/lib/format";

export default function DashboardPage() {
  const repoFilter = useUi((s) => s.repoFilter);
  const { data: allRuns, isPending } = useRuns();
  const { data: allWorktrees, isPending: worktreesPending } = useWorktrees();
  const { data: projects } = useProjects();
  // History comes from the audit log, not from containers. A container carries
  // its own record until `fleet clean` reaps it, so a fourteen-day chart built
  // from containers is a chart of whatever has not been tidied up yet — on this
  // machine, seven runs out of six hundred and ninety-three.
  //
  // Aggregated by the daemon when it has an index, and in the browser when it
  // does not. The records are only fetched for the fallback, which is why the
  // query is disabled once the daemon has answered: five thousand records to
  // draw one chart is exactly what the index exists to stop.
  // The daemon's aggregate covers every repository and cannot be narrowed: the
  // log it indexes has no repo column, because it predates repositories being
  // plural here. So it is used only while nothing is scoped. With a repository
  // picked, the records are fetched and bucketed in the browser instead —
  // slower, and the only version that answers the question actually being asked.
  // Showing the machine-wide pass rate under a repository's name would be a
  // number about other people's work.
  const scoped = repoFilter !== null;
  const { data: aggregate, isPending: aggregatePending } = useHistoryStats(14, {
    enabled: !scoped,
  });
  const { data: history } = useAudit(undefined, 5000, {
    enabled: scoped || (!aggregatePending && !aggregate),
  });

  const runs = scopeToRepo(allRuns ?? [], repoFilter);
  const worktrees = scopeToRepo(allWorktrees ?? [], repoFilter);

  // Live facts from containers, decided ones from the log. Splitting them is
  // the point: only a container can say what is running now, and only the log
  // can say what has ended.
  const liveStats = runStats(runs);
  // The daemon's answer when there is one, the browser's when there is not.
  // Both compute the same thing from the same log; only the place differs.
  const past = (!scoped && aggregate?.stats) || historyStats(history ?? []);
  const stats = {
    ...liveStats,
    finishedToday: past.finishedToday,
    passRate: past.passRate,
    decided: past.decided,
    medianDurationMs: past.medianDurationMs,
  };
  const days =
    !scoped && aggregate ? toDayBuckets(aggregate.days) : bucketAuditByDay(history ?? [], 14);
  // Per-agent activity has no aggregate endpoint yet, so it stays client-side
  // and is simply absent when the records were not fetched — an empty panel
  // rather than a wrong one.
  const agents = byAgentAudit(history ?? []).slice(0, 7);
  const live = runs.filter((r) => r.state === "running");
  const repoName = projects?.find((r) => r.id === repoFilter)?.name;

  return (
    <div className="space-y-6">
      <PageHeader
        title="Dashboard"
        description={
          repoName
            ? `Every sandbox for ${repoName}, the boundary each ran inside, and what the host is carrying.`
            : "Every sandbox across your repositories, the boundary each ran inside, and what the host is carrying."
        }
        actions={
          <>
            <Button asChild variant="outline" size="sm">
              <Link href="/runs">
                <Activity className="size-4" />
                All runs
              </Link>
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

      <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
        <MetricTile
          label="In flight"
          icon={Activity}
          value={stats.live}
          loading={isPending}
          absentReason="Nothing is running"
          footer={
            stats.live === 0
              ? // "None running" rather than "no container carries the label":
                // the list below is full of containers that do, and every one of
                // them has finished. Saying otherwise would contradict the screen
                // it sits on.
                "Nothing is running. Finished runs are kept — that is the record."
              : `${stats.liveFleet} fleet · ${stats.live - stats.liveFleet} interactive`
          }
          spark={days.map((d) => d.total)}
        />
        <MetricTile
          label="Pass rate"
          icon={ShieldCheck}
          value={
            stats.passRate === null ? null : formatPercent(stats.passRate, 0)
          }
          loading={isPending}
          // A pass rate of 0% and "nothing has finished" are different facts.
          absentReason="Nothing has finished yet"
          hint="Share of decided runs that exited 0. Runs that were stopped by hand are excluded — a killed agent is not a failed one."
          footer={`over ${pluralize(stats.decided, "decided run")}`}
          spark={days.map((d) => d.passed)}
          sparkTone="var(--status-good)"
        />
        <MetricTile
          label="Median duration"
          icon={Timer}
          value={
            stats.medianDurationMs === null
              ? null
              : formatDuration(stats.medianDurationMs)
          }
          loading={isPending}
          absentReason="No finished runs to measure"
          footer={`${stats.finishedToday} finished today`}
        />
        <MetricTile
          label="Memory in flight"
          icon={Cpu}
          value={
            stats.live === 0 ? null : formatBytesShort(stats.memInFlightBytes)
          }
          loading={isPending}
          absentReason="Nothing to measure"
          hint="Summed across live containers. A run that was never sampled contributes nothing rather than a zero — the two look the same here, which is why the per-run meters say which."
          footer={
            stats.memLimitBytes > 0
              ? `of ${formatBytesShort(stats.memLimitBytes)} committed · ${formatPercent(
                  stats.cpuInFlightPct,
                  0,
                )} CPU`
              : "no memory caps set"
          }
          sparkTone="var(--chart-2)"
        />
      </div>

      <div className="grid gap-4 lg:grid-cols-2">
        <ChartCard
          title="Runs started per day"
          description="Last 14 days, including the quiet ones."
          aside={
            <span className="text-xs text-muted-foreground tabular-nums">
              {days.reduce((n, d) => n + d.total, 0)} total
            </span>
          }
        >
          <RunVolumeChart data={days} />
        </ChartCard>

        <ChartCard
          title="How runs ended"
          description="A verify that said no is its own outcome — that run did its work and was judged."
        >
          <OutcomeChart data={days} />
        </ChartCard>
      </div>

      <div className="grid gap-4 lg:grid-cols-3">
        <div className="space-y-4 lg:col-span-2">
          <LiveRunsPanel runs={live} loading={isPending} />
          <ChartCard
            title="Runs by agent"
            description="Which adapters are doing the work, counted from the run log — which keeps a run after its container is reaped."
            aside={
              <Button asChild variant="ghost" size="sm" className="h-7 text-xs">
                <Link href="/agents">All agents</Link>
              </Button>
            }
          >
            <AgentActivityChart
              data={agents}
              height={Math.max(160, agents.length * 30 + 24)}
            />
          </ChartCard>
        </div>

        <div className="space-y-4">
          <BoundaryPanel runs={runs} loading={isPending} />
          <LandQueuePanel worktrees={worktrees} loading={worktreesPending} />
          {stats.awaitingVerify > 0 && (
            <div className="flex items-start gap-2.5 rounded-lg border border-caution/30 bg-caution/5 p-3 text-xs">
              <GitMerge className="mt-0.5 size-4 shrink-0 text-caution" />
              <p className="text-muted-foreground">
                <span className="font-medium text-foreground">
                  {pluralize(stats.awaitingVerify, "run")}
                </span>{" "}
                {stats.awaitingVerify === 1 ? "is" : "are"} still deciding — the
                verify runs inside the container and its exit code becomes the
                run&apos;s.
              </p>
            </div>
          )}
        </div>
      </div>
    </div>
  );
}
