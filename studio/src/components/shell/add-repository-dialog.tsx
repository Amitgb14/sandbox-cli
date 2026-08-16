"use client";

import { useEffect, useState } from "react";
import {
  ChevronRight,
  CornerLeftUp,
  FolderGit2,
  Folder,
  Home,
  Check,
} from "lucide-react";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import { Skeleton } from "@/components/ui/skeleton";
import { useAddProject, useBrowse } from "@/lib/api/queries";
import { cn } from "@/lib/utils";
import type { Project } from "@/lib/types";

/**
 * Add a repository for Studio to manage — by browsing the host, or by typing the
 * path.
 *
 * The browsing half runs in the **daemon**, and that is forced rather than
 * chosen: a browser cannot produce a host path. `<input webkitdirectory>` hands
 * over File objects carrying relative paths, and `showDirectoryPicker()` hands
 * over a handle whose `name` is the last segment — neither yields
 * `/Users/you/code/thing`, which is the only form the daemon can mount. So the
 * listing comes from `GET /v1/browse`, which lists directories only, names only,
 * and never dot-directories.
 *
 * The text field stays, and stays authoritative: it is what gets submitted,
 * browsing just fills it in. A repository on a volume the picker will not reach,
 * or one whose root is a dot-directory, is still addable by typing — and pasting
 * a path you already have in your clipboard remains the fastest route.
 *
 * Nothing here validates beyond "not empty". Whether a directory exists, is a
 * git repository, and is not your home directory are facts about the host, and a
 * second opinion offered by a form is one that can be wrong in the reassuring
 * direction. The daemon's refusal is shown verbatim.
 */
