"use client";

import { useEffect } from "react";
import { useRouter } from "next/navigation";
import { useNav } from "@/lib/nav";

/**
 * The nav shortcuts the palette advertises.
 *
 * They exist because the palette lists them: a shortcut shown next to a menu
 * item and not wired up is worse than no shortcut. Only the caller's screens
 * have one (lib/nav.ts). Ignored while focus is in a text field, so typing `d`
 * into the runs filter does not navigate away.
 */
export function GlobalShortcuts() {
  const router = useRouter();
  // A string, so the effect re-binds when the screens change and not on every render.
  const keyed = useNav()
    .flatMap((g) => g.items)
    .filter((i) => i.shortcut)
    .map((i) => `${i.shortcut!.toLowerCase()} ${i.href}`)
    .join("\n");

  useEffect(() => {
    const byKey = new Map(keyed.split("\n").filter(Boolean).map((l) => l.split(" ") as [string, string]));

    function onKey(e: KeyboardEvent) {
      if (!(e.metaKey || e.ctrlKey) || e.altKey) return;
      const target = e.target as HTMLElement | null;
      if (
        target &&
        (target.tagName === "INPUT" ||
          target.tagName === "TEXTAREA" ||
          target.isContentEditable)
      ) {
        return;
      }
      const href = byKey.get(e.key.toLowerCase());
      if (!href) return;
      e.preventDefault();
      router.push(href);
    }

    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [router, keyed]);

  return null;
}
