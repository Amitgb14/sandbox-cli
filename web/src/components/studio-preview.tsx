"use client";

import { useState } from "react";
import { Circle, ImageOff } from "lucide-react";
import { cn } from "@/lib/utils";

/**
 * What Studio looks like, drawn rather than photographed — and labelled as such.
 *
 * These are **interface illustrations, not screenshots**, and the caption on
 * every frame says so. That is a deliberate choice rather than a shortcut worth
 * apologising for: a screenshot of a control plane is a picture of one machine's
 * data at one moment, it carries whatever container names and branch names
 * happened to be on screen, and it goes stale the first time a column moves. A
 * drawn frame stays in step with the prose around it, renders in both themes,
 * costs no bytes, and cannot quietly show a version of the UI that no longer
 * exists.
 *
 * What it must never do is pretend. Nothing here is dressed up as a capture —
 * no window chrome, no cursor, no fake timestamps presented as real readings —
 * because a diagram passed off as evidence is worse than no picture at all, and
 * that is the same bargain the CLI makes when it prints the *age* of a cached
 * usage figure instead of the figure alone.
 *
 * To use real captures instead, drop PNGs in web/public/ and give each frame a
 * `src`: the caption switches to a plain figure caption and the illustration is
 * replaced. The layout is the same either way.
 */

type Frame = {
  id: string;
  title: string;
  caption: string;
  /** A real capture in web/public, if one has been added. */
  src?: string;
};

const FRAMES: Frame[] = [
  {
    id: "runs",
    title: "The runs table",
    caption:
      "Every container sandbox-cli started for this project, live. The same rows `sandbox-cli list` prints — one control plane, two front ends.",
  },
  {
    id: "detail",
    title: "A run, and the boundary it got",
    caption:
      "The resolved posture rather than the requested one: the profile that applied, the egress mode, and what the workspace actually was.",
  },
  {
    id: "console",
    title: "Answering an agent",
    caption:
      "A run launched with a console keeps a terminal open, so an agent that stopped to ask can be answered. Needs a token, always.",
  },
];

/** A window frame that is obviously drawn: no chrome, no cursor, no fake data. */
function Chrome({ children }: { children: React.ReactNode }) {
  return (
    <div className="overflow-hidden rounded-lg border bg-muted/30">
      <div className="flex items-center gap-1.5 border-b bg-muted/50 px-3 py-2">
        {["", "", ""].map((_, i) => (
          <Circle key={i} className="size-2 fill-muted-foreground/25 text-transparent" />
        ))}
        <span className="ml-2 font-mono text-[0.6rem] text-muted-foreground">
          localhost:3100
        </span>
      </div>
      <div className="p-3">{children}</div>
    </div>
  );
}

function Bar({ w, dim }: { w: string; dim?: boolean }) {
  return (
    <div
      className={cn("h-2 rounded-full", dim ? "bg-muted-foreground/15" : "bg-muted-foreground/30")}
      style={{ width: w }}
    />
  );
}

function RunsIllustration() {
  return (
    <div className="flex flex-col gap-2">
      <div className="flex items-center gap-2">
        <Bar w="18%" />
        <Bar w="26%" dim />
        <Bar w="14%" dim />
        <span className="ml-auto rounded-full border border-contained/40 px-1.5 py-0.5 text-[0.55rem] text-contained">
          live
        </span>
      </div>
      {[
        { state: "running", w: "22%" },
        { state: "exited 0", w: "28%" },
        { state: "exited 90", w: "19%" },
      ].map((r) => (
        <div key={r.state} className="flex items-center gap-2 rounded border bg-card px-2 py-1.5">
          <Bar w={r.w} />
          <span
            className={cn(
              "ml-auto font-mono text-[0.55rem]",
              r.state === "exited 90" ? "text-caution" : "text-muted-foreground",
            )}
          >
            {r.state}
          </span>
        </div>
      ))}
    </div>
  );
}

function DetailIllustration() {
  return (
    <div className="flex flex-col gap-2">
      <Bar w="42%" />
      <div className="grid grid-cols-2 gap-2">
        {["profile", "egress", "workspace", "image"].map((k) => (
          <div key={k} className="rounded border bg-card px-2 py-1.5">
            <div className="font-mono text-[0.55rem] text-muted-foreground">{k}</div>
            <div className="mt-1">
              <Bar w="70%" dim />
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}

function ConsoleIllustration() {
  return (
    <div className="rounded border bg-card p-2 font-mono text-[0.58rem] leading-relaxed text-muted-foreground">
      <div className="flex flex-col gap-1">
        <Bar w="66%" dim />
        <Bar w="48%" dim />
        <div className="mt-1 flex items-center gap-1.5 rounded border border-dashed px-1.5 py-1">
          <span className="text-muted-foreground/60">›</span>
          <Bar w="30%" />
          <span className="ml-auto animate-pulse text-muted-foreground/60">▍</span>
        </div>
      </div>
    </div>
  );
}

const ILLUSTRATION: Record<string, () => React.ReactElement> = {
  runs: RunsIllustration,
  detail: DetailIllustration,
  console: ConsoleIllustration,
};

export function StudioPreview() {
  const [active, setActive] = useState(FRAMES[0].id);
  const frame = FRAMES.find((f) => f.id === active) ?? FRAMES[0];
  const Illustration = ILLUSTRATION[frame.id];

  return (
    <div className="flex flex-col gap-3">
      <div className="flex flex-wrap gap-2">
        {FRAMES.map((f) => (
          <button
            key={f.id}
            type="button"
            onClick={() => setActive(f.id)}
            aria-pressed={f.id === active}
            className={cn(
              "rounded-full border px-2.5 py-1 text-[0.7rem] transition-colors",
              f.id === active
                ? "border-foreground/25 bg-muted text-foreground"
                : "border-border text-muted-foreground hover:text-foreground",
            )}
          >
            {f.title}
          </button>
        ))}
      </div>

      <figure className="flex flex-col gap-2">
        {frame.src ? (
          // eslint-disable-next-line @next/next/no-img-element
          <img
            src={frame.src}
            alt={frame.title}
            className="w-full rounded-lg border"
            loading="lazy"
          />
        ) : (
          <Chrome>
            <Illustration />
          </Chrome>
        )}
        <figcaption className="flex items-start gap-2 text-[0.75rem] leading-relaxed text-muted-foreground">
          {frame.src ? null : (
            <span
              className="mt-0.5 flex shrink-0 items-center gap-1 rounded border border-border px-1.5 py-0.5 text-[0.6rem]"
              title="Drawn from the interface, not a screen capture"
            >
              <ImageOff className="size-2.5" />
              illustration
            </span>
          )}
          <span>{frame.caption}</span>
        </figcaption>
      </figure>
    </div>
  );
}
