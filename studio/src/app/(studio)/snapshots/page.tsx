"use client";

import { useMemo, useState } from "react";
import Link from "next/link";
import {
  Camera,
  CloudOff,
  CloudUpload,
  GitBranch,
  History,
  MoreHorizontal,
  RotateCcw,
  Search,
  Settings2,
  ShieldCheck,
  Terminal,
  TriangleAlert,
} from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { PageHeader } from "@/components/common/page-header";
import { EmptyState } from "@/components/common/empty-state";
import {
  useCreateSnapshot,
  useRestoreSnapshot,
  useSetSnapshotRetention,
  useSnapshots,
  useSnapshotSettings,
  useUploadSnapshot,
  useVerifySnapshot,
  useWorktrees,
} from "@/lib/api/queries";
import { useUi } from "@/lib/store";
import { formatBytes, formatRelative, humanDuration } from "@/lib/format";
import type { RestoreMode, Snapshot } from "@/lib/types";

/**
 * Every checkpoint of a workspace, and what can be done with one.
 *
 * A snapshot is a commit of a working tree under `refs/sandbox/snapshots/`,
 * written through a private index so the repository's own index, HEAD, branches
 * and working tree are never touched. It holds files: no container, no image and
 * no credential, which is why this screen can offer to restore one and why it is
 * not a way to resume a stopped machine.
 *
 * The rule this screen is built on is provenance. A snapshot taken through the
 * SDK is restored through the SDK — a script mid-way through something is not a
 * thing to undo from a browser tab — so Restore is disabled on those and says
 * why. They are still *listed*: hiding them would make this table claim they do
 * not exist, and the retention on one is still worth seeing and changing.
 */
