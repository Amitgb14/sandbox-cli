"use client";

import { useState } from "react";
import { FolderOpen, Lock, ShieldAlert } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Switch } from "@/components/ui/switch";
import { HOST_PATHS, INSIDE_LABEL } from "@/lib/reach";
import { cn } from "@/lib/utils";

/**
 * One switch, twelve paths. On the host every one of them is in reach; inside
 * the sandbox eleven of them are not paths at all. The count in the corner is
 * the number the whole product exists to change.
 */
export function BlastRadius({ className }: { className?: string }) {
  const [sandboxed, setSandboxed] = useState(false);
  const [open, setOpen] = useState<string | null>(null);

  const reachable = sandboxed ? 1 : HOST_PATHS.length;
  const selected = HOST_PATHS.find((p) => p.path === open);

  return (
    <div className={cn("overflow-hidden rounded-2xl border bg-card", className)}>
      <div className="flex flex-wrap items-center justify-between gap-4 border-b bg-surface px-4 py-3.5 sm:px-5">
        <label className="flex cursor-pointer items-center gap-3 text-sm">
          <span
            className={cn(
              "font-medium transition-colors",
              sandboxed ? "text-muted-foreground" : "text-exposed",
            )}
          >
            Agent on your host
          </span>
          <Switch
            checked={sandboxed}
            onCheckedChange={setSandboxed}
            aria-label="Run the agent inside sandbox-cli"
          />
          <span
            className={cn(
              "font-medium transition-colors",
              sandboxed ? "text-contained" : "text-muted-foreground",
            )}
          >
            Agent in sandbox-cli
          </span>
        </label>

        <div className="flex items-baseline gap-2">
          <span
            className={cn(
              "text-2xl font-semibold tnum transition-colors",
              sandboxed ? "text-contained" : "text-exposed",
            )}
          >
            {reachable}
          </span>
          <span className="text-xs text-muted-foreground">
            of {HOST_PATHS.length} host locations in reach
          </span>
        </div>
      </div>

      <ul className="grid grid-cols-1 gap-px bg-border sm:grid-cols-2 lg:grid-cols-3">
        {HOST_PATHS.map((p) => {
          const inReach = !sandboxed || p.inside === "workspace";
          const isWorkspace = p.inside === "workspace";
          return (
            <li key={p.path}>
              <button
                type="button"
                onClick={() => setOpen(open === p.path ? null : p.path)}
                aria-expanded={open === p.path}
                className={cn(
                  "flex h-full w-full flex-col items-start gap-1 px-4 py-3.5 text-left transition-colors duration-300",
                  inReach ? "bg-card" : "bg-surface",
                  open === p.path && "bg-accent",
                )}
              >
                <span className="flex w-full items-center gap-2">
                  {isWorkspace ? (
                    <FolderOpen
                      className={cn(
                        "size-3.5 shrink-0",
                        sandboxed ? "text-contained" : "text-exposed",
                      )}
                    />
                  ) : inReach ? (
                    <ShieldAlert className="size-3.5 shrink-0 text-exposed" />
                  ) : (
                    <Lock className="size-3.5 shrink-0 text-muted-foreground" />
                  )}
                  <code
                    className={cn(
                      "truncate font-mono text-[0.8rem] font-medium transition-colors",
                      inReach ? "text-foreground" : "text-muted-foreground line-through decoration-1",
                    )}
                  >
                    {p.path}
                  </code>
                </span>
                <span className="pl-5.5 text-xs text-muted-foreground">
                  {sandboxed ? INSIDE_LABEL[p.inside] : p.what}
                </span>
              </button>
            </li>
          );
        })}
      </ul>

      <div className="border-t px-4 py-3.5 sm:px-5">
        {selected ? (
          <p className="flex items-start gap-2.5 text-sm">
            <Badge
              variant="outline"
              className="mt-0.5 shrink-0 font-mono text-[0.65rem]"
            >
              {selected.path}
            </Badge>
            <span className="text-muted-foreground">{selected.stake}</span>
          </p>
        ) : (
          <p className="text-sm text-muted-foreground">
            {sandboxed
              ? "Everything but the project is either not mounted or belongs to a container that is deleted on exit. Pick a path to read what was at stake."
              : "This is the default when you run an agent with “Allow All” on your machine. Pick a path to read what is at stake."}
          </p>
        )}
      </div>
    </div>
  );
}
