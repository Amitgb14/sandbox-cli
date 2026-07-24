"use client";

import { useState } from "react";
import { motion } from "motion/react";
import { Badge } from "@/components/ui/badge";
import { AGENTS, AGENT_BY_KEY } from "@/lib/agents";
import { cn } from "@/lib/utils";

export function AgentExplorer() {
  const [selected, setSelected] = useState("claude");
  const agent = AGENT_BY_KEY[selected];

  return (
    <div className="flex flex-col gap-4">
      <div className="grid grid-cols-2 gap-2 sm:grid-cols-3 lg:grid-cols-5">
        {AGENTS.map((a) => {
          const on = a.key === selected;
          return (
            <button
              key={a.key}
              onClick={() => setSelected(a.key)}
              aria-pressed={on}
              className={cn(
                "flex flex-col gap-0.5 rounded-lg border p-3 text-left transition-all",
                on
                  ? "border-contained bg-contained/[0.08]"
                  : "bg-card hover:-translate-y-0.5 hover:border-contained/45",
              )}
            >
              <span className={cn("text-sm font-medium", on && "text-contained")}>{a.name}</span>
              <span className="font-mono text-[0.7rem] text-muted-foreground">
                <span className="text-contained">$ </span>
                sandbox-cli {a.key}
              </span>
            </button>
          );
        })}
      </div>

      <motion.div
        key={selected}
        initial={{ opacity: 0, y: 8 }}
        animate={{ opacity: 1, y: 0 }}
        transition={{ duration: 0.25 }}
        className="flex flex-col gap-4 rounded-xl border bg-card p-6 shadow-sm"
        aria-live="polite"
      >
        <div className="flex flex-wrap items-baseline justify-between gap-3">
          <h3 className="font-heading text-xl font-bold">{agent.name}</h3>
          <div className="flex flex-wrap gap-2">
            <Badge variant={agent.baked ? "default" : "secondary"} className="font-mono text-[0.68rem]">
              {agent.baked ? "baked into the image" : "installed on first run"}
            </Badge>
            <Badge variant="outline" className="font-mono text-[0.68rem]">
              ~/.config/sandbox/agents/{agent.key}
            </Badge>
          </div>
        </div>

        <div className="no-scrollbar flex items-center gap-2 overflow-x-auto rounded-md border bg-background px-3 py-2.5 font-mono text-sm">
          <span className="font-semibold text-contained">$</span>
          <span className="whitespace-nowrap">sandbox-cli {agent.key}</span>
        </div>

        <div className="grid gap-5 md:grid-cols-3">
          <Cell title="Install route">
            <p>{agent.install}</p>
          </Cell>

          <Cell title="Forwarded when set">
            <div className="flex flex-wrap gap-1.5">
              {agent.env.map((e) => (
                <code
                  key={e}
                  className="rounded border border-signal/40 bg-muted px-1.5 py-0.5 font-mono text-[0.68rem] text-signal"
                >
                  {e}
                </code>
              ))}
            </div>
            {agent.sets && (
              <div className="mt-2 flex flex-wrap gap-1.5">
                <span className="font-mono text-[0.66rem] uppercase tracking-wider text-muted-foreground">
                  sandbox sets:
                </span>
                {agent.sets.map((s) => (
                  <code
                    key={s}
                    className="rounded border border-contained/40 bg-contained-soft px-1.5 py-0.5 font-mono text-[0.68rem] text-contained"
                  >
                    {s}
                  </code>
                ))}
              </div>
            )}
          </Cell>

          <Cell title="Worth knowing">
            <p>{agent.note}</p>
          </Cell>
        </div>
      </motion.div>
    </div>
  );
}

function Cell({ title, children }: { title: string; children: React.ReactNode }) {
  return (
    <div className="flex flex-col gap-1.5">
      <h4 className="font-mono text-[0.66rem] uppercase tracking-[0.14em] text-muted-foreground">
        {title}
      </h4>
      <div className="text-sm text-muted-foreground">{children}</div>
    </div>
  );
}