export default function SnapshotsPage() {
  const repoFilter = useUi((s) => s.repoFilter);
  // null is the picker's "All repositories"; the endpoints spell that repo=all.
  const repo = repoFilter ?? undefined;
  const { data, isPending } = useSnapshots(repo);
  const { data: worktrees } = useWorktrees();
  // Whether a bucket is configured at all, which decides whether the two storage
  // actions exist. A menu offering to mirror with nowhere to mirror to is a
  // button whose only outcome is an error message.
  const { data: settings } = useSnapshotSettings();
  const storageOn = !!settings?.s3?.bucket;
  const upload = useUploadSnapshot(repo);
  const verify = useVerifySnapshot(repo);

  const [query, setQuery] = useState("");
  const [taking, setTaking] = useState(false);
  const [restoring, setRestoring] = useState<Snapshot | null>(null);
  const [retiming, setRetiming] = useState<Snapshot | null>(null);

  const snapshots = useMemo(
    () =>
      (data ?? []).filter(
        (s) =>
          !query ||
          s.branch?.toLowerCase().includes(query.toLowerCase()) ||
          s.label?.toLowerCase().includes(query.toLowerCase()),
      ),
    [data, query],
  );

  const fromSdk = snapshots.filter((s) => s.source === "sdk").length;

  return (
    <div className="space-y-5">
      <PageHeader
        title="Snapshots"
        description="A commit of a working tree under refs/sandbox/snapshots/ — files only, written without touching your index, HEAD, branches or working tree."
        actions={
          <>
            {/* Retention and the bucket are configured in Settings, and this is
                where somebody is standing when they want to change either — so
                the screen carries the route rather than making them go and find
                it. It lands on the snapshots section, not the top of the page. */}
            <Tooltip>
              <TooltipTrigger asChild>
                <Button asChild size="sm" variant="outline">
                  <Link href="/settings#snapshots" aria-label="Snapshot settings">
                    <Settings2 className="size-4" />
                  </Link>
                </Button>
              </TooltipTrigger>
              <TooltipContent>Retention and storage settings</TooltipContent>
            </Tooltip>
            <Button size="sm" onClick={() => setTaking(true)}>
              <Camera className="size-4" />
              Take a snapshot
            </Button>
          </>
        }
      />

      <div className="flex flex-wrap items-center gap-3">
        <div className="relative">
          <Search className="absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2 text-muted-foreground" />
          <Input
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="Search branches and labels…"
            className="h-8 w-full pl-8 sm:w-72"
          />
        </div>
        <div className="flex items-center gap-2 text-xs text-muted-foreground">
          <Badge variant="outline" className="tabular-nums">
            {snapshots.length} snapshots
          </Badge>
          {fromSdk > 0 && (
            <Badge variant="outline" className="tabular-nums">
              {fromSdk} from the SDK
            </Badge>
          )}
        </div>
      </div>

      <div className="overflow-hidden rounded-lg border">
        <Table>
          <TableHeader className="bg-muted/40">
            <TableRow className="hover:bg-transparent">
              <TableHead className="h-9">Snapshot</TableHead>
              <TableHead className="h-9">Branch</TableHead>
              <TableHead className="h-9">Taken by</TableHead>
              <TableHead className="h-9">Commit</TableHead>
              <TableHead className="h-9">Copy</TableHead>
              <TableHead className="h-9">Kept for</TableHead>
              <TableHead className="h-9">Taken</TableHead>
              <TableHead className="h-9 w-10" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {isPending ? (
              Array.from({ length: 5 }).map((_, i) => (
                <TableRow key={i}>
                  {Array.from({ length: 8 }).map((__, j) => (
                    <TableCell key={j}>
                      <Skeleton className="h-5 w-full" />
                    </TableCell>
                  ))}
                </TableRow>
              ))
            ) : snapshots.length === 0 ? (
              <TableRow className="hover:bg-transparent">
                <TableCell colSpan={8} className="p-0">
                  <EmptyState
                    icon={History}
                    title={query ? "No snapshot matches" : "No snapshots"}
                    description={
                      query
                        ? "Try a shorter search."
                        : "Take one before a risky step, or let a run's crash safety net record them for you."
                    }
                    className="border-0"
                  />
                </TableCell>
              </TableRow>
            ) : (
              snapshots.map((s) => (
                <TableRow key={s.id}>
                  <TableCell>
                    <div className="min-w-0">
                      <p className="truncate text-sm">
                        {s.label || <span className="text-muted-foreground">unlabelled</span>}
                      </p>
                      <p className="truncate font-mono text-[11px] text-muted-foreground">
                        {s.id}
                      </p>
                    </div>
                  </TableCell>
                  <TableCell className="font-mono text-xs">{s.branch || "—"}</TableCell>
                  <TableCell>
                    <SourceCell snapshot={s} />
                  </TableCell>
                  <TableCell className="font-mono text-xs text-muted-foreground">
                    {s.reachable ? (
                      s.commit?.slice(0, 7)
                    ) : (
                      // A manifest can outlive its objects: the ref was deleted
                      // by hand and git collected the content. Offering to
                      // restore that is a promise nothing can keep.
                      <Tooltip>
                        <TooltipTrigger asChild>
                          <span className="cursor-help text-caution">collected</span>
                        </TooltipTrigger>
                        <TooltipContent className="max-w-xs">
                          The manifest is still here but its objects are gone — the ref
                          was deleted and git collected the content. Nothing can be
                          restored from it.
                        </TooltipContent>
                      </Tooltip>
                    )}
                  </TableCell>
                  <TableCell>
                    <RemoteCell snapshot={s} />
                  </TableCell>
                  <TableCell className="text-xs whitespace-nowrap tabular-nums">
                    <span className={s.retention ? "" : "text-muted-foreground"}>
                      {humanDuration(s.retentionEffective)}
                    </span>
                    {s.retention && (
                      <span className="ml-1.5 text-[10px] text-muted-foreground">set</span>
                    )}
                  </TableCell>
                  <TableCell className="text-xs whitespace-nowrap text-muted-foreground">
                    {formatRelative(s.createdAt)}
                  </TableCell>
                  <TableCell>
                    <DropdownMenu>
                      <DropdownMenuTrigger asChild>
                        <Button
                          variant="ghost"
                          size="icon"
                          className="size-7 text-muted-foreground"
                          aria-label="Snapshot actions"
                        >
                          <MoreHorizontal className="size-4" />
                        </Button>
                      </DropdownMenuTrigger>
                      <DropdownMenuContent align="end" className="w-64">
                        <DropdownMenuItem
                          disabled={!restorable(s)}
                          onClick={() => setRestoring(s)}
                        >
                          <RotateCcw className="size-3.5" />
                          Restore…
                        </DropdownMenuItem>
                        {s.source === "sdk" && (
                          <p className="px-2 py-1.5 text-[11px] leading-snug text-muted-foreground">
                            Taken through the SDK, so it is restored through the SDK —
                            a script may be part-way through something this screen
                            cannot see.
                          </p>
                        )}
                        <DropdownMenuSeparator />
                        {storageOn && (
                          <>
                            <DropdownMenuItem
                              disabled={!s.reachable || upload.isPending}
                              onClick={() => upload.mutate(s.id)}
                            >
                              <CloudUpload className="size-3.5" />
                              {s.remote?.uploaded ? "Upload again" : "Mirror to storage"}
                            </DropdownMenuItem>
                            <DropdownMenuItem
                              disabled={!s.remote?.uploaded || verify.isPending}
                              onClick={() => verify.mutate(s.id)}
                            >
                              <ShieldCheck className="size-3.5" />
                              Check it is still there
                            </DropdownMenuItem>
                            <DropdownMenuSeparator />
                          </>
                        )}
                        <DropdownMenuItem onClick={() => setRetiming(s)}>
                          <History className="size-3.5" />
                          Change how long it is kept…
                        </DropdownMenuItem>
                      </DropdownMenuContent>
                    </DropdownMenu>
                  </TableCell>
                </TableRow>
              ))
            )}
          </TableBody>
        </Table>
      </div>

      <TakeDialog
        open={taking}
        onClose={() => setTaking(false)}
        repo={repo}
        branches={(worktrees ?? []).filter((w) => !repo || w.repoId === repo).map((w) => w.branch)}
      />
      <RestoreDialog snapshot={restoring} onClose={() => setRestoring(null)} repo={repo} />
      <RetentionDialog snapshot={retiming} onClose={() => setRetiming(null)} repo={repo} />
    </div>
  );
}

