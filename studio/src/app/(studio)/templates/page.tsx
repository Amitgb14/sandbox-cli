"use client";

import { useState } from "react";
import Link from "next/link";
import { toast } from "sonner";
import { ArrowRight, Cpu, LayoutTemplate, Lock, MemoryStick, Pencil, Plus, Trash2 } from "lucide-react";
import { PageHeader } from "@/components/common/page-header";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Skeleton } from "@/components/ui/skeleton";
import { useDeleteTemplate, useInfo, useSaveTemplate, useTemplates } from "@/lib/api/queries";
import { useCan } from "@/lib/caller";
import { formatMiB } from "@/lib/format";
import { cn } from "@/lib/utils";
import { overLimits } from "@/lib/templates";
import type { Limits, VmTemplate } from "@/lib/types";

/**
 * VM templates: a size a sandbox is launched at, by name — micro to xlarge
 * built in, and any of your own. The Playground offers them; a launch sends
 * the size as the CLI's --cpus, --memory and --disk would, and sandboxd's
 * limits bound it as they bound any request. They are kept by Studio's
 * server, in ~/.config/sandbox/studio.json, so they outlive the tab and the
 * port.
 */
export default function TemplatesPage() {
  const { data, isLoading } = useTemplates();
  const { data: info } = useInfo();
  const limits = info?.capabilities?.limits;
  const can = useCan();
  const remove = useDeleteTemplate();
  const [editing, setEditing] = useState<VmTemplate | "new" | null>(null);
  const maxCpus = Math.max(...(data ?? []).map((t) => t.cpus), 1);
  const maxMem = Math.max(...(data ?? []).map((t) => t.memory_mb), 1);

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="Templates"
        description="Sizes to launch a sandbox at: vCPUs, memory and disk, by name. Pick one in the Playground. This endpoint's limits still apply; a template above them is refused at launch."
        actions={
          // Saved templates are this machine's user's (studio.json): a hosted
          // Studio offers the built-in sizes and nothing to edit.
          process.env.NEXT_PUBLIC_STUDIO_HOSTED !== "on" && (
            <Button onClick={() => setEditing("new")} className="gap-1.5">
              <Plus className="size-4" /> New template
            </Button>
          )
        }
      />
      {limits ? (
        <p className="-mt-3 text-xs text-muted-foreground">
          This endpoint allows up to <span className="font-mono">{limits.max_cpus} vCPUs</span> ·{" "}
          <span className="font-mono">{formatMiB(limits.max_memory_mb)}</span> memory ·{" "}
          <span className="font-mono">{formatMiB(limits.max_disk_mb)}</span> disk per sandbox.
        </p>
      ) : null}

      {isLoading ? (
        <div className="flex flex-col gap-2">
          {Array.from({ length: 5 }, (_, i) => (
            <Skeleton key={i} className="h-14 rounded-lg" />
          ))}
        </div>
      ) : (
        <div className="overflow-hidden rounded-lg border bg-card">
          <div className={cn(ROW, "hidden border-b bg-muted/30 py-2 font-mono text-[10px] tracking-[0.06em] text-muted-foreground uppercase md:grid")}>
            <span>Template</span>
            <span>vCPUs</span>
            <span>Memory</span>
            <span>Disk</span>
            <span />
          </div>
          <ul className="divide-y">
            {(data ?? []).map((t) => {
              const over = overLimits(t, limits);
              return (
                <li key={t.name} className={cn(ROW, "py-3 hover:bg-muted/20")}>
                  <div className="flex min-w-0 items-center gap-3">
                    <span className="flex size-8 shrink-0 items-center justify-center rounded-md bg-primary/10 text-primary">
                      <LayoutTemplate className="size-4" aria-hidden />
                    </span>
                    <span className="min-w-0">
                      <span className="flex items-center gap-1.5 font-mono text-sm font-medium">
                        <span className="truncate">{t.name}</span>
                        {t.builtin ? (
                          <span className="inline-flex items-center gap-0.5 rounded border px-1 text-[10px] font-normal text-muted-foreground" title="Built in: copy it to change it">
                            <Lock className="size-2.5" aria-hidden /> built in
                          </span>
                        ) : null}
                      </span>
                      <span className="block truncate text-[11px] text-muted-foreground">
                        {t.description || "—"}
                        <span className="md:hidden">
                          {" "}
                          · {t.cpus} vCPU · {formatMiB(t.memory_mb)}
                        </span>
                      </span>
                      {over ? <span className="block text-[11px] text-caution">{over}</span> : null}
                    </span>
                  </div>
                  <Meter className="hidden md:flex" icon={Cpu} label={`${t.cpus} vCPU`} frac={t.cpus / maxCpus} />
                  <Meter className="hidden md:flex" icon={MemoryStick} label={formatMiB(t.memory_mb)} frac={t.memory_mb / maxMem} />
                  <span className="hidden font-mono text-xs text-muted-foreground md:block">{t.disk_mb ? formatMiB(t.disk_mb) : "default"}</span>
                  <span className="flex items-center justify-end gap-1">
                    {can("sandbox:create") && (
                      <Button asChild size="sm" variant="ghost" className="h-7 gap-1 px-2 text-xs">
                        <Link href={`/launch?template=${encodeURIComponent(t.name)}`} aria-label={`Launch with ${t.name}`}>
                          Launch <ArrowRight className="size-3.5" aria-hidden />
                        </Link>
                      </Button>
                    )}
                    {process.env.NEXT_PUBLIC_STUDIO_HOSTED !== "on" && (
                    <Button
                      size="icon"
                      variant="ghost"
                      className="size-7"
                      aria-label={t.builtin ? `Copy ${t.name}` : `Edit ${t.name}`}
                      title={t.builtin ? "Copy into a template of your own" : "Edit"}
                      onClick={() => setEditing(t.builtin ? { ...t, name: `${t.name}-custom`, builtin: false } : t)}
                    >
                      {t.builtin ? <Plus className="size-3.5" /> : <Pencil className="size-3.5" />}
                    </Button>
                    )}
                    {!t.builtin && process.env.NEXT_PUBLIC_STUDIO_HOSTED !== "on" && (
                      <Button
                        size="icon"
                        variant="ghost"
                        className="size-7 text-muted-foreground hover:text-destructive"
                        aria-label={`Delete ${t.name}`}
                        disabled={remove.isPending}
                        onClick={() =>
                          confirm(`Delete the template ${t.name}? Sandboxes launched with it keep their size.`) &&
                          remove.mutate(t.name, { onSuccess: () => toast.success(`Deleted ${t.name}`), onError: (e) => toast.error(e.message) })
                        }
                      >
                        <Trash2 className="size-3.5" />
                      </Button>
                    )}
                  </span>
                </li>
              );
            })}
          </ul>
        </div>
      )}
      {process.env.NEXT_PUBLIC_STUDIO_HOSTED !== "on" && (
        <TemplateDialog
          value={editing}
          existing={(data ?? []).map((t) => t.name)}
          limits={limits}
          onClose={() => setEditing(null)}
        />
      )}
    </div>
  );
}

