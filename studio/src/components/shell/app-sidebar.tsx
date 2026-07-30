"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { Box, ChevronsUpDown, Plus } from "lucide-react";
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarGroup,
  SidebarGroupContent,
  SidebarGroupLabel,
  SidebarHeader,
  SidebarMenu,
  SidebarMenuBadge,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarRail,
} from "@/components/ui/sidebar";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Button } from "@/components/ui/button";
import { NAV } from "@/lib/nav";
import { useRuns, useWorktrees } from "@/lib/api/queries";
import { useUi } from "@/lib/store";
import { REPOS } from "@/lib/mock/data";
import { cn } from "@/lib/utils";
import { UsageGauge } from "@/components/shell/usage-gauge";

/**
 * The sidebar.
 *
 * The counts are live and mean one thing each: Runs shows how many are
 * *running*, Worktrees how many branches exist. A badge that showed a total
 * would be a number nobody acts on.
 */
export function AppSidebar() {
  const pathname = usePathname();
  const { data: runs } = useRuns();
  const { data: worktrees } = useWorktrees();
  const repoFilter = useUi((s) => s.repoFilter);
  const setRepoFilter = useUi((s) => s.setRepoFilter);

  const scoped = repoFilter ? runs?.filter((r) => r.repoId === repoFilter) : runs;
  const liveCount = scoped?.filter((r) => r.state === "running").length ?? 0;
  const worktreeCount =
    (repoFilter ? worktrees?.filter((w) => w.repoId === repoFilter) : worktrees)?.filter(
      (w) => !w.primary,
    ).length ?? 0;

  const activeRepo = REPOS.find((r) => r.id === repoFilter);

  function isActive(href: string, prefix?: boolean) {
    if (href === "/") return pathname === "/";
    if (href === "/settings") return pathname === "/settings";
    return prefix ? pathname.startsWith(href) : pathname === href;
  }

  function badgeFor(href: string): number | null {
    if (href === "/runs") return liveCount || null;
    if (href === "/worktrees") return worktreeCount || null;
    return null;
  }

  return (
    <Sidebar collapsible="icon" className="border-r">
      <SidebarHeader>
        <SidebarMenu>
          <SidebarMenuItem>
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <SidebarMenuButton
                  size="lg"
                  className="data-[state=open]:bg-sidebar-accent"
                  tooltip="Switch repository"
                >
                  <div className="flex aspect-square size-8 items-center justify-center rounded-md bg-primary text-primary-foreground">
                    <Box className="size-4" />
                  </div>
                  <div className="grid flex-1 text-left leading-tight">
                    <span className="truncate text-sm font-semibold">Sandbox Studio</span>
                    <span className="truncate text-xs text-muted-foreground">
                      {activeRepo?.name ?? "All repositories"}
                    </span>
                  </div>
                  <ChevronsUpDown className="ml-auto size-4 text-muted-foreground" />
                </SidebarMenuButton>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="start" className="w-64">
                <DropdownMenuLabel className="text-xs text-muted-foreground">
                  Scope every screen to one repository
                </DropdownMenuLabel>
                <DropdownMenuItem onClick={() => setRepoFilter(null)}>
                  <span className={cn(!repoFilter && "font-medium")}>All repositories</span>
                </DropdownMenuItem>
                <DropdownMenuSeparator />
                {REPOS.map((repo) => (
                  <DropdownMenuItem key={repo.id} onClick={() => setRepoFilter(repo.id)}>
                    <div className="flex min-w-0 flex-col">
                      <span className={cn(repoFilter === repo.id && "font-medium")}>
                        {repo.name}
                      </span>
                      {/* An id, not a path: two clones of a same-named repo
                          would otherwise share a label namespace. */}
                      <span className="truncate font-mono text-[10px] text-muted-foreground">
                        {repo.id}
                      </span>
                    </div>
                  </DropdownMenuItem>
                ))}
              </DropdownMenuContent>
            </DropdownMenu>
          </SidebarMenuItem>
        </SidebarMenu>
      </SidebarHeader>

      <SidebarContent>
        {NAV.map((group) => (
          <SidebarGroup key={group.label}>
            <SidebarGroupLabel>{group.label}</SidebarGroupLabel>
            <SidebarGroupContent>
              <SidebarMenu>
                {group.items.map((item) => {
                  const badge = badgeFor(item.href);
                  return (
                    <SidebarMenuItem key={item.href}>
                      <SidebarMenuButton
                        asChild
                        isActive={isActive(item.href, item.prefix)}
                        tooltip={item.title}
                      >
                        <Link href={item.href}>
                          <item.icon />
                          <span>{item.title}</span>
                        </Link>
                      </SidebarMenuButton>
                      {badge !== null && (
                        <SidebarMenuBadge className="tabular-nums">{badge}</SidebarMenuBadge>
                      )}
                    </SidebarMenuItem>
                  );
                })}
              </SidebarMenu>
            </SidebarGroupContent>
          </SidebarGroup>
        ))}
      </SidebarContent>

      <SidebarFooter>
        <UsageGauge />
        <Button asChild className="w-full group-data-[collapsible=icon]:hidden">
          <Link href="/launch">
            <Plus className="size-4" />
            New run
          </Link>
        </Button>
      </SidebarFooter>
      <SidebarRail />
    </Sidebar>
  );
}
