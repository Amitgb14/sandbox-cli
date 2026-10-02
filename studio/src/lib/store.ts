"use client";

/**
 * The little UI state that is not the server's: whether the command palette is
 * open, and which repository the work screens are about. The repository is
 * remembered, per browser, because it is the question every work screen starts
 * with; nothing here is a token or a server address — those are Studio's, not
 * this browser's.
 */

import { create } from "zustand";
import { persist } from "zustand/middleware";

interface UiState {
  paletteOpen: boolean;
  setPaletteOpen: (open: boolean) => void;
  togglePalette: () => void;

  /** The selected repository's id, or null before one is chosen. */
  repo: string | null;
  setRepo: (id: string | null) => void;
}

export const useUi = create<UiState>()(
  persist(
    (set) => ({
      paletteOpen: false,
      setPaletteOpen: (paletteOpen) => set({ paletteOpen }),
      togglePalette: () => set((s) => ({ paletteOpen: !s.paletteOpen })),

      repo: null,
      setRepo: (repo) => set({ repo }),
    }),
    {
      name: "sandbox-studio-ui",
      version: 2,
      // Earlier versions held beta.15's settings — daemon URLs and their
      // tokens among them — which mean nothing to this Studio and should not
      // linger in storage.
      migrate: () => ({ repo: null }),
      partialize: (s) => ({ repo: s.repo }),
    },
  ),
);
