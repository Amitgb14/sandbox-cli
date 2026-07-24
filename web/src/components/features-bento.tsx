"use client";

import type { LucideIcon } from "lucide-react";
import {
  Braces,
  GitBranch,
  Globe,
  House,
  KeyRound,
  ShieldOff,
  Trash2,
  UserRound,
} from "lucide-react";
import { motion } from "motion/react";
import { Spotlight } from "@/components/spotlight";

interface Feature {
  Icon: LucideIcon;
  title: string;
  body: React.ReactNode;
  wide?: boolean;
  figure?: React.ReactNode;
}

const FEATURES: Feature[] = [
  {
    Icon: Braces,
    title: "One choke point, exhaustively tested",
    wide: true,
    body: (
      <>
        <code>runtime.BuildArgs</code> is a pure function: config in, <code>docker</code> argv out. No
        hidden state, no side channels. Print it before you run it.
      </>
    ),
    figure: (
      <>
        <span className="text-contained">sandbox-cli</span> run{" "}
        <span className="text-signal">--dry-run</span> -- npm test
        {"\n"}
        <span className="opacity-70">{"# → docker run --rm -v /you/proj:/workspace …"}</span>
      </>
    ),
  },
  {
    Icon: ShieldOff,
    title: "Refusals you cannot override",
    wide: true,
    body: (
      <>
        Mounting <code>/</code>, your home directory, or any ancestor of it is rejected outright — not
        warned about, not gated behind a flag. There is no <code>--force</code>.
      </>
    ),
    figure: (
      <>
        <span className="text-contained">sandbox-cli</span> run{" "}
        <span className="text-signal">--mount</span> ~ -- bash
        {"\n"}
        <span className="text-exposed">error:</span> refusing to mount host home
      </>
    ),
  },
  {
    Icon: Trash2,
    title: "Disposable by default",
    body: (
      <>
        Every run is <code>--rm</code>. No residue, no drifting state, no container to remember to
        clean up.
      </>
    ),
  },
  {
    Icon: House,
    title: "HOME is a decoy",
    body: (
      <>
        <code>HOME=/sandbox/home</code>, always. Tools that scribble in the home directory find an
        empty, throwaway one.
      </>
    ),
  },
  {
    Icon: UserRound,
    title: "Non-root, and it matters",
    body: "Runs as an unprivileged user. On macOS, mount ownership is virtualized so your files stay yours.",
  },
  {
    Icon: KeyRound,
    title: "Logins that survive, isolated",
    body: "Each agent authenticates into its own sandbox-owned home — persistent across runs, and never your real config.",
  },
  {
    Icon: Globe,
    title: "Egress on a leash",
    body: (
      <>
        Scope the network with <code>--allow</code> so the agent reaches its model API and nothing
        else.
      </>
    ),
  },
  {
    Icon: GitBranch,
    title: "Many agents, one repo",
    body: (
      <>
        <code>--worktree</code> gives each agent its own branch and its own sandbox. Run four in
        parallel without collisions.
      </>
    ),
  },
];

export function FeaturesBento() {
  return (
    <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-6">
      {FEATURES.map(({ Icon, title, body, wide, figure }, i) => (
        <motion.div
          key={title}
          initial={{ opacity: 0, y: 14 }}
          whileInView={{ opacity: 1, y: 0 }}
          viewport={{ once: true, amount: 0.2 }}
          transition={{ duration: 0.4, delay: (i % 3) * 0.06 }}
          className={wide ? "lg:col-span-3" : "lg:col-span-2"}
        >
          <Spotlight className="flex h-full flex-col gap-2 rounded-xl border bg-card p-6 transition-all hover:-translate-y-0.5 hover:border-contained/40 hover:shadow-xl">
            <span className="grid size-9 place-items-center rounded-lg border border-contained/25 bg-contained-soft text-contained">
              <Icon className="size-4.5" />
            </span>
            <h3 className="mt-1 font-heading text-lg font-bold">{title}</h3>
            <div className="text-sm text-muted-foreground [&_code]:rounded [&_code]:bg-muted [&_code]:px-1 [&_code]:py-0.5 [&_code]:font-mono [&_code]:text-[0.85em]">
              {body}
            </div>
            {figure && (
              <pre className="no-scrollbar mt-2 overflow-x-auto rounded-md border bg-background px-3 py-2.5 font-mono text-[0.72rem] leading-relaxed">
                {figure}
              </pre>
            )}
          </Spotlight>
        </motion.div>
      ))}
    </div>
  );
}
