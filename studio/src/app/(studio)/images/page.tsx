"use client";

import { useState } from "react";
import Link from "next/link";
import { toast } from "sonner";
import { AlertTriangle, Download, Layers3, Lock, Play, RotateCw, Trash2, X } from "lucide-react";
import { EmptyState } from "@/components/common/empty-state";
import { PageHeader } from "@/components/common/page-header";
import { Gate } from "@/components/shell/gate";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { useImages, useInfo, useInstallImage, useRemoveImage } from "@/lib/api/queries";
import { useCan } from "@/lib/caller";
import { formatBytes, formatRelative } from "@/lib/format";
import { cn } from "@/lib/utils";
import type { SandboxImage } from "@/lib/types";

/** A desktop image is published beside each base one (images/desktop). */
const desktopOf = (ref: string) => (/sandbox-base(?=:|@|$)/.test(ref) ? ref.replace(/sandbox-base(?=:|@|$)/, "sandbox-desktop") : "");

/**
 * The images this sandboxd starts sandboxes from (capability images): what
 * is installed, what uses each, and its size; an image downloaded and
 * installed ahead of the first sandbox that wants it (on Linux the install
 * builds its root disk too, most of that first wait); and one removed once
 * nothing uses it. They are this machine's operator's, which on a plain
 * sandboxd is whoever holds its token; a gateway does not offer them yet.
 */
function Images() {
  const { data: info } = useInfo();
  const supported = !!info?.capabilities?.capabilities?.images;
  const { data, isLoading, error } = useImages(supported);
  const can = useCan();
  const [adding, setAdding] = useState(false);
  const [removing, setRemoving] = useState<SandboxImage | null>(null);
  const install = useInstallImage();
  const list = data ?? [];
  const total = list.reduce((n, i) => n + (i.state === "installed" ? (i.bytes ?? 0) : 0), 0);
  const installed = list.filter((i) => i.state === "installed").length;
  const def = list.find((i) => i.default)?.image;

  const start = (image: string) =>
    install.mutate(image, {
      onSuccess: () => toast.success(`Installing ${image}`),
      onError: (e) => toast.error(e.message),
    });

  return (
    <div className="flex flex-col gap-6">
      <PageHeader
        title="Images"
        description="What sandboxes here start from. An image is pulled the first time a sandbox asks for it; download one ahead of time and that first sandbox starts without the wait. Remove what nothing uses to free its disk."
        actions={
          supported ? (
            <Button onClick={() => setAdding(true)} className="gap-1.5">
              <Download className="size-4" /> Download image
            </Button>
          ) : null
        }
      />
      {info && !supported ? (
        <EmptyState icon={Layers3} title="Not on this endpoint" description="This sandboxd does not manage its images; each is pulled when a sandbox first asks for it." />
      ) : error ? (
        <p className="text-sm text-destructive">{error.message}</p>
      ) : isLoading || !info ? (
        <div className="flex flex-col gap-2">
          {Array.from({ length: 3 }, (_, i) => (
            <Skeleton key={i} className="h-14 rounded-lg" />
          ))}
        </div>
      ) : list.length === 0 ? (
        <EmptyState icon={Layers3} title="No images yet" description="Download one, or start a sandbox: its image is installed then." />
      ) : (
        <>
          <p className="-mt-3 text-xs text-muted-foreground">
            {installed} installed{total ? ` · ${formatBytes(total)} on disk` : ""} · layers shared between images are counted once, under neither.
          </p>
          <div className="overflow-hidden rounded-lg border bg-card">
            <div className={cn(ROW, "hidden border-b bg-muted/30 py-2 font-mono text-[10px] tracking-[0.06em] text-muted-foreground uppercase md:grid")}>
              <span>Image</span>
              <span>State</span>
              <span>Size</span>
              <span>In use</span>
              <span>Installed</span>
              <span />
            </div>
            <ul className="divide-y" aria-label="Images">
              {list.map((img) => (
                <ImageRow
                  key={img.image}
                  img={img}
                  mayStart={can("sandbox:create")}
                  onRetry={() => start(img.image)}
                  onRemove={() => setRemoving(img)}
                />
              ))}
            </ul>
          </div>
        </>
      )}
      <DownloadDialog open={adding} onOpenChange={setAdding} suggest={[def, def ? desktopOf(def) : ""].filter((s): s is string => !!s && !list.some((i) => i.image === s && i.state === "installed"))} onStart={start} />
      <RemoveDialog img={removing} onClose={() => setRemoving(null)} />
    </div>
  );
}

