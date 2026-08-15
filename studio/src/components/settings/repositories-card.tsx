"use client";

import { useState } from "react";
import { FolderGit2, FolderPlus, Trash2 } from "lucide-react";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { AddRepositoryDialog } from "@/components/shell/add-repository-dialog";
import { useProjects, useRemoveProject } from "@/lib/api/queries";
import { useUi } from "@/lib/store";
import { cn } from "@/lib/utils";
import type { Project } from "@/lib/types";

/**
 * The repositories this Studio manages, and the one control that takes one away.
 *
 * The word on the button is **Forget**, not Remove or Delete, and that is the
 * whole design of this card. Studio holds a *list of directories it will answer
 * about*; taking one off that list touches nothing on disk. A control labelled
 * "Delete" beside a path would make people hesitate over an action that cannot
 * lose anything — and, worse, would make somebody who did not hesitate expect
 * that it had cleaned up.
 *
 * So the button says Forget, the confirmation names the path it is *not*
 * touching, and the daemon backs both up: removing a project only edits
 * ~/.config/sandbox/studio/projects.json.
 */
export function RepositoriesCard() {
  const { data: projects, isPending } = useProjects();
  const remove = useRemoveProject();
  const repoFilter = useUi((s) => s.repoFilter);
  const setRepoFilter = useUi((s) => s.setRepoFilter);
  const [forgetting, setForgetting] = useState<Project | null>(null);
  const [addOpen, setAddOpen] = useState(false);

  return (
    <Card className="surface-sheen gap-4">
      <CardHeader className="flex flex-row items-center justify-between gap-2">
        <CardTitle className="flex items-center gap-2 text-sm font-medium">
          <FolderGit2 className="size-4 text-muted-foreground" />
          Repositories
        </CardTitle>
        <Button variant="outline" size="sm" onClick={() => setAddOpen(true)}>
          <FolderPlus className="size-3.5" />
          Add
        </Button>
      </CardHeader>
      <CardContent className="space-y-3">
        {isPending ? (
          <div className="space-y-2">
            {Array.from({ length: 2 }, (_, i) => (
              <Skeleton key={i} className="h-10 w-full" />
            ))}
          </div>
        ) : (
          <ul className="divide-y rounded-md border">
            {(projects ?? []).map((p) => (
              <li key={p.id} className="flex flex-wrap items-center gap-2 p-3">
                <span className="min-w-0 flex-1">
                  <span className="flex items-center gap-1.5 text-sm">
                    {p.name}
                    {p.default && (
                      <Badge variant="outline" className="text-[10px]">
                        started here
                      </Badge>
                    )}
                    {p.missing && (
                      <Badge variant="outline" className="text-[10px] text-caution">
                        unavailable
                      </Badge>
                    )}
                  </span>
                  <span
                    className={cn(
                      "block truncate font-mono text-[10px] text-muted-foreground",
                      p.missing && "line-through",
                    )}
                  >
                    {p.root}
                  </span>
                </span>

                {p.default ? (
                  <Tooltip>
                    <TooltipTrigger asChild>
                      <span>
                        <Button variant="ghost" size="sm" className="h-7 text-xs" disabled>
                          <Trash2 className="size-3.5" />
                          Forget
                        </Button>
                      </span>
                    </TooltipTrigger>
                    <TooltipContent className="max-w-xs">
                      This is the repository the daemon was started in — what every request
                      naming no repository is about. It would be back on the next listing, so
                      changing it is a restart:{" "}
                      <code className="font-mono">studio.sh up --project DIR</code>.
                    </TooltipContent>
                  </Tooltip>
                ) : (
                  <Button
                    variant="ghost"
                    size="sm"
                    className="h-7 text-xs"
                    onClick={() => setForgetting(p)}
                  >
                    <Trash2 className="size-3.5" />
                    Forget
                  </Button>
                )}
              </li>
            ))}
          </ul>
        )}
        <p className="text-xs text-muted-foreground">
          Forgetting a repository takes it off this list and nothing else. The checkout, its
          branches, its worktrees and any containers it has run stay exactly where they are —
          add it again by path and its history comes back with it.
        </p>
      </CardContent>

      <Dialog open={!!forgetting} onOpenChange={(o) => !o && setForgetting(null)}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>Forget {forgetting?.name}?</DialogTitle>
            <DialogDescription asChild>
              <div className="space-y-2 text-sm">
                <p>Studio stops listing it, and stops scoping screens to it.</p>
                <p>
                  {/* Named explicitly, because this is the fear the word "remove"
                      creates and the only thing worth saying here. */}
                  Nothing on disk is touched.{" "}
                  <code className="font-mono text-xs">{forgetting?.root}</code> stays where it
                  is, with its branches and worktrees intact.
                </p>
              </div>
            </DialogDescription>
          </DialogHeader>
          <DialogFooter className="gap-2">
            <Button variant="outline" onClick={() => setForgetting(null)}>
              Cancel
            </Button>
            <Button
              disabled={remove.isPending}
              onClick={() => {
                const id = forgetting!.id;
                remove.mutate(id, {
                  onSuccess: () => {
                    // A scope pointing at a repository that is no longer listed
                    // filters every screen to empty, which reads as "nothing here"
                    // rather than "that one is gone".
                    if (repoFilter === id) setRepoFilter(null);
                    setForgetting(null);
                  },
                });
              }}
            >
              {remove.isPending ? "Forgetting…" : "Forget it"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <AddRepositoryDialog open={addOpen} onOpenChange={setAddOpen} />
    </Card>
  );
}
