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
import { NAV, isActive } from "@/lib/nav";
import { useRepos, useRuns, useSandboxes } from "@/lib/api/queries";
import { useUi } from "@/lib/store";
import { AddRepositoryDialog } from "@/components/shell/add-repository-dialog";
import { cn } from "@/lib/utils";

/**
 * The sidebar. Each badge means one thing: Sandboxes shows how many are
 * running, Runs how many still hold work that has not come back. A badge that
 * showed a total would be a number nobody acts on.
 *
 * The repository picker scopes the work screens (Launch, Runs, Review, Fleet);
 * Sandboxes is the whole sandboxd's, since a sandbox need not have a
 * repository at all.
 */
export function AppSidebar() {
  const pathname = usePathname();
  const { data: sandboxes } = useSandboxes();
  const { data: runs } = useRuns();
  const { data: repos } = useRepos();
  const repo = useUi((s) => s.repo);
  const setRepo = useUi((s) => s.setRepo);
  const [addOpen, setAddOpen] = useState(false);

  const running = sandboxes?.filter((s) => s.state === "running").length ?? 0;
  const pending = runs?.filter((r) => !r.done).length ?? 0;
  const active = repos?.find((r) => r.id === repo);

  // A remembered repository that is no longer registered would leave every
  // work screen empty for a reason it cannot show; pick the first instead.
  useEffect(() => {
    if (!repos) return;
    if (!repos.some((r) => r.id === repo && !r.missing)) {
      setRepo(repos.find((r) => !r.missing)?.id ?? null);
    }
  }, [repos, repo, setRepo]);

  const badge = (href: string) => (href === "/sandboxes" ? running : href === "/runs" ? pending : 0) || null;

  return (
    <Sidebar collapsible="icon" className="border-r">
      <SidebarHeader>
        <SidebarMenu>
          <SidebarMenuItem>
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <SidebarMenuButton size="lg" className="data-[state=open]:bg-sidebar-accent" tooltip="Repository">
                  <div className="flex aspect-square size-8 items-center justify-center rounded-md bg-primary text-primary-foreground">
                    <Box className="size-4" />
                  </div>
                  <div className="grid flex-1 text-left leading-tight">
                    <span className="truncate text-sm font-semibold">Sandbox Studio</span>
                    <span className="truncate text-xs text-muted-foreground">
                      {active?.name ?? "No repository"}
                    </span>
                  </div>
                  <ChevronsUpDown className="ml-auto size-4 text-muted-foreground" />
                </SidebarMenuButton>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="start" className="w-72">
                <DropdownMenuLabel className="text-xs text-muted-foreground">
                  The repository Launch, Runs, Review and Fleet are about
                </DropdownMenuLabel>
                {(repos ?? []).map((r) => (
                  <DropdownMenuItem key={r.id} disabled={r.missing} onClick={() => !r.missing && setRepo(r.id)}>
                    <div className="flex min-w-0 flex-col">
                      <span className={cn("truncate", repo === r.id && "font-medium", r.missing && "line-through")}>
                        {r.name}
                      </span>
                      <span className="truncate font-mono text-[10px] text-muted-foreground">
                        {r.missing ? "gone from disk — " : ""}
                        {r.path}
                      </span>
                    </div>
                  </DropdownMenuItem>
                ))}
                {repos?.length === 0 && (
                  <div className="px-2 py-1.5 text-xs text-muted-foreground">
                    None yet. Run sandbox-cli studio from a checkout, or add one.
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
                  const n = badge(item.href);
                  return (
                    <SidebarMenuItem key={item.href}>
                      <SidebarMenuButton asChild isActive={isActive(item, pathname)} tooltip={item.title}>
                        <Link href={item.href}>
                          <item.icon />
                          <span>{item.title}</span>
                        </Link>
                      </SidebarMenuButton>
                      {n !== null && <SidebarMenuBadge className="tabular-nums">{n}</SidebarMenuBadge>}
                    </SidebarMenuItem>
                  );
                })}
              </SidebarMenu>
            </SidebarGroupContent>
          </SidebarGroup>
        ))}
      </SidebarContent>

      <SidebarFooter>
        <Button asChild className="w-full group-data-[collapsible=icon]:hidden">
          <Link href="/launch">
            <Plus className="size-4" />
            New run
          </Link>
        </Button>
      </SidebarFooter>
      <SidebarRail />

      <AddRepositoryDialog open={addOpen} onOpenChange={setAddOpen} onAdded={(r) => setRepo(r.id)} />
    </Sidebar>
  );
}
