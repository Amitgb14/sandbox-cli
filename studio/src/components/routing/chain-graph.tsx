"use client";

import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import type { ChainEdge } from "@/lib/routing-history";
import type { ProviderStatus } from "@/lib/types";
import { cn } from "@/lib/utils";

/**
 * The chains, as a picture: who falls back to whom, which of them is answering,
 * and which hops have actually been taken.
 *
 * Three facts on one canvas because each is misleading without the others. A
 * chain that is configured says nothing about whether it works; a provider that
 * is down says nothing about whether anything routes away from it; and a count of
 * failovers says nothing about which of them the current settings would repeat.
 * Together they answer the question people actually open this page with — if
 * Claude goes down right now, what happens, and has it ever worked.
 *
 * Drawn by hand in SVG rather than with the chart library: this is a graph of
 * five labelled nodes, and recharts has no such chart. Laid out on a ring, which
 * is not fashion — a fallback chain has cycles in practice (claude falls back to
 * codex, codex back to claude) and a left-to-right layout has to either drop one
 * of those edges or draw it backwards across the whole picture.
 *
 * What it deliberately does not draw: an agent nobody can route to. The ten
 * adapters without a verified headless mode are not part of any chain and
 * putting them here would suggest they could be.
 */
export function ChainGraph({
  providers,
  edges,
  className,
}: {
  providers: ProviderStatus[];
  edges: ChainEdge[];
  className?: string;
}) {
  const nodes = providers.filter((p) => p.routable);
  if (nodes.length === 0) return null;

  const size = 320;
  const c = size / 2;
  // Room for the label pill, which is wider than the dot it hangs off.
  const radius = c - 58;
  const at = (i: number) => {
    // Starting at the top rather than at three o'clock: with four or five nodes
    // the first one reads as "the primary" and the eye starts there.
    const angle = (i / nodes.length) * 2 * Math.PI - Math.PI / 2;
    return { x: c + radius * Math.cos(angle), y: c + radius * Math.sin(angle) };
  };
  const index = new Map(nodes.map((n, i) => [n.agent, i]));
  const drawn = edges.filter((e) => index.has(e.from) && index.has(e.to));
  const busiest = Math.max(1, ...drawn.map((e) => e.fired));

  return (
    <div className={cn("flex flex-col items-center gap-3", className)}>
      <svg
        viewBox={`0 0 ${size} ${size}`}
        className="h-[320px] w-full max-w-[320px]"
        role="img"
        aria-label="Fallback chains between agents"
      >
        <defs>
          <marker
            id="chain-arrow"
            viewBox="0 0 10 10"
            refX="9"
            refY="5"
            markerWidth="5"
            markerHeight="5"
            orient="auto-start-reverse"
          >
            <path d="M 0 0 L 10 5 L 0 10 z" fill="currentColor" />
          </marker>
        </defs>

        {drawn.map((e) => {
          const a = at(index.get(e.from)!);
          const b = at(index.get(e.to)!);
          // Curved, and always bowed the same way round the ring, so a pair of
          // agents that fall back to each other draws two arcs instead of one
          // line with arrowheads at both ends.
          const mx = (a.x + b.x) / 2;
          const my = (a.y + b.y) / 2;
          const bow = 0.22;
          const cx = mx + (c - mx) * -bow + (b.y - a.y) * bow;
          const cy = my + (c - my) * -bow - (b.x - a.x) * bow;
          const shrink = 26;
          const from = pull(a, { x: cx, y: cy }, shrink);
          const to = pull(b, { x: cx, y: cy }, shrink);
          return (
            <path
              key={`${e.from}-${e.to}`}
              d={`M ${from.x} ${from.y} Q ${cx} ${cy} ${to.x} ${to.y}`}
              fill="none"
              markerEnd="url(#chain-arrow)"
              // Untaken chains are drawn dashed and thin. They are real
              // configuration, so they belong on the picture; they are also
              // untested, and a solid line would say otherwise.
              strokeDasharray={e.fired === 0 ? "3 4" : undefined}
              strokeWidth={1 + (e.fired / busiest) * 3}
              className={cn(
                e.fired === 0
                  ? "text-muted-foreground/50"
                  : e.rescued > 0
                    ? "text-status-good"
                    : "text-status-serious",
              )}
              stroke="currentColor"
            />
          );
        })}

        {nodes.map((n, i) => {
          const p = at(i);
          return (
            <g key={n.agent}>
              <circle
                cx={p.x}
                cy={p.y}
                r="7"
                stroke="var(--background)"
                strokeWidth="2"
                className={statusFill(n)}
                fill="currentColor"
              />
              <text
                x={p.x}
                y={p.y + (p.y < c ? -16 : 24)}
                textAnchor="middle"
                className="fill-foreground font-mono text-[10px]"
              >
                {n.agent}
              </text>
            </g>
          );
        })}
      </svg>

      <div className="flex flex-wrap items-center justify-center gap-x-4 gap-y-1 text-[10px] text-muted-foreground">
        <Legend className="text-status-good" label="answering" />
        <Legend className="text-status-critical" label="not answering" />
        <Legend className="text-muted-foreground" label="not probed" />
        <span>solid = has fired, thicker = more often</span>
        <span>dashed = configured, never used</span>
      </div>

      {drawn.length > 0 && (
        <ul className="w-full space-y-1 text-xs">
          {drawn.map((e) => (
            <li key={`${e.from}-${e.to}`} className="flex items-center gap-2">
              <Tooltip>
                <TooltipTrigger asChild>
                  <span className="font-mono">
                    {e.from} → {e.to}
                  </span>
                </TooltipTrigger>
                <TooltipContent className="max-w-xs">
                  {e.configured
                    ? "A chain says this hop should happen."
                    : "No chain says this today — it is history from a chain that has since changed."}
                </TooltipContent>
              </Tooltip>
              <span className="text-muted-foreground">
                {e.fired === 0
                  ? "never fired"
                  : `fired ${e.fired}×, ${e.rescued} rescued`}
              </span>
              {!e.configured && (
                <span className="text-[10px] text-muted-foreground">(not configured now)</span>
              )}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

/**
 * Three states, not two. An agent with no probeable host — opencode until you
 * name its provider, or anything behind a proxy — is *unprobed*, and colouring
 * it as up would claim a check that never happened.
 */
function statusFill(p: ProviderStatus): string {
  if (!p.probed) return "text-muted-foreground";
  return p.reachable ? "text-status-good" : "text-status-critical";
}

function Legend({ className, label }: { className: string; label: string }) {
  return (
    <span className="flex items-center gap-1">
      <span className={cn("size-2 rounded-full bg-current", className)} />
      {label}
    </span>
  );
}

/** Moves a point `d` px towards `towards`, so an arrow stops at the node edge. */
function pull(p: { x: number; y: number }, towards: { x: number; y: number }, d: number) {
  const dx = towards.x - p.x;
  const dy = towards.y - p.y;
  const len = Math.hypot(dx, dy) || 1;
  return { x: p.x + (dx / len) * d, y: p.y + (dy / len) * d };
}