const ROW = "grid grid-cols-[minmax(0,1fr)_auto] items-center gap-3 px-4 md:grid-cols-[minmax(14rem,2fr)_minmax(9rem,1fr)_6rem_5rem_7rem_11rem]";

function ImageRow({ img, mayStart, onRetry, onRemove }: { img: SandboxImage; mayStart: boolean; onRetry: () => void; onRemove: () => void }) {
  const pinned = img.default || img.pooled;
  const p = img.progress;
  const pct = p?.phase === "pulling" && p.total ? Math.round(((p.done ?? 0) * 100) / p.total) : null;
  return (
    <li className="flex flex-col">
      <div className={cn(ROW, "py-3")}>
        <div className="flex min-w-0 items-center gap-3">
          <span className="flex size-8 shrink-0 items-center justify-center rounded-md bg-primary/10 text-primary">
            <Layers3 className="size-4" aria-hidden />
          </span>
          <span className="min-w-0">
            <span className="block truncate font-mono text-sm" title={img.image}>
              {img.image}
            </span>
            <span className="flex flex-wrap items-center gap-1.5 text-[11px] text-muted-foreground">
              {img.default ? <Tag>default</Tag> : null}
              {img.pooled ? <Tag>pooled</Tag> : null}
              {img.digest ? <span className="truncate font-mono" title={img.digest}>{img.digest.slice(0, 19)}</span> : null}
            </span>
          </span>
        </div>
        <span className="hidden md:block">
          {img.state === "installing" ? (
            <span className="flex flex-col gap-1">
              <span className="text-xs text-status-running">{p?.phase === "building" ? "building disk…" : pct !== null ? `downloading ${pct}%` : "downloading…"}</span>
              <span className="h-1 w-28 overflow-hidden rounded-full bg-muted">
                <span
                  className={cn("block h-full rounded-full bg-status-running transition-all", pct === null && "animate-pulse")}
                  style={{ width: `${p?.phase === "building" ? 100 : (pct ?? 30)}%` }}
                />
              </span>
            </span>
          ) : (
            <span
              className={cn(
                "inline-flex rounded-full border px-2 py-0.5 text-[11px]",
                img.state === "installed" ? "border-status-good/30 bg-status-good/10 text-status-good" : "border-destructive/30 bg-destructive/10 text-destructive",
              )}
            >
              {img.state}
            </span>
          )}
        </span>
        <span className="hidden font-mono text-xs text-muted-foreground md:block">{img.bytes ? formatBytes(img.bytes) : "—"}</span>
        <span className={cn("hidden font-mono text-xs md:block", img.in_use ? "text-foreground" : "text-muted-foreground")}>{img.in_use || "—"}</span>
        <span className="hidden text-xs text-muted-foreground md:block">{img.installed_at ? formatRelative(img.installed_at) : "—"}</span>
        <span className="flex items-center justify-end gap-1">
          {img.state === "installed" && mayStart ? (
            <Button asChild size="sm" variant="ghost" className="h-7 gap-1 px-2 text-xs">
              <Link href={`/launch?image=${encodeURIComponent(img.image)}`} aria-label={`Start a sandbox from ${img.image}`}>
                <Play className="size-3.5" /> Start
              </Link>
            </Button>
          ) : null}
          {img.state === "failed" ? (
            <Button size="sm" variant="ghost" className="h-7 gap-1 px-2 text-xs" onClick={onRetry} aria-label={`Retry ${img.image}`}>
              <RotateCw className="size-3.5" /> Retry
            </Button>
          ) : null}
          {img.state !== "installing" ? (
            <Button
              size="sm"
              variant="ghost"
              className="h-7 gap-1 px-2 text-xs text-muted-foreground hover:text-destructive"
              disabled={pinned || img.in_use > 0}
              title={pinned ? "The server's default or pooled image; change its policy to remove it" : img.in_use ? "A sandbox here starts from it" : undefined}
              onClick={onRemove}
              aria-label={`${img.state === "failed" ? "Clear" : "Remove"} ${img.image}`}
            >
              {pinned ? <Lock className="size-3.5" /> : img.state === "failed" ? <X className="size-3.5" /> : <Trash2 className="size-3.5" />}
              {img.state === "failed" ? "Clear" : "Remove"}
            </Button>
          ) : null}
        </span>
      </div>
      {img.error ? (
        <p className="flex items-start gap-1.5 border-t bg-destructive/5 px-4 py-2 font-mono text-[11px] break-all text-destructive">
          <AlertTriangle className="mt-px size-3 shrink-0" aria-hidden />
          {img.error}
        </p>
      ) : null}
    </li>
  );
}

