"use client";

import { Fragment } from "react";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { Check, Laptop, Moon, PlugZap, Search, Server, Sun } from "lucide-react";
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
import { apiBase, defaultApiBase } from "@/lib/constants";
import { cn } from "@/lib/utils";
import { crumbsFor } from "@/lib/nav";
import { useDaemon, useTransportMode } from "@/lib/api/queries";
import { useUi } from "@/lib/store";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import {
  useActiveConnection,
  useConnectionHealth,
  useConnections,
  useSwitchConnection,
  type ProbeState,
} from "@/hooks/use-connection";

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
          className="h-8 gap-2 text-muted-foreground"
        >
          <Search className="size-3.5" />
          <span className="hidden sm:inline">Search…</span>
          <kbd className="ml-1 hidden rounded border bg-muted px-1 font-mono text-[10px] sm:inline">
            ⌘K
          </kbd>
        </Button>
        <ConnectionSwitcher />
        <TransportBadge />
        <ThemeToggle />
      </div>
    </header>
  );
}

/**
 * Which machine's daemon this is, and a one-click way to reach another.
 *
 * It lives in the header rather than in Settings because switching machines is
 * something you do *while working*, not while configuring: the whole point of a
 * saved connection is that the agents are on the Linux box and the browser is
 * here. Settings still owns adding and forgetting them — this only chooses.
 *
 * Rendered only when there is a choice to make. One daemon and no saved
 * connections is the ordinary case, and a picker with a single entry is chrome
 * that answers a question nobody asked.
 */
function ConnectionSwitcher() {
  const saved = useConnections();
  const { key, url, ready } = useActiveConnection();
  const switchTo = useSwitchConnection();
  // The *built-in* daemon's own URL, not the active one. apiBase() answers
  // "where do requests go", which is the remote once you have switched — so a
  // row labelled "This machine" would otherwise print the remote's host and the
  // remote's health, and the local daemon would never be probed at all.
  const builtIn = defaultApiBase();
  // Both are probed: "this machine" is exactly as capable of being down as any
  // other, and finding out by watching every panel fail is what this replaces.
  const health = useConnectionHealth(ready ? [builtIn, url, ...saved.map((c) => c.url)] : []);

  if (!ready || saved.length === 0) return null;

  const active = saved.find((c) => c.url === key);
  const label = active?.label ?? hostOf(url) ?? "this machine";

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="outline" size="sm" className="h-8 gap-1.5 text-muted-foreground">
          {active ? <Server className="size-3.5" /> : <Laptop className="size-3.5" />}
          <span className="hidden max-w-[10rem] truncate sm:inline">{label}</span>
          <HealthDot state={health[url]} />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-72">
        <DropdownMenuLabel className="text-xs font-normal text-muted-foreground">
          Which machine runs the agents
        </DropdownMenuLabel>
        <DropdownMenuSeparator />
        <ConnectionRow
          icon={<Laptop className="size-3.5 shrink-0" />}
          label="This machine"
          detail={hostOf(builtIn)}
          active={!key}
          state={health[builtIn]}
          onSelect={() => switchTo(null)}
        />
        {saved.map((c) => (
          <ConnectionRow
            key={c.url}
            icon={<Server className="size-3.5 shrink-0" />}
            label={c.label || hostOf(c.url)}
            detail={c.url}
            active={c.url === key}
            state={health[c.url]}
            onSelect={() => switchTo(c)}
          />
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

function ConnectionRow({
  icon,
  label,
  detail,
  active,
  state,
  onSelect,
}: {
  icon: React.ReactNode;
  label: string;
  detail: string;
  active: boolean;
  state?: ProbeState;
  onSelect: () => void;
}) {
  return (
    <DropdownMenuItem onClick={onSelect} className="gap-2">
      {icon}
      <span className="flex min-w-0 flex-1 flex-col">
        <span className={cn("truncate text-sm", active && "font-medium")}>{label}</span>
        <span className="truncate font-mono text-[10px] text-muted-foreground">{detail}</span>
      </span>
      <HealthDot state={state} />
      {active && <Check className="size-3.5 shrink-0" />}
    </DropdownMenuItem>
  );
}

/**
 * Three states, and the third is the one that matters: a daemon nobody has
 * heard back from yet is **unknown**, not down. Painting silence as an outage is
 * how a probe that has been running for 200ms reports every machine as
 * unreachable.
 */
function HealthDot({ state }: { state?: ProbeState }) {
  const title =
    state === "up" ? "answering" : state === "down" ? "not answering" : "checking…";
  return (
    <span
      title={title}
      className={cn(
        "size-1.5 shrink-0 rounded-full",
        state === "up" && "bg-contained",
        state === "down" && "bg-destructive",
        (state === undefined || state === "checking") && "bg-muted-foreground/40",
      )}
    />
  );
}

/** The host of a URL, for a label that has to fit in a button. */
function hostOf(url: string): string {
  try {
    return new URL(url).host;
  } catch {
    return url;
  }
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
        {/* The endpoint actually dialled, not a hardcoded one. This message is
            read by somebody asking "why is this not my data", and naming a port
            they never used sends them to look at the wrong process — the whole
            job of this badge is to be right about where the data came from. */}
        No daemon answered on {apiBase() || "this machine"}, so these are fixtures. Nothing here
        reflects a real container. Click to retry.
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
