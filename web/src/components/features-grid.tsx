"use client";

import { useState } from "react";
import { Eye, GitBranch, KeyRound, Network, Shield } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { FEATURES, GROUP_LABEL, type FeatureGroup } from "@/lib/features";
import { cn } from "@/lib/utils";

const GROUP_ICON: Record<FeatureGroup, typeof Shield> = {
  boundary: Shield,
  credentials: KeyRound,
  network: Network,
  workflow: GitBranch,
  observability: Eye,
};

const GROUPS = Object.keys(GROUP_LABEL) as FeatureGroup[];

const STATE_COPY = {
  default: { label: "on by default", cls: "border-contained/30 text-contained" },
  "opt-out": { label: "on, --flag to disable", cls: "border-contained/30 text-contained" },
  "opt-in": { label: "opt-in", cls: "border-border text-muted-foreground" },
} as const;

export function FeaturesGrid({ className }: { className?: string }) {
  const [filter, setFilter] = useState<FeatureGroup | "all">("all");
  const shown = filter === "all" ? FEATURES : FEATURES.filter((f) => f.group === filter);

  return (
    <div className={cn("flex flex-col gap-6", className)}>
      <div className="no-scrollbar -mx-1 flex gap-1.5 overflow-x-auto px-1">
        <Button
          size="sm"
          variant={filter === "all" ? "default" : "outline"}
          onClick={() => setFilter("all")}
          className="shrink-0"
        >
          Everything
          <span className="ml-1 opacity-60 tnum">{FEATURES.length}</span>
        </Button>
        {GROUPS.map((g) => {
          const Icon = GROUP_ICON[g];
          const n = FEATURES.filter((f) => f.group === g).length;
          return (
            <Button
              key={g}
              size="sm"
              variant={filter === g ? "default" : "outline"}
              onClick={() => setFilter(g)}
              className="shrink-0"
            >
              <Icon className="size-3.5" />
              {GROUP_LABEL[g]}
              <span className="ml-0.5 opacity-60 tnum">{n}</span>
            </Button>
          );
        })}
      </div>

      <ul className="grid grid-cols-1 gap-3 md:grid-cols-2 lg:grid-cols-3">
        {shown.map((f) => {
          const Icon = GROUP_ICON[f.group];
          const state = STATE_COPY[f.state];
          return (
            <li
              key={f.title}
              className="group flex flex-col gap-2.5 rounded-xl border bg-card p-4 transition-colors hover:border-foreground/20"
            >
              <div className="flex items-start justify-between gap-3">
                <Icon className="size-4 shrink-0 text-muted-foreground transition-colors group-hover:text-foreground" />
                <Badge variant="outline" className={cn("shrink-0 text-[0.62rem]", state.cls)}>
                  {state.label}
                </Badge>
              </div>

              <h3 className="text-[0.95rem] leading-snug font-semibold tracking-tight">
                {f.title}
              </h3>

              {f.flag ? (
                <code className="w-fit rounded-md bg-muted px-1.5 py-0.5 font-mono text-[0.7rem] font-medium">
                  {f.flag}
                </code>
              ) : null}

              <p className="text-[0.82rem] leading-relaxed text-muted-foreground">{f.body}</p>

              {f.code ? (
                <pre className="mt-auto rounded-lg bg-[#0b0b0d] px-2.5 py-2 font-mono text-[0.68rem] leading-relaxed break-words whitespace-pre-wrap text-[#d4d4d8]">
                  {f.code}
                </pre>
              ) : null}
            </li>
          );
        })}
      </ul>
    </div>
  );
}