function Tag({ children }: { children: React.ReactNode }) {
  return <span className="rounded border px-1 text-[10px] uppercase">{children}</span>;
}

/** Image references as sandboxd takes them: a name, maybe a registry and a tag or digest. */
const REF = /^[a-z0-9][a-z0-9._\-/:@]*$/i;

function DownloadDialog({ open, onOpenChange, suggest, onStart }: { open: boolean; onOpenChange: (o: boolean) => void; suggest: string[]; onStart: (image: string) => void }) {
  const [ref, setRef] = useState("");
  const v = ref.trim();
  const bad = v !== "" && !REF.test(v);
  return (
    <Dialog
      open={open}
      onOpenChange={(o) => {
        onOpenChange(o);
        if (!o) setRef("");
      }}
    >
      <DialogContent className="sm:max-w-lg">
        <form
          className="flex flex-col gap-4"
          onSubmit={(e) => {
            e.preventDefault();
            if (!v || bad) return;
            onStart(v);
            onOpenChange(false);
            setRef("");
          }}
        >
          <DialogHeader>
            <DialogTitle>Download image</DialogTitle>
            <DialogDescription>
              Pulled from its registry and installed on this machine, so the first sandbox from it starts without the wait. It runs in the background; this
              list shows how far it has got. The server&apos;s policy may limit which images are allowed.
            </DialogDescription>
          </DialogHeader>
          <Input autoFocus aria-label="Image" className="font-mono" placeholder="ghcr.io/owner/image:tag" value={ref} onChange={(e) => setRef(e.target.value)} />
          {bad ? <span className="-mt-2 text-[11px] text-destructive">An image reference: registry/name:tag, or name@sha256:…</span> : null}
          {suggest.length ? (
            <div className="flex flex-col gap-1.5">
              <span className="text-xs text-muted-foreground">Suggested</span>
              <div className="flex flex-wrap gap-1.5">
                {suggest.map((s) => (
                  <button key={s} type="button" onClick={() => setRef(s)} className="rounded-md border px-2 py-1 font-mono text-[11px] text-muted-foreground hover:text-foreground">
                    {s}
                  </button>
                ))}
              </div>
            </div>
          ) : null}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
              Cancel
            </Button>
            <Button type="submit" disabled={!v || bad} className="gap-1.5">
              <Download className="size-4" /> Download
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function RemoveDialog({ img, onClose }: { img: SandboxImage | null; onClose: () => void }) {
  const remove = useRemoveImage();
  const failed = img?.state === "failed";
  return (
    <Dialog open={!!img} onOpenChange={(o) => !o && !remove.isPending && onClose()}>
      <DialogContent className="sm:max-w-md">
        {img ? (
          <>
            <DialogHeader>
              <DialogTitle>{failed ? "Clear failed download" : "Remove image"}</DialogTitle>
              <DialogDescription>
                {failed ? (
                  <>Takes the failed download of <span className="font-mono">{img.image}</span> off the list.</>
                ) : (
                  <>
                    Removes <span className="font-mono break-all">{img.image}</span>
                    {img.bytes ? `, freeing about ${formatBytes(img.bytes)} and any layers no other image uses` : ""}. The next sandbox from it downloads it
                    again.
                  </>
                )}
              </DialogDescription>
            </DialogHeader>
            <DialogFooter>
              <Button variant="outline" onClick={onClose} disabled={remove.isPending}>
                Cancel
              </Button>
              <Button
                variant="destructive"
                disabled={remove.isPending}
                onClick={() =>
                  remove.mutate(img.image, {
                    onSuccess: (r) => {
                      toast.success(failed ? `Cleared ${img.image}` : `Removed ${img.image}${r.freed_bytes ? `, freed ${formatBytes(r.freed_bytes)}` : ""}`);
                      onClose();
                    },
                    onError: (e) => toast.error(e.message),
                  })
                }
              >
                {remove.isPending ? "Removing…" : failed ? "Clear" : "Remove"}
              </Button>
            </DialogFooter>
          </>
        ) : null}
      </DialogContent>
    </Dialog>
  );
}

export default function ImagesPage() {
  return (
    <Gate need="sandboxd">
      <Images />
    </Gate>
  );
}
