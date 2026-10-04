import { SidebarInset, SidebarProvider } from "@/components/ui/sidebar";
import { AppSidebar } from "@/components/shell/app-sidebar";
import { AppHeader } from "@/components/shell/app-header";
import { TokenGate } from "@/components/shell/token-gate";
import { CommandPalette } from "@/components/shell/command-palette";
import { GlobalShortcuts } from "@/components/shell/global-shortcuts";

export default function StudioLayout({ children }: { children: React.ReactNode }) {
  return (
    <SidebarProvider>
      <AppSidebar />
      <SidebarInset className="min-w-0">
        <AppHeader />
        <TokenGate />
        {/* A measure, so a table on a wide screen is not a row of islands. */}
        <main className="min-w-0 flex-1 px-4 py-6 md:px-8 md:py-8">
          <div className="mx-auto w-full max-w-6xl">{children}</div>
        </main>
      </SidebarInset>
      <CommandPalette />
      <GlobalShortcuts />
    </SidebarProvider>
  );
}
