"use client";

import { useEffect, useState } from "react";
import { toast } from "sonner";
import { Ban, FolderPlus, Globe, Layers, Trash2 } from "lucide-react";
import { SectionHeader } from "@/components/common/page-header";
import { GroupCard, hostProblem, normalizeHost, type GroupStore } from "@/components/settings/group-card";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { useDeleteEgressGroup, useEgress, useSaveEgressGroup, useSaveEgressRules } from "@/lib/api/queries";
import { cn } from "@/lib/utils";
import type { EgressRule, EgressSettings, NetworkMode } from "@/lib/types";

export { normalizeHost };

/** Studio's own groups, in studio.json: any name of up to 32, a default flag, made empty and filled in place. */
const LOCAL_GROUPS: GroupStore = {
  useSave: useSaveEgressGroup,
  useRemove: useDeleteEgressGroup,
  name: { pattern: /^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$/, hint: "Lowercase letters, digits and dashes, up to 32." },
  withDefault: true,
  hostsOnCreate: false,
  createLabel: "Create group",
};

/**
 * What a launch's network comes to once Studio's server has added the
 * allowlist groups and deny rules (internal/studio/runs.go,
 * applyEgressRules): picked groups make it an allowlist of their hosts; with
 * none picked, the default groups go into a run that is an allowlist anyway;
 * a deny goes into every run with a network. The Playground shows the
 * result, so its code says what the launch will do.
 */
export function computeEgress(
  egress: EgressSettings | undefined,
  opts: { network: "" | NetworkMode; picked?: string[]; allow: string[]; defaultMode?: NetworkMode },
): { network: "" | NetworkMode; allow: string[]; deny: string[]; groupHosts: string[] } {
  const groups = egress?.groups ?? [];
  const network = opts.picked?.length ? "allowlist" : opts.network;
  const deny = network === "none" ? [] : (egress?.rules ?? []).filter((r) => r.enabled).map((r) => r.host);
  const allowlist = network === "allowlist" || (network === "" && (opts.allow.length > 0 || opts.defaultMode === "allowlist"));
  const from = opts.picked === undefined ? groups.filter((g) => g.default) : groups.filter((g) => opts.picked!.includes(g.name));
  const groupHosts = allowlist ? [...new Set(from.flatMap((g) => g.hosts))] : [];
  return { network, allow: [...new Set([...opts.allow, ...groupHosts])], deny, groupHosts };
}

/**
 * Settings' egress section: allowlist groups — named sets of hosts a launch
 * picks from — and deny rules, refused to every launch. Both kept by Studio's
 * server, in ~/.config/sandbox/studio.json.
 */
export function EgressRules({ defaultMode, ceiling }: { defaultMode?: NetworkMode; ceiling?: NetworkMode }) {
  const { data } = useEgress();
  const [adding, setAdding] = useState(false);
  const groups = data?.groups ?? [];
  return (
    <>
      <section className="flex flex-col gap-3">
        <SectionHeader
          title="Allowlist groups"
          description="Named sets of hosts. A launch on an allowlist picks the groups it needs; those marked default are picked for it when it names none."
          actions={
            <Button size="sm" variant="outline" className="gap-1.5" onClick={() => setAdding(true)} disabled={adding}>
              <FolderPlus className="size-4" /> New group
            </Button>
          }
        />
        <p className="max-w-3xl text-xs text-muted-foreground">
          An allowlist also has the built-in hosts — agents&apos; APIs and package registries — unless a launch leaves them out, and an agent run always reaches its own API.
          {defaultMode === "allowlist"
            ? " This endpoint's default is an allowlist, so a launch that asks for nothing gets the default groups."
            : ` This endpoint's default is ${defaultMode ?? "unknown"}: groups apply to launches that ask for an allowlist.`}{" "}
          sandboxd&apos;s policy still decides; a host it does not let a request add is refused at launch.
        </p>
        <div className="grid max-w-5xl gap-3 md:grid-cols-2">
          {adding ? <GroupCard store={LOCAL_GROUPS} taken={groups.map((g) => g.name)} onDone={() => setAdding(false)} /> : null}
          {groups.map((g) => (
            <GroupCard key={g.name} store={LOCAL_GROUPS} group={g} taken={groups.map((x) => x.name)} />
          ))}
          {!adding && groups.length === 0 ? (
            <p className="flex items-center gap-2 rounded-lg border border-dashed px-4 py-6 text-sm text-muted-foreground md:col-span-2">
              <Layers className="size-4" aria-hidden /> No groups yet. Make one per purpose — go, npm, an internal registry — and pick them in the Playground.
            </p>
          ) : null}
        </div>
      </section>
      <DenyRules rules={data?.rules} ceiling={ceiling} />
    </>
  );
}

