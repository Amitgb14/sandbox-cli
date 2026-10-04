"use client";

import { useState } from "react";
import { CopyButton } from "@/components/common/copy-button";
import { cn } from "@/lib/utils";

/**
 * One snippet per client, in tabs: the Playground's code panel and the
 * empty-state quick start. The last tab chosen is kept for the session, so
 * someone who writes Python is not walked back to the CLI on every screen.
 */
let remembered = "";

export function CodeTabs({
  tabs,
  note,
  className,
}: {
  tabs: { id: string; label: string; code: string }[];
  note?: React.ReactNode;
  className?: string;
}) {
  const [active, setActive] = useState(() => (tabs.some((t) => t.id === remembered) ? remembered : tabs[0]?.id));
  const tab = tabs.find((t) => t.id === active) ?? tabs[0];
  if (!tab) return null;
  return (
    <div className={cn("surface-sheen overflow-hidden rounded-lg border bg-[#0c0c0f] text-[13px]", className)}>
      <div className="flex items-center gap-1 border-b border-white/5 px-1.5 py-1">
        <div role="tablist" aria-label="Client" className="flex flex-1 gap-0.5 overflow-x-auto">
          {tabs.map((t) => (
            <button
              key={t.id}
              type="button"
              role="tab"
              aria-selected={t.id === tab.id}
              onClick={() => {
                remembered = t.id;
                setActive(t.id);
              }}
              className={cn(
                "rounded px-2 py-1 font-mono text-[11px] whitespace-nowrap transition-colors",
                t.id === tab.id ? "bg-white/10 text-white" : "text-white/45 hover:text-white/80",
              )}
            >
              {t.label}
            </button>
          ))}
        </div>
        <CopyButton value={tab.code} className="hover:bg-white/10" />
      </div>
      <pre className="scrollbar-thin overflow-x-auto p-3 font-mono leading-relaxed break-words whitespace-pre-wrap text-zinc-200">{tab.code}</pre>
      {note ? <p className="border-t border-white/5 px-3 py-2 text-[11px] leading-relaxed text-white/45">{note}</p> : null}
    </div>
  );
}
