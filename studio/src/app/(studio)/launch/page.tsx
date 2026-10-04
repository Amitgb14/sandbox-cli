"use client";

import { useState } from "react";
import { useRouter } from "next/navigation";
import { toast } from "sonner";
import { Bot, Check, ChevronRight, SquareTerminal, TerminalSquare, type LucideIcon } from "lucide-react";
import { CodeTabs } from "@/components/common/code-tabs";
import { PageHeader } from "@/components/common/page-header";
import { Card, CardContent } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import { useAgents, useInfo, useLaunch } from "@/lib/api/queries";
import { cliAgent, snippets } from "@/lib/codegen";
import { cn } from "@/lib/utils";
import type { LaunchRequest, NetworkMode } from "@/lib/types";

type Kind = "headless" | "console" | "command";

/** Splits a command line the way a shell would for plain words and quotes. */
function splitArgs(s: string): string[] {
  const out: string[] = [];
  let cur = "";
  let quote: string | null = null;
  let any = false;
  for (const ch of s) {
    if (quote) {
      if (ch === quote) quote = null;
      else cur += ch;
    } else if (ch === '"' || ch === "'") {
      quote = ch;
      any = true;
    } else if (/\s/.test(ch)) {
      if (cur || any) out.push(cur);
      cur = "";
      any = false;
    } else cur += ch;
  }
  if (cur || any) out.push(cur);
  return out;
}

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
  const sandboxOpts = { name: name || undefined, network, allow: allowList, labels: labelMap, volumes: vols };
  const code =
    kind === "command"
      ? snippets({ ...sandboxOpts, command: splitArgs(command), defaultAllow: info?.capabilities?.network.default.allow ?? undefined })
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
            <div role="radiogroup" aria-label="Agent" className="grid grid-cols-2 gap-2 sm:grid-cols-3 md:grid-cols-5">
              {(agents ?? []).map((a) => {
                const on = a.name === agent;
                return (
                  <button
                    key={a.name}
                    type="button"
                    role="radio"
                    aria-checked={on}
                    onClick={() => setAgent(a.name)}
                    className={cn(
                      "flex flex-col items-start gap-1 rounded-lg border bg-card px-3 py-2.5 text-left transition-colors hover:border-foreground/20",
                      on && "border-primary bg-primary/5",
                    )}
                  >
                    <span className="flex w-full items-center justify-between font-mono text-sm">
                      {a.name}
                      {on && <Check className="size-3.5 text-primary" aria-hidden />}
                    </span>
                    <span className={cn("text-[11px]", a.login === "saved" ? "text-contained" : "text-muted-foreground")}>
                      {a.login === "saved" ? "logged in" : a.login === "not kept" ? "login not kept" : "not logged in"}
                    </span>
                  </button>
                );
              })}
            </div>
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
  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="Playground"
        description="Set up a sandbox, launch it, or copy the same setup as code. A sandbox starts in its own home directory and needs no repository: ask the agent, or the command, to clone what it needs."
      />
      <LaunchForm />
    </div>
  );
}
