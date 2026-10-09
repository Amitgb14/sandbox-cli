"use client";

import { useEffect, useState } from "react";
import { toast } from "sonner";
import { Ban, FolderPlus, Globe, Layers, Pencil, Plus, Trash2, X } from "lucide-react";
import { SectionHeader } from "@/components/common/page-header";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { useDeleteEgressGroup, useEgress, useSaveEgressGroup, useSaveEgressRules } from "@/lib/api/queries";
import { cn } from "@/lib/utils";
import type { EgressGroup, EgressRule, EgressSettings, NetworkMode } from "@/lib/types";

/** A host as sandboxd's allow and deny lists take it: a name, or *.name. */
const HOST = /^(\*\.)?([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)*[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$/;
const NAME = /^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$/;

export function normalizeHost(h: string): string {
  return h.trim().toLowerCase().replace(/\.$/, "");
}

/** What is wrong with a host, said for the field under it; "" when nothing is. */
function hostProblem(h: string, taken: string[]): string {
  if (!h) return "";
  if (!HOST.test(h)) return "A host name, or *.name: no scheme, port or path.";
  if (taken.includes(h)) return `${h} is listed already.`;
  return "";
}

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
          {adding ? <GroupCard taken={groups.map((g) => g.name)} onDone={() => setAdding(false)} /> : null}
          {groups.map((g) => (
            <GroupCard key={g.name} group={g} taken={groups.map((x) => x.name)} />
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

/** One group: its hosts as chips, added and removed in place; name and description edited behind the pencil. */
function GroupCard({ group, taken, onDone }: { group?: EgressGroup; taken: string[]; onDone?: () => void }) {
  const isNew = !group;
  const save = useSaveEgressGroup();
  const remove = useDeleteEgressGroup();
  const [editing, setEditing] = useState(isNew);
  const [name, setName] = useState(group?.name ?? "");
  const [description, setDescription] = useState(group?.description ?? "");
  const [host, setHost] = useState("");
  const hosts = group?.hosts ?? [];
  const h = normalizeHost(host);
  const problem = hostProblem(h, hosts);
  const nameTaken = isNew && taken.includes(name);

  function persist(g: EgressGroup, ok?: string) {
    save.mutate(g, {
      onSuccess: () => {
        if (ok) toast.success(ok);
        setEditing(false);
        onDone?.();
      },
      onError: (e) => toast.error(e.message),
    });
  }

  return (
    <div className={cn("flex flex-col overflow-hidden rounded-lg border bg-card", group?.default && "border-primary/40")} aria-label={group ? `Group ${group.name}` : "New group"} role="group">
      <div className="flex items-start gap-3 border-b bg-muted/30 px-3.5 py-2.5">
        <span className="mt-0.5 flex size-7 shrink-0 items-center justify-center rounded-md bg-primary/10 text-primary">
          <Layers className="size-3.5" aria-hidden />
        </span>
        {editing ? (
          <form
            className="flex min-w-0 flex-1 flex-col gap-2"
            onSubmit={(e) => {
              e.preventDefault();
              persist({ name, description, hosts, default: group?.default }, isNew ? `Made ${name}` : "Saved");
            }}
          >
            <div className="flex gap-2">
              <Input
                autoFocus={isNew}
                aria-label="Group name"
                className="h-8 w-40 font-mono text-xs"
                placeholder="go-modules"
                value={name}
                onChange={(e) => setName(e.target.value.toLowerCase())}
                disabled={!isNew}
                required
              />
              <Input aria-label="Group description" className="h-8 min-w-0 flex-1 text-xs" placeholder="what it is for (optional)" maxLength={200} value={description} onChange={(e) => setDescription(e.target.value)} />
            </div>
            {name && !NAME.test(name) ? <span className="text-[11px] text-destructive">Lowercase letters, digits and dashes, up to 32.</span> : null}
            {nameTaken ? <span className="text-[11px] text-destructive">There is a group {name} already.</span> : null}
            <div className="flex gap-1.5">
              <Button type="submit" size="sm" className="h-7" disabled={save.isPending || !NAME.test(name) || nameTaken}>
                {isNew ? "Create group" : "Save"}
              </Button>
              <Button
                type="button"
                size="sm"
                variant="ghost"
                className="h-7"
                onClick={() => {
                  setEditing(false);
                  setName(group?.name ?? "");
                  setDescription(group?.description ?? "");
                  onDone?.();
                }}
              >
                Cancel
              </Button>
            </div>
          </form>
        ) : (
          <>
            <div className="min-w-0 flex-1">
              <p className="flex items-center gap-2 font-mono text-sm font-medium">
                <span className="truncate">{group!.name}</span>
                <span className="text-[11px] font-normal text-muted-foreground">
                  {hosts.length} host{hosts.length === 1 ? "" : "s"}
                </span>
              </p>
              <p className="truncate text-[11px] text-muted-foreground">{group!.description || "—"}</p>
            </div>
            <Label className="flex shrink-0 items-center gap-1.5 text-[11px] font-normal text-muted-foreground" title="Picked for an allowlist launch that names no groups">
              <Switch
                size="sm"
                checked={!!group!.default}
                aria-label={`${group!.name} by default`}
                onCheckedChange={(v) => persist({ ...group!, default: v }, v ? `${group!.name} is picked by default` : `${group!.name} is no longer a default`)}
              />
              default
            </Label>
            <Button size="icon" variant="ghost" className="size-7" aria-label={`Edit ${group!.name}`} title="Edit the description" onClick={() => setEditing(true)}>
              <Pencil className="size-3.5" />
            </Button>
            <Button
              size="icon"
              variant="ghost"
              className="size-7 text-muted-foreground hover:text-destructive"
              aria-label={`Delete group ${group!.name}`}
              disabled={remove.isPending}
              onClick={() =>
                confirm(`Delete the group ${group!.name} and its ${hosts.length} hosts?`) &&
                remove.mutate(group!.name, { onSuccess: () => toast.success(`Deleted ${group!.name}`), onError: (e) => toast.error(e.message) })
              }
            >
              <Trash2 className="size-3.5" />
            </Button>
          </>
        )}
      </div>
      {!isNew ? (
        <div className="flex flex-col gap-2.5 p-3">
          {hosts.length ? (
            <ul className="flex flex-wrap gap-1.5" aria-label={`${group!.name} hosts`}>
              {hosts.map((x) => (
                <li key={x} className="inline-flex items-center gap-1 rounded-md border bg-background py-0.5 pr-0.5 pl-2 font-mono text-[11px]">
                  {x}
                  <button
                    type="button"
                    aria-label={`Remove ${x} from ${group!.name}`}
                    className="rounded p-0.5 text-muted-foreground hover:bg-muted hover:text-destructive"
                    onClick={() => persist({ ...group!, hosts: hosts.filter((y) => y !== x) })}
                  >
                    <X className="size-3" />
                  </button>
                </li>
              ))}
            </ul>
          ) : (
            <p className="text-xs text-muted-foreground">No hosts yet.</p>
          )}
          <form
            className="flex flex-col gap-1"
            onSubmit={(e) => {
              e.preventDefault();
              // Several at once, separated by commas or spaces, as they are pasted.
              const add = host.split(/[\s,]+/).map(normalizeHost).filter(Boolean);
              const bad = add.find((x) => !HOST.test(x));
              if (!add.length || bad) return;
              persist({ ...group!, hosts: [...new Set([...hosts, ...add])] });
              setHost("");
            }}
          >
            <div className="flex gap-1.5">
              <Input
                aria-label={`Add a host to ${group!.name}`}
                className="h-8 min-w-0 flex-1 font-mono text-xs"
                placeholder="proxy.golang.org, *.example.com"
                value={host}
                onChange={(e) => setHost(e.target.value)}
              />
              <Button type="submit" size="sm" variant="outline" className="h-8 gap-1" disabled={!h || (!host.includes(",") && !!problem) || save.isPending}>
                <Plus className="size-3.5" /> Add
              </Button>
            </div>
            {!host.includes(",") && problem ? <span className="text-[11px] text-destructive">{problem}</span> : null}
          </form>
        </div>
      ) : null}
    </div>
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
