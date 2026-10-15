"use client";

import { useEffect, useLayoutEffect, useRef, useSyncExternalStore } from "react";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { Moon, PanelLeftClose, PanelLeftOpen, PlugZap, Search, Sun } from "lucide-react";
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
  SidebarRail,
  SidebarSeparator,
  useSidebar,
} from "@/components/ui/sidebar";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { isActive, useNav } from "@/lib/nav";
import { useCaller } from "@/lib/caller";
import { useInfo, useSandboxes } from "@/lib/api/queries";
import { useUi } from "@/lib/store";
import { cn } from "@/lib/utils";
import { OrgSwitcher } from "@/components/shell/org-switcher";
import { SignOutButton } from "@/components/shell/hosted-session";

/**
 * The sidebar, and the only chrome: a wordmark, search, the screens the
 * caller has (lib/nav.ts), and at the foot which sandboxd or gateway this is,
 * light or dark, and the version. Kept quiet
 * on purpose — the table is what a person came to look at.
 *
 * The one badge, on Sandboxes, is how many are running: a badge that showed a
 * total would be a number nobody acts on.
 *
 * It collapses to a rail of icons (the button at its top, its edge, or ⌘B),
 * each screen named in a tooltip, and stays collapsed across reloads in this
 * browser. On a phone it is a drawer, always shown whole.
 */
