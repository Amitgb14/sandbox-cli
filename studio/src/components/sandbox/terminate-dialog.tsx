"use client";

import { useState } from "react";
import { AlertTriangle, Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { formatMiB } from "@/lib/format";
import type { Sandbox } from "@/lib/types";

/** What a sandbox is called where it is confirmed: its name, else its id. */
export function sandboxLabel(s: Pick<Sandbox, "id" | "name">): string {
  return s.name || s.id;
}

/**
 * Confirms terminating sandboxes by having the name typed, not a click.
 *
 * A terminated sandbox is gone with everything in it — its files, its
 * processes, an agent's unsaved work — and nothing brings it back, while the
 * button that does it sits beside Suspend and Snapshot. A browser confirm()
 * is answered by reflex; typing the name is a moment's reading of which
 * sandbox this is. Several at once are listed, and confirmed with
 * "terminate N", so the count is read too.
 */
export function TerminateDialog({
  sandboxes,
  onClose,
  onConfirm,
  pending,
}: {
  /** null: closed. */
  sandboxes: Sandbox[] | null;
  onClose: () => void;
  onConfirm: (list: Sandbox[]) => void;
  pending?: boolean;
}) {
  return (
    <Dialog open={!!sandboxes?.length} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-md">
        {sandboxes?.length ? (
          // Keyed by the set, so what was typed for one sandbox is never
          // carried over to another.
          <Confirm key={sandboxes.map((s) => s.id).join(",")} list={sandboxes} onClose={onClose} onConfirm={onConfirm} pending={pending} />
        ) : null}
      </DialogContent>
    </Dialog>
  );
}

function Confirm({ list, onClose, onConfirm, pending }: { list: Sandbox[]; onClose: () => void; onConfirm: (l: Sandbox[]) => void; pending?: boolean }) {
  const [typed, setTyped] = useState("");
  const one = list.length === 1;
  const phrase = one ? sandboxLabel(list[0]) : `terminate ${list.length}`;
  const ok = typed.trim() === phrase;
  const shown = list.slice(0, 6);

  return (
    <form
      className="flex flex-col gap-4"
      onSubmit={(e) => {
        e.preventDefault();
        if (ok && !pending) onConfirm(list);
      }}
    >
      <DialogHeader>
        <DialogTitle className="flex items-center gap-2">
          <span className="flex size-7 items-center justify-center rounded-full bg-destructive/10 text-destructive">
            <AlertTriangle className="size-4" aria-hidden />
          </span>
          {one ? "Terminate sandbox" : `Terminate ${list.length} sandboxes`}
        </DialogTitle>
        <DialogDescription>
          {one ? "It is deleted" : "They are deleted"} with everything in {one ? "it" : "them"} — files, running processes, an agent&apos;s
          work not yet pushed. This cannot be undone. Snapshots taken of {one ? "it" : "them"} are kept.
        </DialogDescription>
      </DialogHeader>

      <ul className="divide-y rounded-md border bg-muted/30 text-sm" aria-label="Sandboxes to terminate">
        {shown.map((s) => (
          <li key={s.id} className="flex items-center justify-between gap-3 px-3 py-2">
            <span className="min-w-0">
              <span className="block truncate font-mono font-medium">{sandboxLabel(s)}</span>
              {s.name ? <span className="block truncate font-mono text-[11px] text-muted-foreground">{s.id}</span> : null}
            </span>
            <span className="shrink-0 text-right font-mono text-[11px] text-muted-foreground">
              {s.state} · {s.cpus} vCPU · {formatMiB(s.memory_mb)}
            </span>
          </li>
        ))}
        {list.length > shown.length ? <li className="px-3 py-2 text-xs text-muted-foreground">and {list.length - shown.length} more</li> : null}
      </ul>

      <div className="flex flex-col gap-1.5">
        <Label htmlFor="terminate-confirm" className="text-xs font-normal text-muted-foreground">
          Type <code className="rounded bg-muted px-1 py-0.5 font-mono text-foreground select-all">{phrase}</code> to confirm
        </Label>
        <Input
          id="terminate-confirm"
          autoFocus
          autoComplete="off"
          spellCheck={false}
          className="font-mono"
          aria-label="Type to confirm"
          placeholder={phrase}
          value={typed}
          onChange={(e) => setTyped(e.target.value)}
        />
      </div>

      <DialogFooter>
        <Button type="button" variant="outline" onClick={onClose}>
          Cancel
        </Button>
        <Button type="submit" variant="destructive" disabled={!ok || pending} className="gap-1.5">
          <Trash2 className="size-3.5" />
          {pending ? "Terminating…" : one ? "Terminate sandbox" : `Terminate ${list.length}`}
        </Button>
      </DialogFooter>
    </form>
  );
}
