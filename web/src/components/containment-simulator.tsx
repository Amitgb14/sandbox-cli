"use client";

import { useEffect, useRef, useState } from "react";
import { CornerDownLeft, ShieldCheck, ShieldX, Terminal } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { ContainmentCanvas, type CanvasHandle } from "@/components/containment-canvas";
import { classify, PRESETS, type Outcome } from "@/lib/classify";
import { cn } from "@/lib/utils";

type Entry = { id: number; command: string; outcome: Outcome };

const AUTOPLAY = ["cat ~/.ssh/id_rsa", "rm -rf ~", "npm test", "sudo chmod u+s /bin/bash"];

export function ContainmentSimulator({ className }: { className?: string }) {
  const canvas = useRef<CanvasHandle | null>(null);
  const root = useRef<HTMLDivElement | null>(null);
  const [log, setLog] = useState<Entry[]>([]);
  const [value, setValue] = useState("");
  const nextId = useRef(0);
  const played = useRef(false);

  function send(command: string) {
    const trimmed = command.trim();
    if (!trimmed) return;
    const outcome = classify(trimmed);
    canvas.current?.launch(trimmed, outcome.verdict === "passes");
    setLog((prev) => [{ id: nextId.current++, command: trimmed, outcome }, ...prev].slice(0, 4));
  }

  // Play once when it scrolls into view, then hand over to the visitor.
  useEffect(() => {
    const el = root.current;
    if (!el) return;
    const io = new IntersectionObserver(
      (entries) => {
        if (!entries[0]?.isIntersecting || played.current) return;
        played.current = true;
        AUTOPLAY.forEach((c, i) => setTimeout(() => send(c), 350 + i * 1150));
        io.disconnect();
      },
      { threshold: 0.35 },
    );
    io.observe(el);
    return () => io.disconnect();
  }, []);

  return (
    <div
      ref={root}
      className={cn(
        "overflow-hidden rounded-2xl border bg-card shadow-[0_1px_2px_rgba(9,9,11,0.04),0_12px_40px_-24px_rgba(9,9,11,0.25)]",
        className,
      )}
    >
      {/* legend */}
      <div className="flex items-center justify-between gap-3 border-b px-4 py-2.5 text-xs sm:px-5">
        <span className="inline-flex items-center gap-2 font-mono text-muted-foreground">
          <span className="size-1.5 rounded-full bg-exposed" />
          your machine
        </span>
        <span className="hidden font-mono text-[0.68rem] tracking-[0.16em] text-muted-foreground uppercase sm:inline">
          the boundary
        </span>
        <span className="inline-flex items-center gap-2 font-mono text-muted-foreground">
          <span className="size-1.5 rounded-full bg-contained" />
          /workspace
        </span>
      </div>

      <div className="h-[260px] sm:h-[320px] lg:h-[360px]">
        <ContainmentCanvas ref={canvas} />
      </div>

      {/* controls */}
      <div className="border-t bg-surface/70 px-4 py-4 sm:px-5">
        <div className="flex flex-wrap gap-1.5">
          {PRESETS.map((p) => (
            <Button
              key={p.command}
              variant="outline"
              size="xs"
              onClick={() => send(p.command)}
              className="font-mono text-[0.7rem]"
            >
              {p.label}
            </Button>
          ))}
        </div>

        <form
          className="mt-3 flex items-center gap-2"
          onSubmit={(e) => {
            e.preventDefault();
            send(value);
            setValue("");
          }}
        >
          <div className="relative flex-1">
            <Terminal className="pointer-events-none absolute top-1/2 left-3 size-3.5 -translate-y-1/2 text-muted-foreground" />
            <Input
              value={value}
              onChange={(e) => setValue(e.target.value)}
              placeholder="…or type any command and send it at the wall"
              aria-label="Command to send at the boundary"
              className="pl-9 font-mono text-[0.8rem]"
            />
          </div>
          <Button type="submit" size="sm" className="gap-1.5">
            Send <CornerDownLeft className="size-3.5" />
          </Button>
        </form>

        {/* verdicts */}
        <ul className="mt-4 flex flex-col gap-2">
          {log.length === 0 ? (
            <li className="font-mono text-xs text-muted-foreground">
              awaiting a command…
            </li>
          ) : null}
          {log.map((e) => {
            const blocked = e.outcome.verdict === "contained";
            return (
              <li
                key={e.id}
                className={cn(
                  "flex items-start gap-2.5 rounded-lg border px-3 py-2.5 text-xs",
                  blocked
                    ? "border-exposed-line bg-exposed-soft"
                    : "border-contained-line bg-contained-soft",
                )}
              >
                {blocked ? (
                  <ShieldX className="mt-0.5 size-4 shrink-0 text-exposed" />
                ) : (
                  <ShieldCheck className="mt-0.5 size-4 shrink-0 text-contained" />
                )}
                <div className="min-w-0 flex-1">
                  <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
                    <code className="truncate font-mono font-medium">{e.command}</code>
                    <Badge
                      variant="outline"
                      className={cn(
                        "font-mono text-[0.62rem]",
                        blocked
                          ? "border-exposed/30 text-exposed"
                          : "border-contained/30 text-contained",
                      )}
                    >
                      {e.outcome.by}
                    </Badge>
                  </div>
                  <p className="mt-1 text-muted-foreground">{e.outcome.detail}</p>
                </div>
              </li>
            );
          })}
        </ul>
      </div>
    </div>
  );
}