export function AppSidebar() {
  const pathname = usePathname();
  const { data: sandboxes } = useSandboxes();
  const setPaletteOpen = useUi((s) => s.setPaletteOpen);
  const running = sandboxes?.filter((s) => s.state === "running").length ?? 0;
  const nav = useNav();
  const { state, isMobile, toggleSidebar } = useSidebar();
  const collapsed = state === "collapsed" && !isMobile;
  useRememberedCollapse();

  return (
    <Sidebar collapsible="icon" className="border-r">
      <SidebarHeader className={cn("gap-3 pt-4", collapsed ? "items-center px-2" : "px-3")}>
        {collapsed ? (
          <IconButton label="Expand the sidebar (⌘B)" onClick={toggleSidebar}>
            <PanelLeftOpen />
          </IconButton>
        ) : (
          <div className="flex items-center gap-1">
            <Link href="/" className="min-w-0 flex-1 truncate px-2 text-[1.05rem] font-semibold tracking-tight">
              sandbox<span className="text-muted-foreground">·studio</span>
            </Link>
            {!isMobile && (
              <IconButton label="Collapse the sidebar (⌘B)" onClick={toggleSidebar}>
                <PanelLeftClose />
              </IconButton>
            )}
          </div>
        )}
        <OrgSwitcher compact={collapsed} />
        {collapsed ? (
          <IconButton label="Search (⌘K)" onClick={() => setPaletteOpen(true)} bordered>
            <Search />
          </IconButton>
        ) : (
          <button
            type="button"
            onClick={() => setPaletteOpen(true)}
            className="flex h-8 items-center gap-2 rounded-md border bg-background px-2.5 text-sm text-muted-foreground transition-colors hover:text-foreground"
          >
            <Search className="size-3.5" />
            Search
            <kbd className="ml-auto font-mono text-[10px]">⌘K</kbd>
          </button>
        )}
      </SidebarHeader>

      {/* Never scrolled sideways: what does not fit is truncated. */}
      <SidebarContent className={cn("overflow-x-hidden", collapsed ? "px-0" : "px-1")}>
        {nav.map((group, i) => (
          <div key={group.label}>
            {/* w-auto, so the margin is taken out of the width rather than
                added to it: a full-width rule with a margin overhung the
                sidebar by 8px and gave it a horizontal scrollbar. */}
            {i > 0 && <SidebarSeparator className="mx-3 data-[orientation=horizontal]:w-auto" />}
            <SidebarGroup className="py-1.5">
              <SidebarGroupContent>
                <SidebarMenu>
                  {group.items.map((item) => (
                    <SidebarMenuItem key={item.href}>
                      <SidebarMenuButton
                        asChild
                        isActive={isActive(item, pathname)}
                        tooltip={item.href === "/" && running > 0 ? `${item.title} · ${running} running` : item.title}
                        className="relative h-8 text-muted-foreground hover:text-foreground data-[active=true]:font-medium data-[active=true]:text-foreground"
                      >
                        <Link href={item.href}>
                          <item.icon />
                          <span>{item.title}</span>
                          {/* Collapsed, the count has no room: a dot says some are running. */}
                          {collapsed && item.href === "/" && running > 0 ? (
                            <span aria-hidden className="absolute top-1.5 right-1.5 size-1.5 rounded-full bg-status-running" />
                          ) : null}
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

      <SidebarFooter className={cn("gap-3 pb-4", collapsed ? "items-center px-2" : "px-3")}>
        <SignOutButton />
        <Connection compact={collapsed} />
        <ThemeSwitch compact={collapsed} />
        {collapsed ? null : <Version />}
      </SidebarFooter>
      <SidebarRail />
    </Sidebar>
  );
}

const COLLAPSED_KEY = "studio.sidebar.collapsed";

/**
 * Keeps the sidebar as this browser last left it. The static export cannot
 * read the sidebar's cookie when it renders, so the choice is kept in local
 * storage and applied as the page hydrates, before it is painted, without the
 * slide a click gets (sidebar-instant in globals.css), so a reload does not
 * show it closing. Storage that throws (a private window, blocked site data)
 * leaves it open.
 */
function useRememberedCollapse() {
  const { open, setOpen, isMobile } = useSidebar();
  const restored = useRef(false);
  useLayoutEffect(() => {
    try {
      if (localStorage.getItem(COLLAPSED_KEY) === "1") {
        const root = document.documentElement;
        root.classList.add("sidebar-instant");
        setOpen(false);
        requestAnimationFrame(() => requestAnimationFrame(() => root.classList.remove("sidebar-instant")));
      }
    } catch {}
    restored.current = true;
    // Once, on mount.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
  useEffect(() => {
    if (!restored.current || isMobile) return;
    try {
      localStorage.setItem(COLLAPSED_KEY, open ? "0" : "1");
    } catch {}
  }, [open, isMobile]);
}

/** A square button with its name in a tooltip: what the collapsed sidebar is made of. */
function IconButton({ label, onClick, bordered, children }: { label: string; onClick: () => void; bordered?: boolean; children: React.ReactNode }) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <button
          type="button"
          aria-label={label}
          onClick={onClick}
          className={cn(
            "flex size-8 shrink-0 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-sidebar-accent hover:text-foreground [&>svg]:size-4",
            bordered && "border bg-background",
          )}
        >
          {children}
        </button>
      </TooltipTrigger>
      <TooltipContent side="right">{label}</TooltipContent>
    </Tooltip>
  );
}

/**
 * Which sandboxd Studio is talking to: the context's name and its backend,
 * and in red when it is not answering, which says why every table is empty.
 */
function Connection({ compact }: { compact?: boolean }) {
  const { data, error } = useInfo();
  const caller = useCaller();
  const ok = data && !data.error;
  // Through a gateway the backend is the nodes'; who the key is matters more.
  const label =
    caller.kind === "gateway"
      ? `${data?.context} · ${caller.who.user}${caller.who.tenant ? `@${caller.who.tenant}` : ""}`
      : `${data?.context} · ${data?.capabilities?.backend ?? ""}`;
  if (compact) {
    return (
      <div
        className="flex size-8 items-center justify-center"
        role="img"
        aria-label={ok ? label : "not connected"}
        title={ok ? label : `not connected: ${data?.error ?? error?.message ?? "no response"}`}
      >
        {ok ? <span className="size-1.5 rounded-full bg-contained" /> : <PlugZap className="size-3.5 text-exposed" />}
      </div>
    );
  }
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

// False while hydrating the static export, true after: the server render
// cannot know the stored theme, and React keeps the server's attributes on a
// mismatch, so a switch that rendered the stored choice at once stayed on the
// pre-rendered "dark" for a light page.
const noSubscribe = () => () => {};
function useHydrated() {
  return useSyncExternalStore(noSubscribe, () => true, () => false);
}

function ThemeSwitch({ compact }: { compact?: boolean }) {
  const { resolvedTheme, theme, setTheme } = useTheme();
  const hydrated = useHydrated();
  // Dark is the provider's default; until a choice is stored it may report
  // neither, and the switch should still show which one is in force.
  const current = hydrated ? (resolvedTheme ?? theme ?? "dark") : "dark";
  const opts = [
    { id: "light", label: "Light", icon: Sun },
    { id: "dark", label: "Dark", icon: Moon },
  ] as const;
  if (compact) {
    // One button that switches to the other theme, showing the one in force.
    const next = current === "dark" ? "light" : "dark";
    const Icon = current === "dark" ? Moon : Sun;
    return (
      <IconButton label={`Theme: ${current}. Switch to ${next}`} onClick={() => setTheme(next)} bordered>
        <Icon />
      </IconButton>
    );
  }
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
