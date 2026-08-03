"use client";

import { useState } from "react";
import { AlertTriangle, Check, ChevronDown, Globe, MonitorSmartphone, Terminal } from "lucide-react";
import { STUDIO_SIDES, STUDIO_STEPS, type StudioSide } from "@/lib/studio";
import { CopyButton } from "@/components/copy-button";
import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";

/**
 * Setting up Studio, as a sequence — but a two-process one.
 *
 * This is a separate component from TutorialSteps rather than the same list with
 * more rows, because the thing a reader has to hold in their head is different.
 * The CLI tutorial is one terminal: the only question per step is "did that
 * command work". Studio is a daemon and a browser app that must agree about a
 * port, an origin and a token, and every early failure is a disagreement between
 * the two. So each step is *tagged with the side it belongs to* and the legend
 * sits above the list — the reader should always know which of the two things
 * they are currently touching, which a linear list of shell commands cannot say.
 *
 * The filter is not decoration. Coming back to this page usually means one half
 * is already running, and "show me only the daemon steps" is the actual question
 * on a second visit. It defaults to showing everything, because on a first read
 * the sequence is the point and filtering it would hide the interleaving that
 * makes the setup make sense.
 */

const SIDE_ICON: Record<StudioSide, typeof Terminal> = {
  daemon: Terminal,
  ui: MonitorSmartphone,
  browser: Globe,
};

const SIDES: StudioSide[] = ["daemon", "ui", "browser"];

export function StudioTutorial() {
  const [collapsed, setCollapsed] = useState<Set<number>>(new Set());
  const [only, setOnly] = useState<StudioSide | null>(null);

  function toggle(i: number) {
    setCollapsed((prev) => {
      const next = new Set(prev);
      if (next.has(i)) next.delete(i);
      else next.add(i);
      return next;
    });
  }

  return (
    <div className="flex flex-col gap-4">
      {/* The legend doubles as the filter: the labels have to be explained
          anyway, so making them the control costs no extra row. */}
      <div className="flex flex-wrap items-center gap-2">
        <button
          type="button"
          onClick={() => setOnly(null)}
          aria-pressed={only === null}
          className={cn(
            "rounded-full border px-2.5 py-1 text-[0.7rem] transition-colors",
            only === null
              ? "border-foreground/25 bg-muted text-foreground"
              : "border-border text-muted-foreground hover:text-foreground",
          )}
        >
          All {STUDIO_STEPS.length} steps
        </button>
        {SIDES.map((side) => {
          const Icon = SIDE_ICON[side];
          const active = only === side;
          const count = STUDIO_STEPS.filter((s) => s.side === side).length;
          return (
            <button
              key={side}
              type="button"
              onClick={() => setOnly(active ? null : side)}
              aria-pressed={active}
              title={STUDIO_SIDES[side].hint}
              className={cn(
                "flex items-center gap-1.5 rounded-full border px-2.5 py-1 text-[0.7rem] transition-colors",
                active
                  ? "border-foreground/25 bg-muted text-foreground"
                  : "border-border text-muted-foreground hover:text-foreground",
              )}
            >
              <Icon className="size-3" />
              {STUDIO_SIDES[side].label}
              <span className="font-mono text-[0.62rem] opacity-60">{count}</span>
            </button>
          );
        })}
      </div>

      <ol className="flex flex-col gap-3">
        {STUDIO_STEPS.map((s, i) => {
          if (only && s.side !== only) return null;
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
    </div>
  );
}
