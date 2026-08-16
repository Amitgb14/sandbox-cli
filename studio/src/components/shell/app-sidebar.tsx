"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { Box, ChevronsUpDown, FolderPlus, Plus } from "lucide-react";
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
import { useProjects, useRuns, useWorktrees } from "@/lib/api/queries";
import { useUi } from "@/lib/store";
import { AddRepositoryDialog } from "@/components/shell/add-repository-dialog";
import { cn } from "@/lib/utils";
import { UsageGauge } from "@/components/shell/usage-gauge";

/**
 * The sidebar.
 *
 * The counts are live and mean one thing each: Runs shows how many are
 * *running*, Worktrees how many branches exist. A badge that showed a total
 * would be a number nobody acts on.
 *
 * The repository picker lists what the daemon answers about, and nothing else.
 * It used to render a hardcoded fixture, so a running Studio managing one real
 * repository offered three invented ones and no way to reach the real one — the
 * fixture is now unexported and repositories come from `GET /v1/projects`.
 */
export function AppSidebar() {
  const pathname = usePathname();
  const { data: runs } = useRuns();
  const { data: worktrees } = useWorktrees();
  const { data: projects } = useProjects();
  const repoFilter = useUi((s) => s.repoFilter);
  const setRepoFilter = useUi((s) => s.setRepoFilter);
  const [addOpen, setAddOpen] = useState(false);

  const scoped = repoFilter ? runs?.filter((r) => r.repoId === repoFilter) : runs;
  const liveCount = scoped?.filter((r) => r.state === "running").length ?? 0;
  // The worktree query is already scoped to the picked repository, so no second
  // filter here: filtering scoped data by the same scope is how a count reads
  // zero when the daemon reported rows.
  const worktreeCount = worktrees?.filter((w) => !w.primary).length ?? 0;

  const repos = projects ?? [];
  const activeRepo = repos.find((r) => r.id === repoFilter);

  // A scope can outlive the repository it names inside one session: remove a
  // repository from the picker, or have the daemon restarted against another,
  // and the selected id matches nothing. Every screen then filters to empty and
  // reads as "no runs, no worktrees" rather than as "that repository is gone" —
  // the same failure that made the fixture picker so hard to see. Fall back to
  // all repositories once the daemon has actually answered, never on an empty
  // list, which is also what a daemon that has not replied yet looks like.
  //
  // (The scope itself is deliberately *not* persisted — see partialize in
  // lib/store.ts, which leaves it out precisely so a stale id cannot greet you
  // on a reload.)
  useEffect(() => {
    if (!repoFilter || !projects?.length) return;
    if (!projects.some((p) => p.id === repoFilter)) setRepoFilter(null);
  }, [projects, repoFilter, setRepoFilter]);

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
              <DropdownMenuContent align="start" className="w-72">
                <DropdownMenuLabel className="text-xs text-muted-foreground">
                  Scope every screen to one repository
                </DropdownMenuLabel>
                <DropdownMenuItem onClick={() => setRepoFilter(null)}>
                  <span className={cn(!repoFilter && "font-medium")}>All repositories</span>
                </DropdownMenuItem>
                <DropdownMenuSeparator />
                {repos.map((repo) => (
                  <DropdownMenuItem
                    key={repo.id}
                    // A repository that cannot be read is shown and not offered:
                    // scoping to it would put a refusal behind every panel.
                    disabled={repo.missing}
                    onClick={() => !repo.missing && setRepoFilter(repo.id)}
                  >
                    <div className="flex min-w-0 flex-col">
                      <span
                        className={cn(
                          "truncate",
                          repoFilter === repo.id && "font-medium",
                          repo.missing && "text-muted-foreground line-through",
                        )}
                      >
                        {repo.name}
                        {repo.default && (
                          <span className="ml-1.5 text-[10px] font-normal text-muted-foreground">
                            started here
                          </span>
                        )}
                      </span>
                      {/* The host path, which is what actually gets mounted at
                          /workspace — the id is machinery, and a person picking
                          between two checkouts of one repo needs the path. */}
                      <span className="truncate font-mono text-[10px] text-muted-foreground">
                        {repo.missing ? "unavailable — " : ""}
                        {repo.root}
                      </span>
                    </div>
                  </DropdownMenuItem>
                ))}
                {repos.length === 0 && (
                  <div className="px-2 py-1.5 text-xs text-muted-foreground">
                    No daemon answered, so there is nothing to list.
                  </div>
                )}
                <DropdownMenuSeparator />
                <DropdownMenuItem onClick={() => setAddOpen(true)}>
                  <FolderPlus className="size-4" />
                  <span>Add repository…</span>
                </DropdownMenuItem>
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

      {/* Scoping to what was just added is the point of adding it — anything
          else leaves the user to find their new repository in the list they
          opened this dialog from. */}
      <AddRepositoryDialog
        open={addOpen}
        onOpenChange={setAddOpen}
        onAdded={(project) => setRepoFilter(project.id)}
      />
    </Sidebar>
  );
}
