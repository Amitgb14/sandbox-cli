"use client";

import { useState } from "react";
import { AlertTriangle, Check, ChevronDown, Globe, MonitorSmartphone, Terminal } from "lucide-react";
import { STUDIO_SIDES, type StudioSide, type StudioStep } from "@/lib/studio";
import { CopyButton } from "@/components/copy-button";
import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";

/**
 * One numbered, collapsible step list, shared by every Studio walkthrough.
 *
 * Extracted rather than copied because there are now two tracks (compose and
 * manual) plus the section on the landing page, and a step list whose `expect`
 * affordance renders differently in one of the three is a worse problem than the
 * file it saves. The *content* differs per track; the shape of a step — warn
 * first, then the command, then why, then how you know it worked — does not.
 *
 * Steps are expanded by default and collapsible rather than the reverse: a
 * walkthrough that opens as N closed rows is a table of contents, and it makes
 * the reader choose what to read before they know what any of it is.
 */

export const SIDE_ICON: Record<StudioSide, typeof Terminal> = {
  daemon: Terminal,
  ui: MonitorSmartphone,
  browser: Globe,
};

export function StudioStepList({ steps }: { steps: StudioStep[] }) {
  const [collapsed, setCollapsed] = useState<Set<number>>(new Set());

  function toggle(i: number) {
    setCollapsed((prev) => {
      const next = new Set(prev);
      if (next.has(i)) next.delete(i);
      else next.add(i);
      return next;
    });
  }

  return (
    <ol className="flex flex-col gap-3">
      {steps.map((s, i) => {
        const open = !collapsed.has(i);
        const Icon = SIDE_ICON[s.side];
        return (
          <li key={s.title} className="rounded-xl border bg-card">
            <button
              type="button"
              onClick={() => toggle(i)}
              aria-expanded={open}
              className="flex w-full items-baseline gap-2.5 px-4 py-3.5 text-left"
            >
              <Badge
                variant="outline"
                className="shrink-0 border-border font-mono text-[0.62rem] font-normal text-muted-foreground"
              >
                {i + 1}
              </Badge>
              <h4 className="text-[0.88rem] font-medium">{s.title}</h4>
              <span
                className="ml-auto flex shrink-0 items-center gap-1.5 self-center text-[0.68rem] text-muted-foreground"
                title={STUDIO_SIDES[s.side].hint}
              >
                <Icon className="size-3" />
                <span className="hidden sm:inline">{STUDIO_SIDES[s.side].label}</span>
              </span>
              <ChevronDown
                className={cn(
                  "size-3.5 shrink-0 self-center text-muted-foreground transition-transform",
                  !open && "-rotate-90",
                )}
              />
            </button>

            {open ? (
              <div className="px-4 pb-3.5">
                {s.warn ? (
                  <div className="mb-2.5 flex items-start gap-2.5 rounded-lg border border-caution/40 bg-caution/5 px-3 py-2">
                    <AlertTriangle className="mt-0.5 size-3.5 shrink-0 text-caution" />
                    <p className="text-[0.78rem] leading-relaxed text-foreground">{s.warn}</p>
                  </div>
                ) : null}

                {s.code ? (
                  <div className="group relative overflow-x-auto rounded-lg border bg-muted/40 px-3.5 py-2.5">
                    <pre className="font-mono text-[0.72rem] leading-relaxed whitespace-pre">
                      {s.code}
                    </pre>
                    <div className="absolute top-2 right-2 opacity-0 transition-opacity group-hover:opacity-100">
                      <CopyButton value={s.code} />
                    </div>
                  </div>
                ) : null}

                <p className="mt-2.5 text-[0.8rem] leading-relaxed text-muted-foreground">
                  {s.body}
                </p>

                {s.expect ? (
                  <div className="mt-2.5 flex items-start gap-2.5 rounded-lg border border-contained-line bg-contained-soft/40 px-3 py-2">
                    <Check className="mt-0.5 size-3.5 shrink-0 text-contained" strokeWidth={2.4} />
                    <p className="text-[0.78rem] leading-relaxed text-muted-foreground">
                      <span className="font-medium text-foreground">You should see </span>
                      {s.expect}
                    </p>
                  </div>
                ) : null}
              </div>
            ) : null}
          </li>
        );
      })}
    </ol>
  );
}
