"use client";

import { useState } from "react";
import Link from "next/link";
import {
  Copy,
  ExternalLink,
  FileDiff,
  MoreHorizontal,
  ScrollText,
  Skull,
  Square,
  Terminal,
} from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { useKillRun, useStopRun } from "@/lib/api/queries";
import { buildArgv } from "@/lib/mock/data";
import { formatArgv } from "@/lib/format";
import type { Run } from "@/lib/types";

export function RunRowActions({ run }: { run: Run }) {
  const [killOpen, setKillOpen] = useState(false);
  const stop = useStopRun();
  const kill = useKillRun();
  const live = run.state === "running";

  return (
    <>
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button
            variant="ghost"
            size="icon"
            className="size-7 text-muted-foreground data-[state=open]:bg-accent"
            aria-label="Row actions"
          >
            <MoreHorizontal className="size-4" />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" className="w-52">
          <DropdownMenuLabel className="font-mono text-xs">
            {run.branch ?? run.name}
          </DropdownMenuLabel>
          <DropdownMenuSeparator />
          <DropdownMenuItem asChild>
            <Link href={`/runs/${run.id}`}>
              <ExternalLink className="size-3.5" />
              Open run
            </Link>
          </DropdownMenuItem>
          <DropdownMenuItem asChild>
            <Link href={`/runs/${run.id}?tab=terminal`}>
              <Terminal className="size-3.5" />
              {live ? "Watch terminal" : "Read terminal"}
            </Link>
          </DropdownMenuItem>
          <DropdownMenuItem asChild>
            <Link href={`/runs/${run.id}?tab=diff`}>
              <FileDiff className="size-3.5" />
              What it changed
            </Link>
          </DropdownMenuItem>
          <DropdownMenuItem asChild>
            <Link href={`/runs/${run.id}?tab=logs`}>
              <ScrollText className="size-3.5" />
              Logs
            </Link>
          </DropdownMenuItem>
          <DropdownMenuSeparator />
          <DropdownMenuItem
            onClick={() => {
              navigator.clipboard.writeText(run.id);
              toast.success("Container id copied");
            }}
          >
            <Copy className="size-3.5" />
            Copy container id
          </DropdownMenuItem>
          <DropdownMenuItem
            onClick={() => {
              navigator.clipboard.writeText(formatArgv(buildArgv(run)));
              toast.success("Command copied", {
                description: "The argv as this run was started, in BuildArgs order.",
              });
            }}
          >
            <Copy className="size-3.5" />
            Copy docker argv
          </DropdownMenuItem>

          {live && (
            <>
              <DropdownMenuSeparator />
              <DropdownMenuItem
                onClick={() =>
                  stop.mutate(run.id, {
                    onSuccess: () =>
                      toast.success(`Asked ${run.branch ?? run.name} to exit`, {
                        description: "The guest gets a grace period to finish writing.",
                      }),
                  })
                }
              >
                <Square className="size-3.5" />
                Stop
                <span className="ml-auto text-[10px] text-muted-foreground">graceful</span>
              </DropdownMenuItem>
              <DropdownMenuItem
                variant="destructive"
                onClick={() => setKillOpen(true)}
              >
                <Skull className="size-3.5" />
                Kill…
              </DropdownMenuItem>
            </>
          )}
        </DropdownMenuContent>
      </DropdownMenu>

      {/* Killing an agent costs its work, so it asks. Reading the wrong session
          costs a second; stopping the wrong agent costs its work — which is why
          only this one confirms. */}
      <Dialog open={killOpen} onOpenChange={setKillOpen}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>Kill this sandbox?</DialogTitle>
            <DialogDescription asChild>
              <div className="space-y-2 text-sm">
                <p>
                  <span className="font-mono">{run.branch ?? run.name}</span> is terminated
                  immediately, with no chance to finish what it was writing.
                </p>
                <p>
                  Its files are on a bind mount, so whatever is already saved stays. Anything
                  mid-write does not. If you want the agent to close what it has open, stop it
                  instead.
                </p>
              </div>
            </DialogDescription>
          </DialogHeader>
          <DialogFooter className="gap-2 sm:justify-between">
            <Button
              variant="outline"
              onClick={() => {
                setKillOpen(false);
                stop.mutate(run.id, {
                  onSuccess: () => toast.success("Asked it to exit instead"),
                });
              }}
            >
              <Square className="size-3.5" />
              Stop gracefully
            </Button>
            <Button
              variant="destructive"
              disabled={kill.isPending}
              onClick={() => {
                kill.mutate(run.id, {
                  onSuccess: () => {
                    setKillOpen(false);
                    toast.warning(`Killed ${run.branch ?? run.name}`);
                  },
                });
              }}
            >
              <Skull className="size-3.5" />
              Kill now
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  );
}
