"use client";

import { Suspense, useState } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { toast } from "sonner";
import { PageHeader } from "@/components/common/page-header";
import { StatusBadge } from "@/components/common/status-badge";
import { Labels } from "@/components/sandbox/labels";
import { SandboxTerminal } from "@/components/sandbox/terminal";
import { ProcessOutput } from "@/components/sandbox/output";
import { SandboxFiles } from "@/components/sandbox/files";
import { SandboxEvents } from "@/components/sandbox/events";
import { Button } from "@/components/ui/button";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { useInfo, useKill, useProcesses, useSandbox, useSnapshot, useSuspend } from "@/lib/api/queries";
import { formatRelative } from "@/lib/format";

/** One sandbox: its terminal, its output, its files and its events. */
function SandboxDetail() {
  const id = useSearchParams().get("id") ?? "";
  const router = useRouter();
  const { data: sb, error } = useSandbox(id);
  const live = sb && sb.state !== "terminated";
  const { data: procs } = useProcesses(id, !!live);
  const { data: info } = useInfo();
  const caps = info?.capabilities?.capabilities ?? {};
  const kill = useKill();
  const suspend = useSuspend();
  const snapshot = useSnapshot();
  const [pid, setPid] = useState<number | null>(null);

  if (!id) return <p className="text-sm text-muted-foreground">No sandbox named.</p>;
  if (error) return <p className="text-sm text-destructive">{error.message}</p>;
  if (!sb) return <p className="text-sm text-muted-foreground">Loading…</p>;

  const processes = procs ?? [];
  const tty = processes.find((p) => p.tty && p.state === "running");
  const selected = processes.find((p) => p.pid === pid) ?? processes[processes.length - 1];
  const act = (p: Promise<unknown>, done: string) =>
    p.then(() => toast.success(done)).catch((e: Error) => toast.error(e.message));

  return (
    <div className="flex flex-col gap-5">
      <PageHeader
        title={<span className="font-mono">{sb.name || sb.id}</span>}
        description={`${sb.image} · ${sb.cpus} CPU · ${sb.memory_mb} MiB · created ${formatRelative(sb.created_at)}`}
        actions={
          live ? (
            <div className="flex gap-2">
              {caps.suspend && (
                <Button variant="outline" size="sm" disabled={suspend.isPending}
                  onClick={() => act(suspend.mutateAsync({ id, resume: sb.state === "suspended" }), sb.state === "suspended" ? "Resumed" : "Suspended")}>
                  {sb.state === "suspended" ? "Resume" : "Suspend"}
                </Button>
              )}
              {caps.memory_snapshot && sb.state === "running" && (
                <Button variant="outline" size="sm" disabled={snapshot.isPending}
                  onClick={() => act(snapshot.mutateAsync(id), "Snapshot taken")}>
                  Snapshot
                </Button>
              )}
              <Button variant="destructive" size="sm" disabled={kill.isPending}
                onClick={() => {
                  if (confirm("Terminate this sandbox? Anything not brought back goes with it.")) {
                    act(kill.mutateAsync(id), "Terminated").then(() => router.push("/sandboxes"));
                  }
                }}>
                Terminate
              </Button>
            </div>
          ) : undefined
        }
      >
        <div className="mt-2 flex flex-wrap items-center gap-3 text-sm">
          <StatusBadge outcome={sb.state} />
          <span className="font-mono text-xs">network {sb.network.mode}{sb.network.allow?.length ? ` · ${sb.network.allow.length} names` : ""}</span>
          {sb.env_names?.length ? <span className="font-mono text-xs text-muted-foreground">env {sb.env_names.join(", ")}</span> : null}
          {sb.volumes?.map((v) => (
            <span key={v.name} className="font-mono text-xs text-muted-foreground">{v.name}:{v.path}{v.read_only ? ":ro" : ""}</span>
          ))}
          <Labels labels={sb.labels} />
        </div>
      </PageHeader>

      <Tabs defaultValue={tty ? "terminal" : "output"}>
        <TabsList>
          <TabsTrigger value="terminal" disabled={!tty}>Terminal</TabsTrigger>
          <TabsTrigger value="output" disabled={processes.length === 0}>Output</TabsTrigger>
          <TabsTrigger value="files" disabled={!live || sb.state !== "running"}>Files</TabsTrigger>
          <TabsTrigger value="events">Events</TabsTrigger>
        </TabsList>
        <TabsContent value="terminal" className="pt-3">
          {tty ? <SandboxTerminal sandbox={id} pid={tty.pid} /> : <p className="text-sm text-muted-foreground">No process here has a terminal.</p>}
        </TabsContent>
        <TabsContent value="output" className="flex flex-col gap-3 pt-3">
          <div className="flex flex-wrap gap-1.5">
            {processes.map((p) => (
              <Button key={p.pid} size="sm" variant={selected?.pid === p.pid ? "default" : "outline"} className="font-mono text-xs" onClick={() => setPid(p.pid)}>
                {p.pid} · {p.argv[0]} {p.state === "exited" ? `(${p.exit_code})` : ""}
              </Button>
            ))}
          </div>
          {selected ? <ProcessOutput sandbox={id} pid={selected.pid} /> : null}
        </TabsContent>
        <TabsContent value="files" className="pt-3">
          <SandboxFiles sandbox={id} />
        </TabsContent>
        <TabsContent value="events" className="pt-3">
          <SandboxEvents sandbox={id} />
        </TabsContent>
      </Tabs>
    </div>
  );
}

export default function SandboxPage() {
  return (
    <Suspense fallback={<p className="text-sm text-muted-foreground">Loading…</p>}>
      <SandboxDetail />
    </Suspense>
  );
}
