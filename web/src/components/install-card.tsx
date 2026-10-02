"use client";

import { useState } from "react";
import { ArrowUpRight, Check } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { CopyButton } from "@/components/copy-button";
import { WindowChrome } from "@/components/code-block";
import { INSTALL_ROUTES, RELEASES_URL, VERSION } from "@/lib/site";
import { cn } from "@/lib/utils";

export function InstallCard({ className }: { className?: string }) {
  const [route, setRoute] = useState(INSTALL_ROUTES[0].id);
  const active = INSTALL_ROUTES.find((r) => r.id === route) ?? INSTALL_ROUTES[0];

  return (
    <div
      className={cn(
        "overflow-hidden rounded-2xl border bg-card shadow-[0_1px_2px_rgba(9,9,11,0.04),0_16px_50px_-30px_rgba(9,9,11,0.3)]",
        className,
      )}
    >
      <div className="flex flex-wrap items-center justify-between gap-3 border-b px-4 py-3">
        <div className="flex items-center gap-2.5">
          <Badge className="gap-1.5 font-mono text-[0.7rem]">
            <Check className="size-3" />v{VERSION}
          </Badge>
          <span className="text-xs text-muted-foreground">latest release</span>
        </div>
        <a
          href={RELEASES_URL}
          target="_blank"
          rel="noopener noreferrer"
          className="inline-flex items-center gap-1 text-xs text-muted-foreground transition-colors hover:text-foreground"
        >
          all releases <ArrowUpRight className="size-3" />
        </a>
      </div>

      <Tabs value={route} onValueChange={(v) => setRoute(String(v))} className="gap-0">
        <div className="no-scrollbar overflow-x-auto border-b px-2">
          <TabsList variant="line" className="h-9 gap-0.5">
            {INSTALL_ROUTES.map((r) => (
              <TabsTrigger
                key={r.id}
                value={r.id}
                className="px-2.5 text-[0.8rem] whitespace-nowrap"
              >
                {r.label}
              </TabsTrigger>
            ))}
          </TabsList>
        </div>

        {INSTALL_ROUTES.map((r) => (
          <TabsContent key={r.id} value={r.id} className="p-0">
            {/* The route's own name, because the tabs above choose between
                machines and this says which one you are looking at. */}
            <WindowChrome label={`Terminal — ${r.label}`} />
            <div className="group relative flex items-start gap-3 bg-[#0b0b0d] px-4 py-4">
              <pre className="no-scrollbar min-w-0 flex-1 overflow-x-auto font-mono text-[0.78rem] leading-relaxed text-[#e7e7ea]">
                {r.lines.map((line, i) => (
                  <div key={i} className="whitespace-pre">
                    <span className="pr-2 text-[#6ee7b7] select-none">
                      {i === 0 ? "$" : " "}
                    </span>
                    <span className={line.trimStart().startsWith("#") ? "text-[#8a8a94]" : ""}>
                      {line}
                    </span>
                  </div>
                ))}
              </pre>
              <CopyButton
                value={r.lines.filter((l) => !l.trimStart().startsWith("#")).join("\n")}
                className="text-[#a1a1aa] hover:bg-white/10 hover:text-white"
              />
            </div>
          </TabsContent>
        ))}
      </Tabs>

      {active.note ? (
        <p className="border-t px-4 py-3 text-xs leading-relaxed text-muted-foreground">
          {active.note}
        </p>
      ) : null}
    </div>
  );
}
