"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { Moon, PlugZap, Search, Sun } from "lucide-react";
import { useTheme } from "next-themes";
import { Button } from "@/components/ui/button";
import { Separator } from "@/components/ui/separator";
import { SidebarTrigger } from "@/components/ui/sidebar";
import {
  Breadcrumb,
  BreadcrumbItem,
  BreadcrumbLink,
  BreadcrumbList,
  BreadcrumbPage,
  BreadcrumbSeparator,
} from "@/components/ui/breadcrumb";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { Badge } from "@/components/ui/badge";
import { crumbsFor } from "@/lib/nav";
import { useDaemon, useTransportMode } from "@/lib/api/queries";
import { useUi } from "@/lib/store";

export function AppHeader() {
  const pathname = usePathname();
  const crumbs = crumbsFor(pathname);
  const setPaletteOpen = useUi((s) => s.setPaletteOpen);

  return (
    <header className="sticky top-0 z-30 flex h-14 shrink-0 items-center gap-2 border-b bg-background/80 px-4 backdrop-blur-md">
      <SidebarTrigger className="-ml-1" />
      <Separator orientation="vertical" className="mr-1 h-4" />

      <Breadcrumb className="min-w-0">
        <BreadcrumbList>
          {crumbs.map((c, i) => (
            <BreadcrumbItem key={`${c.href}-${i}`} className="min-w-0">
              {c.current ? (
                <BreadcrumbPage className="max-w-[16rem] truncate">{c.label}</BreadcrumbPage>
              ) : (
                <>
                  <BreadcrumbLink asChild>
                    <Link href={c.href}>{c.label}</Link>
                  </BreadcrumbLink>
                  <BreadcrumbSeparator />
                </>
              )}
            </BreadcrumbItem>
          ))}
        </BreadcrumbList>
      </Breadcrumb>

      <div className="ml-auto flex items-center gap-2">
        <Button
          variant="outline"
          size="sm"
          onClick={() => setPaletteOpen(true)}
          className="h-8 gap-2 text-muted-foreground"
        >
          <Search className="size-3.5" />
          <span className="hidden sm:inline">Search…</span>
          <kbd className="ml-1 hidden rounded border bg-muted px-1 font-mono text-[10px] sm:inline">
            ⌘K
          </kbd>
        </Button>
        <TransportBadge />
        <ThemeToggle />
      </div>
    </header>
  );
}

/**
 * Whether what is on screen came from the daemon or from a fixture.
 *
 * A control plane that cannot say which it is showing is worse than one showing
 * nothing — so this is a permanent part of the chrome, not a dismissable banner.
 */
function TransportBadge() {
  const { mode, retry } = useTransportMode();
  const { data: daemon } = useDaemon();

  if (mode === "live") {
    return (
      <Tooltip>
        <TooltipTrigger asChild>
          <Badge
            variant="outline"
            className="h-8 gap-1.5 border-contained/40 bg-contained/10 px-2.5 text-contained"
          >
            <span className="size-1.5 rounded-full bg-contained" />
            <span className="hidden font-mono text-[11px] md:inline">
              {daemon?.engine ?? "daemon"} {daemon?.version ?? ""}
            </span>
          </Badge>
        </TooltipTrigger>
        <TooltipContent>
          Connected to the sandbox daemon. Everything on screen is a live reading.
        </TooltipContent>
      </Tooltip>
    );
  }

  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button
          variant="outline"
          size="sm"
          onClick={retry}
          className="h-8 gap-1.5 border-caution/40 bg-caution/10 px-2.5 text-caution hover:bg-caution/20 hover:text-caution"
        >
          <PlugZap className="size-3.5" />
          <span className="hidden text-[11px] md:inline">Fixture data</span>
        </Button>
      </TooltipTrigger>
      <TooltipContent className="max-w-xs">
        No daemon answered on localhost:8787, so these are fixtures. Nothing here reflects a real
        container. Click to retry.
      </TooltipContent>
    </Tooltip>
  );
}

function ThemeToggle() {
  const { theme, setTheme } = useTheme();
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button
          variant="ghost"
          size="icon"
          // `relative` is load-bearing: the two icons are stacked on top of each
          // other so one can rotate out as the other rotates in, and an absolute
          // child with no positioned ancestor would anchor to the sticky header
          // instead — which put the moon off-centre in the button.
          className="relative size-8"
          onClick={() => setTheme(theme === "dark" ? "light" : "dark")}
          aria-label="Toggle theme"
        >
          <Sun className="absolute top-1/2 left-1/2 size-4 -translate-x-1/2 -translate-y-1/2 scale-0 rotate-90 transition-transform dark:scale-100 dark:rotate-0" />
          <Moon className="absolute top-1/2 left-1/2 size-4 -translate-x-1/2 -translate-y-1/2 scale-100 rotate-0 transition-transform dark:scale-0 dark:-rotate-90" />
        </Button>
      </TooltipTrigger>
      <TooltipContent>
        {/* Dark is the designed mode; light is stepped for its own surface
            rather than an automatic flip. */}
        Switch to {theme === "dark" ? "light" : "dark"}
      </TooltipContent>
    </Tooltip>
  );
}
