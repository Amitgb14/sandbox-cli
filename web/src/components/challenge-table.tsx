"use client";

import { useState } from "react";
import { Wrench } from "lucide-react";
import { CHALLENGES, type Challenge } from "@/lib/tutorial";
import { CopyButton } from "@/components/copy-button";
import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";

/**
 * What actually goes wrong, and what to do about it.
 *
 * Written as symptom → cause → fix, in that order, because that is the order
 * somebody arrives in: they have the symptom and nothing else. The cause column
 * is not an apology — nearly every row here is a deliberate default doing its
 * job in a way that reads like a failure the first time, and knowing *which*
 * decision produced the symptom is what tells you whether to work around it or
 * change it.
 *
 * Rows are not sorted by severity. They are in the order you are likely to meet
 * them: upgrade breakage, then first-run network refusals, then the slower
 * costs that only show up after a few weeks of use.
 */

const SCOPES = [
  { id: "all", label: "Everything" },
  { id: "dev", label: "Local development" },
  { id: "prod", label: "Production" },
] as const;

type ScopeId = (typeof SCOPES)[number]["id"];

function matches(c: Challenge, scope: ScopeId) {
  return scope === "all" || c.scope === "both" || c.scope === scope;
}

export function ChallengeTable() {
  const [scope, setScope] = useState<ScopeId>("all");
  const rows = CHALLENGES.filter((c) => matches(c, scope));

  return (
    <div className="flex flex-col gap-5">
      <div
        role="tablist"
        aria-label="Filter challenges by deployment"
        className="flex flex-wrap gap-1.5 rounded-xl border bg-card p-1.5"
      >
        {SCOPES.map((s) => (
          <button
            key={s.id}
            role="tab"
            aria-selected={s.id === scope}
            onClick={() => setScope(s.id)}
            className={cn(
              "flex items-center gap-2 rounded-lg px-3.5 py-2 text-[0.82rem] font-medium transition-colors",
              s.id === scope
                ? "bg-foreground text-background"
                : "text-muted-foreground hover:bg-muted/60 hover:text-foreground",
            )}
          >
            {s.label}
            <span
              className={cn(
                "font-mono text-[0.62rem]",
                s.id === scope ? "text-background/70" : "text-muted-foreground/70",
              )}
            >
              {CHALLENGES.filter((c) => matches(c, s.id)).length}
            </span>
          </button>
        ))}
      </div>

      <div className="flex flex-col gap-3">
        {rows.map((c) => (
          <div key={c.symptom} className="rounded-xl border bg-card px-4 py-3.5">
            <div className="flex flex-wrap items-baseline gap-x-2.5 gap-y-1">
              <h4 className="text-[0.88rem] font-medium">{c.symptom}</h4>
              {c.scope !== "both" ? (
                <Badge
                  variant="outline"
                  className="border-border text-[0.6rem] font-normal text-muted-foreground"
                >
                  {c.scope === "prod" ? "production" : "local development"}
                </Badge>
              ) : null}
            </div>

            <p className="mt-2 text-[0.8rem] leading-relaxed text-muted-foreground">{c.cause}</p>

            <div className="mt-2.5 rounded-lg border border-contained-line bg-contained-soft/40 px-3 py-2.5">
              <div className="flex items-start gap-2.5">
                <Wrench className="mt-0.5 size-3.5 shrink-0 text-contained" />
                <p className="text-[0.79rem] leading-relaxed text-foreground">{c.fix}</p>
              </div>

              {c.fixCode ? (
                <div className="group relative mt-2 overflow-x-auto rounded-md border bg-card px-3 py-2">
                  <pre className="font-mono text-[0.7rem] leading-relaxed whitespace-pre">
                    {c.fixCode}
                  </pre>
                  <div className="absolute top-1.5 right-1.5 opacity-0 transition-opacity group-hover:opacity-100">
                    <CopyButton value={c.fixCode} />
                  </div>
                </div>
              ) : null}
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}
