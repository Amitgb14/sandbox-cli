"use client";

import { useEffect, useState } from "react";
import {
  ChevronRight,
  File as FileIcon,
  Folder,
  Link2,
  RotateCw,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { useSearchParams } from "next/navigation";
import { PageHeader } from "@/components/common/page-header";
import { useFileContent, useFiles, useProjects, useWorktrees } from "@/lib/api/queries";
import { BranchPicker, branchValue } from "@/components/editor/branch-picker";
import { useUi } from "@/lib/store";
import { formatBytesShort } from "@/lib/format";
import { cn } from "@/lib/utils";
import type { FileEntry } from "@/lib/types";

/**
 * Browse the scoped repository's files.
 *
 * Read-only, and that is a boundary decision rather than an unfinished feature.
 * The agent edits the workspace from inside a container; a control plane that
 * could also write to it over HTTP would be a second editor for the same tree
 * with none of the isolation that makes the first one safe.
 *
 * Every path on this screen came from the daemon — a row's `path` is sent back
 * verbatim as the next request. Nothing here assembles one, because the string
 * this page hands over is exactly the string the daemon's containment check is
 * about, and a client that built its own would be inventing the input to a
 * security decision.
 */
export default function FilesPage() {
  const repoFilter = useUi((s) => s.repoFilter);
  const { data: projects } = useProjects();
  const [dir, setDir] = useState("");
  const [open, setOpen] = useState<string | null>(null);
  // "" is the repository's own checkout; anything else is that branch's
  // worktree, which is a different directory on disk.
  const [branch, setBranch] = useState("");

  // ?branch= arrives from the Changes screen's "Browse these files", so the two
  // Editor screens stay on the same branch when you move between them. Applied
  // once per distinct value rather than on every render, or it would fight the
  // picker the moment somebody changed it.
  const search = useSearchParams();
  const linked = search.get("branch");
  useEffect(() => {
    if (linked) setBranch(linked);
  }, [linked]);

  const repo = projects?.find((p) => (repoFilter ? p.id === repoFilter : p.default));
  // What to call the branch on screen. With no worktree picked this is the
  // checkout's own branch, which the daemon reports — the header used to say
  // nothing at all there, so the one case where you are looking at `main` was
  // the one case the screen would not name.
  const { data: worktrees } = useWorktrees();
  const branchLabel = branch || worktrees?.find((w) => w.primary)?.branch || "";
  const { data: listing, isPending, isError, error, refetch, isFetching } = useFiles(
    dir,
    branch || undefined,
  );

  // The trail, built from the directory the daemon reported rather than from
  // what was clicked — they agree, and reading it back means the crumbs cannot
  // drift from what is actually listed.
  const segments = (listing?.path ?? dir).split("/").filter(Boolean);

  return (
    <div className="space-y-5">
      <PageHeader
        title="Files"
        description={
          <>
            The working tree of{" "}
            <span className="font-medium">{repo?.name ?? "the scoped repository"}</span>
            {branchLabel ? (
              <>
                {" "}on <code className="font-mono text-xs">{branchLabel}</code>
              </>
            ) : null}
            , as it stands on disk right now — including whatever an agent has written and not
            committed. Read-only: edits happen inside the sandbox, not from here.
          </>
        }
        actions={
          <>
            <BranchPicker
              value={branchValue(branch)}
              onChange={(b) => {
                // Both reset: a path in one branch's worktree need not exist in
                // another's, and keeping the old selection would ask for a file
                // that is not there and render the refusal as if it were a bug.
                setBranch(b);
                setDir("");
                setOpen(null);
              }}
              className="w-[16rem]"
            />
            <Button variant="outline" size="sm" onClick={() => refetch()} disabled={isFetching}>
              <RotateCw className={cn("size-4", isFetching && "animate-spin")} />
              Refresh
            </Button>
          </>
        }
      />

      {/* Breadcrumbs. The root is always reachable in one click, which matters
          more here than anywhere else: a deep path is where people get stuck. */}
      <div className="flex flex-wrap items-center gap-1 text-sm">
        <button
          onClick={() => setDir("")}
          className={cn(
            "rounded px-1.5 py-0.5 font-mono text-xs hover:bg-accent",
            segments.length === 0 && "font-semibold",
          )}
        >
          {repo?.name ?? "repository"}
        </button>
        {segments.map((seg, i) => {
          const href = segments.slice(0, i + 1).join("/");
          return (
            <span key={href} className="flex items-center gap-1">
              <ChevronRight className="size-3 text-muted-foreground" />
              <button
                onClick={() => setDir(href)}
                className={cn(
                  "rounded px-1.5 py-0.5 font-mono text-xs hover:bg-accent",
                  i === segments.length - 1 && "font-semibold",
                )}
              >
                {seg}
              </button>
            </span>
          );
        })}
      </div>

      <div className="grid gap-4 lg:grid-cols-[minmax(0,22rem)_minmax(0,1fr)]">
        <Card className="h-fit">
          <CardHeader className="pb-3">
            <CardTitle className="text-sm">
              {listing?.entries.length ?? 0} {listing?.entries.length === 1 ? "entry" : "entries"}
              {listing?.truncated && (
                <Badge variant="outline" className="ml-2 text-[10px]">
                  first {listing.entries.length} only
                </Badge>
              )}
            </CardTitle>
          </CardHeader>
          <CardContent className="max-h-[70vh] overflow-y-auto p-0">
            {isPending && (
              <div className="space-y-2 p-4">
                {Array.from({ length: 6 }, (_, i) => (
                  <Skeleton key={i} className="h-6 w-full" />
                ))}
              </div>
            )}
            {isError && (
              <p className="p-4 text-sm text-destructive">
                {error instanceof Error ? error.message : String(error)}
              </p>
            )}
            {!isPending && !isError && listing?.entries.length === 0 && (
              <p className="p-4 text-sm text-muted-foreground">This directory is empty.</p>
            )}
            <ul className="divide-y">
              {dir !== "" && (
                <li>
                  <button
                    className="flex w-full items-center gap-2 px-4 py-2 text-left text-sm hover:bg-accent"
                    onClick={() => setDir(segments.slice(0, -1).join("/"))}
                  >
                    <Folder className="size-4 text-muted-foreground" />
                    <span className="font-mono text-xs">..</span>
                  </button>
                </li>
              )}
              {listing?.entries.map((e) => (
                <EntryRow
                  key={e.path}
                  entry={e}
                  selected={open === e.path}
                  onOpen={() => (e.dir ? (setDir(e.path), setOpen(null)) : setOpen(e.path))}
                />
              ))}
            </ul>
          </CardContent>
        </Card>

        <FileViewer path={open} branch={branch} />
      </div>
    </div>
  );
}

function EntryRow({
  entry,
  selected,
  onOpen,
}: {
  entry: FileEntry;
  selected: boolean;
  onOpen: () => void;
}) {
  return (
    <li>
      <button
        onClick={onOpen}
        className={cn(
          "flex w-full items-center gap-2 px-4 py-2 text-left text-sm hover:bg-accent",
          selected && "bg-accent",
        )}
      >
        {entry.dir ? (
          <Folder className="size-4 shrink-0 text-muted-foreground" />
        ) : (
          <FileIcon className="size-4 shrink-0 text-muted-foreground" />
        )}
        <span className="min-w-0 flex-1 truncate font-mono text-xs">{entry.name}</span>
        {/* A symlink is marked rather than resolved: opening it may be refused,
            because a link out of the repository is not readable through this
            API, and a row that looked like any other would make that refusal
            look like a bug. */}
        {entry.symlink && <Link2 className="size-3 shrink-0 text-muted-foreground" />}
        {!entry.dir && entry.size !== undefined && (
          <span className="shrink-0 text-[10px] tabular-nums text-muted-foreground">
            {formatBytesShort(entry.size)}
          </span>
        )}
      </button>
    </li>
  );
}

function FileViewer({ path, branch }: { path: string | null; branch: string }) {
  const { data, isPending, isError, error } = useFileContent(path, branch || undefined);

  if (!path) {
    return (
      <Card className="flex min-h-[16rem] items-center justify-center">
        <p className="p-6 text-sm text-muted-foreground">Pick a file to read it.</p>
      </Card>
    );
  }

  return (
    <Card className="min-w-0">
      <CardHeader className="pb-3">
        <CardTitle className="flex flex-wrap items-center gap-2 text-sm">
          <span className="min-w-0 truncate font-mono text-xs">{path}</span>
          {data && (
            <span className="text-[10px] font-normal text-muted-foreground">
              {formatBytesShort(data.size)}
            </span>
          )}
          {data?.binary && (
            <Badge variant="outline" className="text-[10px]">
              binary
            </Badge>
          )}
          {data?.truncated && (
            <Badge variant="outline" className="text-[10px]">
              truncated
            </Badge>
          )}
        </CardTitle>
      </CardHeader>
      <CardContent className="min-w-0">
        {isPending && <Skeleton className="h-64 w-full" />}
        {isError && (
          // The daemon's own words. "No such file in this repository" is what a
          // symlink pointing outside answers with, and rewriting it here would
          // turn a boundary into a mystery.
          <p className="text-sm text-destructive">
            {error instanceof Error ? error.message : String(error)}
          </p>
        )}
        {data?.binary && (
          <p className="text-sm text-muted-foreground">
            {formatBytesShort(data.size)} of binary content, not shown — rendering it as text
            would be noise, and the size is the useful fact about it.
          </p>
        )}
        {data && !data.binary && (
          <>
            {data.truncated && (
              <p className="mb-2 text-xs text-muted-foreground">
                Showing the first {formatBytesShort(data.content?.length ?? 0)} of{" "}
                {formatBytesShort(data.size)}.
              </p>
            )}
            {/* Its own scroll container, so a long line scrolls the code rather
                than the page. */}
            <pre className="max-h-[70vh] overflow-auto rounded-md bg-muted/40 p-3 font-mono text-xs leading-relaxed">
              {data.content}
            </pre>
          </>
        )}
      </CardContent>
    </Card>
  );
}
