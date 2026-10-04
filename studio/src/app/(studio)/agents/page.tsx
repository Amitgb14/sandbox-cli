"use client";

import Link from "next/link";
import { ArrowRight, Bot, KeyRound } from "lucide-react";
import { EmptyState } from "@/components/common/empty-state";
import { PageHeader } from "@/components/common/page-header";
import { Card, CardContent } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { useAgents } from "@/lib/api/queries";
import { cn } from "@/lib/utils";
import { useCan } from "@/lib/caller";
import type { Agent } from "@/lib/types";

/**
 * The agents Studio runs: only those with a verified headless mode, because an
 * agent that stops to ask with nobody there does not fail, it hangs. The
 * interactive-only wrappers stay in the CLI (sandbox-cli agent ls).
 */
export default function AgentsPage() {
  const { data, isLoading } = useAgents();
  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="Agents"
        description="The agents with a verified headless mode. A login is copied into each sandbox an agent runs in, and back out when it ends: log in once with sandbox-cli agent <name>, or start a console here."
      />
      {isLoading ? (
        <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
          {Array.from({ length: 3 }, (_, i) => (
            <Skeleton key={i} className="h-32 rounded-xl" />
          ))}
        </div>
      ) : (data ?? []).length === 0 ? (
        <EmptyState icon={Bot} title="No agents" description="This sandbox-cli knows no agent with a verified headless mode." />
      ) : (
        <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-3">
          {data!.map((a) => (
            <AgentCard key={a.name} agent={a} />
          ))}
        </div>
      )}
      <p className="text-xs text-muted-foreground">
        Other agents can be run interactively from the CLI; Studio lists only these.
      </p>
    </div>
  );
}

function AgentCard({ agent: a }: { agent: Agent }) {
  const saved = a.login === "saved";
  const can = useCan();
  return (
    <Card className="surface-sheen gap-0 py-0 transition-colors hover:border-foreground/20">
      <CardContent className="flex flex-col gap-4 p-4">
        <div className="flex items-center gap-3">
          <div className="flex size-9 items-center justify-center rounded-lg bg-primary/10 text-primary">
            <Bot className="size-4" aria-hidden />
          </div>
          <div className="min-w-0">
            <p className="font-mono text-sm font-medium">{a.name}</p>
            <p className="text-xs text-muted-foreground">verified headless</p>
          </div>
        </div>
        <div className="flex items-center justify-between">
          <span className={cn("flex items-center gap-1.5 text-xs", saved ? "text-contained" : "text-muted-foreground")}>
            <KeyRound className="size-3.5" aria-hidden />
            {saved ? "login saved" : a.login === "not kept" ? "not kept between runs" : "not logged in yet"}
          </span>
          {can("sandbox:create") && (
            <Link href="/launch" className="flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground">
              Launch <ArrowRight className="size-3.5" aria-hidden />
            </Link>
          )}
        </div>
      </CardContent>
    </Card>
  );
}
