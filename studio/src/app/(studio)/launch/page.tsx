"use client";

import { useState } from "react";
import { useRouter } from "next/navigation";
import { toast } from "sonner";
import { PageHeader } from "@/components/common/page-header";
import { RepoGate } from "@/components/common/repo-gate";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import { useAgents, useInfo, useLaunch } from "@/lib/api/queries";
import type { LaunchRequest, NetworkMode, Repo } from "@/lib/types";

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

function LaunchForm({ repo }: { repo: Repo }) {
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
  const [git, setGit] = useState(false);

  const offered = (agents ?? []).filter((a) => kind !== "headless" || a.unattended);
  const ceiling = info?.capabilities?.network.ceiling;

  function submit(e: React.FormEvent) {
    e.preventDefault();
    const req: LaunchRequest = { repo: repo.id, name: name || undefined, git: git || undefined };
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
    const allowList = allow.split(",").map((a) => a.trim()).filter(Boolean);
    if (allowList.length) req.allow = allowList;
    const l = pairs(labels);
    if (Object.keys(l).length) req.labels = l;
    const vols = volumes
      .split(",")
      .map((v) => v.trim())
      .filter(Boolean)
      .map((v) => {
        const [n, p, ro] = v.split(":");
        return { name: n, path: p, read_only: ro === "ro" || undefined };
      });
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
    <form onSubmit={submit} className="flex max-w-3xl flex-col gap-6">
      <p className="text-sm text-muted-foreground">
        On a clone of <span className="font-mono text-foreground">{repo.path}</span> at its HEAD. When the run ends, bring
        its work back from Runs; nothing on your machine is written until you merge it.
      </p>

      <RadioGroup value={kind} onValueChange={(v) => setKind(v as Kind)} className="grid gap-2 sm:grid-cols-3">
        {(
          [
            ["headless", "An agent, unattended", "runs the prompt to completion; only agents that never stop to ask"],
            ["console", "An agent, interactive", "a terminal you attach to here"],
            ["command", "A command", "anything the image can run"],
          ] as const
        ).map(([v, title, hint]) => (
          <Label key={v} htmlFor={`kind-${v}`} className="flex cursor-pointer flex-col items-start gap-1 rounded-md border p-3 has-[[data-state=checked]]:border-primary">
            <span className="flex items-center gap-2">
              <RadioGroupItem id={`kind-${v}`} value={v} />
              {title}
            </span>
            <span className="text-xs font-normal text-muted-foreground">{hint}</span>
          </Label>
        ))}
      </RadioGroup>

      {kind === "command" ? (
        <div className="flex flex-col gap-2">
          <Label htmlFor="command">Command</Label>
          <Input id="command" className="font-mono" placeholder="npm test" value={command} onChange={(e) => setCommand(e.target.value)} required />
        </div>
      ) : (
        <>
          <div className="flex flex-col gap-2">
            <Label>Agent</Label>
            <Select value={agent} onValueChange={setAgent}>
              <SelectTrigger className="w-56">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                {offered.map((a) => (
                  <SelectItem key={a.name} value={a.name}>
                    {a.name} {a.login === "saved" ? "· logged in" : ""}
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
          </div>
          <div className="flex flex-col gap-2">
            <Label htmlFor="prompt">{kind === "console" ? "First turn (optional)" : "Prompt"}</Label>
            <Textarea id="prompt" rows={4} value={prompt} onChange={(e) => setPrompt(e.target.value)} required={kind === "headless"} />
          </div>
        </>
      )}

      <details className="rounded-md border p-4">
        <summary className="cursor-pointer text-sm font-medium">Sandbox options</summary>
        <div className="mt-4 grid gap-4 sm:grid-cols-2">
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
          <Label className="flex items-center gap-2 self-end text-sm font-normal">
            <Checkbox checked={git} onCheckedChange={(v) => setGit(v === true)} />
            Commit with my git name and email
          </Label>
        </div>
      </details>

      <div>
        <Button type="submit" disabled={launch.isPending}>
          {launch.isPending ? "Starting…" : "Launch"}
        </Button>
      </div>
    </form>
  );
}

export default function LaunchPage() {
  return (
    <div className="flex flex-col gap-4">
      <PageHeader title="Launch" description="A fresh sandbox on a clone of the repository, with your config, profile and the server's policy applied." />
      <RepoGate>{(repo) => <LaunchForm repo={repo} />}</RepoGate>
    </div>
  );
}
