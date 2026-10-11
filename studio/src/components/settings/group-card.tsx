"use client";

import { useState } from "react";
import { toast } from "sonner";
import { Layers, Pencil, Plus, Trash2, X } from "lucide-react";
import type { UseMutationResult } from "@tanstack/react-query";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { cn } from "@/lib/utils";
import type { EgressGroup } from "@/lib/types";

/** A host as sandboxd's allow and deny lists take it: a name, or *.name. */
export const HOST = /^(\*\.)?([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)*[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$/;

export function normalizeHost(h: string): string {
  return h.trim().toLowerCase().replace(/\.$/, "");
}

/** Hosts as they are pasted: separated by commas or spaces. */
function hostsOf(text: string): string[] {
  return text.split(/[\s,]+/).map(normalizeHost).filter(Boolean);
}

/** What is wrong with a host, said for the field under it; "" when nothing is. */
export function hostProblem(h: string, taken: string[]): string {
  if (!h) return "";
  if (!HOST.test(h)) return "A host name, or *.name: no scheme, port or path.";
  if (taken.includes(h)) return `${h} is listed already.`;
  return "";
}

/**
 * Where a group is kept, and what that store takes. Local Studio keeps its
 * groups in studio.json, with a default flag; a gateway keeps the admin's, with
 * none (an admin's edit must not widen a launch that did not ask), a stricter
 * name and at least one host — so a new one is made with its hosts.
 */
export interface GroupStore {
  useSave: () => UseMutationResult<unknown, Error, EgressGroup>;
  useRemove: () => UseMutationResult<unknown, Error, string>;
  name: { pattern: RegExp; hint: string };
  /** Shows the default switch, and keeps the flag on a save. */
  withDefault: boolean;
  /** A new group is made with its first hosts: the store refuses an empty one. */
  hostsOnCreate: boolean;
  /** The new-group form's submit button. */
  createLabel: string;
}

/** One group: its hosts as chips, added and removed in place; name and description edited behind the pencil. */
export function GroupCard({ group, taken, onDone, store }: { group?: EgressGroup; taken: string[]; onDone?: () => void; store: GroupStore }) {
  const isNew = !group;
  const save = store.useSave();
  const remove = store.useRemove();
  const [editing, setEditing] = useState(isNew);
  const [name, setName] = useState(group?.name ?? "");
  const [description, setDescription] = useState(group?.description ?? "");
  const [host, setHost] = useState("");
  const [firstHosts, setFirstHosts] = useState("");
  const hosts = group?.hosts ?? [];
  const h = normalizeHost(host);
  const problem = hostProblem(h, hosts);
  const nameTaken = isNew && taken.includes(name);
  const first = hostsOf(firstHosts);
  const badFirst = first.find((x) => !HOST.test(x));
  const needFirst = isNew && store.hostsOnCreate;

  function persist(g: EgressGroup, ok?: string) {
    const out: EgressGroup = store.withDefault ? g : { name: g.name, description: g.description, hosts: g.hosts };
    save.mutate(out, {
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
              if (needFirst && (!first.length || badFirst)) return;
              persist({ name, description, hosts: needFirst ? [...new Set(first)] : hosts, default: group?.default }, isNew ? `Made ${name}` : "Saved");
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
            {name && !store.name.pattern.test(name) ? <span className="text-[11px] text-destructive">{store.name.hint}</span> : null}
            {nameTaken ? <span className="text-[11px] text-destructive">There is a group {name} already.</span> : null}
            {needFirst ? (
              <>
                <Input
                  aria-label="Group hosts"
                  className="h-8 font-mono text-xs"
                  placeholder="proxy.golang.org, *.example.com"
                  value={firstHosts}
                  onChange={(e) => setFirstHosts(e.target.value)}
                />
                {badFirst ? <span className="text-[11px] text-destructive">{badFirst}: a host name, or *.name; no scheme, port or path.</span> : null}
              </>
            ) : null}
            <div className="flex gap-1.5">
              <Button
                type="submit"
                size="sm"
                className="h-7"
                disabled={save.isPending || !store.name.pattern.test(name) || nameTaken || (needFirst && (!first.length || !!badFirst))}
              >
                {isNew ? store.createLabel : "Save"}
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
            {store.withDefault ? (
              <Label className="flex shrink-0 items-center gap-1.5 text-[11px] font-normal text-muted-foreground" title="Picked for an allowlist launch that names no groups">
                <Switch
                  size="sm"
                  checked={!!group!.default}
                  aria-label={`${group!.name} by default`}
                  onCheckedChange={(v) => persist({ ...group!, default: v }, v ? `${group!.name} is picked by default` : `${group!.name} is no longer a default`)}
                />
                default
              </Label>
            ) : null}
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
              const add = hostsOf(host);
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
