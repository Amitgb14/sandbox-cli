"use client";

import { useState } from "react";
import { Boxes, Wrench } from "lucide-react";
import { STUDIO_COMPOSE_STEPS, STUDIO_STEPS } from "@/lib/studio";
import { StudioStepList } from "@/components/studio-steps";
import { cn } from "@/lib/utils";

/**
 * The two ways to bring Studio up, as a choice you make once rather than a page
 * you scroll past.
 *
 * Both tracks are complete on their own — that is the point of separating them.
 * Interleaving compose and manual instructions in one list is how a reader ends
 * up running half of each and getting a UI that cannot reach a daemon, which is
 * also the single most common way this setup fails.
 *
 * Compose is first because it is fewer decisions, not because it is more
 * secure — it is less so if you take the `--profile api` branch, which mounts
 * the docker socket. That warning lives on the step itself rather than here,
 * where it would be read once and forgotten by the time it applies.
 */

const TRACKS = [
  {
    id: "compose",
    label: "docker compose",
    icon: Boxes,
    blurb:
      "One file at the repository root. The default profile containerises the UI and leaves the API on your host, which is the recommended shape rather than a limitation.",
    steps: STUDIO_COMPOSE_STEPS,
  },
  {
    id: "manual",
    label: "manual",
    icon: Wrench,
    blurb:
      "Two processes you start yourself. More steps, and the one worth reading if you want to know what compose is doing on your behalf — or if you are not running the UI in a container at all.",
    steps: STUDIO_STEPS,
  },
] as const;

export function StudioSetup() {
  const [track, setTrack] = useState<(typeof TRACKS)[number]["id"]>("compose");
  const current = TRACKS.find((t) => t.id === track) ?? TRACKS[0];

  return (
    <div className="flex flex-col gap-4">
      <div
        role="tablist"
        aria-label="How to bring Studio up"
        className="flex flex-wrap gap-2"
      >
        {TRACKS.map((t) => {
          const active = t.id === track;
          return (
            <button
              key={t.id}
              type="button"
              role="tab"
              aria-selected={active}
              onClick={() => setTrack(t.id)}
              className={cn(
                "flex items-center gap-1.5 rounded-full border px-3 py-1.5 text-[0.78rem] transition-colors",
                active
                  ? "border-foreground/25 bg-muted text-foreground"
                  : "border-border text-muted-foreground hover:text-foreground",
              )}
            >
              <t.icon className="size-3.5" />
              {t.label}
              <span className="font-mono text-[0.62rem] opacity-60">{t.steps.length}</span>
            </button>
          );
        })}
      </div>

      <p className="text-[0.82rem] leading-relaxed text-muted-foreground">{current.blurb}</p>

      <StudioStepList steps={current.steps} />
    </div>
  );
}
