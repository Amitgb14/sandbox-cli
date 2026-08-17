"use client";

import { useEffect, useMemo, useState } from "react";
import Link from "next/link";
import {
  Activity,
  CheckCircle2,
  CircleSlash,
  History,
  Network,
  Play,
  RotateCw,
  Shuffle,
  XCircle,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectLabel,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { PageHeader } from "@/components/common/page-header";
import { ChainGraph } from "@/components/routing/chain-graph";
import { EpisodeFlow } from "@/components/routing/episode-flow";
import { FailoverTrend } from "@/components/routing/failover-trend";
import { UptimeStrip } from "@/components/routing/uptime-strip";
import {
  useAgents,
  useAudit,
  useProbeHistory,
  useRouting,
  useSetProviders,
} from "@/lib/api/queries";
import { chainEdges, episodesFrom, failoverDays, routeStats } from "@/lib/routing-history";
import { useUi } from "@/lib/store";
import { cn } from "@/lib/utils";
import type { ProviderStatus } from "@/lib/types";

/**
 * Agent routing: what happens when a provider is down.
 *
 * Two things on one screen, because they answer each other. The **provider
 * status** is the question a chain is configured against — until it existed, the
 * only way to ask "is Claude down" was to launch a run and see. The **chains**
 * are the standing answer, remembered per primary agent and applied by every
 * launch.
 *
 * The **activity** is the third, and it is a derivation over the run log rather
 * than anything new being collected — which is why it can answer for history
 * written before this screen existed. It is drawn three ways because the
 * questions are three: the graph is "if Claude goes down now, what happens", the
 * trend is "is this getting worse", and the flow rows are "what happened that
 * time". One picture answering all three answers none of them well.
 *
 * What the screen must not do is imply more than routing does. Studio probes
 * before launching *and* the daemon watches a run afterwards, handing the work
 * over when one fails having written nothing — but a daemon restarted mid-run
 * leaves that run alone, and a run that changed files is never retried. Both
 * limits are stated here rather than discovered when a run fails and nothing
 * falls through.
 */
export default function RoutingPage() {
  const { data: providers, isPending, isFetching, refetch } = useRouting();
  const { data: agents } = useAgents();
  // The activity below is a derivation over the run log — no new collection, and
  // it therefore answers for history written before this screen existed.
  const { data: audit } = useAudit(undefined, 5000);
  const episodes = useMemo(() => episodesFrom(audit ?? []), [audit]);
  const stats = useMemo(() => routeStats(episodes), [episodes]);
  const { data: history } = useProbeHistory();
  const setProviders = useSetProviders();
  const routingPrefs = useUi((s) => s.routingPrefs);
  const setRoutingPref = useUi((s) => s.setRoutingPref);

  const routable = (providers ?? []).filter((p) => p.routable);
  const down = routable.filter((p) => p.probed && !p.reachable);
  const edges = useMemo(
    () => chainEdges(routingPrefs, episodes),
    [routingPrefs, episodes],
  );
  const days = useMemo(() => failoverDays(episodes), [episodes]);

  return (
    <div className="space-y-5">
      <PageHeader
        title="Routing"
        description={
          <>
            When an agent&apos;s provider is not answering, a chain runs the next one instead.
            Studio checks before launching, and the daemon watches a run afterwards: one that
            fails having written nothing is handed to the next agent with a briefing. A daemon
            restarted mid-run leaves that run alone.
          </>
        }
        actions={
          <Button variant="outline" size="sm" onClick={() => refetch()} disabled={isFetching}>
            <RotateCw className={cn("size-4", isFetching && "animate-spin")} />
            Re-check
          </Button>
        }
      />

      {down.length > 0 && (
        <div className="flex items-start gap-2.5 rounded-lg border border-status-critical/30 bg-status-critical/5 p-3 text-sm">
          <XCircle className="mt-0.5 size-4 shrink-0 text-status-critical" />
          <p className="text-muted-foreground">
            <span className="font-medium text-foreground">
              {down.map((p) => p.agent).join(", ")}
            </span>{" "}
            {down.length === 1 ? "is" : "are"} not answering right now. A run naming{" "}
            {down.length === 1 ? "it" : "them"} will start its fallback instead, or be refused
            if the chain has none.
          </p>
        </div>
      )}

      <Card>
        <CardHeader className="pb-3">
          <CardTitle className="text-sm">Providers</CardTitle>
        </CardHeader>
        <CardContent className="p-0">
          {isPending ? (
            <div className="space-y-2 p-4">
              {Array.from({ length: 5 }, (_, i) => (
                <Skeleton key={i} className="h-10 w-full" />
              ))}
            </div>
          ) : (
            <ul className="divide-y">
              {(providers ?? []).map((p) => (
                <ProviderRow
                  key={p.agent}
                  status={p}
                  label={agents?.find((a) => a.name === p.agent)?.label ?? p.agent}
                  onSet={(host) =>
                    setProviders.mutate({
                      // The whole *managed* map with this edit applied: the
                      // endpoint writes a set, so sending one key would forget
                      // the others — and `managed` rather than `overridden`,
                      // since a host from config.yaml is not Studio's to copy.
                      ...Object.fromEntries(
                        (providers ?? [])
                          .filter((o) => o.managed)
                          .map((o) => [o.agent, o.host ?? ""]),
                      ),
                      [p.agent]: host,
                    })
                  }
                  saving={setProviders.isPending}
                />
              ))}
            </ul>
          )}
        </CardContent>
      </Card>

      <Card>
        <CardHeader className="pb-3">
          <CardTitle className="flex items-center gap-2 text-sm">
            <Activity className="size-4" />
            Uptime
          </CardTitle>
        </CardHeader>
        <CardContent>
          {history ? (
            <UptimeStrip data={history} />
          ) : (
            <Skeleton className="h-24 w-full" />
          )}
        </CardContent>
      </Card>

      <Card>
        <CardHeader className="pb-3">
          <CardTitle className="flex items-center gap-2 text-sm">
            <Network className="size-4" />
            Chains, as they stand
          </CardTitle>
        </CardHeader>
        <CardContent>
          <p className="mb-3 text-xs text-muted-foreground">
            What is configured, what is answering, and which hops have actually been
            taken — the three together, because each is misleading alone: a configured
            chain says nothing about whether it works, and a count of failovers says
            nothing about which of them today&apos;s settings would repeat.
          </p>
          <ChainGraph providers={providers ?? []} edges={edges} />
        </CardContent>
      </Card>

      <Card>
        <CardHeader className="pb-3">
          <CardTitle className="flex flex-wrap items-center justify-between gap-2 text-sm">
            <span className="flex items-center gap-2">
              <History className="size-4" />
              Activity
            </span>
            <span className="text-xs font-normal text-muted-foreground">
              from the run log — nothing extra is collected
            </span>
          </CardTitle>
        </CardHeader>
        <CardContent className="space-y-3">
          {stats.switched === 0 ? (
            <p className="text-sm text-muted-foreground">
              No run has fallen through yet. That is the answer you want — it means no
              provider has been down while you were working — but it also means the chains
              below are so far untested.
            </p>
          ) : (
            <>
              <div className="grid gap-3 sm:grid-cols-3">
                <Stat label="Chains fired" value={stats.switched} />
                <Stat
                  label="Rescued"
                  value={stats.rescued}
                  hint="The fallback ran and the work finished."
                  tone="good"
                />
                <Stat
                  label="Still failed"
                  value={stats.wasted}
                  hint="The chain fired and the work still did not land — a container spent for nothing."
                  tone={stats.wasted > 0 ? "critical" : undefined}
                />
              </div>
              {stats.from.length > 0 && (
                <p className="text-xs text-muted-foreground">
                  Routed away from{" "}
                  {stats.from.map((f) => `${f.agent} (${f.count})`).join(", ")}
                  {stats.reasons.length > 0 && (
                    <> — most often because {stats.reasons[0].reason}.</>
                  )}
                </p>
              )}
              <FailoverTrend data={days} />
              <EpisodeFlow episodes={episodes.filter((e) => e.switched).slice(0, 8)} />
            </>
          )}
        </CardContent>
      </Card>

      <Card>
        <CardHeader className="pb-3">
          <CardTitle className="flex items-center gap-2 text-sm">
            <Shuffle className="size-4" />
            Chains
          </CardTitle>
        </CardHeader>
        <CardContent className="space-y-3">
          <p className="text-xs text-muted-foreground">
            Set per primary agent, because a fallback is a statement about a pair: &ldquo;if
            Claude is down, use Codex&rdquo; says nothing about what should happen when the
            primary is Gemini. The Launch screen starts from whatever is set here, and a
            change there is remembered here.
          </p>
          <ul className="divide-y rounded-md border">
            {routable.map((p) => {
              const chain = routingPrefs[p.agent] ?? [];
              return (
                <li key={p.agent} className="flex flex-wrap items-center gap-3 p-3">
                  <span className="min-w-24 font-mono text-xs">{p.agent}</span>
                  <span className="text-xs text-muted-foreground">falls back to</span>
                  <Select
                    value={chain[0] ?? "__none"}
                    onValueChange={(v) => setRoutingPref(p.agent, v === "__none" ? [] : [v])}
                  >
                    <SelectTrigger className="h-8 w-56 text-xs">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectGroup>
                        <SelectLabel>No fallback</SelectLabel>
                        <SelectItem value="__none">Fail instead</SelectItem>
                      </SelectGroup>
                      <SelectGroup>
                        <SelectLabel>Fall back to</SelectLabel>
                        {routable
                          .filter((o) => o.agent !== p.agent)
                          .map((o) => (
                            <SelectItem key={o.agent} value={o.agent}>
                              {o.agent}
                            </SelectItem>
                          ))}
                      </SelectGroup>
                    </SelectContent>
                  </Select>
                  {chain.length > 0 && (
                    <Button asChild variant="ghost" size="sm" className="ml-auto h-7 text-xs">
                      <Link href={`/launch?agent=${encodeURIComponent(p.agent)}`}>
                        <Play className="size-3.5" />
                        Launch with this
                      </Link>
                    </Button>
                  )}
                </li>
              );
            })}
          </ul>
          <p className="text-xs text-muted-foreground">
            Only agents with a verified non-interactive mode can be routed to: a Studio run is
            detached, and an agent that stops to ask permission does not fail — it hangs, in
            the fallback slot, where nobody is looking. On the command line the same standing
            choice is <code className="font-mono">routing:</code> in your own config, and{" "}
            <code className="font-mono">--fallback</code> for one run.
          </p>
        </CardContent>
      </Card>
    </div>
  );
}

function ProviderRow({
  status,
  label,
  onSet,
  saving,
}: {
  status: ProviderStatus;
  label: string;
  onSet: (host: string) => void;
  saving: boolean;
}) {
  const [editing, setEditing] = useState(false);
  const [host, setHost] = useState(status.host ?? "");
  // Follow the server when it answers with something different — a save returns
  // a fresh probe, and a field still holding what was typed would look unsaved.
  useEffect(() => setHost(status.host ?? ""), [status.host]);

  return (
    <li className="flex flex-wrap items-center gap-3 px-4 py-2.5">
      <StatusDot status={status} />
      <span className="min-w-32 text-sm">{label}</span>
      {editing ? (
        <span className="flex min-w-0 flex-1 items-center gap-2">
          <Input
            autoFocus
            value={host}
            onChange={(e) => setHost(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") {
                onSet(host.trim());
                setEditing(false);
              }
              if (e.key === "Escape") {
                setHost(status.host ?? "");
                setEditing(false);
              }
            }}
            placeholder="api.groq.com — or blank to stop probing this one"
            className="h-7 font-mono text-[11px]"
          />
          <Button
            size="sm"
            className="h-7 text-xs"
            disabled={saving}
            onClick={() => {
              onSet(host.trim());
              setEditing(false);
            }}
          >
            Save
          </Button>
        </span>
      ) : (
        <button
          onClick={() => setEditing(true)}
          className="min-w-0 flex-1 truncate rounded px-1 text-left font-mono text-[11px] text-muted-foreground hover:bg-accent"
          title="Set the host to probe for this agent"
        >
          {status.host || "not set — click to say which provider yours uses"}
        </button>
      )}
      {status.gateway && (
        <Tooltip>
          <TooltipTrigger asChild>
            <Badge variant="outline" className="gap-1 text-[10px]">
              via {status.gateway}
            </Badge>
          </TooltipTrigger>
          <TooltipContent className="max-w-xs">
            This agent&apos;s calls go through a gateway rather than to its vendor, so this is
            what gets probed — and what an egress allowlist has to permit. The key is yours:
            sandbox-cli carries the variable&apos;s name and never a value.
          </TooltipContent>
        </Tooltip>
      )}
      {status.overridden && (
        <Tooltip>
          <TooltipTrigger asChild>
            <Badge variant="outline" className="text-[10px]">
              {status.managed ? "yours" : "config.yaml"}
            </Badge>
          </TooltipTrigger>
          <TooltipContent className="max-w-xs">
            {status.managed
              ? "Set here, in the file Studio writes."
              : "Set in your own config.yaml, which outranks anything set here — edit it there. Saving from this screen would appear to work and revert on the next daemon start."}
          </TooltipContent>
        </Tooltip>
      )}
      {!status.routable && (
        <Tooltip>
          <TooltipTrigger asChild>
            <Badge variant="outline" className="text-[10px]">
              not routable
            </Badge>
          </TooltipTrigger>
          <TooltipContent className="max-w-xs">
            No verified non-interactive mode, so a detached run of it could hang waiting for
            an approval nobody is there to give. It cannot appear in a chain.
          </TooltipContent>
        </Tooltip>
      )}
      {status.reason && (
        <span className="text-[11px] text-status-critical">{status.reason}</span>
      )}
    </li>
  );
}

