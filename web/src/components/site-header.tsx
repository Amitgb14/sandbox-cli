"use client";

import { useEffect, useState } from "react";
import { usePathname } from "next/navigation";
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
import {
  NavigationMenu,
  NavigationMenuContent,
  NavigationMenuItem,
  NavigationMenuLink,
  NavigationMenuList,
  NavigationMenuTrigger,
  navigationMenuTriggerStyle,
} from "@/components/ui/navigation-menu";
import { GithubMark, Wordmark } from "@/components/logo";
import { NAV, type NavEntry } from "@/lib/nav";
import { DOC_URL, REPO_URL, VERSION } from "@/lib/site";
import { cn } from "@/lib/utils";

/**
 * Desktop nav: three grouped menus and one plain link (see lib/nav.ts for why
 * the flat row had to go).
 *
 * Two details are load-bearing rather than decorative:
 *
 *  - `closeOnClick` on every link. These are same-page anchors, so without it
 *    the menu stays open hanging over the section it just scrolled you to —
 *    which reads as the click having failed.
 *  - `align="end"`. The menu sits at the right-hand end of the header, so a
 *    popup centred under its trigger would hang off the viewport for the last
 *    group at exactly the widths where the row only just fits.
 */

/**
 * True for a nav target that is a route rather than an anchor on this page.
 *
 * Routes are rendered through `next/link` — via Base UI's `render` prop, so the
 * menu keeps its own behaviour (`closeOnClick`, focus handling) and only the
 * element underneath changes. A raw `<a href="/multi-agent/">` would work today
 * and break the moment the site is served from a subpath, because `basePath` is
 * applied by the framework's Link and by nothing else.
 */
function isRoute(href: string) {
  return href.startsWith("/");
}

/** One row of the mobile sheet, a Link for routes and an anchor for anchors. */
function MobileNavLink({
  href,
  label,
  onNavigate,
  className,
}: {
  href: string;
  label: string;
  onNavigate: () => void;
  className?: string;
}) {
  const styles = cn(
    "rounded-md text-sm text-muted-foreground transition-colors hover:bg-muted hover:text-foreground",
    className,
  );
  if (isRoute(href)) {
    return (
      <Link href={href} onClick={onNavigate} className={styles}>
        {label}
      </Link>
    );
  }
  return (
    <a href={href} onClick={onNavigate} className={styles}>
      {label}
    </a>
  );
}

/**
 * The nav, home link and install link are parameters so a second route can pass
 * its own — but the *defaults* are derived from the route rather than fixed,
 * and that is a fix rather than a flourish.
 *
 * They used to default to the landing page's own anchors, `#top` and
 * `#install`, which are right on `/` and silently wrong everywhere else: on a
 * sub-page the wordmark scrolled to the top of the page you were already on
 * instead of going home, and Install pointed at a section that page does not
 * have. Two of the three sub-pages had the bug — one had remembered to pass
 * both props, which is precisely the kind of thing a person remembers once.
 *
 * So the default asks where it is: an anchor on the landing page, and a route
 * anywhere else. A page may still override either.
 */
