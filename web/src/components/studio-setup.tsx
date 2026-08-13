"use client";

import { useState } from "react";
import { Boxes, Wrench, Zap } from "lucide-react";
import {
  STUDIO_COMPOSE_STEPS,
  STUDIO_SCRIPT_STEPS,
  STUDIO_STEPS,
} from "@/lib/studio";
import { StudioStepList } from "@/components/studio-steps";
import { cn } from "@/lib/utils";

/**
 * The three ways to bring Studio up, as a choice you make once rather than a
 * page you scroll past.
 *
 * Each track is complete on its own — that is the point of separating them.
 * Interleaving script, compose and manual instructions in one list is how a
 * reader ends up running half of each and getting a UI that cannot reach a
 * daemon, which is also the single most common way this setup fails.
 *
 * The order is fewest-decisions first, and that is not the same as
 * most-secure-first: all three land on the same posture — UI containerised, API
 * on the host — and the one place that changes is compose's `--profile api`
 * branch and the script's `--api-in-docker`, which mount the docker socket. That
 * warning lives on the step itself rather than here, where it would be read once
 * and forgotten by the time it applies.
 */

const TRACKS = [
  {
    id: "script",
    label: "one command",
    icon: Zap,
    blurb:
      "A script that installs both binaries, pulls the UI image and starts the pair — with the project resolved to your repository root and one token handed to both halves. The same shape as the other two, typed correctly.",
    steps: STUDIO_SCRIPT_STEPS,
  },
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
  const [track, setTrack] = useState<(typeof TRACKS)[number]["id"]>("script");
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
