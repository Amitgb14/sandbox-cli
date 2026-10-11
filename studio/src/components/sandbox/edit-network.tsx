"use client";

import { useState } from "react";
import Link from "next/link";
import { toast } from "sonner";
import { Check } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Label } from "@/components/ui/label";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
import { Textarea } from "@/components/ui/textarea";
import { useAgents, useEgress, useInfo, useUpdateNetwork } from "@/lib/api/queries";
import { cn } from "@/lib/utils";
import type { NetworkMode, NetworkPolicy, Sandbox } from "@/lib/types";

const HOST = /^(\*\.)?([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)*[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$/;

function hostsOf(text: string): string[] {
  return [...new Set(text.split(/[\s,]+/).map((h) => h.trim().toLowerCase().replace(/\.$/, "")).filter(Boolean))];
}

/**
 * Changes a running sandbox's network: none, an allowlist built from the
 * allowlist groups, or open, with its deny list. It goes straight to the API
 * (PATCH /v1/sandboxes/{ref}), which takes the whole policy, so the groups
 * are turned into hosts here: the built-in ones if kept, the groups' hosts,
 * those typed, and — on an agent's sandbox — that agent's API, without which
 * the agent stops. sandboxd checks it all against its ceiling and may_allow,
 * and the backend applies it to the running VM; one that cannot says so.
 */
export function EditNetwork({ sb, open, onOpenChange }: { sb: Sandbox; open: boolean; onOpenChange: (o: boolean) => void }) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-lg">{open ? <Form sb={sb} onDone={() => onOpenChange(false)} /> : null}</DialogContent>
    </Dialog>
  );
}