/**
 * Where this snapshot's second copy is, if it has one.
 *
 * Three states worth distinguishing, and the middle one is why this column
 * exists at all. "Local only" is the default and is not a failure — most people
 * configure no bucket. A *failed* upload is different: the machine tried, did
 * not manage it, and somebody who pressed "take a snapshot" while a bucket was
 * configured believes they have an off-machine copy. That one is called out in
 * the row rather than left in a toast nobody was watching for.
 *
 * What it reports is what the upload did, not what the bucket holds right now —
 * the row's "Check it is still there" action is the one that asks.
 */
function RemoteCell({ snapshot }: { snapshot: Snapshot }) {
  const remote = snapshot.remote;
  if (!remote) {
    return <span className="text-xs text-muted-foreground">local only</span>;
  }
  if (remote.error) {
    return (
      <Tooltip>
        <TooltipTrigger asChild>
          <span className="flex cursor-help items-center gap-1.5 text-xs text-caution">
            <TriangleAlert className="size-3.5" />
            not mirrored
          </span>
        </TooltipTrigger>
        <TooltipContent className="max-w-sm">
          This snapshot was taken but never left the machine: {remote.error}
        </TooltipContent>
      </Tooltip>
    );
  }
  if (!remote.uploaded) {
    return (
      <span className="flex items-center gap-1.5 text-xs text-muted-foreground">
        <CloudOff className="size-3.5" />
        local only
      </span>
    );
  }
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <span className="flex cursor-help items-center gap-1.5 text-xs text-muted-foreground">
          <CloudUpload className="size-3.5" />
          {remote.bucket || "mirrored"}
        </span>
      </TooltipTrigger>
      <TooltipContent className="max-w-sm">
        <p className="font-mono text-[11px]">{remote.key}</p>
        <p className="mt-1">
          {remote.bytes ? `${formatBytes(remote.bytes)} as a git bundle. ` : ""}
          Recorded at upload — use “Check it is still there” to ask the bucket.
        </p>
      </TooltipContent>
    </Tooltip>
  );
}

/** A snapshot with no objects left cannot be restored whatever its provenance. */
function restorable(s: Snapshot): boolean {
  return s.reachable && s.source !== "sdk";
}

function SourceCell({ snapshot }: { snapshot: Snapshot }) {
  if (snapshot.source === "sdk") {
    return (
      <span className="flex items-center gap-1.5 text-xs text-muted-foreground">
        <Terminal className="size-3.5" />
        SDK
      </span>
    );
  }
  return (
    <span className="flex items-center gap-1.5 text-xs">
      <GitBranch className="size-3.5 text-muted-foreground" />
      {snapshot.agent || "sandbox"}
    </span>
  );
}

