import { SidebarInset, SidebarProvider, SidebarTrigger } from "@/components/ui/sidebar";
import { AppSidebar } from "@/components/shell/app-sidebar";
import { TokenGate } from "@/components/shell/token-gate";
import { CommandPalette } from "@/components/shell/command-palette";
import { GlobalShortcuts } from "@/components/shell/global-shortcuts";

/**
 * The sidebar is the only chrome. On a phone, where it is a drawer, a slim
 * bar holds the button that opens it; everywhere else the page starts at its
 * title.
 */
export default function StudioLayout({ children }: { children: React.ReactNode }) {
  return (
    <SidebarProvider>
      <AppSidebar />
      <SidebarInset className="min-w-0">
        <div className="sticky top-0 z-30 flex h-12 items-center gap-2 border-b bg-background/80 px-3 backdrop-blur md:hidden">
          <SidebarTrigger />
          <span className="text-sm font-semibold tracking-tight">sandbox·studio</span>
        </div>
        <TokenGate />
        {/* A measure, so a table on a wide screen is not a row of islands. */}
        <main className="min-w-0 flex-1 px-4 py-6 md:px-10 md:py-10">
          <div className="mx-auto w-full max-w-6xl">{children}</div>
        </main>
      </SidebarInset>
      <CommandPalette />
      <GlobalShortcuts />
    </SidebarProvider>
  );
}
