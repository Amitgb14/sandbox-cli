import {
  Bot,
  Boxes,
  Camera,
  HardDrive,
  Play,
  Settings,
  Terminal,
  type LucideIcon,
} from "lucide-react";

/**
 * The navigation model, in one place.
 *
 * The sidebar, the command palette and the breadcrumbs all read this. Three
 * hand-maintained copies of a route list is how a nav item ends up reachable
 * from the palette and invisible in the sidebar.
 *
 * Routes are flat and detail screens take their subject as a query parameter
 * (/sandbox?id=…): Studio is a static export embedded in sandbox-cli, and a
 * static export has no server to answer a path it did not build.
 */

export interface NavItem {
  title: string;
  href: string;
  icon: LucideIcon;
  /** Shown in the palette, where there is room to say what a screen is for. */
  hint: string;
  shortcut?: string;
  /** Other paths that light this item up (a detail screen under a list). */
  also?: string[];
}

export interface NavGroup {
  label: string;
  items: NavItem[];
}

export const NAV: NavGroup[] = [
  {
    label: "Sandboxes",
    items: [
      {
        title: "Sandboxes",
        href: "/",
        icon: Boxes,
        hint: "Every sandbox on this sandboxd: its overview, terminal, logs, files and events",
        shortcut: "S",
        also: ["/sandboxes", "/sandbox"],
      },
      {
        title: "Snapshots",
        href: "/snapshots",
        icon: Camera,
        hint: "Sandboxes captured whole, to start new ones from",
      },
      {
        title: "Volumes",
        href: "/volumes",
        icon: HardDrive,
        hint: "Named filesystems that outlive the sandboxes they are mounted in",
      },
    ],
  },
  {
    label: "Build",
    items: [
      {
        title: "Playground",
        href: "/launch",
        icon: Play,
        hint: "Set up a sandbox, launch it, or copy the same setup as CLI, curl, Python or TypeScript",
        shortcut: "N",
      },
      {
        title: "Agents",
        href: "/agents",
        icon: Bot,
        hint: "The agents Studio runs, all with a verified headless mode, and whose login is saved",
        shortcut: "A",
      },
    ],
  },
  {
    label: "System",
    items: [
      {
        title: "Settings",
        href: "/settings",
        icon: Settings,
        hint: "The context, and what this sandboxd can deliver",
        shortcut: ",",
      },
    ],
  },
];

export const ALL_NAV_ITEMS = NAV.flatMap((g) => g.items);

/** Whether a nav item is the screen at pathname. */
export function isActive(item: NavItem, pathname: string): boolean {
  const p = pathname.replace(/\/$/, "") || "/";
  if (item.href === "/") return p === "/";
  return p === item.href || (item.also ?? []).includes(p);
}

export interface Crumb {
  label: string;
  href: string;
  /** The last crumb is the current page and is not a link. */
  current: boolean;
}

/** Breadcrumbs from a pathname: Studio, then the screen. */
export function crumbsFor(pathname: string): Crumb[] {
  const p = pathname.replace(/\/$/, "") || "/";
  if (p === "/") return [{ label: "Sandboxes", href: "/", current: true }];
  const item = ALL_NAV_ITEMS.find((i) => isActive(i, p));
  const crumbs: Crumb[] = [{ label: "Studio", href: "/", current: false }];
  if (item && item.href !== p) crumbs.push({ label: item.title, href: item.href, current: false });
  crumbs.push({ label: item && item.href === p ? item.title : p === "/sandbox" ? "Sandbox" : p.slice(1), href: p, current: true });
  return crumbs;
}

export const TERMINAL_ICON = Terminal;
