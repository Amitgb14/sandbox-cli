"use client";

import { useState } from "react";
import { ArrowDownRight, ArrowUpRight, Minus } from "lucide-react";
import { OPTION_GROUPS, type Direction } from "@/lib/tutorial";
import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";

/**
 * Every option, grouped by the question you arrived with.
 *
 * Two columns carry the weight and neither is the flag name. **Default** is
 * first because the most common question about an option is not what it does
 * but whether you need it at all — most of these you do not, and a reference
 * that only lists what a flag *adds* leaves you unable to tell a knob from a
 * requirement. **Direction** is second: whether an option widens or tightens the
 * boundary is the only property that matters for the ones that touch it, and
 * three quarters of them do not touch it at all, which is itself worth seeing.
 *
 * Options refused from a project .sandbox.yaml are marked rather than listed
 * separately. The distinction is not a category of flag — it is a property of
 * *where you may set it*, and it belongs next to the key it constrains.
 */

const DIR_META: Record<Direction, { icon: typeof ArrowUpRight; label: string; cls: string }> = {
  widen: { icon: ArrowUpRight, label: "widens", cls: "text-caution" },
  tighten: { icon: ArrowDownRight, label: "tightens", cls: "text-contained" },
  neutral: { icon: Minus, label: "neutral", cls: "text-muted-foreground/50" },
};

export function OptionCatalog() {
  const [active, setActive] = useState(OPTION_GROUPS[0].id);
  const group = OPTION_GROUPS.find((g) => g.id === active) ?? OPTION_GROUPS[0];

  return (
    <div className="flex flex-col gap-5">
      <div
        role="tablist"
        aria-label="Option group"
        className="flex flex-wrap gap-1.5 rounded-xl border bg-card p-1.5"
      >
        {OPTION_GROUPS.map((g) => (
          <button
            key={g.id}
            role="tab"
            aria-selected={g.id === active}
            onClick={() => setActive(g.id)}
            className={cn(
              "flex items-center gap-2 rounded-lg px-3.5 py-2 text-left text-[0.82rem] font-medium transition-colors",
              g.id === active
                ? "bg-foreground text-background"
                : "text-muted-foreground hover:bg-muted/60 hover:text-foreground",
            )}
          >
            {g.label}
            <span
              className={cn(
                "font-mono text-[0.62rem]",
                g.id === active ? "text-background/70" : "text-muted-foreground/70",
              )}
            >
              {g.options.length}
            </span>
          </button>
        ))}
      </div>

      <p className="text-[0.85rem] leading-relaxed text-muted-foreground">{group.question}</p>

      <div className="overflow-hidden rounded-2xl border bg-card">
        <div className="overflow-x-auto">
          <table className="w-full min-w-[46rem] border-collapse text-left">
            <thead>
              <tr className="border-b">
                <th className="px-4 py-3 text-[0.8rem] font-medium sm:px-5">Flag</th>
                <th className="px-4 py-3 text-[0.8rem] font-medium">What it does</th>
                <th className="px-4 py-3 text-[0.8rem] font-medium">Without it</th>
                <th className="px-4 py-3 text-[0.8rem] font-medium">Boundary</th>
              </tr>
            </thead>
            <tbody>
              {group.options.map((o) => {
                const dir = DIR_META[o.dir];
                return (
                  <tr key={o.flag + o.what} className="border-b last:border-0 align-top">
                    <td className="px-4 py-3 sm:px-5">
                      <code className="block font-mono text-[0.72rem] leading-relaxed text-foreground">
                        {o.flag}
                      </code>
                      {o.key && o.key !== "—" ? (
                        <code className="mt-1 block font-mono text-[0.66rem] text-muted-foreground">
                          {o.key}
                        </code>
                      ) : null}
                      {o.userConfigOnly ? (
                        <Badge
                          variant="outline"
                          className="mt-1.5 border-border text-[0.6rem] font-normal text-muted-foreground"
                        >
                          your config only
                        </Badge>
                      ) : null}
                    </td>
                    <td className="max-w-sm px-4 py-3 text-[0.78rem] leading-relaxed text-foreground">
                      {o.what}
                    </td>
                    <td className="max-w-sm px-4 py-3 text-[0.78rem] leading-relaxed text-muted-foreground">
                      {o.fallback}
                    </td>
                    <td className="px-4 py-3">
                      <span className={cn("flex items-center gap-1.5 text-[0.72rem]", dir.cls)}>
                        <dir.icon className="size-3.5 shrink-0" />
                        {dir.label}
                      </span>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      </div>

      <p className="text-[0.78rem] leading-relaxed text-muted-foreground">
        <span className="font-medium text-foreground">your config only</span> means the key is
        refused from a project&rsquo;s <code className="font-mono text-[0.9em]">.sandbox.yaml</code>{" "}
        — that file travels with the repository and the agent can rewrite it between runs, so it may
        describe the project and never the security boundary. Set those in{" "}
        <code className="font-mono text-[0.9em]">~/.config/sandbox/config.yaml</code>, or load a
        project file you have read with{" "}
        <code className="font-mono text-[0.9em]">--config ./.sandbox.yaml</code>.
      </p>
    </div>
  );
}
