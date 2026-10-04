"use client";

import { Fragment } from "react";
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
import { useInfo } from "@/lib/api/queries";
import { useUi } from "@/lib/store";

export function AppHeader() {
  const pathname = usePathname();
  const crumbs = crumbsFor(pathname);
  const setPaletteOpen = useUi((s) => s.setPaletteOpen);

  return (
    <header className="sticky top-0 z-30 flex h-14 shrink-0 items-center gap-2 border-b bg-background/70 px-4 backdrop-blur-xl md:px-6">
      <SidebarTrigger className="-ml-1" />
      <Separator orientation="vertical" className="mr-1 h-4" />

      <Breadcrumb className="min-w-0">
        <BreadcrumbList>
          {/*
            The separator is a sibling of the item, never a child of it.
            BreadcrumbSeparator renders its own <li>, and BreadcrumbItem is an
            <li> too — nesting them put an <li> inside an <li>, which is invalid
            HTML, so the parser relocated it and the hydrated tree stopped
            matching the server's.

            Rendered between items by index rather than on `!c.current`: the two
            agree only while the last crumb is the current one, and the version
            keyed on `current` leaves a trailing separator the moment it isn't.
          */}
          {crumbs.map((c, i) => (
            <Fragment key={`${c.href}-${i}`}>
              <BreadcrumbItem className="min-w-0">
                {c.current ? (
                  <BreadcrumbPage className="max-w-[16rem] truncate">{c.label}</BreadcrumbPage>
                ) : (
                  <BreadcrumbLink asChild>
                    <Link href={c.href}>{c.label}</Link>
                  </BreadcrumbLink>
                )}
              </BreadcrumbItem>
              {i < crumbs.length - 1 && <BreadcrumbSeparator />}
            </Fragment>
          ))}
        </BreadcrumbList>
      </Breadcrumb>

      <div className="ml-auto flex items-center gap-2">
        <Button
          variant="outline"
          size="sm"
          onClick={() => setPaletteOpen(true)}
          className="h-8 w-40 justify-start gap-2 bg-muted/40 text-muted-foreground max-sm:w-auto"
        >
          <Search className="size-3.5" />
          <span className="hidden sm:inline">Search…</span>
          <kbd className="ml-auto hidden rounded border bg-background px-1 font-mono text-[10px] sm:inline">
            ⌘K
          </kbd>
        </Button>
        <ConnectionBadge />
        <ThemeToggle />
      </div>
    </header>
  );
}

/**
 * Which sandboxd Studio is talking to: the context's name and its backend.
 * A permanent part of the chrome, because "whose sandboxes are these" is the
 * first question on any screen, and a red badge when sandboxd is not
 * answering says why every table is empty.
 */
function ConnectionBadge() {
  const { data, error } = useInfo();
  const ok = data && !data.error;
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Badge
          variant="outline"
          className={
            ok
              ? "h-8 gap-1.5 border-contained/40 bg-contained/10 px-2.5 text-contained"
              : "h-8 gap-1.5 border-exposed/40 bg-exposed/10 px-2.5 text-exposed"
          }
        >
          {ok ? <span className="size-1.5 rounded-full bg-contained" /> : <PlugZap className="size-3.5" />}
          <span className="hidden font-mono text-[11px] md:inline">
            {ok ? `${data.context} · ${data.capabilities?.backend ?? ""}` : "not connected"}
          </span>
        </Badge>
      </TooltipTrigger>
      <TooltipContent className="max-w-xs">
        {ok
          ? `Context ${data.context}: sandboxd's ${data.capabilities?.backend} backend, API ${data.capabilities?.api_version}.`
          : `sandboxd did not answer: ${data?.error ?? error?.message ?? "no response"}.`}
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
