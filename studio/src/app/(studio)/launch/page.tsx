"use client";

import { Suspense, useState } from "react";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { toast } from "sonner";
import { Bot, Check, ChevronRight, Globe, ListChecks, Server, SquareTerminal, TerminalSquare, WifiOff, type LucideIcon } from "lucide-react";
import { CodeTabs } from "@/components/common/code-tabs";
import { PageHeader } from "@/components/common/page-header";
import { AgentList } from "@/components/launch/agent-list";
import { every, scheduleIntervals } from "@/components/sandbox/snapshots";
import { ImagePicker } from "@/components/launch/image-picker";
import { computeEgress } from "@/components/settings/egress-rules";
import { Gate } from "@/components/shell/gate";
import { Card, CardContent } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import { useAgents, useEgress, useInfo, useLaunch, useNode, useSandboxes, useSnapshots, useTemplates } from "@/lib/api/queries";
import { useCaller } from "@/lib/caller";
import { cliAgent, snippets } from "@/lib/codegen";
import { formatMiB, formatRelative, splitArgs } from "@/lib/format";
import { overLimits } from "@/lib/templates";
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
].filter(
  // Hosted, an unattended agent is a job: the gateway runs it with the API
  // key the user stored as a secret, which a Playground run has no way to use.
  ([k]) => k !== "headless" || process.env.NEXT_PUBLIC_STUDIO_HOSTED !== "on",
) as [Kind, string, string, LucideIcon][];

