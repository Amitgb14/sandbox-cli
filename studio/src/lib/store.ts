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

/**
 * The persisted slice, named so `migrate` can say what it returns.
 *
 * Every key here is one `partialize` keeps; a stored object legitimately has
 * fewer of them (it was written by an older build), and zustand merges whatever
 * migrate returns over the defaults, which is what fills the gaps.
 */
type PersistedUi = Pick<
  UiState,
  | "terminalFollow"
  | "terminalWrap"
  | "terminalTimestamps"
  | "diffView"
  | "usageCollapsed"
  | "usageHidden"
  | "connections"
  | "routingPrefs"
  | "recentRuns"
>;

/** One daemon this browser can be pointed at. */
export interface SavedConnection {
  /** What to call it in the list — the host by default, since that is what
   *  distinguishes two daemons in practice. */
  label: string;
  /** Full URL, exactly as the client dials it. Empty is not saveable: that is
   *  "this machine", which needs no entry and is always offered. */
  url: string;
  /** The bearer token for *that* daemon. A token belongs to one machine, which
   *  is the whole reason a list of URLs alone would not work. */
  token: string;
}

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
  /**
   * Daemons this browser knows how to reach, so switching machines is a click
   * rather than a paste.
   *
   * The *active* connection is not in here — it stays in the two localStorage
   * keys `client.ts` and `constants.ts` own, because those are read during a
   * request rather than during a render, and moving them would put a fetch's
   * base URL behind a React store. This is an address book beside them.
   *
   * It holds tokens, and that is a deliberate acceptance rather than an
   * oversight: the active token is already in localStorage, so the alternative —
   * saving URLs only — would not reduce what a script on this origin can read,
   * and would make the feature useless by asking for the token again on every
   * switch. What it does mean is that this list is exactly as sensitive as the
   * machine it is on.
   */
  connections: SavedConnection[];
  saveConnection: (c: SavedConnection) => void;
  forgetConnection: (url: string) => void;

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
   * **Hidden by default**, which is the third position this default has held and
   * the reasoning has changed each time: off while the only source was a cache
   * Claude Code had stopped maintaining and the panel was a fossil explaining
   * itself, on once the status line began recording live figures, and off again
   * now — not because the number is wrong but because a permanent gauge in the
   * corner is not what most people want the sidebar for. Nothing behind it
   * changed; this is a question about the chrome, and the switch in Settings is
   * where somebody who does want it says so.
   *
   * A default change alone would not have reached anyone: this is persisted, so
   * a browser that has already stored the old default keeps it forever. The
   * `version`/`migrate` pair below is what makes the new default apply once to
   * those, and exactly once — a browser that has since chosen for itself keeps
   * its choice.
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
      usageHidden: true,
      setUsageHidden: (usageHidden) => set({ usageHidden }),

      connections: [],
      // Keyed by URL: it is what actually identifies a daemon, and saving the
      // same one twice under two labels would make "which of these am I on"
      // unanswerable from the list.
      saveConnection: (c) =>
        set((s) => ({
          connections: [...s.connections.filter((x) => x.url !== c.url), c].sort((a, b) =>
            a.label.localeCompare(b.label),
          ),
        })),
      forgetConnection: (url) =>
        set((s) => ({ connections: s.connections.filter((c) => c.url !== url) })),

      routingPrefs: {},
      setRoutingPref: (primary, fallback) =>
        set((s) => ({ routingPrefs: { ...s.routingPrefs, [primary]: fallback } })),

      recentRuns: [],
      pushRecentRun: (id) =>
        set((s) => ({ recentRuns: [id, ...s.recentRuns.filter((r) => r !== id)].slice(0, 8) })),
    }),
    {
      name: "sandbox-studio-ui",
      // Bumped when a *default* changes in a way that should reach browsers
      // which already stored the old one. Without it a persisted preference is
      // permanent: zustand merges stored state over defaults, so changing a
      // default only ever affects someone who has never opened the app.
      version: 1,
      migrate: (persisted, from) => {
        const state = (persisted ?? {}) as Partial<UiState>;
        // v0 -> v1: the usage panel went from shown-by-default to hidden. Applied
        // once, to browsers carrying the old default, and then never again — a
        // migration that re-asserted the default on every load would be a setting
        // that cannot be changed.
        if (from < 1) return { ...state, usageHidden: true } as PersistedUi;
        return state as PersistedUi;
      },
      // The palette must not reopen itself on reload, and a repo filter that
      // outlived the repo is a confusing empty table.
      partialize: (s) => ({
        terminalFollow: s.terminalFollow,
        terminalWrap: s.terminalWrap,
        terminalTimestamps: s.terminalTimestamps,
        diffView: s.diffView,
        usageCollapsed: s.usageCollapsed,
        usageHidden: s.usageHidden,
        connections: s.connections,
        routingPrefs: s.routingPrefs,
        recentRuns: s.recentRuns,
      }),
    },
  ),
);
