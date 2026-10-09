"use client";

import { useEffect, useState } from "react";
import { toast } from "sonner";
import { Ban, Check, Globe, Plus, Trash2 } from "lucide-react";
import { SectionHeader } from "@/components/common/page-header";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { useEgressRules, useSaveEgressRules } from "@/lib/api/queries";
import { cn } from "@/lib/utils";
import type { EgressRule, NetworkMode } from "@/lib/types";

/** A host as sandboxd's allow and deny lists take it: a name, or *.name. */
const HOST = /^(\*\.)?([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)*[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$/;

export function normalizeHost(h: string): string {
  return h.trim().toLowerCase().replace(/\.$/, "");
}

/**
 * The hosts the enabled rules add to a launch, as Studio's server adds them
 * (internal/studio/runs.go, applyEgressRules): a deny into every run with a
 * network, an allow only into a run that is an allowlist. The Playground
 * shows the result, so its code says what the launch will do.
 */
export function applyRules(
  rules: EgressRule[] | undefined,
  network: "" | NetworkMode,
  allow: string[],
  defaultMode?: NetworkMode,
): { allow: string[]; deny: string[] } {
  const on = (rules ?? []).filter((r) => r.enabled);
  const deny = network === "none" ? [] : on.filter((r) => r.action === "deny").map((r) => r.host);
  const allowlist = network === "allowlist" || (network === "" && (allow.length > 0 || defaultMode === "allowlist"));
  const extra = allowlist ? on.filter((r) => r.action === "allow").map((r) => r.host) : [];
  return { allow: [...new Set([...allow, ...extra])], deny };
}

/**
 * Egress rules: hosts every Studio launch allows or denies, kept by Studio's
 * server. Edited as a list and saved whole.
 */
export function EgressRules({ defaultMode, ceiling }: { defaultMode?: NetworkMode; ceiling?: NetworkMode }) {
  const { data } = useEgressRules();
  const save = useSaveEgressRules();
  const [rules, setRules] = useState<EgressRule[]>([]);
  const [host, setHost] = useState("");
  const [action, setAction] = useState<"allow" | "deny">("allow");
  const [note, setNote] = useState("");
  useEffect(() => {
    if (data) setRules(data);
  }, [data]);

  const h = normalizeHost(host);
  const invalid = h !== "" && !HOST.test(h);
  const duplicate = h !== "" && rules.some((r) => r.host === h);

  function persist(next: EgressRule[]) {
    setRules(next);
    save.mutate(next, {
      onSuccess: () => toast.success("Egress rules saved"),
      onError: (e) => {
        toast.error(e.message);
        if (data) setRules(data);
      },
    });
  }

  function add(e: React.FormEvent) {
    e.preventDefault();
    if (!h || invalid || duplicate) return;
    persist([...rules, { host: h, action, enabled: true, note: note.trim() || undefined }]);
    setHost("");
    setNote("");
  }

  return (
    <section className="flex flex-col gap-3">
      <SectionHeader
        title="Egress rules"
        description="Hosts every launch from Studio allows or denies, on top of what the launch asks for. sandboxd's policy still decides: a host it does not let a request add is refused at launch."
      />
      <ul className="max-w-3xl text-xs text-muted-foreground">
        <li>
          <span className="font-medium text-foreground">Allow</span> widens a run that is an allowlist
          {defaultMode === "allowlist" ? " — this endpoint's default is one" : ` — this endpoint's default is ${defaultMode ?? "unknown"}, so only runs that ask for an allowlist`}. An open run reaches the host already.
        </li>
        <li>
          <span className="font-medium text-foreground">Deny</span> is refused to every run with a network, open ones included
          {ceiling ? ` (ceiling ${ceiling})` : ""}.
        </li>
      </ul>

      <div className="max-w-3xl overflow-hidden rounded-lg border bg-card">
        <form onSubmit={add} className="flex flex-wrap items-start gap-2 border-b bg-muted/30 p-3">
          <div className="flex min-w-48 flex-1 flex-col gap-1">
            <Input
              aria-label="Host"
              className="h-8 font-mono text-xs"
              placeholder="proxy.golang.org or *.example.com"
              value={host}
              onChange={(e) => setHost(e.target.value)}
              aria-invalid={invalid || duplicate}
            />
            {invalid ? (
              <span className="text-[11px] text-destructive">A host name, or *.name: no scheme, port or path.</span>
            ) : duplicate ? (
              <span className="text-[11px] text-destructive">There is a rule for {h} already.</span>
            ) : null}
          </div>
          <Select value={action} onValueChange={(v) => setAction(v as "allow" | "deny")}>
            <SelectTrigger size="sm" className="w-24" aria-label="Action">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="allow">allow</SelectItem>
              <SelectItem value="deny">deny</SelectItem>
            </SelectContent>
          </Select>
          <Input aria-label="Note" className="h-8 w-44 text-xs" placeholder="note (optional)" maxLength={200} value={note} onChange={(e) => setNote(e.target.value)} />
          <Button type="submit" size="sm" className="h-8 gap-1" disabled={!h || invalid || duplicate || save.isPending}>
            <Plus className="size-3.5" /> Add rule
          </Button>
        </form>
        {rules.length === 0 ? (
          <p className="flex items-center gap-2 px-4 py-6 text-sm text-muted-foreground">
            <Globe className="size-4" aria-hidden /> No rules. Launches get what they ask for, under the server&apos;s policy.
          </p>
        ) : (
          <ul className="divide-y" aria-label="Egress rules">
            {rules.map((r, i) => (
              <li key={r.host} className={cn("grid grid-cols-[auto_5rem_minmax(0,1fr)_auto] items-center gap-3 px-3 py-2", !r.enabled && "opacity-60")}>
                <Switch
                  size="sm"
                  checked={r.enabled}
                  aria-label={`${r.enabled ? "Disable" : "Enable"} ${r.host}`}
                  onCheckedChange={(v) => persist(rules.map((x, j) => (j === i ? { ...x, enabled: v } : x)))}
                />
                <span
                  className={cn(
                    "inline-flex w-fit items-center gap-1 rounded-full border px-2 py-0.5 text-[11px]",
                    r.action === "allow" ? "border-status-good/30 bg-status-good/10 text-status-good" : "border-destructive/30 bg-destructive/10 text-destructive",
                  )}
                >
                  {r.action === "allow" ? <Check className="size-3" /> : <Ban className="size-3" />}
                  {r.action}
                </span>
                <span className="min-w-0">
                  <span className="block truncate font-mono text-xs">{r.host}</span>
                  {r.note ? <span className="block truncate text-[11px] text-muted-foreground">{r.note}</span> : null}
                </span>
                <Button
                  size="icon"
                  variant="ghost"
                  className="size-7 text-muted-foreground hover:text-destructive"
                  aria-label={`Remove ${r.host}`}
                  disabled={save.isPending}
                  onClick={() => persist(rules.filter((_, j) => j !== i))}
                >
                  <Trash2 className="size-3.5" />
                </Button>
              </li>
            ))}
          </ul>
        )}
      </div>
      {save.isPending ? <p className="text-xs text-muted-foreground">Saving…</p> : null}
      <p className="max-w-3xl text-xs text-muted-foreground">
        Kept in ~/.config/sandbox/studio.json. For rules that apply to every sandbox-cli run too, use network.allow in ~/.config/sandbox/config.yaml.
      </p>
    </section>
  );
}
