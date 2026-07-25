"use client";

import { useEffect, useState } from "react";
import { cn } from "@/lib/utils";

/**
 * The three places sandbox-cli reports resource usage, reproduced. The numbers
 * walk on a client-side interval only — the server renders the same starting
 * frame every time, so there is nothing for hydration to disagree about.
 */
export function LiveGauge({ className }: { className?: string }) {
  const [mem, setMem] = useState(412);
  const [cpu, setCpu] = useState(82);
  const [secs, setSecs] = useState(47);

  useEffect(() => {
    if (window.matchMedia("(prefers-reduced-motion: reduce)").matches) return;
    const id = setInterval(() => {
      setMem((m) => clamp(m + (Math.random() - 0.45) * 34, 180, 940));
      setCpu((c) => clamp(c + (Math.random() - 0.5) * 26, 6, 178));
      setSecs((s) => s + 1);
    }, 1100);
    return () => clearInterval(id);
  }, []);

  const bars = 8;
  const filled = Math.round((mem / 7600) * bars * 6);

  return (
    <div className={cn("flex flex-col gap-3", className)}>
      {/* the sticky footer gauge, non-interactive runs */}
      <Frame label="non-interactive run — the sticky footer gauge">
        <div className="text-[#8a8a94]">
          <div>{"$ npm test"}</div>
          <div>{"  128 passing"}</div>
        </div>
        <div className="mt-1.5 flex items-center justify-between gap-3 border-t border-white/10 pt-1.5 text-[#e7e7ea]">
          <span className="truncate">
            <span className="text-[#6ee7b7]">sandbox-cli</span>
            <span className="text-white/25"> | </span>
            mem <span className="tnum">{Math.round(mem)}MiB</span>
            <span className="text-white/40">/7.6GiB</span>{" "}
            <span className="text-[#6ee7b7]">
              [{"=".repeat(Math.max(0, Math.min(bars, filled)))}
              {"-".repeat(Math.max(0, bars - filled))}]
            </span>{" "}
            cpu <span className="tnum">{Math.round(cpu)}%</span>
            <span className="text-white/40"> · {fmt(secs)}</span>
          </span>
          <span className="hidden shrink-0 text-[#a5b4fc] sm:inline">git:feature/login</span>
        </div>
      </Frame>

      {/* the status line inside Claude */}
      <Frame label="inside a sandbox-cli claude session — Claude's own status line">
        <div className="flex items-center justify-between gap-3 text-[#e7e7ea]">
          <span className="truncate">
            <span className="text-[#6ee7b7]">⬢ sandbox</span>
            <span className="text-white/40"> · </span>opus 5
            <span className="text-white/40"> · </span>mem{" "}
            <span className="tnum">{Math.round(mem)}MiB</span>
            <span className="text-white/40"> · </span>cpu{" "}
            <span className="tnum">{Math.round(cpu)}%</span>
            <span className="text-white/40"> · </span>5h{" "}
            <span className="tnum">23%</span>
            <span className="text-white/40"> (2h14m)</span>
            <span className="text-white/40"> · </span>wk{" "}
            <span className="tnum">49%</span>
          </span>
          <span className="hidden shrink-0 text-[#a5b4fc] sm:inline">git:feature/login</span>
        </div>
      </Frame>

      {/* the peak summary, printed after every run */}
      <Frame label="after every run, interactive included — the peak summary">
        <div className="text-[#e7e7ea]">
          <span className="text-[#6ee7b7]">sandbox-cli:</span> peak mem{" "}
          <span className="tnum">{Math.round(mem)}MiB</span> · cpu peak{" "}
          <span className="tnum">{Math.round(cpu + 56)}%</span> ·{" "}
          <span className="tnum">12m04s</span> · <span className="text-[#a5b4fc]">git:feature/login</span>
        </div>
      </Frame>
    </div>
  );
}

function Frame({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="overflow-hidden rounded-xl border bg-card">
      <p className="border-b px-3 py-1.5 text-[0.68rem] text-muted-foreground">{label}</p>
      <div className="no-scrollbar overflow-x-auto bg-[#0b0b0d] px-3 py-2.5 font-mono text-[0.72rem] leading-relaxed">
        {children}
      </div>
    </div>
  );
}

function clamp(n: number, lo: number, hi: number) {
  return Math.max(lo, Math.min(hi, n));
}

function fmt(s: number) {
  return `${Math.floor(s / 60)}m${String(s % 60).padStart(2, "0")}s`;
}
