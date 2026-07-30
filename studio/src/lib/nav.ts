import {
  Activity,
  Bot,
  GitBranch,
  LayoutDashboard,
  Play,
  Settings,
  ShieldCheck,
  Terminal,
  type LucideIcon,
} from "lucide-react";

/**
 * The navigation model, in one place.
 *
 * The sidebar, the command palette and the breadcrumbs all read this. Three
 * hand-maintained copies of a route list is how a nav item ends up reachable
 * from the palette and invisible in the sidebar.
 */

export interface NavItem {
  title: string;
  href: string;
  icon: LucideIcon;
  /** Shown in the palette, where there is room to say what a screen is for. */
  hint: string;
  shortcut?: string;
  /** Match child routes too (`/runs/abc123` lights up `/runs`). */
  prefix?: boolean;
}

export interface NavGroup {
  label: string;
  items: NavItem[];
}

export const NAV: NavGroup[] = [
  {
    label: "Overview",
    items: [
      {
        title: "Dashboard",
        href: "/",
        icon: LayoutDashboard,
        hint: "Live runs, throughput, and what the host is carrying",
        shortcut: "D",
      },
      {
        title: "Runs",
        href: "/runs",
        icon: Activity,
        hint: "Every sandbox, running or finished",
        shortcut: "R",
        prefix: true,
      },
    ],
  },
  {
    label: "Work",
    items: [
      {
        title: "Launch",
        href: "/launch",
        icon: Play,
        hint: "Start a run, with the boundary spelled out before you do",
        shortcut: "N",
      },
      {
        title: "Worktrees",
        href: "/worktrees",
        icon: GitBranch,
        hint: "One branch per agent, and what is waiting to land",
        shortcut: "W",
      },
      {
        title: "Agents",
        href: "/agents",
        icon: Bot,
        hint: "Every adapter, its login and what crosses the boundary",
        shortcut: "A",
      },
    ],
  },
  {
    label: "System",
    items: [
      {
        title: "Doctor",
        href: "/settings/doctor",
        icon: ShieldCheck,
        hint: "Whether this host can deliver what the profile promises",
      },
      {
        title: "Settings",
        href: "/settings",
        icon: Settings,
        hint: "Profile, egress, resources, appearance",
        shortcut: ",",
      },
    ],
  },
];

export const ALL_NAV_ITEMS = NAV.flatMap((g) => g.items);

/** Human-readable segment labels for the breadcrumb trail. */
const SEGMENT_LABELS: Record<string, string> = {
  runs: "Runs",
  launch: "Launch",
  worktrees: "Worktrees",
  agents: "Agents",
  settings: "Settings",
  doctor: "Doctor",
};

export interface Crumb {
  label: string;
  href: string;
  /** The last crumb is the current page and is not a link. */
  current: boolean;
}

/**
 * Breadcrumbs from a pathname. A run id segment is passed through as-is and the
 * page overrides it with the run's name once loaded — a trail that guessed at a
 * title before the data arrived would flicker.
 */
export function crumbsFor(pathname: string): Crumb[] {
  const segments = pathname.split("/").filter(Boolean);
  if (segments.length === 0) return [{ label: "Dashboard", href: "/", current: true }];
  const crumbs: Crumb[] = [{ label: "Studio", href: "/", current: false }];
  let href = "";
  segments.forEach((seg, i) => {
    href += `/${seg}`;
    crumbs.push({
      label: SEGMENT_LABELS[seg] ?? seg,
      href,
      current: i === segments.length - 1,
    });
  });
  return crumbs;
}

export const TERMINAL_ICON = Terminal;
