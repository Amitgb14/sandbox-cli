"use client";

import { useState } from "react";
import { toast } from "sonner";
import { Lock, Plus, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { useInfo, useUpdateSandbox } from "@/lib/api/queries";
import type { Sandbox } from "@/lib/types";

const NAME = /^[a-z0-9][a-z0-9-]{0,62}$/;
const KEY = /^[a-z0-9][a-z0-9._/-]{0,62}$/;

/** Idle timeouts offered, in seconds; the server's limit trims the list. */
const IDLE = [300, 900, 1800, 3600, 7200, 14400, 28800, 86400, 259200, 604800];

function idleLabel(secs: number): string {
  if (secs === 0) return "never";
  if (secs % 86400 === 0) return `${secs / 86400} day${secs === 86400 ? "" : "s"}`;
  if (secs % 3600 === 0) return `${secs / 3600} h`;
  return `${secs / 60} min`;
}

/**
 * Renames, relabels or retimes a live sandbox: this server's records of it,
 * changed with nothing asked of its VM (PATCH /v1/sandboxes/{ref}). Only
 * what changed is sent. Labels a gateway keeps for itself (gateway.*) are
 * shown and sent back as they are, which it accepts; it refuses any change.
 */
export function EditDetails({ sb, open, onOpenChange }: { sb: Sandbox; open: boolean; onOpenChange: (o: boolean) => void }) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-lg">{open ? <Form sb={sb} onDone={() => onOpenChange(false)} /> : null}</DialogContent>
    </Dialog>
  );
}

function Form({ sb, onDone }: { sb: Sandbox; onDone: () => void }) {
  const { data: info } = useInfo();
  const update = useUpdateSandbox();
  const max = info?.capabilities?.limits.max_idle_timeout_secs ?? 0;
  const all = Object.entries(sb.labels ?? {});
  const fixed = all.filter(([k]) => k.startsWith("gateway."));
  const [name, setName] = useState(sb.name ?? "");
  const [rows, setRows] = useState<[string, string][]>(all.filter(([k]) => !k.startsWith("gateway.")));
  const [idle, setIdle] = useState(sb.idle_timeout_secs);

  const choices = [...new Set([...IDLE.filter((s) => !max || s <= max), ...(max ? [] : [0]), sb.idle_timeout_secs])].sort((a, b) => (a || Infinity) - (b || Infinity));
  const keys = rows.map(([k]) => k.trim());
  const badKey = keys.find((k) => k && !KEY.test(k));
  const dupKey = keys.find((k, i) => k && keys.indexOf(k) !== i);
  const badName = name !== "" && !NAME.test(name);
  const labels = Object.fromEntries([...fixed, ...rows.map(([k, v]) => [k.trim(), v] as [string, string]).filter(([k]) => k)]);
  const labelsChanged = JSON.stringify(Object.entries(labels).sort()) !== JSON.stringify([...all].sort());
  const changed = name !== (sb.name ?? "") || labelsChanged || idle !== sb.idle_timeout_secs;

  function submit(e: React.FormEvent) {
    e.preventDefault();
    if (badName || badKey || dupKey || !changed) return;
    const req: { id: string; name?: string; labels?: Record<string, string>; idle_timeout_secs?: number } = { id: sb.id };
    if (name !== (sb.name ?? "")) req.name = name;
    if (labelsChanged) req.labels = labels;
    if (idle !== sb.idle_timeout_secs) req.idle_timeout_secs = idle;
    update.mutate(req, {
      onSuccess: () => {
        toast.success(`Saved ${name || sb.id}`);
        onDone();
      },
      onError: (err) => toast.error(err.message),
    });
  }

  return (
    <form className="flex flex-col gap-4" onSubmit={submit}>
      <DialogHeader>
        <DialogTitle>Edit {sb.name || sb.id}</DialogTitle>
        <DialogDescription>Its name, labels and idle timeout. It keeps running; nothing in it changes.</DialogDescription>
      </DialogHeader>

      <div className="flex flex-col gap-1.5">
        <Label htmlFor="edit-name">Name</Label>
        <Input id="edit-name" className="font-mono" placeholder="no name" value={name} onChange={(e) => setName(e.target.value.toLowerCase())} />
        {badName ? (
          <span className="text-[11px] text-destructive">Lowercase letters, digits and dashes, starting with a letter or digit.</span>
        ) : (
          <span className="text-[11px] text-muted-foreground">Unique among your live sandboxes; commands and links by the old name stop finding it.</span>
        )}
      </div>

      <div className="flex flex-col gap-1.5">
        <span className="text-sm font-medium">Labels</span>
        <div className="flex flex-col gap-1.5" role="group" aria-label="Labels">
          {fixed.map(([k, v]) => (
            <div key={k} className="flex items-center gap-1.5 font-mono text-xs text-muted-foreground" title="Kept by the gateway">
              <Lock className="size-3" aria-hidden />
              {k}={v}
            </div>
          ))}
          {rows.map(([k, v], i) => (
            <div key={i} className="flex items-center gap-1.5">
              <Input
                aria-label={`Label ${i + 1} key`}
                className="h-8 w-40 font-mono text-xs"
                placeholder="key"
                value={k}
                onChange={(e) => setRows(rows.map((r, j) => (j === i ? [e.target.value.toLowerCase(), r[1]] : r)))}
              />
              <span className="text-muted-foreground">=</span>
              <Input
                aria-label={`Label ${i + 1} value`}
                className="h-8 min-w-0 flex-1 font-mono text-xs"
                placeholder="value"
                maxLength={256}
                value={v}
                onChange={(e) => setRows(rows.map((r, j) => (j === i ? [r[0], e.target.value] : r)))}
              />
              <Button type="button" size="icon" variant="ghost" className="size-8 text-muted-foreground" aria-label={`Remove label ${k || i + 1}`} onClick={() => setRows(rows.filter((_, j) => j !== i))}>
                <X className="size-3.5" />
              </Button>
            </div>
          ))}
          <Button type="button" size="sm" variant="outline" className="h-7 w-fit gap-1" onClick={() => setRows([...rows, ["", ""]])} disabled={rows.length + fixed.length >= 32}>
            <Plus className="size-3.5" /> Add label
          </Button>
        </div>
        {badKey ? <span className="text-[11px] text-destructive">{badKey}: keys are lowercase letters, digits and . _ / -</span> : null}
        {dupKey ? <span className="text-[11px] text-destructive">{dupKey} is there twice.</span> : null}
      </div>

      <div className="flex flex-col gap-1.5">
        <Label>Idle auto-stop</Label>
        <Select value={String(idle)} onValueChange={(v) => setIdle(Number(v))}>
          <SelectTrigger className="w-48" aria-label="Idle auto-stop">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {choices.map((s) => (
              <SelectItem key={s} value={String(s)}>
                {s === 0 ? "never" : `after ${idleLabel(s)} idle`}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <span className="text-[11px] text-muted-foreground">
          Terminated after this long with nothing running in it and no request to it{max ? `; this endpoint allows up to ${idleLabel(max)}` : ""}.
        </span>
      </div>

      <DialogFooter>
        <Button type="button" variant="outline" onClick={onDone}>
          Cancel
        </Button>
        <Button type="submit" disabled={!changed || badName || !!badKey || !!dupKey || update.isPending}>
          {update.isPending ? "Saving…" : "Save"}
        </Button>
      </DialogFooter>
    </form>
  );
}
