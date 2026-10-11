"use client";

import { useState } from "react";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { Menu } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Sheet, SheetContent, SheetHeader, SheetTitle, SheetTrigger } from "@/components/ui/sheet";
import { DOC_SEGMENTS, pageHref } from "@/lib/docs";
import { cn } from "@/lib/utils";

/**
 * The docs' sidebar: the segments and their pages, the current one marked.
 *
 * A client component for one reason, `usePathname`: the layout that holds it
 * is shared by every page and is not re-rendered per page, so only the client
 * knows which page is showing. Paths are compared without their trailing
 * slash, which `trailingSlash: true` adds to every exported address.
 */
function trim(p: string) {
  return p.length > 1 ? p.replace(/\/$/, "") : p;
}

function SegmentList({ onNavigate }: { onNavigate?: () => void }) {
  const current = trim(usePathname() ?? "");
  return (
    <nav aria-label="Documentation" className="flex flex-col gap-6">
      {DOC_SEGMENTS.map((segment) => (
        <div key={segment.title} className="flex flex-col gap-1">
          <p className="eyebrow pb-1">{segment.title}</p>
          <ul className="flex flex-col gap-0.5">
            {segment.pages.map((page) => {
              const href = pageHref(page);
              const active = trim(href) === current;
              return (
                <li key={page.source}>
                  <Link
                    href={href}
                    onClick={onNavigate}
                    aria-current={active ? "page" : undefined}
                    className={cn(
                      "block rounded-md px-2.5 py-1.5 text-sm transition-colors",
                      active
                        ? "bg-muted font-medium text-foreground"
                        : "text-muted-foreground hover:bg-muted/60 hover:text-foreground",
                    )}
                  >
                    {page.title}
                  </Link>
                </li>
              );
            })}
          </ul>
        </div>
      ))}
    </nav>
  );
}

/** The sidebar from `lg` up: sticky beside the page, scrolling on its own. */
export function DocsSidebar() {
  return (
    <aside className="sticky top-14 hidden h-[calc(100vh-3.5rem)] w-56 shrink-0 overflow-y-auto py-8 pr-4 lg:block">
      <SegmentList />
    </aside>
  );
}

/**
 * Below `lg` the sidebar is behind a button at the top of the page, in the
 * same sheet the site header uses for its own menu.
 */
export function DocsMobileNav({ current }: { current: string }) {
  const [open, setOpen] = useState(false);
  return (
    <div className="mb-6 flex items-center gap-2 border-b pb-3 lg:hidden">
      <Sheet open={open} onOpenChange={setOpen}>
        <SheetTrigger
          render={
            <Button variant="outline" size="sm" className="gap-1.5">
              <Menu className="size-3.5" />
              Docs
            </Button>
          }
        />
        <SheetContent side="left" className="w-72 overflow-y-auto">
          <SheetHeader>
            <SheetTitle>Documentation</SheetTitle>
          </SheetHeader>
          <div className="px-4 pb-6">
            <SegmentList onNavigate={() => setOpen(false)} />
          </div>
        </SheetContent>
      </Sheet>
      <span className="min-w-0 truncate text-sm text-muted-foreground">{current}</span>
    </div>
  );
}
