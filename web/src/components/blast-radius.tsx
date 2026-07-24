"use client";

import { useState } from "react";
import { motion } from "motion/react";
import { Cookie, FileKey, FileLock2, Folder, FolderGit2, Globe } from "lucide-react";
import { Switch } from "@/components/ui/switch";
import { cn } from "@/lib/utils";

const PATHS = [
  { path: "~/.ssh/id_rsa", Icon: FileKey, workspace: false },
  { path: "~/.aws/credentials", Icon: FileLock2, workspace: false },
  { path: "~/work/other-repo/.env", Icon: Folder, workspace: false },
  { path: "browser cookies & tokens", Icon: Cookie, workspace: false },
  { path: "the open internet", Icon: Globe, workspace: false },
  { path: "~/projects/myapp", Icon: FolderGit2, workspace: true },
];

/** One switch that flips the whole host filesystem between exposed and contained. */
export function BlastRadius() {
  const [contained, setContained] = useState(false);

  return (
    <div className="mt-12 flex flex-col gap-5">
      <div className="flex flex-wrap items-center justify-center gap-3">
        <span
          className={cn(
            "font-mono text-sm transition-colors",
            contained ? "text-muted-foreground" : "font-semibold text-exposed",
          )}
        >
          Agent unsandboxed
        </span>
        <Switch
          checked={contained}
          onCheckedChange={setContained}
          aria-label="Toggle sandbox-cli containment"
          className="data-[checked]:bg-contained"
        />
        <span
          className={cn(
            "font-mono text-sm transition-colors",
            contained ? "font-semibold text-contained" : "text-muted-foreground",
          )}
        >
          Wrapped in sandbox-cli
        </span>
      </div>

      <div className="flex flex-col gap-1.5 rounded-xl border bg-card p-5 shadow-sm" aria-live="polite">
        {PATHS.map(({ path, Icon, workspace }, i) => {
          const state = !contained ? "reachable" : workspace ? "mounted" : "blocked";
          return (
            <motion.div
              key={path}
              layout
              transition={{ duration: 0.35, delay: i * 0.035, ease: "easeOut" }}
              className={cn(
                "flex items-center gap-2.5 rounded-md border px-2.5 py-2 font-mono text-sm transition-colors duration-500",
                state === "reachable" && "border-exposed/35 bg-exposed-soft text-exposed",
                state === "mounted" && "border-contained/40 bg-contained-soft text-contained",
                state === "blocked" && "border-contained/20 bg-contained/[0.06] text-muted-foreground",
              )}
            >
              <Icon className="size-4 shrink-0" />
              <span>{path}</span>
              {workspace && <span className="opacity-60">— the project</span>}
              <span className="ml-auto text-[0.68rem] uppercase tracking-[0.08em] opacity-90">
                {state === "reachable" ? "reachable" : state === "mounted" ? "mounted" : "not mounted"}
              </span>
            </motion.div>
          );
        })}
      </div>

      <p className="text-center text-sm text-muted-foreground">
        {contained
          ? "One path mounted. The rest was never in the container to begin with."
          : "Everything above is reachable by an agent running with “Allow All”."}
      </p>
    </div>
  );
}
