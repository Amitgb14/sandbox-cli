import { SidebarInset, SidebarProvider } from "@/components/ui/sidebar";
import { AppSidebar } from "@/components/shell/app-sidebar";
import { AppHeader } from "@/components/shell/app-header";
import { TokenBar } from "@/components/shell/token-bar";
import { CommandPalette } from "@/components/shell/command-palette";
import { GlobalShortcuts } from "@/components/shell/global-shortcuts";

export default function StudioLayout({ children }: { children: React.ReactNode }) {
  return (
    <SidebarProvider>
      <AppSidebar />
      <SidebarInset className="min-w-0">
        <AppHeader />
        <TokenBar />
        <main className="min-w-0 flex-1 p-4 md:p-6">{children}</main>
      </SidebarInset>
      <CommandPalette />
      <GlobalShortcuts />
    </SidebarProvider>
  );
}
