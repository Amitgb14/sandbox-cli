"use client";

import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import type { ProbeBucket, ProbeHistory } from "@/lib/types";
import { cn } from "@/lib/utils";

/**
 * Has this provider been answering, and when was it not.
 *
 * The one thing on this screen that is *collected* rather than derived, and the
 * strip is drawn so that the difference shows. Every other panel here reads the
 * run log, which exists whether or not anybody is watching; a provider's health
 * at a moment nobody asked is not recorded anywhere, so the daemon has to sample
 * it on a timer — and a daemon that was off recorded nothing.
 *
 * Three states, therefore, not two. A slot with no samples is **unknown**, drawn
 * as a gap rather than as an outage: the commonest reason for one is a closed
 * laptop, and a strip that painted that red would report an incident every night.
 * With no prober running at all, the whole row is unknown and the caption says
 * so rather than leaving somebody to conclude their providers have been down for
 * a week.
 *
 * The percentage is over *taken* samples and always carries its count. 100% of
 * two readings is not the claim 100% of six hundred is, and a bare percentage
 * invites reading the first as the second.
 */
export function UptimeStrip({ data }: { data: ProbeHistory }) {
  if (data.providers.length === 0) {
    return (
      <p className="text-sm text-muted-foreground">
        Nothing to show yet — no provider has been sampled.
      </p>
    );
  }

  return (
    <div className="space-y-2">
      {data.providers.map((p) => (
        <div key={p.agent} className="flex items-center gap-3">
          <span className="w-20 shrink-0 font-mono text-xs">{p.agent}</span>
          <div className="flex h-5 min-w-0 flex-1 gap-px">
            {p.buckets.map((b, i) => (
              <Tooltip key={i}>
                <TooltipTrigger asChild>
                  <span
                    className={cn(
                      "h-full min-w-0 flex-1 rounded-[1px]",
                      state(b) === "up" && "bg-status-good/70",
                      state(b) === "down" && "bg-status-critical",
                      // Not a colour with meaning: this is the absence of a
                      // reading, and it has to look like an absence.
                      state(b) === "unknown" && "bg-muted",
                    )}
                  />
                </TooltipTrigger>
                <TooltipContent>
                  <span className="text-xs">
                    {new Date(b.at).toLocaleString()}
                    {state(b) === "unknown"
                      ? " — not sampled"
                      : state(b) === "down"
                        ? ` — did not answer${b.reason ? `: ${b.reason}` : ""}`
                        : " — answered"}
                  </span>
                </TooltipContent>
              </Tooltip>
            ))}
          </div>
          <span className="w-28 shrink-0 text-right text-xs tabular-nums text-muted-foreground">
            {p.samples ? (
              <>
                {(p.uptime! * 100).toFixed(p.uptime === 1 ? 0 : 1)}%{" "}
                <span className="text-[10px]">of {p.samples}</span>
              </>
            ) : (
              <span className="text-[10px]">no samples</span>
            )}
          </span>
        </div>
      ))}

      <p className="text-[11px] text-muted-foreground">
        {data.interval > 0 ? (
          <>
            Sampled every {Math.round(data.interval / 60)} min over the last {data.hours}h — one
            credential-free HEAD request per provider, so a 401 counts as answering. Grey is
            not an outage: it is a span nothing was recorded in, usually a daemon that was not
            running.
          </>
        ) : (
          <>
            This daemon is not recording uptime (<code>-probe-interval 0</code>), so anything
            here is history from a run that was. Grey means not sampled, never down.
          </>
        )}
      </p>
    </div>
  );
}

function state(b: ProbeBucket): "up" | "down" | "unknown" {
  if (b.down > 0) return "down";
  if (b.up > 0) return "up";
  return "unknown";
}
