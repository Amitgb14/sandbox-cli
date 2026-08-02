"use client";

import { useState } from "react";
import Link from "next/link";
import {
  Bot,
  CircleSlash,
  ExternalLink,
  KeyRound,
  Play,
  Search,
  Terminal,
  Zap,
} from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { PageHeader } from "@/components/common/page-header";
import { EmptyState } from "@/components/common/empty-state";
import { MetricTile } from "@/components/common/metric-tile";
import { useAgents } from "@/lib/api/queries";
import { DELIVERY_LABEL } from "@/lib/constants";
import { formatArgv, formatRelative, pluralize, tildify } from "@/lib/format";
import { cn } from "@/lib/utils";
import type { Agent } from "@/lib/types";

/**
 * The adapters.
 *
 * The distinction the page is built around is **verified headless**, because it
 * is the one with consequences: only those five may be named in a `fleet.yaml`.
 * A fleet is unattended, and an agent that stops to ask for permission does not
 * fail — it hangs until somebody kills it.
 */
export default function AgentsPage() {
  const { data, isPending } = useAgents();
  const [query, setQuery] = useState("");
  const [filter, setFilter] = useState<"all" | "fleet" | "logged-in">("all");

  const agents = (data ?? []).filter((a) => {
    if (filter === "fleet" && !a.headlessVerified) return false;
    if (filter === "logged-in" && !a.auth.persisted) return false;
    if (!query) return true;
    const hay = `${a.name} ${a.label} ${a.envAllow.join(" ")} ${a.delivery}`.toLowerCase();
    return hay.includes(query.toLowerCase());
  });

  const total = data?.length ?? 0;
  const fleetEligible = data?.filter((a) => a.headlessVerified).length ?? 0;
  const loggedIn = data?.filter((a) => a.auth.persisted).length ?? 0;
  const sessions = data?.reduce((n, a) => n + a.sessions, 0) ?? 0;

  return (
    <div className="space-y-6">
      <PageHeader
        title="Agents"
        description="Every adapter sandbox-cli knows how to start, what it forwards across the boundary, and whether it can be trusted to run unattended."
      />

      <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
        <MetricTile label="Adapters" icon={Bot} value={total || null} loading={isPending} footer="wired into the command tree" />
        <MetricTile
          label="Fleet-eligible"
          icon={Zap}
          value={fleetEligible || null}
          loading={isPending}
          hint="Adapters with a recorded non-interactive argv. A new descriptor without one fails the contract test rather than quietly widening what a fleet.yaml may name."
          footer="verified headless"
        />
        <MetricTile
          label="Logged in"
          icon={KeyRound}
          value={loggedIn || null}
          loading={isPending}
          absentReason="No agent has a persisted login yet"
          footer="have a persisted HOME"
        />
        <MetricTile
          label="Conversations"
          icon={Terminal}
          value={sessions || null}
          loading={isPending}
          absentReason="No transcripts found"
          hint="Sessions found in verified transcript stores. An agent whose format has no confirmed reader lists its sessions as partial rather than guessing at a title and a turn count."
          footer="across verified stores"
        />
      </div>

      <div className="flex flex-wrap items-center gap-3">
        <div className="relative">
          <Search className="absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2 text-muted-foreground" />
          <Input
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="Search adapters…"
            className="h-8 w-full pl-8 sm:w-64"
          />
        </div>
        <Tabs value={filter} onValueChange={(v) => setFilter(v as typeof filter)}>
          <TabsList>
            <TabsTrigger value="all">All</TabsTrigger>
            <TabsTrigger value="fleet">Fleet-eligible</TabsTrigger>
            <TabsTrigger value="logged-in">Logged in</TabsTrigger>
          </TabsList>
        </Tabs>
      </div>

      {isPending ? (
        <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
          {Array.from({ length: 6 }).map((_, i) => (
            <Skeleton key={i} className="h-56 w-full" />
          ))}
        </div>
      ) : agents.length === 0 ? (
        <EmptyState
          icon={Bot}
          title="No adapters match"
          description="Clear the search, or switch back to All."
        />
      ) : (
        <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
          {agents.map((agent) => (
            <AgentCard key={agent.name} agent={agent} />
          ))}
        </div>
      )}
    </div>
  );
}