/** Hosts no launch reaches, each with a switch; edited as a list and saved whole. */
function DenyRules({ rules: saved, ceiling }: { rules?: EgressRule[]; ceiling?: NetworkMode }) {
  const save = useSaveEgressRules();
  const [rules, setRules] = useState<EgressRule[]>([]);
  const [host, setHost] = useState("");
  const [note, setNote] = useState("");
  useEffect(() => {
    if (saved) setRules(saved);
  }, [saved]);
  const h = normalizeHost(host);
  const problem = hostProblem(
    h,
    rules.map((r) => r.host),
  );

  function persist(next: EgressRule[]) {
    setRules(next);
    save.mutate(next, {
      onSuccess: () => toast.success("Deny rules saved"),
      onError: (e) => {
        toast.error(e.message);
        if (saved) setRules(saved);
      },
    });
  }

  return (
    <section className="flex flex-col gap-3">
      <SectionHeader
        title="Deny rules"
        description={`Hosts no launch from Studio reaches, whatever its network — an allowlist or open${ceiling ? ` (this endpoint's ceiling is ${ceiling})` : ""}.`}
      />
      <div className="max-w-3xl overflow-hidden rounded-lg border bg-card">
        <form
          onSubmit={(e) => {
            e.preventDefault();
            if (!h || problem) return;
            persist([...rules, { host: h, action: "deny", enabled: true, note: note.trim() || undefined }]);
            setHost("");
            setNote("");
          }}
          className="flex flex-wrap items-start gap-2 border-b bg-muted/30 p-3"
        >
          <div className="flex min-w-48 flex-1 flex-col gap-1">
            <Input aria-label="Host to deny" className="h-8 font-mono text-xs" placeholder="pastebin.com or *.example.com" value={host} onChange={(e) => setHost(e.target.value)} aria-invalid={!!problem} />
            {problem ? <span className="text-[11px] text-destructive">{problem}</span> : null}
          </div>
          <Input aria-label="Note" className="h-8 w-44 text-xs" placeholder="note (optional)" maxLength={200} value={note} onChange={(e) => setNote(e.target.value)} />
          <Button type="submit" size="sm" className="h-8 gap-1" disabled={!h || !!problem || save.isPending}>
            <Ban className="size-3.5" /> Deny
          </Button>
        </form>
        {rules.length === 0 ? (
          <p className="flex items-center gap-2 px-4 py-6 text-sm text-muted-foreground">
            <Globe className="size-4" aria-hidden /> No deny rules.
          </p>
        ) : (
          <ul className="divide-y" aria-label="Deny rules">
            {rules.map((r, i) => (
              <li key={r.host} className={cn("grid grid-cols-[auto_minmax(0,1fr)_auto] items-center gap-3 px-3 py-2", !r.enabled && "opacity-60")}>
                <Switch
                  size="sm"
                  checked={r.enabled}
                  aria-label={`${r.enabled ? "Disable" : "Enable"} ${r.host}`}
                  onCheckedChange={(v) => persist(rules.map((x, j) => (j === i ? { ...x, enabled: v } : x)))}
                />
                <span className="min-w-0">
                  <span className="flex items-center gap-1.5 truncate font-mono text-xs">
                    <Ban className="size-3 shrink-0 text-destructive" aria-hidden />
                    {r.host}
                  </span>
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
      <p className="max-w-3xl text-xs text-muted-foreground">
        Groups and rules are kept in ~/.config/sandbox/studio.json. For hosts every sandbox-cli run gets, use network.allow in ~/.config/sandbox/config.yaml.
      </p>
    </section>
  );
}
