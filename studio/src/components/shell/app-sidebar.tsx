"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { Box, Plus } from "lucide-react";
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
import { Button } from "@/components/ui/button";
import { NAV, isActive } from "@/lib/nav";
import { useSandboxes } from "@/lib/api/queries";

/**
 * The sidebar. The one badge, on Sandboxes, is how many are running: a badge
 * that showed a total would be a number nobody acts on.
 */
export function AppSidebar() {
  const pathname = usePathname();
  const { data: sandboxes } = useSandboxes();
  const running = sandboxes?.filter((s) => s.state === "running").length ?? 0;
  const badge = (href: string) => (href === "/sandboxes" ? running : 0) || null;

  return (
    <Sidebar collapsible="icon" className="border-r">
      <SidebarHeader className="pt-3">
        <Link href="/" className="flex items-center gap-2.5 px-2 group-data-[collapsible=icon]:px-0">
          <div className="flex aspect-square size-8 shrink-0 items-center justify-center rounded-lg bg-gradient-to-br from-primary to-primary/70 text-primary-foreground shadow-sm shadow-primary/30">
            <Box className="size-4" />
          </div>
          <div className="grid leading-tight group-data-[collapsible=icon]:hidden">
            <span className="text-sm font-semibold tracking-tight">Sandbox Studio</span>
            <span className="text-[11px] text-muted-foreground">microVM sandboxes</span>
          </div>
        </Link>
      </SidebarHeader>

      <SidebarContent>
        {NAV.map((group) => (
          <SidebarGroup key={group.label}>
            <SidebarGroupLabel className="text-[11px] font-medium tracking-wide text-muted-foreground/70 uppercase">{group.label}</SidebarGroupLabel>
            <SidebarGroupContent>
              <SidebarMenu>
                {group.items.map((item) => {
                  const n = badge(item.href);
                  return (
                    <SidebarMenuItem key={item.href}>
                      <SidebarMenuButton
                        asChild
                        isActive={isActive(item, pathname)}
                        tooltip={item.title}
                        className="text-muted-foreground hover:text-foreground data-[active=true]:font-medium data-[active=true]:text-foreground data-[active=true]:[&>svg]:text-primary"
                      >
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
        <Button asChild className="w-full shadow-sm shadow-primary/20 group-data-[collapsible=icon]:hidden">
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