function AgentCard({ agent }: { agent: Agent }) {
  return (
    <Card className="surface-sheen gap-3">
      <CardHeader className="gap-1">
        <div className="flex items-start justify-between gap-2">
          <div className="min-w-0">
            <CardTitle className="truncate text-sm font-medium">{agent.label}</CardTitle>
            <p className="font-mono text-xs text-muted-foreground">{agent.name}</p>
          </div>
          {agent.headlessVerified ? (
            <Tooltip>
              <TooltipTrigger asChild>
                <Badge
                  variant="outline"
                  className="shrink-0 gap-1 border-contained/40 bg-contained/10 text-[10px] text-contained"
                >
                  <Zap className="size-3" />
                  headless
                </Badge>
              </TooltipTrigger>
              <TooltipContent className="max-w-xs">
                Has a verified non-interactive argv, so it may appear in a fleet.yaml.
              </TooltipContent>
            </Tooltip>
          ) : (
            <Tooltip>
              <TooltipTrigger asChild>
                <Badge variant="outline" className="shrink-0 gap-1 text-[10px] text-muted-foreground">
                  <CircleSlash className="size-3" />
                  interactive
                </Badge>
              </TooltipTrigger>
              <TooltipContent className="max-w-xs">
                No recorded headless argv, so a fleet will not start it. Unattended, an agent that
                asks for permission hangs rather than fails.
              </TooltipContent>
            </Tooltip>
          )}
        </div>
      </CardHeader>

      <CardContent className="space-y-3 text-xs">
        <div className="flex flex-wrap items-center gap-1.5">
          <Badge variant="secondary" className="text-[10px]">
            {DELIVERY_LABEL[agent.delivery]}
          </Badge>
          {agent.statusLine && (
            <Tooltip>
              <TooltipTrigger asChild>
                <Badge variant="outline" className="text-[10px]">
                  status line
                </Badge>
              </TooltipTrigger>
              <TooltipContent className="max-w-xs">
                The only adapter with one — it has a hook for it. The others show nothing on
                screen, which is a limit rather than an oversight.
              </TooltipContent>
            </Tooltip>
          )}
          {agent.historySync && (
            <Tooltip>
              <TooltipTrigger asChild>
                <Badge variant="outline" className="text-[10px]">
                  history sync
                </Badge>
              </TooltipTrigger>
              <TooltipContent className="max-w-xs">
                Mounts the host&apos;s per-project history bucket, so sessions resolve inside the
                sandbox and out. Scoped to the one project.
              </TooltipContent>
            </Tooltip>
          )}
        </div>

        {/* Login state, which is the thing that decides whether an unattended
            run can work at all. */}
        <div className="space-y-1 rounded-md border bg-card/40 p-2.5">
          <div className="flex items-center justify-between gap-2">
            <span className="flex items-center gap-1.5 text-muted-foreground">
              <KeyRound className="size-3.5" />
              Login
            </span>
            <span className={cn(agent.auth.persisted ? "text-contained" : "text-muted-foreground")}>
              {agent.auth.persisted ? "persisted" : "not set up"}
            </span>
          </div>
          {agent.auth.persisted ? (
            <>
              <p className="truncate font-mono text-[11px] text-muted-foreground" title={agent.auth.path}>
                {tildify(agent.auth.path)}
              </p>
              <p className="text-[11px] text-muted-foreground">
                last touched {formatRelative(agent.auth.lastSeen)}
              </p>
            </>
          ) : (
            <p className="text-[11px] text-muted-foreground">
              Run it interactively once to log in. A fleet cannot: there is nobody there to answer
              the browser.
            </p>
          )}
        </div>

        {agent.envAllow.length > 0 && (
          <div className="space-y-1">
            <p className="text-[11px] font-medium tracking-wide text-muted-foreground uppercase">
              Forwarded if set
            </p>
            <div className="flex flex-wrap gap-1">
              {agent.envAllow.map((e) => (
                <Badge key={e} variant="outline" className="font-mono text-[10px]">
                  {e}
                </Badge>
              ))}
            </div>
          </div>
        )}

        {agent.env.length > 0 && (
          <div className="space-y-1">
            <p className="text-[11px] font-medium tracking-wide text-muted-foreground uppercase">
              Set by sandbox-cli
            </p>
            <div className="flex flex-wrap gap-1">
              {agent.env.map((e) => (
                <Badge key={e} variant="secondary" className="font-mono text-[10px]">
                  {e}
                </Badge>
              ))}
            </div>
          </div>
        )}

        {agent.autonomousInvocation && (
          <div className="space-y-1">
            <p className="text-[11px] font-medium tracking-wide text-muted-foreground uppercase">
              Headless argv
            </p>
            <code className="block overflow-x-auto rounded bg-muted/60 px-2 py-1.5 font-mono text-[11px] whitespace-nowrap">
              {formatArgv(agent.autonomousInvocation)}
            </code>
          </div>
        )}

        <div className="flex items-center justify-between gap-2 border-t pt-2.5">
          <span className="text-[11px] text-muted-foreground">
            {agent.contextStore === "verified"
              ? `${pluralize(agent.sessions, "conversation")} · verified store`
              : agent.contextStore === "empty"
                ? "store found, nothing in it yet"
                : "no verified store — reported untracked, not guessed at"}
          </span>
          <div className="flex items-center gap-1">
            {agent.docs && (
              <Button asChild variant="ghost" size="icon" className="size-7">
                <a href={agent.docs} target="_blank" rel="noreferrer" aria-label="Docs">
                  <ExternalLink className="size-3.5" />
                </a>
              </Button>
            )}
            <Button asChild variant="outline" size="sm" className="h-7 text-xs">
              <Link href={`/launch?agent=${agent.name}`}>
                <Play className="size-3.5" />
                Run
              </Link>
            </Button>
          </div>
        </div>
      </CardContent>
    </Card>
  );
}