const ROW =
  "grid grid-cols-[minmax(0,1fr)_auto] items-center gap-3 px-4 md:grid-cols-[minmax(12rem,1.6fr)_minmax(0,1fr)_minmax(0,1fr)_6rem_9.5rem]";

function Meter({ icon: Icon, label, frac, className }: { icon: typeof Cpu; label: string; frac: number; className?: string }) {
  return (
    <span className={cn("flex min-w-0 items-center gap-2", className)}>
      <Icon className="size-3.5 shrink-0 text-muted-foreground" aria-hidden />
      <span className="w-16 shrink-0 font-mono text-xs tabular-nums">{label}</span>
      <span className="h-1.5 min-w-8 flex-1 overflow-hidden rounded-full bg-muted">
        <span className="block h-full rounded-full bg-primary/60" style={{ width: `${Math.max(4, Math.min(1, frac) * 100)}%` }} />
      </span>
    </span>
  );
}

const CPU_CHOICES = [1, 2, 4, 8, 16];
const MEM_CHOICES = [512, 1024, 2048, 4096, 8192, 16384];

function TemplateDialog({
  value,
  existing,
  limits,
  onClose,
}: {
  value: VmTemplate | "new" | null;
  existing: string[];
  limits?: Limits;
  onClose: () => void;
}) {
  return (
    <Dialog open={value !== null} onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        {value !== null ? (
          <TemplateForm
            key={value === "new" ? "new" : value.name}
            initial={value === "new" ? undefined : value}
            existing={existing}
            limits={limits}
            onDone={onClose}
          />
        ) : null}
      </DialogContent>
    </Dialog>
  );
}

