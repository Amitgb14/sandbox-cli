"use client";

import { motion } from "motion/react";
import { Badge } from "@/components/ui/badge";
import { buttonVariants } from "@/components/ui/button";
import { CopyButton } from "@/components/copy-button";
import { ContainmentSimulator } from "@/components/containment-simulator";
import { CountUp } from "@/components/count-up";
import { cn } from "@/lib/utils";

export const INSTALL_CMD =
  "curl -fsSL https://raw.githubusercontent.com/Aegmis/sandbox-cli/main/install.sh | sh";

const STATS = [
  { value: 15, suffix: "", label: "agents wrapped" },
  { value: 1, suffix: "", label: "host path mounted" },
  { value: 0, suffix: "", label: "credentials reachable" },
  { literal: "--rm", label: "every run" },
] as const;

export function Hero() {
  return (
    <section className="relative overflow-hidden px-6 pb-12 pt-16" id="top">
      {/* ambient wash: warm on the exposed side, cool on the contained side */}
      <div
        aria-hidden="true"
        className="pointer-events-none absolute inset-x-0 -top-40 -z-10 h-[42rem]"
        style={{
          background:
            "radial-gradient(48% 55% at 22% 18%, color-mix(in oklch, var(--exposed) 12%, transparent), transparent 72%), radial-gradient(48% 55% at 78% 22%, color-mix(in oklch, var(--contained) 16%, transparent), transparent 72%)",
        }}
      />

      <div className="mx-auto w-full max-w-6xl">
        <motion.div
          initial={{ opacity: 0, y: 18 }}
          animate={{ opacity: 1, y: 0 }}
          transition={{ duration: 0.5, ease: "easeOut" }}
          className="flex max-w-3xl flex-col gap-5"
        >
          <span className="eyebrow">
            <span className="h-px w-6 bg-current opacity-60" />
            Containment for coding agents
          </span>

          <h1 className="font-heading text-5xl font-extrabold leading-[1.04] tracking-[-0.042em] sm:text-6xl lg:text-7xl">
            Your agent gets <span className="text-exposed">root</span>.
            <br />
            It just doesn&apos;t get <span className="text-contained">your machine</span>.
          </h1>

          <p className="max-w-2xl text-lg text-muted-foreground">
            <strong className="font-semibold text-foreground">sandbox-cli</strong> runs Claude Code,
            Codex, Gemini and twelve more agents inside a disposable container. Only the project you
            point it at is mounted. Everything else — your SSH keys, cloud credentials, the other 200
            repos on your disk — simply isn&apos;t there to reach.
          </p>

          <div className="flex w-fit max-w-full items-center gap-2 rounded-lg border bg-card p-1.5 pl-3 shadow-sm">
            <span className="font-mono font-semibold text-contained" aria-hidden="true">
              $
            </span>
            <code className="no-scrollbar overflow-x-auto whitespace-nowrap font-mono text-[0.8rem]">
              {INSTALL_CMD}
            </code>
            <CopyButton value={INSTALL_CMD} label="Copy" />
          </div>

          <div className="mt-1 flex flex-wrap gap-2.5">
            <a href="#usage" className={cn(buttonVariants({ size: "lg" }), "bg-contained text-background hover:bg-contained/90")}>
              Get started
            </a>
            <a href="#boundary" className={buttonVariants({ variant: "outline", size: "lg" })}>
              See what it blocks
            </a>
          </div>
        </motion.div>

        <motion.div
          initial={{ opacity: 0, y: 24 }}
          animate={{ opacity: 1, y: 0 }}
          transition={{ duration: 0.55, delay: 0.15, ease: "easeOut" }}
          className="mt-14"
          id="boundary"
        >
          <ContainmentSimulator />
        </motion.div>

        <div className="mt-14 flex flex-wrap justify-center gap-x-10 gap-y-5 border-y py-8">
          {STATS.map((s) => (
            <div key={s.label} className="flex items-baseline gap-2 font-mono text-sm text-muted-foreground">
              <b className="font-heading text-2xl font-bold tracking-tight text-contained tabular-nums">
                {"literal" in s ? s.literal : <CountUp to={s.value} />}
              </b>
              {s.label}
            </div>
          ))}
        </div>
      </div>
    </section>
  );
}

export function HeroBadge() {
  return <Badge variant="secondary">Open source · Go · MIT</Badge>;
}
