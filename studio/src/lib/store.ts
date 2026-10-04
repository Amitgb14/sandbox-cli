"use client";

/**
 * The little UI state that is not the server's: whether the command palette is
 * open. Nothing here is a token or a server address — those are Studio's, not
 * this browser's.
 */

import { create } from "zustand";

interface UiState {
  paletteOpen: boolean;
  setPaletteOpen: (open: boolean) => void;
  togglePalette: () => void;
}

export const useUi = create<UiState>()((set) => ({
  paletteOpen: false,
  setPaletteOpen: (paletteOpen) => set({ paletteOpen }),
  togglePalette: () => set((s) => ({ paletteOpen: !s.paletteOpen })),
}));