function TakeDialog({
  open,
  onClose,
  repo,
  branches,
}: {
  open: boolean;
  onClose: () => void;
  repo?: string;
  branches: string[];
}) {
  const [label, setLabel] = useState("");
  const [branch, setBranch] = useState("");
  const take = useCreateSnapshot(repo);

  return (
    <Dialog open={open} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>Take a snapshot</DialogTitle>
          <DialogDescription>
            Commits the working tree under refs/sandbox/snapshots/. Your index, HEAD,
            branches and working tree are not touched.
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-3">
          <div className="space-y-1.5">
            <Label htmlFor="snapshot-label">Label</Label>
            <Input
              id="snapshot-label"
              value={label}
              onChange={(e) => setLabel(e.target.value)}
              placeholder="before the migration"
            />
            <p className="text-[11px] text-muted-foreground">
              Optional, and worth it: without one a checkpoint is a hex id in a list.
            </p>
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="snapshot-branch">Branch</Label>
            <select
              id="snapshot-branch"
              value={branch}
              onChange={(e) => setBranch(e.target.value)}
              className="h-9 w-full rounded-md border bg-transparent px-3 text-sm"
            >
              <option value="">the repository&apos;s own checkout</option>
              {branches.map((b) => (
                <option key={b} value={b}>
                  {b}
                </option>
              ))}
            </select>
            <p className="text-[11px] text-muted-foreground">
              A branch is snapshotted in its own worktree, which is where an agent&apos;s
              work actually is.
            </p>
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button
            disabled={take.isPending}
            onClick={() =>
              take.mutate(
                { branch: branch || undefined, label: label || undefined },
                {
                  onSuccess: () => {
                    setLabel("");
                    onClose();
                  },
                  // Left open on failure, deliberately: an unchanged tree is
                  // reported as information rather than an error, and closing
                  // the dialog would look like it worked.
                },
              )
            }
          >
            <Camera className="size-3.5" />
            Take it
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function RestoreDialog({
  snapshot,
  onClose,
  repo,
}: {
  snapshot: Snapshot | null;
  onClose: () => void;
  repo?: string;
}) {
  const [mode, setMode] = useState<RestoreMode>("branch");
  const restore = useRestoreSnapshot(repo);

  return (
    <Dialog open={!!snapshot} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>
            Restore <span className="font-mono">{snapshot?.label || snapshot?.id}</span>?
          </DialogTitle>
          <DialogDescription asChild>
            <div className="space-y-2 text-sm">
              <p>
                Branch mode is the default and the only one that cannot destroy anything:
                it points a new branch at the snapshot and leaves your working tree alone.
              </p>
              {mode === "worktree" && (
                <p className="text-destructive">
                  Worktree mode writes the files back over what is there now. It is
                  refused on a dirty tree rather than offering a force — but everything
                  committed since this snapshot stays only on its branch.
                </p>
              )}
            </div>
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-1.5">
          <Label htmlFor="restore-mode">Mode</Label>
          <select
            id="restore-mode"
            value={mode}
            onChange={(e) => setMode(e.target.value as RestoreMode)}
            className="h-9 w-full rounded-md border bg-transparent px-3 text-sm"
          >
            <option value="branch">branch — a new branch at the snapshot</option>
            <option value="worktree">worktree — put the files back in place</option>
            <option value="patch">patch — a diff, and nothing is written</option>
          </select>
        </div>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button
            variant={mode === "worktree" ? "destructive" : "default"}
            disabled={restore.isPending}
            onClick={() => {
              if (!snapshot) return;
              restore.mutate({ id: snapshot.id, mode }, { onSuccess: () => onClose() });
            }}
          >
            <RotateCcw className="size-3.5" />
            Restore
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function RetentionDialog({
  snapshot,
  onClose,
  repo,
}: {
  snapshot: Snapshot | null;
  onClose: () => void;
  repo?: string;
}) {
  const [value, setValue] = useState("");
  const set = useSetSnapshotRetention(repo);

  return (
    <Dialog
      open={!!snapshot}
      onOpenChange={(o) => {
        if (!o) onClose();
        else setValue(snapshot?.retention ?? "");
      }}
    >
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>How long to keep it</DialogTitle>
          <DialogDescription>
            A Go duration — 72h, 168h, 720h. Empty returns it to the configured default,
            which is {humanDuration(snapshot?.retentionEffective)} for this one.
          </DialogDescription>
        </DialogHeader>
        <Input
          value={value}
          onChange={(e) => setValue(e.target.value)}
          placeholder="720h"
          className="font-mono"
        />
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button
            disabled={set.isPending}
            onClick={() => {
              if (!snapshot) return;
              set.mutate({ id: snapshot.id, retention: value.trim() }, { onSuccess: onClose });
            }}
          >
            Save
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
