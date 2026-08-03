"use client";

import { useState } from "react";
import Image from "next/image";
import { cn } from "@/lib/utils";

/**
 * What Studio actually looks like — real captures, not illustrations.
 *
 * These replaced a set of drawn frames, and the reason for the swap is the same
 * reason the drawings carried an `illustration` label in the first place: a
 * picture on a docs page is read as evidence, so it either is one or it says it
 * is not. These are, so the label is gone.
 *
 * Every caption points at something visible in its own frame rather than
 * narrating the product in general. A caption that praises a screen the reader
 * is already looking at adds nothing; one that names the number they would
 * otherwise skip — the denominator behind a pass rate, the `session` kind that
 * is *why* a run shows no verify — is the only kind worth writing.
 *
 * next/image rather than <img>: the export sets `images: { unoptimized: true }`,
 * so this costs no build step, and passing the intrinsic width/height is what
 * keeps the page from reflowing as each capture loads.
 */

type Frame = {
  id: string;
  title: string;
  src: string;
  caption: string;
};

/** Intrinsic size of the captures; all three were taken at one window size. */
const W = 1696;
const H = 893;

const FRAMES: Frame[] = [
  {
    id: "dashboard",
    title: "Dashboard",
    src: "/studio/dashboard.png",
    caption:
      "Every sandbox across your repositories, and what the host is carrying. The pass rate keeps its denominator — 90% is over 841 decided runs rather than over everything that ever started — and memory in flight reads as a dash instead of 0 when nothing is running, because a run that was never measured is not a run that used nothing.",
  },
  {
    id: "runs",
    title: "Runs",
    src: "/studio/runs.png",
    caption:
      "Every container carrying the sandbox.cli label, running or finished — a run stays here after it exits, because how it ended is the point. The KIND column is doing real work: this one is a session, which is why it has no verify to have failed.",
  },
  {
    id: "worktrees",
    title: "Worktrees",
    src: "/studio/worktrees.png",
    caption:
      "One branch per agent, each in its own directory mounted at its own host path so git cannot prune it away mid-session. Verify reads “never checked” rather than passed for branches nothing judged, and the dirty count next to a branch is one of the two facts land refuses on.",
  },
];

export function StudioPreview() {
  const [active, setActive] = useState(FRAMES[0].id);
  const frame = FRAMES.find((f) => f.id === active) ?? FRAMES[0];

  return (
    <div className="flex flex-col gap-3">
      <div role="tablist" aria-label="Studio screens" className="flex flex-wrap gap-2">
        {FRAMES.map((f) => {
          const on = f.id === active;
          return (
            <button
              key={f.id}
              type="button"
              role="tab"
              aria-selected={on}
              onClick={() => setActive(f.id)}
              className={cn(
                "rounded-full border px-2.5 py-1 text-[0.7rem] transition-colors",
                on
                  ? "border-foreground/25 bg-muted text-foreground"
                  : "border-border text-muted-foreground hover:text-foreground",
              )}
            >
              {f.title}
            </button>
          );
        })}
      </div>

      <figure className="flex flex-col gap-2.5">
        <div className="overflow-hidden rounded-xl border bg-muted/20">
          <Image
            src={frame.src}
            alt={`Sandbox Studio — ${frame.title}`}
            width={W}
            height={H}
            priority={frame.id === FRAMES[0].id}
            className="h-auto w-full"
          />
        </div>
        <figcaption className="text-[0.78rem] leading-relaxed text-muted-foreground">
          {frame.caption}
        </figcaption>
      </figure>
    </div>
  );
}
