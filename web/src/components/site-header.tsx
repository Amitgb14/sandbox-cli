"use client";

import { useEffect, useState } from "react";
import Link from "next/link";
import { Menu } from "lucide-react";
import { Button, buttonVariants } from "@/components/ui/button";
import {
  Sheet,
  SheetContent,
  SheetHeader,
  SheetTitle,
  SheetTrigger,
} from "@/components/ui/sheet";
import { GithubMark, Wordmark } from "@/components/logo";
import { DOC_URL, MULTI_AGENT_PATH, REPO_URL, VERSION } from "@/lib/site";
import { cn } from "@/lib/utils";

export type NavItem = { href: string; label: string };

const NAV: NavItem[] = [
  { href: "#threat", label: "Why" },
  { href: "#features", label: "Features" },
  { href: "#command", label: "The command" },
  { href: "#config", label: "Config" },
  { href: "#agents", label: "Agents" },
  { href: "#compare", label: "Compare" },
  { href: "#deploy", label: "Dev & prod" },
  { href: MULTI_AGENT_PATH, label: "Multi-agent" },
  { href: "#setup", label: "Setup" },
];

/**
 * The nav is a parameter rather than a constant because the site now has more
 * than one route. Every default is the landing page's, so `<SiteHeader />` is
 * unchanged; a sub-page passes its own sections and absolute links home, since
 * an in-page anchor like `#threat` means nothing on a page that has no such
 * section and silently does nothing when clicked.
 */
export function SiteHeader({
  nav = NAV,
  homeHref = "#top",
  installHref = "#install",
}: {
  nav?: NavItem[];
  homeHref?: string;
  installHref?: string;
} = {}) {
  const [stuck, setStuck] = useState(false);
  const [open, setOpen] = useState(false);

  useEffect(() => {
    const onScroll = () => setStuck(window.scrollY > 8);
    onScroll();
    window.addEventListener("scroll", onScroll, { passive: true });
    return () => window.removeEventListener("scroll", onScroll);
  }, []);

  return (
    <header
      className={cn(
        "sticky top-0 z-50 w-full transition-colors duration-200",
        stuck ? "border-b bg-background/80 backdrop-blur-md" : "bg-transparent",
      )}
    >
      <div className="mx-auto flex h-14 w-full max-w-6xl items-center gap-4 px-5 sm:px-6">
        <a href={homeHref} className="flex items-center gap-2 text-[0.95rem]">
          <Wordmark />
        </a>
        <span className="hidden rounded-full border px-2 py-0.5 font-mono text-[0.65rem] text-muted-foreground sm:inline">
          v{VERSION}
        </span>

        <nav className="ml-auto hidden items-center gap-1 lg:flex">
          {nav.map((n) =>
            n.href.startsWith("#") ? (
              <a
                key={n.href}
                href={n.href}
                className="rounded-md px-2.5 py-1.5 text-sm text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
              >
                {n.label}
              </a>
            ) : (
              // A route, not an anchor: next/link applies basePath and keeps the
              // navigation client-side. A raw href would break the moment the
              // site is served from a subpath.
              <Link
                key={n.href}
                href={n.href}
                className="rounded-md px-2.5 py-1.5 text-sm text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
              >
                {n.label}
              </Link>
            ),
          )}
          <a
            href={DOC_URL.guide}
            target="_blank"
            rel="noopener noreferrer"
            className="rounded-md px-2.5 py-1.5 text-sm text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
          >
            Docs
          </a>
        </nav>

        <div className="ml-auto flex items-center gap-2 lg:ml-0">
          <a
            href={REPO_URL}
            target="_blank"
            rel="noopener noreferrer"
            className={cn(buttonVariants({ variant: "outline", size: "sm" }), "gap-1.5")}
          >
            <GithubMark className="size-3.5" />
            <span className="hidden sm:inline">GitHub</span>
          </a>
          <a href={installHref} className={cn(buttonVariants({ size: "sm" }), "hidden sm:inline-flex")}>
            Install
          </a>

          <Sheet open={open} onOpenChange={setOpen}>
            <SheetTrigger
              render={
                <Button variant="ghost" size="icon-sm" className="lg:hidden" aria-label="Menu">
                  <Menu />
                </Button>
              }
            />
            <SheetContent side="right" className="w-64">
              <SheetHeader>
                <SheetTitle>
                  <Wordmark />
                </SheetTitle>
              </SheetHeader>
              <nav className="flex flex-col gap-1 px-4 pb-6">
                {nav.map((n) =>
                  n.href.startsWith("#") ? (
                    <a
                      key={n.href}
                      href={n.href}
                      onClick={() => setOpen(false)}
                      className="rounded-md px-2 py-2 text-sm text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
                    >
                      {n.label}
                    </a>
                  ) : (
                    <Link
                      key={n.href}
                      href={n.href}
                      onClick={() => setOpen(false)}
                      className="rounded-md px-2 py-2 text-sm text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
                    >
                      {n.label}
                    </Link>
                  ),
                )}
                <a
                  href={DOC_URL.guide}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="rounded-md px-2 py-2 text-sm text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
                >
                  Docs
                </a>
                <a
                  href={installHref}
                  onClick={() => setOpen(false)}
                  className={cn(buttonVariants({ size: "sm" }), "mt-2")}
                >
                  Install v{VERSION}
                </a>
              </nav>
            </SheetContent>
          </Sheet>
        </div>
      </div>
    </header>
  );
}