function StatusDot({ status }: { status: ProviderStatus }) {
  // Three states, not two. "Not checked" is its own answer: an agent with no
  // single provider host has nothing to ask, and reporting that as down would
  // condemn a working agent on the strength of a question nobody put.
  if (!status.probed) {
    return (
      <Tooltip>
        <TooltipTrigger asChild>
          <CircleSlash className="size-4 shrink-0 text-muted-foreground" />
        </TooltipTrigger>
        <TooltipContent className="max-w-xs">
          Not checked — no host is set for this agent, so there is nothing to ask. It
          still works and can still be routed to; it just cannot be skipped before a run.
          Click the host to say which provider yours uses.
        </TooltipContent>
      </Tooltip>
    );
  }
  if (status.reachable) {
    return <CheckCircle2 className="size-4 shrink-0 text-status-good" />;
  }
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <XCircle className="size-4 shrink-0 text-status-critical" />
      </TooltipTrigger>
      <TooltipContent className="max-w-xs">
        Not answering from this machine. That is what an outage looks like — and also what a
        proxy or an offline laptop looks like, which is why the reason is shown beside it.
      </TooltipContent>
    </Tooltip>
  );
}

function Stat({
  label,
  value,
  hint,
  tone,
}: {
  label: string;
  value: number;
  hint?: string;
  tone?: "good" | "critical";
}) {
  return (
    <div className="rounded-md border p-3">
      <p className="text-xs text-muted-foreground">{label}</p>
      <p
        className={cn(
          "text-2xl font-semibold tabular-nums",
          tone === "good" && "text-status-good",
          tone === "critical" && "text-status-critical",
        )}
      >
        {value}
      </p>
      {hint && <p className="mt-0.5 text-[11px] text-muted-foreground">{hint}</p>}
    </div>
  );
}