function TemplateForm({ initial, existing, limits, onDone }: { initial?: VmTemplate; existing: string[]; limits?: Limits; onDone: () => void }) {
  // Editing one of yours keeps its name; a copy of a built-in, or a new one, chooses it.
  const renaming = !initial || !existing.includes(initial.name);
  const [name, setName] = useState(initial?.name ?? "");
  const [description, setDescription] = useState(initial?.description ?? "");
  const [cpus, setCpus] = useState(String(initial?.cpus ?? 2));
  const [memory, setMemory] = useState(String(initial?.memory_mb ?? 2048));
  const [disk, setDisk] = useState(initial?.disk_mb ? String(initial.disk_mb) : "");
  const save = useSaveTemplate();
  const t: VmTemplate = { name, description, cpus: Number(cpus), memory_mb: Number(memory), disk_mb: disk ? Number(disk) : 0 };
  const over = overLimits(t, limits);
  const nameTaken = renaming && existing.includes(name);

  return (
    <form
      className="flex flex-col gap-4"
      onSubmit={(e) => {
        e.preventDefault();
        save.mutate(t, {
          onSuccess: () => {
            toast.success(`Saved ${name}`);
            onDone();
          },
          onError: (err) => toast.error(err.message),
        });
      }}
    >
      <DialogHeader>
        <DialogTitle>{renaming ? "New template" : `Edit ${initial!.name}`}</DialogTitle>
        <DialogDescription>A size to launch sandboxes at. Leave the disk empty for the server&apos;s default.</DialogDescription>
      </DialogHeader>
      <div className="grid gap-4 sm:grid-cols-2">
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="tpl-name">Name</Label>
          <Input
            id="tpl-name"
            className="font-mono"
            placeholder="ci-runner"
            value={name}
            onChange={(e) => setName(e.target.value.toLowerCase())}
            pattern="[a-z0-9]([a-z0-9\-]{0,30}[a-z0-9])?"
            title="Lowercase letters, digits and dashes, up to 32"
            disabled={!renaming}
            required
          />
          {nameTaken ? <span className="text-[11px] text-caution">That name is taken; saving replaces yours, and a built-in cannot be.</span> : null}
        </div>
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="tpl-desc">Description</Label>
          <Input id="tpl-desc" placeholder="optional" maxLength={200} value={description} onChange={(e) => setDescription(e.target.value)} />
        </div>
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="tpl-cpus">vCPUs</Label>
          <Input id="tpl-cpus" type="number" min={1} max={256} step={1} value={cpus} onChange={(e) => setCpus(e.target.value)} required />
          <Choices values={CPU_CHOICES} current={Number(cpus)} max={limits?.max_cpus} format={(n) => String(n)} onPick={(n) => setCpus(String(n))} />
        </div>
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="tpl-mem">Memory (MiB)</Label>
          <Input id="tpl-mem" type="number" min={128} step={128} value={memory} onChange={(e) => setMemory(e.target.value)} required />
          <Choices values={MEM_CHOICES} current={Number(memory)} max={limits?.max_memory_mb} format={formatMiB} onPick={(n) => setMemory(String(n))} />
        </div>
        <div className="flex flex-col gap-1.5">
          <Label htmlFor="tpl-disk">Disk (MiB)</Label>
          <Input id="tpl-disk" type="number" min={256} step={256} placeholder="server default" value={disk} onChange={(e) => setDisk(e.target.value)} />
        </div>
      </div>
      {over ? <p className="text-xs text-caution">{over}. It can be saved, and launching with it here is refused.</p> : null}
      <DialogFooter>
        <Button type="button" variant="ghost" onClick={onDone}>
          Cancel
        </Button>
        <Button type="submit" disabled={save.isPending}>
          {save.isPending ? "Saving…" : "Save template"}
        </Button>
      </DialogFooter>
    </form>
  );
}

function Choices({
  values,
  current,
  max,
  format,
  onPick,
}: {
  values: number[];
  current: number;
  max?: number;
  format: (n: number) => string;
  onPick: (n: number) => void;
}) {
  return (
    <div className="flex flex-wrap gap-1">
      {values.map((v) => (
        <button
          key={v}
          type="button"
          onClick={() => onPick(v)}
          disabled={max !== undefined && v > max}
          className={cn(
            "rounded border px-1.5 py-0.5 font-mono text-[11px] transition-colors disabled:opacity-40",
            v === current ? "border-primary bg-primary/10 text-primary" : "text-muted-foreground hover:text-foreground",
          )}
        >
          {format(v)}
        </button>
      ))}
    </div>
  );
}
