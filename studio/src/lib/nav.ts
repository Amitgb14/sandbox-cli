import {
  Bot,
  Boxes,
  Building2,
  Camera,
  CircleUser,
  HardDrive,
  ListChecks,
  LockKeyhole,
  Play,
  ScrollText,
  Server,
  Settings,
  Terminal,
  Unplug,
  UserRoundCog,
  Users,
  Workflow,
  type LucideIcon,
} from "lucide-react";
import { can, isAdmin, useCaller, type Caller } from "@/lib/caller";
import type { Scope } from "@/lib/types";

/**
 * The navigation model, in one place.
 *
 * The sidebar, the command palette and the shortcuts all read this. Three
 * hand-maintained copies of a route list is how a nav item ends up reachable
 * from the palette and invisible in the sidebar.
 *
 * Routes are flat and detail screens take their subject as a query parameter
 * (/sandbox?id=…): Studio is a static export embedded in sandbox-cli, and a
 * static export has no server to answer a path it did not build.
 *
 * Which items there are depends on the caller (lib/caller.ts): a plain
 * sandboxd gets exactly the screens it always had; a gateway adds its tenant
 * screens, and an admin key the admin ones.
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
  /** Only through a gateway, or only to an admin key on one. */
  need?: "gateway" | "admin";
  /** A scope without which the screen has nothing to offer. */
  scope?: Scope;
}

export interface NavGroup {
  label: string;
  items: NavItem[];
}

/** The admin screens. In a build with NEXT_PUBLIC_STUDIO_ADMIN=off this is empty and the strings are gone. */
// The test is written out here, not imported as ADMIN_BUILD: the minifier folds
// a literal comparison, and does not follow a constant across modules.
const ADMIN_GROUP: NavGroup[] = process.env.NEXT_PUBLIC_STUDIO_ADMIN !== "off"
  ? [
      {
        label: "Admin",
        items: [
          { title: "Nodes", href: "/admin/nodes", icon: Server, need: "admin", hint: "The nodes behind the gateway: health, capacity, cordon and drain" },
          { title: "Lost sandboxes", href: "/admin/lost", icon: Unplug, need: "admin", hint: "Sandboxes on nodes that stopped answering" },
          { title: "Users & keys", href: "/admin/keys", icon: Users, need: "admin", hint: "Issue and revoke API keys; any user's SSH keys" },
          { title: "Audit", href: "/admin/audit", icon: ScrollText, need: "admin", hint: "Every authenticated request and SSH login the gateway recorded" },
          { title: "All organizations", href: "/admin/orgs", icon: Building2, need: "admin", hint: "Every organization on the gateway, who made it and how many members it has" },
        ],
      },
    ]
  : [];

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
        scope: "sandbox:create",
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
    label: "Fleet",
    items: [
      { title: "Jobs", href: "/jobs", icon: ListChecks, need: "gateway", also: ["/job"], hint: "Commands and agent runs the gateway runs to completion, each in a sandbox of its own" },
      { title: "Services", href: "/services", icon: Workflow, need: "gateway", also: ["/service"], hint: "Sandbox specs the gateway keeps a count of, with health checks" },
      { title: "Secrets", href: "/secrets", icon: LockKeyhole, need: "gateway", hint: "The tenant's secrets, by name; values are write-only" },
      { title: "SSH", href: "/ssh", icon: Terminal, need: "gateway", hint: "How to ssh into a sandbox, your SSH keys, and short-lived access tokens" },
    ],
  },
  ...ADMIN_GROUP,
  {
    label: "System",
    items: [
      { title: "Members", href: "/members", icon: UserRoundCog, need: "gateway", hint: "Who belongs to the current organization; owners add and remove members" },
      { title: "Account", href: "/account", icon: CircleUser, need: "gateway", hint: "Who this API key is: user, tenant, organization and scopes" },
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

/** Whether the caller may see a nav item's screen at all. */
export function allowed(item: Pick<NavItem, "need" | "scope">, caller: Caller): boolean {
  if (item.need === "gateway" && caller.kind !== "gateway") return false;
  if (item.need === "admin" && !isAdmin(caller)) return false;
  if (item.scope && !can(caller, item.scope)) return false;
  return true;
}

/** The groups the caller sees, with empty ones dropped. */
export function navFor(caller: Caller): NavGroup[] {
  return NAV.map((g) => ({ ...g, items: g.items.filter((i) => allowed(i, caller)) })).filter((g) => g.items.length > 0);
}

export function useNav(): NavGroup[] {
  return navFor(useCaller());
}

/** Whether a nav item is the screen at pathname. */
export function isActive(item: NavItem, pathname: string): boolean {
  const p = pathname.replace(/\/$/, "") || "/";
  if (item.href === "/") return p === "/";
  return p === item.href || (item.also ?? []).includes(p);
}
