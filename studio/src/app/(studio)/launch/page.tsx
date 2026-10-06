"use client";

import { useState } from "react";
import { useRouter } from "next/navigation";
import { toast } from "sonner";
import { Bot, ChevronRight, SquareTerminal, TerminalSquare, type LucideIcon } from "lucide-react";
import { CodeTabs } from "@/components/common/code-tabs";
import { PageHeader } from "@/components/common/page-header";
import { AgentList } from "@/components/launch/agent-list";
import { ImagePicker } from "@/components/launch/image-picker";
import { Gate } from "@/components/shell/gate";
import { Card, CardContent } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import { useAgents, useInfo, useLaunch, useNode, useSandboxes, useSnapshots } from "@/lib/api/queries";
import { useCaller } from "@/lib/caller";
import { cliAgent, snippets } from "@/lib/codegen";
import { formatRelative, splitArgs } from "@/lib/format";
import { cn } from "@/lib/utils";
import type { LaunchRequest, NetworkMode } from "@/lib/types";

type Kind = "headless" | "console" | "command";

function pairs(s: string): Record<string, string> {
  const out: Record<string, string> = {};
  for (const part of s.split(",").map((p) => p.trim()).filter(Boolean)) {
    const i = part.indexOf("=");
    if (i > 0) out[part.slice(0, i)] = part.slice(i + 1);
  }
  return out;
}

const KINDS: [Kind, string, string, LucideIcon][] = [
  ["headless", "Agent, unattended", "Runs the prompt to completion and exits", Bot],
  ["console", "Agent, interactive", "A terminal you attach to here", TerminalSquare],
  ["command", "Command", "Anything the image can run", SquareTerminal],
];

function Section({ step, title, children }: { step: number; title: string; children: React.ReactNode }) {
  return (
    <section className="flex flex-col gap-3">
      <h2 className="flex items-center gap-2 text-sm font-semibold">
        <span className="flex size-5 items-center justify-center rounded-full bg-muted text-[11px] text-muted-foreground tabular-nums">
          {step}
        </span>
        {title}
      </h2>
      {children}
    </section>
  );
}

