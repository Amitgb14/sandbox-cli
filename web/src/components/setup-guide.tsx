"use client";

import { useState } from "react";
import { AlertTriangle } from "lucide-react";
import { SETUP_PATHS } from "@/lib/setup";
import { CopyButton } from "@/components/copy-button";
import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";

/**
 * Setup, per platform, ending in `sandbox-cli doctor` every time.
 *
 * The Podman path deliberately says it does not work rather than being left off
 * the page. People will reach for it — rootless Podman is a stronger default
 * than rootful Docker — and an absence reads as an oversight, while
 * instructions that fail read as a lie. Naming the three concrete blockers is
 * more useful than either.
 */
export function SetupGuide() {
  const [active, setActive] = useState(SETUP_PATHS[0].id);
  const path = SETUP_PATHS.find((p) => p.id === active) ?? SETUP_PATHS[0];

  return (
    <div className="flex flex-col gap-5">
      <div
        role="tablist"
        aria-label="Setup platform"
        className="flex flex-wrap gap-1.5 rounded-xl border bg-card p-1.5"
      >
        {SETUP_PATHS.map((p) => (
          <button
            key={p.id}
            role="tab"
            aria-selected={p.id === active}
            onClick={() => setActive(p.id)}
            className={cn(
              "flex flex-col items-start gap-0.5 rounded-lg px-3.5 py-2 text-left transition-colors",
              p.id === active
                ? "bg-foreground text-background"
                : "text-muted-foreground hover:bg-muted/60 hover:text-foreground",
            )}
          >
            <span className="text-[0.82rem] font-medium">{p.label}</span>
            <span
              className={cn(
                "font-mono text-[0.62rem]",
                p.id === active ? "text-background/70" : "text-muted-foreground/70",
              )}
            >
              {p.engine}
            </span>
          </button>
        ))}
      </div>

      {path.unsupported ? (
        <div className="flex items-start gap-3 rounded-xl border border-caution/40 bg-caution/5 px-4 py-3.5">
          <AlertTriangle className="mt-0.5 size-4 shrink-0 text-caution" />
          <p className="text-[0.82rem] leading-relaxed text-muted-foreground">
            <span className="font-medium text-foreground">
              Podman is not supported yet.
            </span>{" "}
            {path.unsupported}
          </p>
        </div>
      ) : null}

      <ol className="flex flex-col gap-3">
        {path.steps.map((s, i) => (
          <li key={s.title} className="rounded-xl border bg-card px-4 py-3.5">
            <div className="flex items-baseline gap-2.5">
              <Badge
                variant="outline"
                className="shrink-0 border-border font-mono text-[0.62rem] font-normal text-muted-foreground"
              >
                {i + 1}
              </Badge>
              <h4 className="text-[0.88rem] font-medium">{s.title}</h4>
            </div>

            {s.code ? (
              <div className="group relative mt-2.5 overflow-x-auto rounded-lg border bg-muted/40 px-3.5 py-2.5">
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
          </li>
        ))}
      </ol>
    </div>
  );
}
