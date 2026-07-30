"use client";

import { useEffect } from "react";
import { useParams, useRouter, useSearchParams } from "next/navigation";
import Link from "next/link";
import { Activity, FileDiff, ScrollText, Settings2, SearchX, Terminal } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { EmptyState } from "@/components/common/empty-state";
import { RunHeader } from "@/components/run-detail/run-header";
import { TerminalView } from "@/components/run-detail/terminal-view";
import { MetricsView } from "@/components/run-detail/metrics-view";
import { DiffView } from "@/components/run-detail/diff-view";
import { LogsView } from "@/components/run-detail/logs-view";
import { ConfigView } from "@/components/run-detail/config-view";
import { useRun } from "@/lib/api/queries";
import { useUi } from "@/lib/store";

const TABS = [
  { value: "terminal", label: "Terminal", icon: Terminal },
  { value: "metrics", label: "Metrics", icon: Activity },
  { value: "diff", label: "Changes", icon: FileDiff },
  { value: "logs", label: "Logs", icon: ScrollText },
  { value: "config", label: "Config", icon: Settings2 },
] as const;

export default function RunDetailPage() {
  const { id } = useParams<{ id: string }>();
  const router = useRouter();
  const search = useSearchParams();
  const { data: run, isPending, isError } = useRun(id);
  const pushRecentRun = useUi((s) => s.pushRecentRun);

  useEffect(() => {
    if (run) pushRecentRun(run.id);
  }, [run, pushRecentRun]);

  // The tab lives in the URL so a link can point at one — the row actions link
  // straight to "what it changed", and a shared link keeps the tab.
  const tabParam = search.get("tab");
  const tab = TABS.some((t) => t.value === tabParam) ? tabParam! : "terminal";

  function setTab(next: string) {
    const params = new URLSearchParams(search.toString());
    params.set("tab", next);
    router.replace(`/runs/${id}?${params.toString()}`, { scroll: false });
  }

  if (isPending) {
    return (
      <div className="space-y-6">
        <Skeleton className="h-8 w-64" />
        <Skeleton className="h-6 w-96" />
        <Skeleton className="h-10 w-full max-w-lg" />
        <Skeleton className="h-96 w-full" />
      </div>
    );
  }

  if (isError || !run) {
    return (
      <EmptyState
        icon={SearchX}
        title="No sandbox with that id"
        description="A reference is matched against containers carrying the sandbox.cli label and is never handed to the engine to resolve — so an id that is not one of ours finds nothing rather than somebody else's container."
        action={
          <Button asChild variant="outline" size="sm">
            <Link href="/runs">Back to runs</Link>
          </Button>
        }
      />
    );
  }

  return (
    <div className="space-y-6">
      <RunHeader run={run} />

      <Tabs value={tab} onValueChange={setTab} className="gap-4">
        <TabsList>
          {TABS.map((t) => (
            <TabsTrigger key={t.value} value={t.value} className="gap-1.5">
              <t.icon className="size-3.5" />
              {t.label}
            </TabsTrigger>
          ))}
        </TabsList>

        <TabsContent value="terminal">
          <TerminalView run={run} />
        </TabsContent>
        <TabsContent value="metrics">
          <MetricsView run={run} />
        </TabsContent>
        <TabsContent value="diff">
          <DiffView run={run} />
        </TabsContent>
        <TabsContent value="logs">
          <LogsView run={run} />
        </TabsContent>
        <TabsContent value="config">
          <ConfigView run={run} />
        </TabsContent>
      </Tabs>
    </div>
  );
}
