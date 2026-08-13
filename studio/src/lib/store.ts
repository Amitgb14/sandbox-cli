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
   * Shown by default, because the panel is correct wherever the reading still
   * moves — and it will move again for everyone if the recording written by the
   * status line is wired up (docs/proposals/usage-stats.md).
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
        recentRuns: s.recentRuns,
      }),
    },
  ),
);
