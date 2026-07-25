"use client";

import { useMemo, useState } from "react";
import { AnimatePresence, motion } from "motion/react";
import { ArrowDownRight, ArrowUpRight, Minus } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { CopyButton } from "@/components/copy-button";
import { buildArgv, hostReach, OPTIONS, type OptionId } from "@/lib/argv";
import { cn } from "@/lib/utils";

const AGENTS = [
  { id: "claude", label: "claude" },
  { id: "codex", label: "codex" },
  { id: "run", label: "run -- bash" },
];

const DIRECTION_ICON = {
  widens: ArrowUpRight,
  tightens: ArrowDownRight,
  neutral: Minus,
} as const;

const LINE_COLOR: Record<string, string> = {
  base: "text-[#d4d4d8]",
  harden: "text-[#6ee7b7]",
  mount: "text-[#e7e7ea]",
  env: "text-[#fbbf24]",
  image: "text-[#a5b4fc]",
  cmd: "text-white",
};

/**
 * Toggle real flags, watch the real argv. The point is not that the command is
 * long — it is that every host-connected path in it is one you can name, and
 * the tool will print the whole thing before it runs anything.
 */
export function DryRunBuilder({ className }: { className?: string }) {
  const [agent, setAgent] = useState("claude");
  const [on, setOn] = useState<Set<OptionId>>(new Set<OptionId>(["allow"]));

  const lines = useMemo(() => buildArgv(agent, on), [agent, on]);
  const reach = hostReach(agent, on);

  function toggle(id: OptionId) {
    setOn((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }

  const commandLine = useMemo(() => {
    const flags = OPTIONS.filter((o) => on.has(o.id)).map((o) => o.flag);
    return `sandbox-cli ${agent === "run" ? "run" : agent} ${[...flags, "--dry-run"].join(" ")}${
      agent === "run" ? " -- bash" : ""
    }`;
  }, [agent, on]);

  return (
    <div className={cn("grid grid-cols-1 gap-4 lg:grid-cols-[minmax(0,340px)_minmax(0,1fr)]", className)}>
      {/* ------------------------------------------------------- the flags */}
      <div className="flex flex-col overflow-hidden rounded-2xl border bg-card">
        <div className="border-b bg-surface px-4 py-3">
          <p className="eyebrow">agent</p>
          <div className="mt-2 flex flex-wrap gap-1.5">
            {AGENTS.map((a) => (
              <Button
                key={a.id}
                size="xs"
                variant={agent === a.id ? "default" : "outline"}
                onClick={() => setAgent(a.id)}
                className="font-mono text-[0.72rem]"
              >
                {a.label}
              </Button>
            ))}
          </div>
        </div>

        <ul className="flex-1 divide-y">
          {OPTIONS.map((o) => {
            const Icon = DIRECTION_ICON[o.direction];
            const active = on.has(o.id);
            return (
              <li key={o.id}>
                <button
                  type="button"
                  onClick={() => toggle(o.id)}
                  aria-pressed={active}
                  className={cn(
                    "flex w-full items-start gap-3 px-4 py-2.5 text-left transition-colors hover:bg-muted/60",
                    active && "bg-accent/60",
                  )}
                >
                  <span
                    className={cn(
                      "mt-0.5 flex size-4 shrink-0 items-center justify-center rounded border transition-colors",
                      active ? "border-primary bg-primary text-primary-foreground" : "border-input",
                    )}
                  >
                    {active ? (
                      <svg viewBox="0 0 10 10" className="size-2.5 fill-none stroke-current stroke-2">
                        <path d="M1.5 5.2 3.8 7.5 8.5 2.5" strokeLinecap="round" strokeLinejoin="round" />
                      </svg>
                    ) : null}
                  </span>
                  <span className="min-w-0 flex-1">
                    <span className="flex items-center gap-2">
                      <code className="font-mono text-[0.78rem] font-medium">{o.flag}</code>
                      <Icon
                        className={cn(
                          "size-3 shrink-0",
                          o.direction === "widens" && "text-exposed",
                          o.direction === "tightens" && "text-contained",
                          o.direction === "neutral" && "text-muted-foreground",
                        )}
                      />
                    </span>
                    <span className="mt-0.5 block text-xs text-muted-foreground">{o.label}</span>
                  </span>
                </button>
              </li>
            );
          })}
        </ul>
      </div>

      {/* -------------------------------------------------------- the argv */}
      <div className="flex min-w-0 flex-col gap-4">
        <div className="flex flex-wrap items-center justify-between gap-3 rounded-2xl border bg-card px-4 py-3">
          <div className="flex min-w-0 items-center gap-2">
            <span className="font-mono text-contained select-none">$</span>
            <code className="no-scrollbar overflow-x-auto font-mono text-[0.78rem] whitespace-nowrap">
              {commandLine}
            </code>
          </div>
          <div className="flex items-center gap-3">
            <Badge
              variant="outline"
              className={cn(
                "gap-1.5 font-mono text-[0.65rem]",
                reach > 3 ? "border-caution/40 text-caution" : "border-contained/40 text-contained",
              )}
            >
              {reach} host {reach === 1 ? "path" : "paths"} in reach
            </Badge>
            <CopyButton value={commandLine} />
          </div>
        </div>

        <div className="min-w-0 overflow-hidden rounded-2xl border bg-[#0b0b0d]">
          <div className="flex items-center gap-1.5 border-b border-white/8 px-4 py-2.5">
            <span className="size-2 rounded-full bg-white/15" />
            <span className="size-2 rounded-full bg-white/15" />
            <span className="size-2 rounded-full bg-white/15" />
            <span className="ml-2 font-mono text-[0.68rem] text-white/40">
              sandbox-cli --dry-run
            </span>
          </div>
          <div className="no-scrollbar max-h-[460px] overflow-auto px-4 py-3.5">
            <AnimatePresence initial={false} mode="popLayout">
              {lines.map((l) => (
                <motion.div
                  key={`${l.text}-${l.kind}`}
                  layout
                  initial={{ opacity: 0, x: -8 }}
                  animate={{ opacity: 1, x: 0 }}
                  exit={{ opacity: 0, x: 8 }}
                  transition={{ duration: 0.18 }}
                  title={l.why}
                  className={cn(
                    "font-mono text-[0.74rem] leading-relaxed whitespace-pre",
                    LINE_COLOR[l.kind],
                    l.from && "border-l-2 border-[#6ee7b7]/50 pl-2",
                  )}
                >
                  {l.text}
                  {l.kind !== "cmd" ? <span className="text-white/25"> \</span> : null}
                </motion.div>
              ))}
            </AnimatePresence>
          </div>
        </div>

        <p className="text-xs leading-relaxed text-muted-foreground">
          Highlighted lines are the ones your toggles added. Hover any line for what it does. The
          only host paths in the whole command are the <code className="font-mono">--mount</code>{" "}
          sources — count them, and that is the blast radius.
        </p>
      </div>
    </div>
  );
}