function Form({ sb, onDone }: { sb: Sandbox; onDone: () => void }) {
  const { data: info } = useInfo();
  const { data: egress } = useEgress();
  const { data: agents } = useAgents();
  const update = useUpdateNetwork();
  const caps = info?.capabilities;
  const ceiling = caps?.network.ceiling;
  const baseline = caps?.network.default.mode === "allowlist" ? (caps.network.default.allow ?? []) : (info?.baseline_egress ?? []);
  const agentHost = agents?.find((a) => a.name === sb.labels?.agent)?.provider_host;
  const groups = egress?.groups ?? [];

  // Start from what the sandbox has: the built-in hosts kept if it has them
  // all, and every other host it reaches as typed hosts, so saving without a
  // change changes nothing.
  const current = sb.network.allow ?? [];
  const hadBaseline = baseline.length > 0 && baseline.every((h) => current.includes(h));
  const [mode, setMode] = useState<NetworkMode>(sb.network.mode);
  const [keepBaseline, setKeepBaseline] = useState(sb.network.mode !== "allowlist" || hadBaseline);
  const [picked, setPicked] = useState<string[]>([]);
  const [extra, setExtra] = useState(current.filter((h) => !(hadBaseline && baseline.includes(h)) && h !== agentHost).join(", "));
  const [deny, setDeny] = useState((sb.network.deny ?? []).join(", "));

  const groupHosts = groups.filter((g) => picked.includes(g.name)).flatMap((g) => g.hosts);
  const typed = hostsOf(extra);
  const allow = [...new Set([...(keepBaseline ? baseline : []), ...groupHosts, ...typed, ...(agentHost ? [agentHost] : [])])];
  const denied = hostsOf(deny);
  const bad = [...typed, ...denied].find((h) => !HOST.test(h));
  const studioDeny = (egress?.rules ?? []).filter((r) => r.enabled && !denied.includes(r.host)).map((r) => r.host);
  const canUpdate = !!caps?.capabilities?.network_policy_update;
  const empty = mode === "allowlist" && allow.length === 0;

  function submit(e: React.FormEvent) {
    e.preventDefault();
    if (bad || empty) return;
    const network: NetworkPolicy = { mode };
    if (mode === "allowlist") network.allow = allow;
    if (mode !== "none" && denied.length) network.deny = denied;
    update.mutate(
      { id: sb.id, network },
      {
        onSuccess: () => {
          toast.success(`Network of ${sb.name || sb.id} is now ${mode}`);
          onDone();
        },
        onError: (err) => toast.error(err.message),
      },
    );
  }

  return (
    <form className="flex flex-col gap-4" onSubmit={submit}>
      <DialogHeader>
        <DialogTitle>Change network</DialogTitle>
        <DialogDescription>
          Applies to <span className="font-mono">{sb.name || sb.id}</span> now, while it runs. Connections it already has to a host no longer allowed are cut.
        </DialogDescription>
      </DialogHeader>
      {!canUpdate ? (
        <p className="rounded-md border border-caution/30 bg-caution/10 px-3 py-2 text-xs text-caution">
          This endpoint cannot change a running sandbox&apos;s network ({caps?.backend ?? "its backend"}). Start a new sandbox with the network it needs.
        </p>
      ) : null}

      <RadioGroup value={mode} onValueChange={(v) => setMode(v as NetworkMode)} className="grid grid-cols-3 gap-2" aria-label="Network">
        {(["none", "allowlist", "open"] as NetworkMode[]).map((m) => {
          const off = m === "open" && ceiling !== "open";
          return (
            <Label
              key={m}
              htmlFor={`edit-net-${m}`}
              className={cn(
                "flex cursor-pointer items-center justify-between rounded-md border px-3 py-2 text-sm font-normal has-[[data-state=checked]]:border-primary has-[[data-state=checked]]:bg-primary/5",
                off && "cursor-not-allowed opacity-50",
              )}
              title={off ? `This endpoint's ceiling is ${ceiling}` : undefined}
            >
              {m}
              <RadioGroupItem id={`edit-net-${m}`} value={m} disabled={off} />
            </Label>
          );
        })}
      </RadioGroup>

      {mode === "allowlist" ? (
        <div className="flex flex-col gap-3">
          {groups.length ? (
            <div className="flex flex-col gap-1.5">
              <span className="text-xs text-muted-foreground">Add groups</span>
              <div className="flex flex-wrap gap-1.5" role="group" aria-label="Allowlist groups">
                {groups.map((g) => {
                  const on = picked.includes(g.name);
                  return (
                    <button
                      key={g.name}
                      type="button"
                      role="checkbox"
                      aria-checked={on}
                      aria-label={`Group ${g.name}`}
                      title={g.hosts.join("\n")}
                      onClick={() => setPicked(on ? picked.filter((n) => n !== g.name) : [...picked, g.name])}
                      className={cn("flex items-center gap-1.5 rounded-md border px-2 py-1 font-mono text-xs", on ? "border-primary bg-primary/10" : "hover:border-foreground/30")}
                    >
                      {on ? <Check className="size-3" /> : null}
                      {g.name}
                      <span className="text-[10px] text-muted-foreground">{g.hosts.length}</span>
                    </button>
                  );
                })}
              </div>
            </div>
          ) : (
            <p className="text-xs text-muted-foreground">
              No allowlist groups.{" "}
              <Link href="/settings" className="underline underline-offset-2">
                Make some in Settings
              </Link>
              .
            </p>
          )}
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="edit-net-hosts" className="text-xs font-normal text-muted-foreground">
              Hosts
            </Label>
            <Textarea id="edit-net-hosts" rows={3} className="font-mono text-xs" placeholder="example.com, *.example.org" value={extra} onChange={(e) => setExtra(e.target.value)} />
          </div>
          <Label className="flex items-center gap-2 text-xs font-normal">
            <Checkbox checked={keepBaseline} onCheckedChange={(v) => setKeepBaseline(!!v)} aria-label="Include the built-in hosts" />
            Include the built-in hosts <span className="text-muted-foreground">({baseline.length})</span>
          </Label>
          {agentHost ? (
            <p className="text-[11px] text-muted-foreground">
              <span className="font-mono">{agentHost}</span> stays: this sandbox runs {sb.labels?.agent}, which needs its API.
            </p>
          ) : null}
          <p className="font-mono text-[11px] leading-relaxed break-all text-muted-foreground">
            {allow.length} hosts: {allow.join(" · ") || "none"}
          </p>
        </div>
      ) : null}

      {mode !== "none" ? (
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="edit-net-deny" className="text-xs font-normal text-muted-foreground">
            Denied
          </Label>
          <Textarea id="edit-net-deny" rows={2} className="font-mono text-xs" placeholder="hosts never reached" value={deny} onChange={(e) => setDeny(e.target.value)} />
          {studioDeny.length ? (
            <button
              type="button"
              className="w-fit text-[11px] text-muted-foreground underline underline-offset-2 hover:text-foreground"
              onClick={() => setDeny([...denied, ...studioDeny].join(", "))}
            >
              Add Studio&apos;s {studioDeny.length} deny rule{studioDeny.length === 1 ? "" : "s"}
            </button>
          ) : null}
        </div>
      ) : null}

      {bad ? <p className="text-xs text-destructive">{bad}: a host name, or *.name; no scheme, port or path.</p> : null}
      {empty ? <p className="text-xs text-caution">An allowlist of nothing is refused; choose none to reach nothing.</p> : null}

      <DialogFooter>
        <Button type="button" variant="outline" onClick={onDone}>
          Cancel
        </Button>
        <Button type="submit" disabled={!canUpdate || !!bad || empty || update.isPending}>
          {update.isPending ? "Applying…" : "Apply"}
        </Button>
      </DialogFooter>
    </form>
  );
}
