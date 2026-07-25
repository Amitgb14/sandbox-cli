"use client";

import { useState } from "react";
import { Box, GitBranch, GitMerge } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";

const BRANCHES = [
  {
    branch: "feature-a",
    prompt: "implement the API",
    container: "sandbox-dk0gtrd15s2g",
    mem: "412MiB",
    cpu: "82%",
  },
  {
    branch: "feature-b",
    prompt: "port the tests",
    container: "sandbox-9f2la8hq4vzn",
    mem: "308MiB",
    cpu: "61%",
  },
  {
    branch: "docs/rewrite",
    prompt: "rewrite the guide",
    container: "sandbox-m4x1pq7bd0cs",
    mem: "196MiB",
    cpu: "24%",
  },
];

/**
 * Three agents, three branches, three containers, one repo — and your working
 * copy still on whatever branch you left it on. The lines are the point: each
 * sandbox reaches its own worktree and nothing else.
 */
export function ParallelAgents({ className }: { className?: string }) {
  const [hover, setHover] = useState<string | null>(null);

  return (
    <div className={cn("overflow-hidden rounded-2xl border bg-card", className)}>
      <div className="grid grid-cols-1 gap-0 lg:grid-cols-[minmax(0,15rem)_minmax(0,1fr)]">
        {/* the repo */}
        <div className="relative flex flex-col justify-center gap-3 border-b bg-surface px-5 py-6 lg:border-r lg:border-b-0">
          <div className="flex items-center gap-2">
            <GitBranch className="size-4 text-muted-foreground" />
            <code className="font-mono text-sm font-medium">~/projects/app</code>
          </div>
          <p className="text-xs leading-relaxed text-muted-foreground">
            Your checkout, untouched and still on whatever branch you had. The worktrees live in a
            sandbox-owned directory, so the project folder stays clean.
          </p>
          <code className="rounded-md bg-muted px-2 py-1.5 font-mono text-[0.68rem] break-all text-muted-foreground">
            ~/.config/sandbox/worktrees/app-9f95/&lt;branch&gt;
          </code>
        </div>

        {/* the sandboxes */}
        <ul className="divide-y">
          {BRANCHES.map((b, i) => (
            <li
              key={b.branch}
              onMouseEnter={() => setHover(b.branch)}
              onMouseLeave={() => setHover(null)}
              className={cn(
                "relative flex flex-wrap items-center gap-x-4 gap-y-2 px-5 py-4 transition-colors lg:pl-16",
                hover === b.branch && "bg-muted/50",
              )}
            >
              {/* the wire from the repo into this sandbox */}
              <svg
                aria-hidden="true"
                viewBox="0 0 60 40"
                preserveAspectRatio="none"
                className="pointer-events-none absolute top-0 left-0 hidden h-full w-11 lg:block"
              >
                <path
                  d="M0 20 C 28 20, 32 20, 60 20"
                  fill="none"
                  stroke="var(--contained)"
                  strokeWidth="1.2"
                  strokeDasharray="4 6"
                  opacity={hover === b.branch ? 0.9 : 0.35}
                  style={{
                    animation: `marquee-dash 1.6s linear infinite`,
                    animationDelay: `${i * 0.25}s`,
                  }}
                />
              </svg>

              <div className="flex min-w-0 flex-1 flex-col gap-1">
                <div className="flex flex-wrap items-center gap-2">
                  <Badge variant="outline" className="gap-1.5 font-mono text-[0.68rem]">
                    <GitBranch className="size-2.5" />
                    {b.branch}
                  </Badge>
                  <code className="truncate font-mono text-xs text-muted-foreground">
                    -p &quot;{b.prompt}&quot;
                  </code>
                </div>
                <code className="flex items-center gap-1.5 font-mono text-[0.68rem] text-muted-foreground">
                  <Box className="size-3" />
                  {b.container}
                </code>
              </div>

              <div className="flex shrink-0 items-center gap-3 font-mono text-[0.68rem] text-muted-foreground tnum">
                <span>mem {b.mem}</span>
                <span>cpu {b.cpu}</span>
                <span className="flex size-1.5 rounded-full bg-contained" />
              </div>
            </li>
          ))}
        </ul>
      </div>

      <div className="flex flex-wrap items-center gap-x-5 gap-y-2 border-t bg-surface px-5 py-3.5 text-xs text-muted-foreground">
        <span className="inline-flex items-center gap-1.5">
          <GitMerge className="size-3.5" />
          then, from your normal checkout:
        </span>
        <code className="font-mono text-foreground">git diff main...feature-a</code>
        <code className="font-mono text-foreground">
          sandbox-cli worktree commit feature-a -m &quot;…&quot;
        </code>
        <code className="font-mono text-foreground">sandbox-cli worktree rm feature-a</code>
      </div>

      <style>{`@keyframes marquee-dash { to { stroke-dashoffset: -20; } }`}</style>
    </div>
  );
}
