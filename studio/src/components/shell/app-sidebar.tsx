"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { Moon, PlugZap, Search, Sun } from "lucide-react";
import { useTheme } from "next-themes";
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarGroup,
  SidebarGroupContent,
  SidebarHeader,
  SidebarMenu,
  SidebarMenuBadge,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarSeparator,
} from "@/components/ui/sidebar";
import { isActive, useNav } from "@/lib/nav";
import { useCaller } from "@/lib/caller";
import { useInfo, useSandboxes } from "@/lib/api/queries";
import { useUi } from "@/lib/store";
import { cn } from "@/lib/utils";

/**
 * The sidebar, and the only chrome: a wordmark, search, the screens the
 * caller has (lib/nav.ts), and at the foot which sandboxd or gateway this is,
 * light or dark, and the version. Kept quiet
 * on purpose — the table is what a person came to look at.
 *
 * The one badge, on Sandboxes, is how many are running: a badge that showed a
 * total would be a number nobody acts on.
 */
export function AppSidebar() {
  const pathname = usePathname();
  const { data: sandboxes } = useSandboxes();
  const setPaletteOpen = useUi((s) => s.setPaletteOpen);
  const running = sandboxes?.filter((s) => s.state === "running").length ?? 0;
  const nav = useNav();

  return (
    <Sidebar collapsible="offcanvas" className="border-r">
      <SidebarHeader className="gap-3 px-3 pt-4">
        <Link href="/" className="px-2 text-[1.05rem] font-semibold tracking-tight">
          sandbox<span className="text-muted-foreground">·studio</span>
        </Link>
        <button
          type="button"
          onClick={() => setPaletteOpen(true)}
          className="flex h-8 items-center gap-2 rounded-md border bg-background px-2.5 text-sm text-muted-foreground transition-colors hover:text-foreground"
        >
          <Search className="size-3.5" />
          Search
          <kbd className="ml-auto font-mono text-[10px]">⌘K</kbd>
        </button>
      </SidebarHeader>

      <SidebarContent className="px-1">
        {nav.map((group, i) => (
          <div key={group.label}>
            {i > 0 && <SidebarSeparator className="mx-3" />}
            <SidebarGroup className="py-1.5">
              <SidebarGroupContent>
                <SidebarMenu>
                  {group.items.map((item) => (
                    <SidebarMenuItem key={item.href}>
                      <SidebarMenuButton
                        asChild
                        isActive={isActive(item, pathname)}
                        className="h-8 text-muted-foreground hover:text-foreground data-[active=true]:font-medium data-[active=true]:text-foreground"
                      >
                        <Link href={item.href}>
                          <item.icon />
                          <span>{item.title}</span>
                        </Link>
                      </SidebarMenuButton>
                      {item.href === "/" && running > 0 && (
                        <SidebarMenuBadge className="tabular-nums">{running}</SidebarMenuBadge>
                      )}
                    </SidebarMenuItem>
                  ))}
                </SidebarMenu>
              </SidebarGroupContent>
            </SidebarGroup>
          </div>
        ))}
      </SidebarContent>

      <SidebarFooter className="gap-3 px-3 pb-4">
        <Connection />
        <ThemeSwitch />
        <Version />
      </SidebarFooter>
    </Sidebar>
  );
}

/**
 * Which sandboxd Studio is talking to: the context's name and its backend,
 * and in red when it is not answering, which says why every table is empty.
 */
function Connection() {
  const { data, error } = useInfo();
  const caller = useCaller();
  const ok = data && !data.error;
  // Through a gateway the backend is the nodes'; who the key is matters more.
  const label =
    caller.kind === "gateway"
      ? `${data?.context} · ${caller.who.user}${caller.who.tenant ? `@${caller.who.tenant}` : ""}`
      : `${data?.context} · ${data?.capabilities?.backend ?? ""}`;
  return (
    <div
      className="flex items-center gap-2 px-1 text-xs"
      title={
        ok
          ? `Context ${data.context}: sandboxd's ${data.capabilities?.backend} backend, API ${data.capabilities?.api_version}.`
          : `sandboxd did not answer: ${data?.error ?? error?.message ?? "no response"}.`
      }
    >
      {ok ? (
        <span className="size-1.5 rounded-full bg-contained" />
      ) : (
        <PlugZap className="size-3.5 text-exposed" />
      )}
      <span className={cn("truncate font-mono", ok ? "text-muted-foreground" : "text-exposed")}>
        {ok ? label : "not connected"}
      </span>
    </div>
  );
}

function ThemeSwitch() {
  const { resolvedTheme, theme, setTheme } = useTheme();
  // Dark is the provider's default; until a choice is stored it may report
  // neither, and the switch should still show which one is in force.
  const current = resolvedTheme ?? theme ?? "dark";
  const opts = [
    { id: "light", label: "Light", icon: Sun },
    { id: "dark", label: "Dark", icon: Moon },
  ] as const;
  return (
    <div role="radiogroup" aria-label="Theme" className="grid grid-cols-2 rounded-md border bg-background p-0.5">
      {opts.map((o) => (
        <button
          key={o.id}
          type="button"
          role="radio"
          aria-checked={current === o.id}
          onClick={() => setTheme(o.id)}
          className={cn(
            "flex items-center justify-center gap-1.5 rounded px-2 py-1 text-xs transition-colors",
            current === o.id ? "bg-muted font-medium text-foreground" : "text-muted-foreground hover:text-foreground",
          )}
        >
          <o.icon className="size-3.5" />
          {o.label}
        </button>
      ))}
    </div>
  );
}

function Version() {
  const { data } = useInfo();
  return <p className="px-1 font-mono text-[11px] text-muted-foreground/70">{data ? `sandbox-cli ${data.version}` : " "}</p>;
}