function LaunchForm() {
  const router = useRouter();
  const { data: agents } = useAgents();
  const { data: info } = useInfo();
  const launch = useLaunch();
  const [kind, setKind] = useState<Kind>("headless");
  const [agent, setAgent] = useState("claude");
  const [prompt, setPrompt] = useState("");
  const [command, setCommand] = useState("");
  const [name, setName] = useState("");
  const [network, setNetwork] = useState<"" | NetworkMode>("");
  const [allow, setAllow] = useState("");
  const [labels, setLabels] = useState("");
  const [volumes, setVolumes] = useState("");
  const [from, setFrom] = useState<"image" | "snapshot">("image");
  const [image, setImage] = useState("");
  const [snapshot, setSnapshot] = useState("");
  const canSnapshot = !!(info?.capabilities?.capabilities?.memory_snapshot || info?.capabilities?.capabilities?.disk_snapshot);
  const { data: snapshots } = useSnapshots(canSnapshot);
  const { data: node } = useNode(useCaller().kind === "sandboxd");
  const { data: sandboxes } = useSandboxes();
  const built = node?.images ?? [];
  const inUse = (sandboxes ?? []).map((sb) => sb.image);
  const fromSnapshot = from === "snapshot" && canSnapshot;
  const start = fromSnapshot ? { snapshot: snapshot || undefined } : { image: image.trim() || undefined };

  const ceiling = info?.capabilities?.network.ceiling;
  const chosen = agents?.find((a) => a.name === agent);
  const allowList = allow.split(",").map((a) => a.trim()).filter(Boolean);
  const labelMap = pairs(labels);
  const vols = volumes
    .split(",")
    .map((v) => v.trim())
    .filter(Boolean)
    .map((v) => {
      const [n, p, ro] = v.split(":");
      return { name: n, path: p, read_only: ro === "ro" || undefined };
    });
  // What the code panel shows: the same choices, for each client.
  const sandboxOpts = { ...start, name: name || undefined, network, allow: allowList, labels: labelMap, volumes: vols };
  const code =
    kind === "command"
      ? snippets({ ...sandboxOpts, command: splitArgs(command), defaultAllow:
            info?.capabilities?.network.default.mode === "allowlist"
              ? (info.capabilities.network.default.allow ?? undefined)
              : info?.baseline_egress })
      : [{ id: "cli", label: "CLI", code: cliAgent(agent, sandboxOpts, kind === "console" ? prompt || undefined : undefined) }];

  function submit(e: React.FormEvent) {
    e.preventDefault();
    const req: LaunchRequest = { name: name || undefined };
    if (kind === "command") req.command = splitArgs(command);
    else {
      req.agent = agent;
      req.prompt = prompt || undefined;
      req.console = kind === "console" || undefined;
      if (kind === "console") {
        req.rows = 30;
        req.cols = 110;
      }
    }
    if (fromSnapshot && !snapshot) {
      toast.error("Choose a snapshot to start from, or start from an image");
      return;
    }
    Object.assign(req, start);
    if (network) req.network = network;
    if (allowList.length) req.allow = allowList;
    if (Object.keys(labelMap).length) req.labels = labelMap;
    if (vols.length) req.volumes = vols;
    launch.mutate(req, {
      onSuccess: (res) => {
        toast.success(`Started ${res.sandbox}`);
        router.push(`/sandbox?id=${res.sandbox}`);
      },
      onError: (err) => toast.error(err.message),
    });
  }

  return (
    <form onSubmit={submit} className="grid items-start gap-8 lg:grid-cols-[minmax(0,1fr)_24rem]">
      <div className="flex min-w-0 flex-col gap-8">
        <Section step={1} title="What to run">
          <RadioGroup value={kind} onValueChange={(v) => setKind(v as Kind)} className="grid gap-2 sm:grid-cols-3">
            {KINDS.map(([v, title, hint, Icon]) => (
              <Label
                key={v}
                htmlFor={`kind-${v}`}
                className="flex cursor-pointer flex-col items-start gap-2 rounded-lg border bg-card p-3.5 transition-colors hover:border-foreground/20 has-[[data-state=checked]]:border-primary has-[[data-state=checked]]:bg-primary/5"
              >
                <span className="flex w-full items-center justify-between">
                  <Icon className="size-4 text-muted-foreground" aria-hidden />
                  <RadioGroupItem id={`kind-${v}`} value={v} />
                </span>
                <span className="text-sm font-medium">{title}</span>
                <span className="text-xs font-normal text-muted-foreground">{hint}</span>
              </Label>
            ))}
          </RadioGroup>
        </Section>

        {kind === "command" ? (
          <Section step={2} title="Command">
            <Input id="command" aria-label="Command" className="font-mono" placeholder="npm test" value={command} onChange={(e) => setCommand(e.target.value)} required />
          </Section>
        ) : (
          <Section step={2} title="Agent">
            <AgentList agents={agents ?? []} value={agent} onChange={setAgent} />
            <Label htmlFor="prompt" className="mt-2">
              {kind === "console" ? "First turn (optional)" : "Prompt"}
            </Label>
            <Textarea
              id="prompt"
              rows={5}
              placeholder={kind === "console" ? "Leave empty to start at the agent's prompt" : "What should the agent do?"}
              value={prompt}
              onChange={(e) => setPrompt(e.target.value)}
              required={kind === "headless"}
            />
          </Section>
        )}

        <Section step={3} title="Start from">
          <RadioGroup value={fromSnapshot ? "snapshot" : "image"} onValueChange={(v) => setFrom(v as "image" | "snapshot")} className="flex flex-wrap gap-4" aria-label="Start from">
            <Label className="flex items-center gap-2 font-normal">
              <RadioGroupItem value="image" />
              An image
            </Label>
            <Label className={cn("flex items-center gap-2 font-normal", !canSnapshot && "text-muted-foreground")}>
              <RadioGroupItem value="snapshot" disabled={!canSnapshot} />
              A snapshot
              {!canSnapshot ? <span className="text-xs">(this endpoint takes none)</span> : null}
            </Label>
          </RadioGroup>
          {fromSnapshot ? (
            snapshots?.length ? (
              <Select value={snapshot} onValueChange={setSnapshot}>
                <SelectTrigger aria-label="Snapshot" className="font-mono text-[13px]">
                  <SelectValue placeholder="Choose a snapshot" />
                </SelectTrigger>
                <SelectContent>
                  {snapshots.map((s) => (
                    <SelectItem key={s.id} value={s.id} className="font-mono text-[13px]">
                      {s.id} · {s.image.replace(/^.*\//, "")} · {formatRelative(s.created_at)}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            ) : (
              <p className="text-sm text-muted-foreground">No snapshots yet. Take one from a running sandbox&apos;s panel, with Snapshot.</p>
            )
          ) : (
            <div className="flex flex-col gap-1.5">
              <ImagePicker value={image} onChange={setImage} built={built} inUse={inUse} />
              <p className="text-xs text-muted-foreground">
                A desktop image gives the sandbox a screen, in its Desktop tab; it runs best with 2 GiB (the CLI&apos;s{" "}
                <span className="font-mono">--memory 2048</span>).
              </p>
            </div>
          )}
        </Section>

        <details className="group rounded-lg border bg-card">
          <summary className="flex cursor-pointer list-none items-center gap-2 px-4 py-3 text-sm font-medium">
            <ChevronRight className="size-4 text-muted-foreground transition-transform group-open:rotate-90" aria-hidden />
            Sandbox options
            <span className="ml-auto text-xs font-normal text-muted-foreground">name, network, labels, volumes</span>
          </summary>
          <div className="grid gap-4 border-t p-4 sm:grid-cols-2">
            <div className="flex flex-col gap-2">
              <Label htmlFor="name">Name</Label>
              <Input id="name" placeholder="optional" value={name} onChange={(e) => setName(e.target.value)} />
            </div>
            <div className="flex flex-col gap-2">
              <Label>Network</Label>
              <Select value={network || "default"} onValueChange={(v) => setNetwork(v === "default" ? "" : (v as NetworkMode))}>
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="default">the server&apos;s default</SelectItem>
                  <SelectItem value="none">none</SelectItem>
                  <SelectItem value="allowlist">allowlist</SelectItem>
                  {ceiling === "open" ? <SelectItem value="open">open</SelectItem> : null}
                </SelectContent>
              </Select>
            </div>
            <div className="flex flex-col gap-2">
              <Label htmlFor="allow">Also allow</Label>
              <Input id="allow" className="font-mono" placeholder="proxy.golang.org, …" value={allow} onChange={(e) => setAllow(e.target.value)} />
            </div>
            <div className="flex flex-col gap-2">
              <Label htmlFor="labels">Labels</Label>
              <Input id="labels" className="font-mono" placeholder="team=infra, ticket=123" value={labels} onChange={(e) => setLabels(e.target.value)} />
            </div>
            <div className="flex flex-col gap-2">
              <Label htmlFor="volumes">Volumes</Label>
              <Input id="volumes" className="font-mono" placeholder="cache:/sandbox/home/.cache" value={volumes} onChange={(e) => setVolumes(e.target.value)} />
            </div>
          </div>
        </details>
      </div>

      <div className="flex min-w-0 flex-col gap-4 lg:sticky lg:top-20">
        <Card className="surface-sheen gap-0 py-0">
          <CardContent className="flex flex-col gap-4 p-4">
            <h2 className="text-sm font-semibold">This run</h2>
            <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-2 text-sm">
              <dt className="text-muted-foreground">From</dt>
              <dd className="truncate font-mono text-[13px]">{start.snapshot ?? start.image ?? "the server's image"}</dd>
              <dt className="text-muted-foreground">Starts in</dt>
              <dd className="truncate font-mono text-[13px]">/sandbox/home</dd>
              <dt className="text-muted-foreground">Runs</dt>
              <dd className="truncate font-mono text-[13px]">
                {kind === "command" ? command || "—" : `${agent}${kind === "console" ? " (console)" : ""}`}
              </dd>
              <dt className="text-muted-foreground">Network</dt>
              <dd className="font-mono text-[13px]">{network || info?.capabilities?.network.default.mode || "default"}</dd>
              {kind !== "command" && (
                <>
                  <dt className="text-muted-foreground">Login</dt>
                  <dd className={chosen?.login === "saved" ? "text-contained" : "text-muted-foreground"}>
                    {chosen?.login === "saved" ? "saved" : chosen?.login === "not kept" ? "not kept" : "not yet"}
                  </dd>
                </>
              )}
            </dl>
            <Button type="submit" disabled={launch.isPending} className="w-full shadow-sm shadow-primary/20">
              {launch.isPending ? "Starting…" : "Launch"}
            </Button>
            <p className="text-xs text-muted-foreground">Your config, profile and the server&apos;s policy apply. A control that cannot be delivered refuses the run.</p>
          </CardContent>
        </Card>
        <CodeTabs
          tabs={code}
          note={
            kind === "command" ? (
              <>The same sandbox from a script. Set SANDBOX_TOKEN, and point the address at your sandboxd.</>
            ) : (
              <>
                An agent run is the CLI&apos;s: it copies the agent&apos;s login in and out. Studio runs it
                {kind === "headless" ? " unattended, with the agent's own non-interactive flags and your prompt" : " on a terminal you attach to here"}.
              </>
            )
          }
        />
      </div>
    </form>
  );
}

export default function LaunchPage() {
  // Through a gateway, a key without sandbox:create is refused every launch.
  return (
    <Gate scope="sandbox:create">
    <div className="flex flex-col gap-6">
      <PageHeader
        title="Playground"
        description="Set up a sandbox, launch it, or copy the same setup as code. A sandbox starts in its own home directory and needs no repository: ask the agent, or the command, to clone what it needs."
      />
      <LaunchForm />
    </div>
    </Gate>
  );
}