const NETWORKS: ["default" | NetworkMode, string, string, LucideIcon][] = [
  ["default", "Server default", "", Server],
  ["none", "None", "No network at all", WifiOff],
  ["allowlist", "Allowlist", "Only the hosts of the groups you pick", ListChecks],
  ["open", "Open", "Anywhere, minus the deny rules", Globe],
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
  const params = useSearchParams();
  const { data: templates } = useTemplates();
  const { data: egressSettings } = useEgress();
  // "" is the server's default size; a link from Templates names one.
  const [templateName, setTemplateName] = useState(params.get("template") ?? "");
  const { data: agents } = useAgents();
  const { data: info } = useInfo();
  const launch = useLaunch();
  const [kind, setKind] = useState<Kind>(process.env.NEXT_PUBLIC_STUDIO_HOSTED === "on" ? "console" : "headless");
  const [agent, setAgent] = useState("claude");
  const [prompt, setPrompt] = useState("");
  const [command, setCommand] = useState("");
  const [name, setName] = useState("");
  const [network, setNetwork] = useState<"" | NetworkMode>("");
  const [allow, setAllow] = useState("");
  // The allowlist groups picked; null until touched, which is the default groups.
  const [picked, setPicked] = useState<string[] | null>(null);
  const [baseline, setBaseline] = useState(true);
  const [labels, setLabels] = useState("");
  const [volumes, setVolumes] = useState("");
  const [from, setFrom] = useState<"image" | "snapshot">("image");
  const [image, setImage] = useState(params.get("image") ?? "");
  const [snapshot, setSnapshot] = useState("");
  const canSnapshot = !!(info?.capabilities?.capabilities?.memory_snapshot || info?.capabilities?.capabilities?.disk_snapshot);
  const { data: snapshots } = useSnapshots(canSnapshot);
  const { data: node } = useNode(useCaller().kind === "sandboxd");
  const { data: sandboxes } = useSandboxes();
  const built = node?.images ?? [];
  const inUse = (sandboxes ?? []).map((sb) => sb.image);
  const fromSnapshot = from === "snapshot" && canSnapshot;
  // A snapshot schedule, within what the server allows (its limits).
  const minEvery = info?.capabilities?.limits.min_snapshot_every_secs ?? 300;
  const maxKeep = info?.capabilities?.limits.max_snapshot_keep ?? 5;
  const intervals = scheduleIntervals(minEvery);
  const [scheduled, setScheduled] = useState(false);
  const [everySecs, setEverySecs] = useState("");
  const [keep, setKeep] = useState("3");
  const schedEvery = Number(everySecs || intervals.find((s) => s >= 1800) || intervals[intervals.length - 1]);
  const schedKeep = Math.min(Number(keep), maxKeep);
  const schedule = scheduled && canSnapshot ? { snapshotEverySecs: schedEvery, snapshotKeep: schedKeep } : {};
  const start = fromSnapshot ? { snapshot: snapshot || undefined } : { image: image.trim() || undefined };

  const ceiling = info?.capabilities?.network.ceiling;
  const limits = info?.capabilities?.limits;
  const template = templates?.find((t) => t.name === templateName);
  const size = template ? { cpus: template.cpus, memoryMb: template.memory_mb, diskMb: template.disk_mb || undefined } : {};
  const sizeOver = template ? overLimits(template, limits) : "";
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
  // The groups and deny rules Studio's server adds to the launch, so the code
  // says what the launch will do.
  const defaultMode = info?.capabilities?.network.default.mode;
  const groups = egressSettings?.groups ?? [];
  const pickedNow = picked ?? groups.filter((g) => g.default).map((g) => g.name);
  const isAllowlist = network === "allowlist";
  const egress = computeEgress(egressSettings, {
    network,
    picked: isAllowlist && picked !== null ? picked : undefined,
    allow: isAllowlist ? allowList : [],
    defaultMode,
  });
  const noBaseline = isAllowlist && !baseline;
  const baselineHosts = defaultMode === "allowlist" ? (info?.capabilities?.network.default.allow ?? []) : (info?.baseline_egress ?? []);
  const reach = [...new Set([...(noBaseline || !(isAllowlist || (network === "" && defaultMode === "allowlist")) ? [] : baselineHosts), ...egress.allow])];
  const emptyAllowlist = isAllowlist && reach.length === 0 && kind === "command";
  // What the code panel shows: the same choices, for each client.
  const sandboxOpts = {
    ...start,
    ...schedule,
    ...size,
    name: name || undefined,
    network: egress.network,
    allow: egress.allow,
    deny: egress.deny,
    noBaseline,
    labels: labelMap,
    volumes: vols,
  };
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
    if (schedule.snapshotEverySecs) {
      req.snapshot_every_secs = schedule.snapshotEverySecs;
      req.snapshot_keep = schedule.snapshotKeep;
    }
    if (sizeOver) {
      toast.error(`${template!.name}: ${sizeOver}`);
      return;
    }
    if (template) {
      req.cpus = template.cpus;
      req.memory_mb = template.memory_mb;
      if (template.disk_mb) req.disk_mb = template.disk_mb;
    }
    if (network) req.network = network;
    // The user's own names and the groups picked: the server adds the groups'
    // hosts and the deny rules itself.
    if (isAllowlist) {
      if (allowList.length) req.allow = allowList;
      if (picked !== null) req.egress_groups = picked;
      if (noBaseline) req.no_baseline = true;
    }
    if (emptyAllowlist) {
      toast.error("The allowlist is empty: pick a group, name a host, or keep the built-in hosts");
      return;
    }
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
          {process.env.NEXT_PUBLIC_STUDIO_HOSTED === "on" && (
            <p className="text-xs text-muted-foreground">
              To run an agent unattended, submit it as a job on{" "}
              <Link href="/jobs" className="underline underline-offset-2 hover:text-foreground">
                Jobs
              </Link>
              , with its API key stored under Secrets. An interactive agent asks you to log in inside its sandbox.
            </p>
          )}
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
          <div className="flex flex-col gap-2 border-t pt-3">
            <Label className={cn("flex items-center gap-2 font-normal", !canSnapshot && "text-muted-foreground")}>
              <Checkbox checked={scheduled && canSnapshot} disabled={!canSnapshot} onCheckedChange={(v) => setScheduled(!!v)} aria-label="Snapshot on a schedule" />
              Snapshot it on a schedule
              {!canSnapshot ? <span className="text-xs">(this endpoint takes no snapshots)</span> : null}
            </Label>
            {scheduled && canSnapshot ? (
              <div className="flex flex-wrap items-center gap-2 pl-6 text-sm">
                <span className="text-muted-foreground">every</span>
                <Select value={String(schedEvery)} onValueChange={setEverySecs}>
                  <SelectTrigger size="sm" className="w-28" aria-label="Snapshot every">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {intervals.map((s) => (
                      <SelectItem key={s} value={String(s)}>
                        {every(s)}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
                <span className="text-muted-foreground">keep the newest</span>
                <Select value={String(schedKeep)} onValueChange={setKeep}>
                  <SelectTrigger size="sm" className="w-16" aria-label="Snapshots kept">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {Array.from({ length: Math.max(maxKeep, 1) }, (_, n) => n + 1).map((n) => (
                      <SelectItem key={n} value={String(n)}>
                        {n}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
                <span className="w-full text-xs text-muted-foreground">
                  sandboxd takes them while the sandbox runs, whether or not Studio is open, and removes older scheduled ones; each is a
                  copy of the sandbox on the host&apos;s disk.
                </span>
              </div>
            ) : null}
          </div>
        </Section>

        <Section step={4} title="Size">
          <RadioGroup value={templateName || "default"} onValueChange={(v) => setTemplateName(v === "default" ? "" : v)} aria-label="Size" className="grid gap-2 sm:grid-cols-3 xl:grid-cols-4">
            {[{ name: "default", description: "What sandboxd gives a request that names none" } as const, ...(templates ?? [])].map((t) => {
              const over = "cpus" in t ? overLimits(t, limits) : "";
              return (
                <Label
                  key={t.name}
                  htmlFor={`size-${t.name}`}
                  title={over || t.description}
                  className={cn(
                    "flex cursor-pointer flex-col items-start gap-1 rounded-lg border bg-card p-3 transition-colors hover:border-foreground/20 has-[[data-state=checked]]:border-primary has-[[data-state=checked]]:bg-primary/5",
                    over && "opacity-50",
                  )}
                >
                  <span className="flex w-full items-center justify-between">
                    <span className="font-mono text-sm font-medium">{t.name}</span>
                    <RadioGroupItem id={`size-${t.name}`} value={t.name} disabled={!!over} />
                  </span>
                  <span className="font-mono text-[11px] font-normal text-muted-foreground">
                    {"cpus" in t ? `${t.cpus} vCPU · ${formatMiB(t.memory_mb)}${t.disk_mb ? ` · ${formatMiB(t.disk_mb)} disk` : ""}` : "no size asked for"}
                  </span>
                  {over ? <span className="text-[11px] font-normal text-caution">above this endpoint&apos;s limits</span> : null}
                </Label>
              );
            })}
          </RadioGroup>
          <p className="text-xs text-muted-foreground">
            Sizes are kept in{" "}
            <Link href="/templates" className="underline underline-offset-2 hover:text-foreground">
              Templates
            </Link>
            , where you can add your own.
          </p>
        </Section>

        <Section step={5} title="Network">
          <RadioGroup value={network || "default"} onValueChange={(v) => setNetwork(v === "default" ? "" : (v as NetworkMode))} aria-label="Network" className="grid gap-2 sm:grid-cols-4">
            {NETWORKS.map(([v, title, hint, Icon]) => {
              const off = v === "open" && ceiling !== "open";
              return (
                <Label
                  key={v}
                  htmlFor={`net-${v}`}
                  className={cn(
                    "flex cursor-pointer flex-col items-start gap-1.5 rounded-lg border bg-card p-3 transition-colors hover:border-foreground/20 has-[[data-state=checked]]:border-primary has-[[data-state=checked]]:bg-primary/5",
                    off && "cursor-not-allowed opacity-50",
                  )}
                  title={off ? `This endpoint's ceiling is ${ceiling}` : undefined}
                >
                  <span className="flex w-full items-center justify-between">
                    <Icon className="size-4 text-muted-foreground" aria-hidden />
                    <RadioGroupItem id={`net-${v}`} value={v} disabled={off} />
                  </span>
                  <span className="text-sm font-medium">{title}</span>
                  <span className="text-xs font-normal text-muted-foreground">
                    {v === "default" ? `${defaultMode ?? "…"}${defaultMode === "allowlist" ? ", with the default groups" : ""}` : hint}
                  </span>
                </Label>
              );
            })}
          </RadioGroup>
          {isAllowlist ? (
            <div className="flex flex-col gap-3 rounded-lg border bg-card p-3.5">
              <div className="flex items-center justify-between gap-2">
                <span className="text-xs font-medium">Groups</span>
                {process.env.NEXT_PUBLIC_STUDIO_HOSTED !== "on" && (
                <Link href="/settings" className="text-[11px] text-muted-foreground underline-offset-2 hover:text-foreground hover:underline">
                  Manage groups
                </Link>
                )}
              </div>
              {groups.length ? (
                <div className="flex flex-wrap gap-2" role="group" aria-label="Allowlist groups">
                  {groups.map((g) => {
                    const on = pickedNow.includes(g.name);
                    return (
                      <button
                        key={g.name}
                        type="button"
                        role="checkbox"
                        aria-checked={on}
                        aria-label={`Group ${g.name}`}
                        title={g.hosts.join("\n") || "no hosts"}
                        onClick={() => setPicked(on ? pickedNow.filter((n) => n !== g.name) : [...pickedNow, g.name])}
                        className={cn(
                          "flex items-center gap-2 rounded-md border px-2.5 py-1.5 text-left transition-colors",
                          on ? "border-primary bg-primary/10" : "hover:border-foreground/30",
                        )}
                      >
                        <span className={cn("flex size-3.5 items-center justify-center rounded-sm border", on ? "border-primary bg-primary text-primary-foreground" : "border-muted-foreground/40")}>
                          {on ? <Check className="size-2.5" /> : null}
                        </span>
                        <span className="font-mono text-xs">{g.name}</span>
                        <span className="text-[11px] text-muted-foreground">{g.hosts.length}</span>
                        {g.default ? <span className="rounded border px-1 text-[9px] text-muted-foreground uppercase">default</span> : null}
                      </button>
                    );
                  })}
                </div>
              ) : (
                <p className="text-xs text-muted-foreground">
                  No groups yet.{" "}
                  <Link href="/settings" className="underline underline-offset-2">
                    Make some in Settings
                  </Link>{" "}
                  to pick hosts by purpose.
                </p>
              )}
              <div className="flex flex-col gap-1.5">
                <Label htmlFor="allow" className="text-xs">
                  Also allow
                </Label>
                <Input id="allow" className="h-8 font-mono text-xs" placeholder="one-off hosts: example.com, …" value={allow} onChange={(e) => setAllow(e.target.value)} />
              </div>
              <Label className="flex items-center gap-2 text-xs font-normal">
                <Checkbox checked={baseline} onCheckedChange={(v) => setBaseline(!!v)} aria-label="Include the built-in hosts" />
                Include the built-in hosts
                <span className="text-muted-foreground">({baselineHosts.length}: agents&apos; APIs, package registries, github.com)</span>
              </Label>
              <details className="text-xs">
                <summary className="cursor-pointer text-muted-foreground">
                  Reaches {reach.length} host{reach.length === 1 ? "" : "s"}
                  {kind !== "command" && chosen?.provider_host ? `, and ${chosen.provider_host}` : ""}
                  {egress.deny.length ? `; ${egress.deny.length} denied` : ""}
                </summary>
                <p className="mt-1.5 font-mono text-[11px] leading-relaxed break-all text-muted-foreground">{reach.join(" · ") || "nothing"}</p>
              </details>
              {emptyAllowlist ? <p className="text-xs text-caution">Nothing to reach: pick a group, name a host, or keep the built-in hosts.</p> : null}
            </div>
          ) : null}
        </Section>

        <details className="group rounded-lg border bg-card">
          <summary className="flex cursor-pointer list-none items-center gap-2 px-4 py-3 text-sm font-medium">
            <ChevronRight className="size-4 text-muted-foreground transition-transform group-open:rotate-90" aria-hidden />
            Sandbox options
            <span className="ml-auto text-xs font-normal text-muted-foreground">name, labels, volumes</span>
          </summary>
          <div className="grid gap-4 border-t p-4 sm:grid-cols-2">
            <div className="flex flex-col gap-2">
              <Label htmlFor="name">Name</Label>
              <Input id="name" placeholder="optional" value={name} onChange={(e) => setName(e.target.value)} />
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
              {schedule.snapshotEverySecs ? (
                <>
                  <dt className="text-muted-foreground">Snapshots</dt>
                  <dd className="truncate text-[13px]">
                    every {every(schedule.snapshotEverySecs)}, keep {schedule.snapshotKeep}
                  </dd>
                </>
              ) : null}
              <dt className="text-muted-foreground">Size</dt>
              <dd className="truncate text-[13px]">
                {template ? (
                  <>
                    <span className="font-mono">{template.name}</span>
                    <span className="text-muted-foreground">
                      {" "}
                      · {template.cpus} vCPU · {formatMiB(template.memory_mb)}
                    </span>
                  </>
                ) : (
                  <span className="text-muted-foreground">the server&apos;s default</span>
                )}
              </dd>
              <dt className="text-muted-foreground">Starts in</dt>
              <dd className="truncate font-mono text-[13px]">/sandbox/home</dd>
              <dt className="text-muted-foreground">Runs</dt>
              <dd className="truncate font-mono text-[13px]">
                {kind === "command" ? command || "—" : `${agent}${kind === "console" ? " (console)" : ""}`}
              </dd>
              <dt className="text-muted-foreground">Network</dt>
              <dd className="font-mono text-[13px]">
                {network || defaultMode || "default"}
                {isAllowlist ? <span className="text-muted-foreground"> · {reach.length} hosts</span> : null}
              </dd>
              {isAllowlist ? (
                <>
                  <dt className="text-muted-foreground">Groups</dt>
                  <dd className="truncate font-mono text-[13px]">{pickedNow.join(", ") || "none"}</dd>
                </>
              ) : null}
              {egress.deny.length ? (
                <>
                  <dt className="text-muted-foreground">Denied</dt>
                  <dd className="truncate text-[13px]">
                    <Link href="/settings" className="hover:underline">
                      {egress.deny.length} host{egress.deny.length === 1 ? "" : "s"}
                    </Link>
                  </dd>
                </>
              ) : null}
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
      <Suspense fallback={<p className="text-sm text-muted-foreground">Loading…</p>}>
        <LaunchForm />
      </Suspense>
    </div>
    </Gate>
  );
}
