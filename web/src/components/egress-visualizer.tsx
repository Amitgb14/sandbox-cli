"use client";

import { useEffect, useRef, useState } from "react";
import { motion } from "motion/react";
import { Ban, Check, Globe, ShieldCheck } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Switch } from "@/components/ui/switch";
import { DESTINATIONS, VERDICT_COPY, type Verdict } from "@/lib/egress";
import { cn } from "@/lib/utils";

const TONE: Record<Verdict, { text: string; ring: string; dot: string }> = {
  baseline: { text: "text-contained", ring: "border-contained-line", dot: "bg-contained" },
  allowed: { text: "text-contained", ring: "border-contained-line", dot: "bg-contained" },
  blocked: { text: "text-exposed", ring: "border-exposed-line", dot: "bg-exposed" },
};

/**
 * The allowlist is the feature people assume must break `npm install`. It does
 * not — and the fastest way to say so is to show the registry traffic sailing
 * through next to the exfiltration that does not.
 */
export function EgressVisualizer({ className }: { className?: string }) {
  const [enforcing, setEnforcing] = useState(true);
  const [firing, setFiring] = useState<number>(-1);
  const tick = useRef(0);

  useEffect(() => {
    const reduced = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    if (reduced) return;
    const id = setInterval(() => {
      tick.current = (tick.current + 1) % DESTINATIONS.length;
      setFiring(tick.current);
    }, 780);
    return () => clearInterval(id);
  }, []);

  const blocked = DESTINATIONS.filter((d) => d.verdict === "blocked").length;

  return (
    <div className={cn("overflow-hidden rounded-2xl border bg-card", className)}>
      <div className="flex flex-wrap items-center justify-between gap-4 border-b bg-surface px-4 py-3.5 sm:px-5">
        <label className="flex cursor-pointer items-center gap-3 text-sm">
          <Switch
            checked={enforcing}
            onCheckedChange={setEnforcing}
            aria-label="Enable the egress allowlist"
          />
          <span className="font-mono text-[0.8rem] font-medium">
            {enforcing ? "network: allowlist" : "network: default"}
          </span>
        </label>
        <div className="flex items-center gap-2 text-xs text-muted-foreground">
          {enforcing ? (
            <>
              <ShieldCheck className="size-3.5 text-contained" />
              <span>
                <span className="font-medium text-foreground tnum">{blocked}</span> of{" "}
                {DESTINATIONS.length} destinations refused
              </span>
            </>
          ) : (
            <>
              <Globe className="size-3.5 text-exposed" />
              <span>everything the container asks for, it gets</span>
            </>
          )}
        </div>
      </div>

      <ul className="divide-y">
        {DESTINATIONS.map((d, i) => {
          const verdict: Verdict = enforcing ? d.verdict : "allowed";
          const stopped = enforcing && d.verdict === "blocked";
          const tone = TONE[verdict];
          const active = firing === i;
          return (
            <li
              key={d.host}
              className={cn(
                "grid grid-cols-[minmax(0,1fr)_auto] items-center gap-x-4 gap-y-1 px-4 py-2.5 transition-colors sm:grid-cols-[minmax(0,15rem)_minmax(0,1fr)_auto] sm:px-5",
                active && "bg-muted/50",
              )}
            >
              <div className="min-w-0">
                <code
                  className={cn(
                    "block truncate font-mono text-[0.8rem] font-medium",
                    stopped && "text-muted-foreground",
                  )}
                >
                  {d.host}
                </code>
                <span className="block truncate text-xs text-muted-foreground">{d.what}</span>
              </div>

              {/* the wire */}
              <div className="relative col-span-2 h-4 sm:col-span-1 sm:h-3">
                <span className="absolute top-1/2 right-0 left-0 h-px -translate-y-1/2 bg-border" />
                {/* the firewall sits at 42% of the wire */}
                <span
                  className={cn(
                    "absolute top-1/2 h-3.5 w-px -translate-y-1/2 transition-colors",
                    enforcing ? "bg-contained" : "bg-border",
                  )}
                  style={{ left: "42%" }}
                />
                {active ? (
                  <motion.span
                    key={`${d.host}-${firing}`}
                    initial={{ left: "0%", opacity: 0 }}
                    animate={
                      stopped
                        ? { left: ["0%", "40%", "40%"], opacity: [0, 1, 0] }
                        : { left: ["0%", "100%"], opacity: [0, 1, 1, 0] }
                    }
                    transition={{ duration: stopped ? 0.62 : 0.78, ease: "easeOut" }}
                    className={cn(
                      "absolute top-1/2 size-1.5 -translate-y-1/2 rounded-full",
                      tone.dot,
                    )}
                  />
                ) : null}
              </div>

              <Badge
                variant="outline"
                className={cn("gap-1 font-mono text-[0.62rem]", tone.ring, tone.text)}
              >
                {stopped ? <Ban className="size-2.5" /> : <Check className="size-2.5" />}
                {enforcing ? VERDICT_COPY[d.verdict].label : "no policy"}
              </Badge>
            </li>
          );
        })}
      </ul>

      <div className="border-t bg-surface px-4 py-3.5 text-xs leading-relaxed text-muted-foreground sm:px-5">
        {enforcing ? (
          <>
            Default-deny, programmed with <code className="font-mono">iptables</code> inside the
            container at startup and then dropped back to the non-root user. It fails closed. Domains
            resolve to IPs once at startup, so hosts behind rotating CDN addresses can still be
            refused — add them explicitly.
          </>
        ) : (
          <>
            Without <code className="font-mono">--allow</code> the container has ordinary outbound
            networking, exactly like any other Docker container. That is the default because most
            people want the agent to install things — the allowlist is there for when you do not
            trust what it might read on the way.
          </>
        )}
      </div>
    </div>
  );
}
