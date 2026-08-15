"use client";

import { create } from "zustand";
import { persist } from "zustand/middleware";

/**
 * Client UI state — and only state the server has no opinion about.
 *
 * Anything the daemon owns lives in TanStack Query, not here. Two stores that
 * both believe they know the run list is the same class of bug as two copies of
 * a bootstrap script: they drift, and nobody sees it until they disagree.
 */

interface UiState {
  /** ⌘K palette. */
  paletteOpen: boolean;
  setPaletteOpen: (open: boolean) => void;
  togglePalette: () => void;

  /** The repository the whole app is scoped to. `null` is every repo. */
  repoFilter: string | null;
  setRepoFilter: (repoId: string | null) => void;

  /**
   * The fallback chain to offer for each primary agent, remembered so a routing
   * choice survives the launch that made it.
   *
   * Keyed by the primary rather than kept as one list, because a fallback is a
   * statement about a *pair*: "if claude is down, use codex" says nothing about
   * what should happen when the primary is gemini. One global list would apply
   * somebody's claude answer to an agent they never thought about.
   *
   * Persisted: it is a standing preference about how you want to work, the same
   * kind of thing as the terminal settings below — not a per-visit choice, and
   * not something the daemon has an opinion about.
   */
  routingPrefs: Record<string, string[]>;
  setRoutingPref: (primary: string, fallback: string[]) => void;

  /** Terminal preferences, remembered across runs because they are about you. */
  terminalFollow: boolean;
  setTerminalFollow: (v: boolean) => void;
  terminalWrap: boolean;
  setTerminalWrap: (v: boolean) => void;
  terminalTimestamps: boolean;
  setTerminalTimestamps: (v: boolean) => void;

  /** Unified or split diff. */
  diffView: "unified" | "split";
  setDiffView: (v: "unified" | "split") => void;

  /**
   * Whether the sidebar usage panel is collapsed to its header.
   *
   * Persisted, because it is a standing preference rather than a per-visit one:
   * these numbers move on the agent's schedule, not yours, so someone who does
   * not want a permanent gauge in the corner does not want it again tomorrow.
   */
  usageCollapsed: boolean;
  setUsageCollapsed: (v: boolean) => void;

  /**
   * Whether the usage panel appears at all.
   *
   * Collapsing hides the numbers and keeps the header; this removes the panel.
   * They are different requests, and the second one became worth serving when
   * the figures stopped being maintained upstream: Claude Code no longer writes
   * the cache the host reads, so on many machines the panel is a permanent
   * fossil with an explanation attached. Explaining something useless every time
   * you look at the sidebar is its own kind of noise.
   *
   * **Shown by default**, and the round trip is worth recording. It was flipped
   * off when the only source was a cache Claude Code had stopped maintaining, so
   * the panel was a fossil explaining itself; it is on again now that the status
   * line records the live figures and `agentusage` prefers them. The default
   * tracks whether the number can move, which is the only thing that ever made
   * it worth showing.
   */
  usageHidden: boolean;
  setUsageHidden: (v: boolean) => void;

  /** Recently visited runs, for the palette. */
  recentRuns: string[];
  pushRecentRun: (id: string) => void;
}

export const useUi = create<UiState>()(
  persist(
    (set) => ({
      paletteOpen: false,
      setPaletteOpen: (paletteOpen) => set({ paletteOpen }),
      togglePalette: () => set((s) => ({ paletteOpen: !s.paletteOpen })),

      repoFilter: null,
      setRepoFilter: (repoFilter) => set({ repoFilter }),

      terminalFollow: true,
      setTerminalFollow: (terminalFollow) => set({ terminalFollow }),
      terminalWrap: true,
      setTerminalWrap: (terminalWrap) => set({ terminalWrap }),
      terminalTimestamps: false,
      setTerminalTimestamps: (terminalTimestamps) => set({ terminalTimestamps }),

      diffView: "unified",
      setDiffView: (diffView) => set({ diffView }),

      usageCollapsed: false,
      setUsageCollapsed: (usageCollapsed) => set({ usageCollapsed }),
      usageHidden: false,
      setUsageHidden: (usageHidden) => set({ usageHidden }),

      routingPrefs: {},
      setRoutingPref: (primary, fallback) =>
        set((s) => ({ routingPrefs: { ...s.routingPrefs, [primary]: fallback } })),

      recentRuns: [],
      pushRecentRun: (id) =>
        set((s) => ({ recentRuns: [id, ...s.recentRuns.filter((r) => r !== id)].slice(0, 8) })),
    }),
    {
      name: "sandbox-studio-ui",
      // The palette must not reopen itself on reload, and a repo filter that
      // outlived the repo is a confusing empty table.
      partialize: (s) => ({
        terminalFollow: s.terminalFollow,
        terminalWrap: s.terminalWrap,
        terminalTimestamps: s.terminalTimestamps,
        diffView: s.diffView,
        usageCollapsed: s.usageCollapsed,
        usageHidden: s.usageHidden,
        routingPrefs: s.routingPrefs,
        recentRuns: s.recentRuns,
      }),
    },
  ),
);
