"use client";

import { useState } from "react";
import { CircleAlert, Download, Info, KeyRound, Package } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { CopyButton } from "@/components/copy-button";
import { AGENTS } from "@/lib/agents";
import { cn } from "@/lib/utils";

export function AgentExplorer({ className }: { className?: string }) {
  const [id, setId] = useState(AGENTS[0].id);
  const agent = AGENTS.find((a) => a.id === id) ?? AGENTS[0];

  return (
    <div className={cn("grid grid-cols-1 gap-3 lg:grid-cols-[minmax(0,19rem)_minmax(0,1fr)]", className)}>
      {/* the roster */}
      <ul className="no-scrollbar flex gap-1.5 overflow-x-auto rounded-2xl border bg-card p-1.5 lg:max-h-[30rem] lg:flex-col lg:overflow-y-auto">
        {AGENTS.map((a) => (
          <li key={a.id} className="shrink-0 lg:shrink">
            <button
              type="button"
              onClick={() => setId(a.id)}
              aria-pressed={a.id === id}
              className={cn(
                "flex w-full items-center gap-2.5 rounded-lg px-2.5 py-2 text-left transition-colors",
                a.id === id ? "bg-primary text-primary-foreground" : "hover:bg-muted",
              )}
            >
              <span className="flex min-w-0 flex-1 flex-col">
                <code className="font-mono text-[0.8rem] font-medium">{a.id}</code>
                <span
                  className={cn(
                    "hidden truncate text-[0.7rem] lg:block",
                    a.id === id ? "text-primary-foreground/60" : "text-muted-foreground",
                  )}
                >
                  {a.name} · {a.vendor}
                </span>
              </span>
              <span
                className={cn(
                  "hidden shrink-0 font-mono text-[0.62rem] lg:inline",
                  a.id === id ? "text-primary-foreground/60" : "text-muted-foreground",
                )}
              >
                {a.delivery === "baked" ? "baked" : a.size}
              </span>
            </button>
          </li>
        ))}
      </ul>

      {/* the detail */}
      <div className="flex flex-col gap-3 rounded-2xl border bg-card p-4 sm:p-5">
        <div className="flex flex-wrap items-start justify-between gap-3">
          <div>
            <h3 className="text-lg font-semibold tracking-tight">{agent.name}</h3>
            <p className="text-sm text-muted-foreground">{agent.vendor}</p>
          </div>
          <Badge
            variant="outline"
            className={cn(
              "gap-1.5 text-[0.65rem]",
              agent.delivery === "baked"
                ? "border-contained/30 text-contained"
                : "border-border text-muted-foreground",
            )}
          >
            {agent.delivery === "baked" ? (
              <>
                <Package className="size-2.5" /> in the base image
              </>
            ) : (
              <>
                <Download className="size-2.5" /> installs on first use · {agent.size}
              </>
            )}
          </Badge>
        </div>

        <div className="flex items-center gap-2 rounded-lg border bg-surface px-3 py-2">
          <span className="font-mono text-contained select-none">$</span>
          <code className="no-scrollbar flex-1 overflow-x-auto font-mono text-[0.78rem] whitespace-nowrap">
            {agent.example}
          </code>
          <CopyButton value={agent.example} size="xs" />
        </div>

        <dl className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <div className="flex flex-col gap-1.5">
            <dt className="eyebrow">
              <KeyRound className="size-3" /> login
            </dt>
            <dd className="text-[0.82rem] leading-relaxed text-muted-foreground">{agent.login}</dd>
          </div>
          <div className="flex flex-col gap-1.5">
            <dt className="eyebrow">
              <Info className="size-3" /> persisted at
            </dt>
            <dd>
              <code className="font-mono text-[0.72rem] break-all text-muted-foreground">
                ~/.config/sandbox/agents/{agent.id} {"->"} /sandbox/home
              </code>
            </dd>
          </div>
        </dl>

        <div className="flex flex-col gap-1.5">
          <p className="eyebrow">forwarded only if set</p>
          <ul className="flex flex-wrap gap-1">
            {agent.env.map((e) => (
              <li key={e}>
                <code className="rounded bg-muted px-1.5 py-0.5 font-mono text-[0.68rem] text-muted-foreground">
                  {e}
                </code>
              </li>
            ))}
          </ul>
        </div>

        {agent.allow?.length ? (
          <div className="flex flex-col gap-1.5">
            <p className="eyebrow">add to --allow</p>
            <ul className="flex flex-wrap gap-1">
              {agent.allow.map((d) => (
                <li key={d}>
                  <code className="rounded bg-contained-soft px-1.5 py-0.5 font-mono text-[0.68rem] text-contained">
                    {d}
                  </code>
                </li>
              ))}
            </ul>
          </div>
        ) : null}

        {agent.gotcha ? (
          <p className="mt-auto flex items-start gap-2.5 rounded-lg border border-caution/25 bg-caution-soft px-3 py-2.5 text-[0.8rem] leading-relaxed text-foreground/80">
            <CircleAlert className="mt-0.5 size-3.5 shrink-0 text-caution" />
            {agent.gotcha}
          </p>
        ) : null}
      </div>
    </div>
  );
}
