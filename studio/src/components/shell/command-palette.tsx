"use client";

import { useEffect } from "react";
import { useRouter } from "next/navigation";
import { Boxes, Moon, Sun } from "lucide-react";
import { useTheme } from "next-themes";
import {
  CommandDialog,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
  CommandSeparator,
  CommandShortcut,
} from "@/components/ui/command";
import { ALL_NAV_ITEMS } from "@/lib/nav";
import { useUi } from "@/lib/store";
import { useSandboxes } from "@/lib/api/queries";
import { StatusBadge } from "@/components/common/status-badge";

/**
 * ⌘K: every screen, every live sandbox, and the theme. A palette that only
 * navigated would be a slower sidebar; the sandboxes are what makes it worth
 * the keystroke.
 */
export function CommandPalette() {
  const router = useRouter();
  const open = useUi((s) => s.paletteOpen);
  const setOpen = useUi((s) => s.setPaletteOpen);
  const toggle = useUi((s) => s.togglePalette);
  const { setTheme, theme } = useTheme();
  const { data: sandboxes } = useSandboxes();

  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      if (e.key.toLowerCase() === "k" && (e.metaKey || e.ctrlKey)) {
        e.preventDefault();
        toggle();
      }
    }
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, [toggle]);

  const go = (href: string) => {
    setOpen(false);
    router.push(href);
  };
  const live = (sandboxes ?? []).filter((s) => s.state !== "terminated");

  return (
    <CommandDialog open={open} onOpenChange={setOpen}>
      <CommandInput placeholder="Go to a screen or a sandbox…" />
      <CommandList>
        <CommandEmpty>Nothing matches.</CommandEmpty>
        <CommandGroup heading="Screens">
          {ALL_NAV_ITEMS.map((i) => (
            <CommandItem key={i.href} value={`${i.title} ${i.hint}`} onSelect={() => go(i.href)}>
              <i.icon />
              <span>{i.title}</span>
              {i.shortcut ? <CommandShortcut>⌘{i.shortcut}</CommandShortcut> : null}
            </CommandItem>
          ))}
        </CommandGroup>
        {live.length > 0 && (
          <>
            <CommandSeparator />
            <CommandGroup heading="Sandboxes">
              {live.map((s) => (
                <CommandItem
                  key={s.id}
                  value={`${s.id} ${s.name ?? ""} ${Object.entries(s.labels ?? {}).map(([k, v]) => `${k}=${v}`).join(" ")}`}
                  onSelect={() => go(`/sandbox?id=${s.id}`)}
                >
                  <Boxes />
                  <span className="font-mono text-xs">{s.name || s.id}</span>
                  <StatusBadge outcome={s.state} size="sm" className="ml-auto" />
                </CommandItem>
              ))}
            </CommandGroup>
          </>
        )}
        <CommandSeparator />
        <CommandGroup heading="Appearance">
          <CommandItem onSelect={() => setTheme(theme === "dark" ? "light" : "dark")}>
            {theme === "dark" ? <Sun /> : <Moon />}
            <span>Switch to {theme === "dark" ? "light" : "dark"}</span>
          </CommandItem>
        </CommandGroup>
      </CommandList>
    </CommandDialog>
  );
}
