"use client";

import { useState } from "react";
import { SESSION_COMMANDS, SESSION_NOTE } from "@/lib/sessions";
import { cn } from "@/lib/utils";

/**
 * The four session commands, one tab each, showing the terminal output verbatim.
 *
 * Tabs rather than four stacked frames because the commands are alternatives to
 * each other — you reach for exactly one — and because the tab row doubles as
 * the list of what exists, which is the thing a reader is actually missing.
 *
 * The frames scroll horizontally instead of wrapping: `list` prints a
 * tab-aligned table, and a wrapped column is a table that has stopped being one.
 */
export function SessionCommands({ className }: { className?: string }) {
  const [active, setActive] = useState(SESSION_COMMANDS[0].id);
  const cmd = SESSION_COMMANDS.find((c) => c.id === active) ?? SESSION_COMMANDS[0];

  return (
    <div className={cn("flex flex-col gap-3", className)}>
      <div role="tablist" aria-label="Session commands" className="flex flex-wrap gap-2">
        {SESSION_COMMANDS.map((c) => {
          const on = c.id === active;
          return (
            <button
              key={c.id}
              type="button"
              role="tab"
              aria-selected={on}
              onClick={() => setActive(c.id)}
              className={cn(
                "rounded-full border px-3 py-1 font-mono text-[0.72rem] transition-colors",
                on
                  ? "border-foreground/25 bg-muted text-foreground"
                  : "border-border text-muted-foreground hover:text-foreground",
              )}
            >
              {c.label}
            </button>
          );
        })}
      </div>

      <p className="text-[0.85rem] leading-relaxed text-muted-foreground">{cmd.blurb}</p>

      <div className="flex flex-col gap-2">
        {cmd.frames.map((f) => (
          <div key={f.prompt} className="overflow-hidden rounded-xl border bg-card">
            <div className="no-scrollbar overflow-x-auto bg-[#0b0b0d] px-3 py-2.5 font-mono text-[0.72rem] leading-relaxed whitespace-pre">
              <div className="text-[#e7e7ea]">
                <span className="text-[#6ee7b7]">$ </span>
                {f.prompt}
              </div>
              {f.header ? <div className="mt-1.5 text-white/45">{f.header}</div> : null}
              {f.rows.map((r) => (
                <div key={r} className="text-[#e7e7ea]">
                  {r}
                </div>
              ))}
              {f.trailing?.map((t) => (
                <div key={t} className="mt-1.5 text-[#8a8a94]">
                  {t}
                </div>
              ))}
            </div>
          </div>
        ))}
      </div>

      <p className="text-[0.85rem] leading-relaxed text-muted-foreground">{cmd.note}</p>

      <p className="rounded-lg border bg-surface px-3 py-2.5 text-[0.8rem] leading-relaxed text-muted-foreground">
        {SESSION_NOTE}
      </p>
    </div>
  );
}
