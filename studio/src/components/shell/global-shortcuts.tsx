"use client";

import { useEffect } from "react";
import { useRouter } from "next/navigation";
import { ALL_NAV_ITEMS } from "@/lib/nav";

/**
 * The nav shortcuts the palette advertises.
 *
 * They exist because the palette lists them: a shortcut shown next to a menu
 * item and not wired up is worse than no shortcut. Ignored while focus is in a
 * text field, so typing `d` into the runs filter does not navigate away.
 */
export function GlobalShortcuts() {
  const router = useRouter();

  useEffect(() => {
    const byKey = new Map(
      ALL_NAV_ITEMS.filter((i) => i.shortcut).map((i) => [i.shortcut!.toLowerCase(), i.href]),
    );

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
  }, [router]);

  return null;
}
