"use client";

import { useState } from "react";
import { toast } from "sonner";
import { Checkbox } from "@/components/ui/checkbox";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Label } from "@/components/ui/label";
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group";
import { Input } from "@/components/ui/input";
import { useInfo, useResize, useResizeInPlace, useTemplates } from "@/lib/api/queries";
import { formatMiB } from "@/lib/format";
import { overLimits } from "@/lib/templates";
import { cn } from "@/lib/utils";
import type { Sandbox } from "@/lib/types";

/** Why a sandbox cannot be resized here, or "" when it can. */
export function resizeBlocked(sb: Sandbox, caps?: Record<string, boolean>, backend?: string): string {
  const who = backend ?? "This endpoint";
  if (caps?.resize) return sb.state === "running" ? "" : "Only a running sandbox is resized.";
  if (!caps?.disk_snapshot)
    return `${caps?.memory_snapshot ? `${who} takes memory snapshots, whose copies keep the original's size` : `${who} takes no snapshots to copy it from`}, and a running VM's vCPUs and memory are fixed. Start a new sandbox at the size you need.`;
  if (sb.state !== "running") return "Only a running sandbox is resized.";
  if (sb.volumes?.length) return "A sandbox with volumes cannot be snapshotted, so it cannot be resized.";
  return "";
}

/**
 * A new size for a running sandbox, the only way a VM gets one: a copy. Its
 * files, name, labels, network and idle timeout come across; its processes
 * and memory do not, nor its environment, whose values are never readable
 * back. Studio's server does it end to end (internal/studio/resize.go).
 */
export function ResizeDialog({
  sb,
  open,
  onOpenChange,
  onReplaced,
}: {
  sb: Sandbox;
  open: boolean;
  onOpenChange: (o: boolean) => void;
  onReplaced?: (id: string) => void;
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-lg">{open ? <Chooser sb={sb} onDone={() => onOpenChange(false)} onReplaced={onReplaced} /> : null}</DialogContent>
    </Dialog>
  );
}

/** In place where the endpoint can (capability resize), else by a copy. */
function Chooser({ sb, onDone, onReplaced }: { sb: Sandbox; onDone: () => void; onReplaced?: (id: string) => void }) {
  const { data: info } = useInfo();
  return info?.capabilities?.capabilities?.resize ? <InPlace sb={sb} onDone={onDone} /> : <Form sb={sb} onDone={onDone} onReplaced={onReplaced} />;
}

/**
 * The endpoint restarts the sandbox at its new size: the same sandbox, with
 * its files, volumes and network; its processes end, and it refuses while
 * one runs. A disk only grows. Through a gateway the growth counts against
 * the quota and must fit on the sandbox's node; the refusal says which.
 */
function InPlace({ sb, onDone }: { sb: Sandbox; onDone: () => void }) {
  const { data: templates } = useTemplates();
  const { data: info } = useInfo();
  const resize = useResizeInPlace();
  const limits = info?.capabilities?.limits;
  const [cpus, setCpus] = useState(String(sb.cpus));
  const [mem, setMem] = useState(String(sb.memory_mb));
  const [disk, setDisk] = useState(String(sb.disk_mb));
  const want = { cpus: Number(cpus), memory_mb: Number(mem), disk_mb: Number(disk) };
  const problem =
    !(want.cpus > 0) || (limits && want.cpus > limits.max_cpus)
      ? `vCPUs: more than 0${limits ? `, at most ${limits.max_cpus}` : ""}`
      : !(want.memory_mb >= 128) || (limits && want.memory_mb > limits.max_memory_mb)
        ? `Memory: 128 MiB to ${limits ? formatMiB(limits.max_memory_mb) : "the limit"}`
        : !(want.disk_mb >= sb.disk_mb)
          ? `Disk: only grows, from ${formatMiB(sb.disk_mb)}`
          : limits && want.disk_mb > limits.max_disk_mb
            ? `Disk: at most ${formatMiB(limits.max_disk_mb)}`
            : "";
  const same = want.cpus === sb.cpus && want.memory_mb === sb.memory_mb && want.disk_mb === sb.disk_mb;

  function submit(e: React.FormEvent) {
    e.preventDefault();
    resize.mutate(
      { id: sb.id, ...want },
      {
        onSuccess: (r) => {
          toast.success(`${sb.name || sb.id} is now ${r.cpus} vCPU · ${formatMiB(r.memory_mb)} · disk ${formatMiB(r.disk_mb)}`);
          onDone();
        },
        onError: (err) => toast.error(err.message),
      },
    );
  }

  return (
    <form className="flex flex-col gap-4" onSubmit={submit}>
      <DialogHeader>
        <DialogTitle>Resize {sb.name || sb.id}</DialogTitle>
        <DialogDescription>
          Now {sb.cpus} vCPU · {formatMiB(sb.memory_mb)} · disk {formatMiB(sb.disk_mb)}. It restarts at the new size in a few seconds and stays the same sandbox:
          its files, volumes and network are kept; running processes end, so stop them first. A disk only grows.
        </DialogDescription>
      </DialogHeader>
      {templates?.length ? (
        <div className="flex flex-wrap gap-1.5" aria-label="Sizes">
          {templates.map((x) => (
            <Button
              key={x.name}
              type="button"
              variant="outline"
              size="sm"
              title={overLimits(x, limits) || x.description}
              disabled={!!overLimits(x, limits)}
              onClick={() => {
                setCpus(String(x.cpus));
                setMem(String(x.memory_mb));
                if (x.disk_mb && x.disk_mb > sb.disk_mb) setDisk(String(x.disk_mb));
              }}
            >
              {x.name}
            </Button>
          ))}
        </div>
      ) : null}
      <div className="grid grid-cols-3 gap-2">
        <Label className="flex flex-col items-start gap-1 text-xs font-normal">
          vCPUs
          <Input aria-label="vCPUs" type="number" min={0.25} step={0.25} value={cpus} onChange={(e) => setCpus(e.target.value)} className="font-mono" />
        </Label>
        <Label className="flex flex-col items-start gap-1 text-xs font-normal">
          Memory (MiB)
          <Input aria-label="Memory (MiB)" type="number" min={128} step={128} value={mem} onChange={(e) => setMem(e.target.value)} className="font-mono" />
        </Label>
        <Label className="flex flex-col items-start gap-1 text-xs font-normal">
          Disk (MiB)
          <Input aria-label="Disk (MiB)" type="number" min={sb.disk_mb} step={1024} value={disk} onChange={(e) => setDisk(e.target.value)} className="font-mono" />
        </Label>
      </div>
      {problem ? <p className="text-xs text-destructive">{problem}</p> : null}
      <DialogFooter>
        <Button type="button" variant="outline" onClick={onDone} disabled={resize.isPending}>
          Cancel
        </Button>
        <Button type="submit" disabled={!!problem || same || resize.isPending}>
          {resize.isPending ? "Restarting…" : "Resize and restart"}
        </Button>
      </DialogFooter>
    </form>
  );
}

function Form({ sb, onDone, onReplaced }: { sb: Sandbox; onDone: () => void; onReplaced?: (id: string) => void }) {
  const { data: templates } = useTemplates();
  const { data: info } = useInfo();
  const resize = useResize();
  const limits = info?.capabilities?.limits;
  const [pick, setPick] = useState("");
  const [dropEnv, setDropEnv] = useState(false);
  const [keep, setKeep] = useState(false);
  const t = templates?.find((x) => x.name === pick);
  const same = !!t && t.cpus === sb.cpus && t.memory_mb === sb.memory_mb && (!t.disk_mb || t.disk_mb === sb.disk_mb);
  const env = sb.env_names ?? [];

  function submit(e: React.FormEvent) {
    e.preventDefault();
    if (!t) return;
    resize.mutate(
      { id: sb.id, cpus: t.cpus, memory_mb: t.memory_mb, disk_mb: t.disk_mb || undefined, drop_env: dropEnv || undefined, keep_snapshot: keep || undefined },
      {
        onSuccess: (r) => {
          toast.success(`${sb.name || r.sandbox.id} is now ${t.cpus} vCPU · ${formatMiB(t.memory_mb)}${r.snapshot ? `; kept ${r.snapshot}` : ""}`);
          onDone();
          onReplaced?.(r.sandbox.id);
        },
        onError: (err) => toast.error(err.message),
      },
    );
  }

  return (
    <form className="flex flex-col gap-4" onSubmit={submit}>
      <DialogHeader>
        <DialogTitle>Resize {sb.name || sb.id}</DialogTitle>
        <DialogDescription>
          Now {sb.cpus} vCPU · {formatMiB(sb.memory_mb)}. A running VM&apos;s size is fixed, so this snapshots it and starts a copy at the new size in its
          place: files, name, labels, network and idle timeout come across; running processes and memory do not.
        </DialogDescription>
      </DialogHeader>
      <RadioGroup value={pick} onValueChange={setPick} aria-label="New size" className="grid grid-cols-2 gap-2 sm:grid-cols-3">
        {(templates ?? []).map((x) => {
          const over = overLimits(x, limits);
          const current = x.cpus === sb.cpus && x.memory_mb === sb.memory_mb;
          return (
            <Label
              key={x.name}
              htmlFor={`resize-${x.name}`}
              title={over || x.description}
              className={cn(
                "flex cursor-pointer flex-col items-start gap-0.5 rounded-md border p-2.5 font-normal has-[[data-state=checked]]:border-primary has-[[data-state=checked]]:bg-primary/5",
                over && "cursor-not-allowed opacity-50",
              )}
            >
              <span className="flex w-full items-center justify-between font-mono text-sm">
                {x.name}
                <RadioGroupItem id={`resize-${x.name}`} value={x.name} disabled={!!over} />
              </span>
              <span className="font-mono text-[11px] text-muted-foreground">
                {x.cpus} vCPU · {formatMiB(x.memory_mb)}
              </span>
              {current ? <span className="text-[10px] text-muted-foreground">current</span> : null}
            </Label>
          );
        })}
      </RadioGroup>
      {env.length ? (
        <Label className="flex items-start gap-2 rounded-md border border-caution/30 bg-caution/10 px-3 py-2 text-xs font-normal text-caution">
          <Checkbox checked={dropEnv} onCheckedChange={(v) => setDropEnv(!!v)} aria-label="Go on without the environment" className="mt-0.5" />
          <span>
            Its environment does not come across: <span className="font-mono">{env.join(", ")}</span>. Values are never readable back, so there is nothing to
            copy. Go on without it.
          </span>
        </Label>
      ) : null}
      <Label className="flex items-center gap-2 text-xs font-normal">
        <Checkbox checked={keep} onCheckedChange={(v) => setKeep(!!v)} aria-label="Keep the snapshot" />
        Keep the snapshot, as a way back
      </Label>
      <DialogFooter>
        <Button type="button" variant="outline" onClick={onDone} disabled={resize.isPending}>
          Cancel
        </Button>
        <Button type="submit" disabled={!t || same || (env.length > 0 && !dropEnv) || resize.isPending}>
          {resize.isPending ? "Resizing…" : t ? `Resize to ${t.name}` : "Resize"}
        </Button>
      </DialogFooter>
    </form>
  );
}