export function SiteHeader({
  nav = NAV,
  homeHref,
  installHref,
}: {
  nav?: NavEntry[];
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

  // `usePathname` returns the route with the trailing slash this site exports
  // with, so `/sdk/` and `/sdk` both have to read as "not the landing page".
  const pathname = usePathname();
  const onLanding = pathname === "/" || pathname === "";
  const home = homeHref ?? (onLanding ? "#top" : "/");
  const install = installHref ?? (onLanding ? "#install" : "/#install");

  return (
    <header
      className={cn(
        "sticky top-0 z-50 w-full transition-colors duration-200",
        stuck ? "border-b bg-background/80 backdrop-blur-md" : "bg-transparent",
      )}
    >
      <div className="mx-auto flex h-14 w-full max-w-6xl items-center gap-4 px-5 sm:px-6">
        {isRoute(home) ? (
          <Link href={home} className="flex items-center gap-2 text-[0.95rem]">
            <Wordmark />
          </Link>
        ) : (
          <a href={home} className="flex items-center gap-2 text-[0.95rem]">
            <Wordmark />
          </a>
        )}
        <span className="hidden rounded-full border px-2 py-0.5 font-mono text-[0.65rem] text-muted-foreground sm:inline">
          v{VERSION}
        </span>

        <NavigationMenu align="end" className="ml-auto hidden lg:flex">
          <NavigationMenuList className="gap-0.5">
            {nav.map((entry) =>
              entry.kind === "link" ? (
                <NavigationMenuItem key={entry.href}>
                  <NavigationMenuLink
                    href={entry.href}
                    closeOnClick
                    render={isRoute(entry.href) ? <Link href={entry.href} /> : undefined}
                    className={cn(
                      navigationMenuTriggerStyle(),
                      "text-muted-foreground hover:text-foreground",
                    )}
                  >
                    {entry.label}
                  </NavigationMenuLink>
                </NavigationMenuItem>
              ) : (
                <NavigationMenuItem key={entry.label}>
                  <NavigationMenuTrigger className="text-muted-foreground hover:text-foreground">
                    {entry.label}
                  </NavigationMenuTrigger>
                  <NavigationMenuContent>
                    <ul className="grid w-[22rem] gap-0.5">
                      {entry.items.map((item) => (
                        <li key={item.href}>
                          <NavigationMenuLink
                            href={item.href}
                            closeOnClick
                            render={isRoute(item.href) ? <Link href={item.href} /> : undefined}
                            className="flex-col items-start gap-0.5 p-2.5"
                          >
                            <span className="text-[0.83rem] font-medium text-foreground">
                              {item.label}
                            </span>
                            <span className="text-[0.72rem] leading-snug text-muted-foreground">
                              {item.hint}
                            </span>
                          </NavigationMenuLink>
                        </li>
                      ))}
                    </ul>
                  </NavigationMenuContent>
                </NavigationMenuItem>
              ),
            )}
            <NavigationMenuItem>
              <NavigationMenuLink
                href={DOC_URL.guide}
                target="_blank"
                rel="noopener noreferrer"
                className={cn(
                  navigationMenuTriggerStyle(),
                  "text-muted-foreground hover:text-foreground",
                )}
              >
                Docs
              </NavigationMenuLink>
            </NavigationMenuItem>
          </NavigationMenuList>
        </NavigationMenu>

        <div className="ml-auto flex items-center gap-2 lg:ml-3">
          <a
            href={REPO_URL}
            target="_blank"
            rel="noopener noreferrer"
            className={cn(buttonVariants({ variant: "outline", size: "sm" }), "gap-1.5")}
          >
            <GithubMark className="size-3.5" />
            <span className="hidden sm:inline">GitHub</span>
          </a>
          <a href={install} className={cn(buttonVariants({ size: "sm" }), "hidden sm:inline-flex")}>
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
            {/* The sheet keeps the same grouping, as headed sections rather than
                collapsibles: on a phone the whole list is one scroll, and a
                disclosure that hides four links behind a tap buys nothing. */}
            <SheetContent side="right" className="w-72 overflow-y-auto">
              <SheetHeader>
                <SheetTitle>
                  <Wordmark />
                </SheetTitle>
              </SheetHeader>
              <nav className="flex flex-col gap-4 px-4 pb-6">
                {nav.map((entry) =>
                  entry.kind === "link" ? (
                    <MobileNavLink
                      key={entry.href}
                      href={entry.href}
                      label={entry.label}
                      onNavigate={() => setOpen(false)}
                      className="px-2 py-2"
                    />
                  ) : (
                    <div key={entry.label} className="flex flex-col gap-0.5">
                      <span className="eyebrow px-2 pb-1">{entry.label}</span>
                      {entry.items.map((item) => (
                        <MobileNavLink
                          key={item.href}
                          href={item.href}
                          label={item.label}
                          onNavigate={() => setOpen(false)}
                          className="px-2 py-1.5"
                        />
                      ))}
                    </div>
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
                  href={install}
                  onClick={() => setOpen(false)}
                  className={cn(buttonVariants({ size: "sm" }))}
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
