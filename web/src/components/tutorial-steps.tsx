"use client";

import { useState } from "react";
import { AlertTriangle, Check, ChevronDown } from "lucide-react";
import { TUTORIAL_STEPS } from "@/lib/tutorial";
import { CopyButton } from "@/components/copy-button";
import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";

/**
 * The first session, as ten steps you can follow in order.
 *
 * Every step is expanded by default and collapsible rather than the other way
 * round. A tutorial that opens as ten closed rows is a table of contents: it
 * makes the reader decide what to read before they know what any of it is, and
 * the whole point of a *sequence* is that they should not have to. Collapsing is
 * for the second visit, when you already know which step you came back for.
 *
 * `expect` is rendered as its own affordance rather than folded into the prose.
 * Somebody following along needs to know whether to continue or stop, and that
 * question is answered by a different sentence than the one explaining why the
 * step exists.
 */
export function TutorialSteps() {
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
      {TUTORIAL_STEPS.map((s, i) => {
        const open = !collapsed.has(i);
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
              <ChevronDown
                className={cn(
                  "ml-auto size-3.5 shrink-0 self-center text-muted-foreground transition-transform",
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