export function AddRepositoryDialog({
  open,
  onOpenChange,
  onAdded,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  /** Called with what the daemon recorded — the repository *root*, which is not
   *  necessarily the directory that was picked. */
  onAdded?: (project: Project) => void;
}) {
  const [path, setPath] = useState("");
  // undefined means "wherever the daemon starts you", which is the home
  // directory. Deliberately not seeded with a guess: this client does not know
  // the host's home path until the daemon says.
  const [dir, setDir] = useState<string | undefined>(undefined);
  const add = useAddProject();
  const { data: listing, isPending, isError, error } = useBrowse(dir, open);

  // Reset between openings. A dialog that reopened deep in a tree with a stale
  // path in the field looks like it remembered something it did not.
  useEffect(() => {
    if (!open) return;
    setPath("");
    setDir(undefined);
    add.reset();
    // add.reset is stable for the life of the mutation; re-running on it would
    // clear the error the moment it is set.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  function submit(p = path) {
    const trimmed = p.trim();
    if (!trimmed) return;
    add.mutate(trimmed, {
      onSuccess: (project) => {
        setPath("");
        onOpenChange(false);
        onAdded?.(project);
      },
    });
  }

  const here = listing?.path ?? "";
  const segments = here.split("/").filter(Boolean);

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle className="flex items-center gap-2">
            <FolderGit2 className="size-4" />
            Add a repository
          </DialogTitle>
          <DialogDescription asChild>
            <div className="text-sm">
              Pick a git repository on this machine. Studio records its root, so any
              directory inside it will do — nothing is copied, and nothing runs until you
              launch it.
            </div>
          </DialogDescription>
        </DialogHeader>

        {/* Where you are, one click per level. */}
        <div className="flex flex-wrap items-center gap-1 rounded-md border bg-muted/30 px-2 py-1.5">
          <Button
            variant="ghost"
            size="sm"
            className="h-6 gap-1 px-1.5"
            onClick={() => setDir(listing?.home)}
            title="Home"
          >
            <Home className="size-3.5" />
          </Button>
          {segments.map((seg, i) => {
            const target = "/" + segments.slice(0, i + 1).join("/");
            return (
              <span key={target} className="flex items-center gap-1">
                <ChevronRight className="size-3 text-muted-foreground" />
                <button
                  onClick={() => setDir(target)}
                  className={cn(
                    "rounded px-1 py-0.5 font-mono text-[11px] hover:bg-accent",
                    i === segments.length - 1 && "font-semibold",
                  )}
                >
                  {seg}
                </button>
              </span>
            );
          })}
        </div>

        <div className="max-h-72 min-h-[12rem] overflow-y-auto rounded-md border">
          {isPending && (
            <div className="space-y-2 p-3">
              {Array.from({ length: 5 }, (_, i) => (
                <Skeleton key={i} className="h-6 w-full" />
              ))}
            </div>
          )}
          {isError && (
            // The daemon's own words — "permission denied" on a directory you
            // cannot read is your machine's answer, not a fault to translate.
            <p className="p-3 text-sm text-destructive">
              {error instanceof Error ? error.message : String(error)}
            </p>
          )}
          {listing && (
            <ul className="divide-y">
              {listing.parent && (
                <li>
                  <button
                    className="flex w-full items-center gap-2 px-3 py-2 text-left text-sm hover:bg-accent"
                    onClick={() => setDir(listing.parent)}
                  >
                    <CornerLeftUp className="size-4 text-muted-foreground" />
                    <span className="font-mono text-xs">..</span>
                  </button>
                </li>
              )}
              {listing.entries.map((e) => (
                <li key={e.path}>
                  <div className="flex items-center gap-2 px-3 py-2 hover:bg-accent">
                    <button
                      className="flex min-w-0 flex-1 items-center gap-2 text-left"
                      onClick={() => setDir(e.path)}
                    >
                      {e.repo ? (
                        <FolderGit2 className="size-4 shrink-0 text-primary" />
                      ) : (
                        <Folder className="size-4 shrink-0 text-muted-foreground" />
                      )}
                      <span className="truncate font-mono text-xs">{e.name}</span>
                      {e.registered && (
                        <Badge variant="outline" className="shrink-0 text-[10px]">
                          added
                        </Badge>
                      )}
                    </button>
                    {/* Selecting a repository from the row it is on, rather than
                        making you open it first — the common case is picking a
                        repo you can already see. */}
                    {e.repo && !e.registered && (
                      <Button
                        size="sm"
                        variant="secondary"
                        className="h-6 shrink-0 px-2 text-[11px]"
                        onClick={() => {
                          setPath(e.path);
                          submit(e.path);
                        }}
                        disabled={add.isPending}
                      >
                        <Check className="size-3" />
                        Add
                      </Button>
                    )}
                  </div>
                </li>
              ))}
              {listing.entries.length === 0 && (
                <p className="p-3 text-sm text-muted-foreground">
                  No sub-directories here. Dot-directories are never listed — type the
                  path below to reach one.
                </p>
              )}
            </ul>
          )}
        </div>

        {listing?.truncated && (
          <p className="text-xs text-muted-foreground">
            Showing the first {listing.entries.length} directories only.
          </p>
        )}

        <div className="space-y-2">
          <Label htmlFor="repo-path">Repository path</Label>
          <Input
            id="repo-path"
            spellCheck={false}
            autoComplete="off"
            placeholder={listing?.path ? `${listing.path}/my-project` : "/Users/you/code/my-project"}
            className="font-mono text-sm"
            value={path}
            onChange={(e) => setPath(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") {
                e.preventDefault();
                submit();
              }
            }}
          />
          {add.isError && (
            <p className="text-sm text-destructive">
              {add.error instanceof Error ? add.error.message : String(add.error)}
            </p>
          )}
        </div>

        <DialogFooter className="gap-2 sm:justify-between">
          <Button
            variant="outline"
            onClick={() => setPath(here)}
            disabled={!here}
            title="Put the directory you are browsing into the field"
          >
            Use this folder
          </Button>
          <div className="flex gap-2">
            <Button variant="ghost" onClick={() => onOpenChange(false)}>
              Cancel
            </Button>
            <Button onClick={() => submit()} disabled={!path.trim() || add.isPending}>
              {add.isPending ? "Adding…" : "Add repository"}
            </Button>
          </div>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
