"use client";

import { useState } from "react";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { useAddRepo } from "@/lib/api/queries";
import type { Repo } from "@/lib/types";

/**
 * Registering a repository is the one request that names a host path, so the
 * dialog says what the server will check rather than letting a refusal be the
 * first anyone hears of it.
 */
export function AddRepositoryDialog({
  open,
  onOpenChange,
  onAdded,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onAdded: (repo: Repo) => void;
}) {
  const [path, setPath] = useState("");
  const add = useAddRepo();
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Add a repository</DialogTitle>
          <DialogDescription>
            An absolute path to a git checkout on this machine. Its root is registered — not the
            directory you name — and refused if it is your home directory or a directory above it.
          </DialogDescription>
        </DialogHeader>
        <form
          className="flex flex-col gap-3"
          onSubmit={(e) => {
            e.preventDefault();
            add.mutate(path, {
              onSuccess: (repo) => {
                onAdded(repo);
                onOpenChange(false);
                setPath("");
              },
            });
          }}
        >
          <Label htmlFor="repo-path">Path</Label>
          <Input
            id="repo-path"
            autoFocus
            placeholder="/home/you/src/app"
            className="font-mono"
            value={path}
            onChange={(e) => setPath(e.target.value)}
          />
          {add.error ? <p className="text-sm text-destructive">{add.error.message}</p> : null}
          <DialogFooter>
            <Button type="submit" disabled={!path.trim() || add.isPending}>
              {add.isPending ? "Adding…" : "Add"}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
