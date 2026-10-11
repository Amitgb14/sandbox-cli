import { SiteHeader } from "@/components/site-header";
import { SiteFooter } from "@/components/site-footer";
import { DocsSidebar } from "@/components/docs/docs-nav";
import { type NavEntry } from "@/lib/nav";
import { SETUP_PATH, STUDIO_PATH } from "@/lib/site";

/**
 * Every /docs page: the site's header and footer around a sidebar and the
 * page. The header gets routes rather than the landing page's anchors, which
 * would do nothing here (see SiteHeader).
 */
const NAV: NavEntry[] = [
  { kind: "link", href: "/", label: "Home" },
  { kind: "link", href: SETUP_PATH, label: "Setup guide" },
  { kind: "link", href: STUDIO_PATH, label: "Studio" },
];

export default function DocsLayout({ children }: { children: React.ReactNode }) {
  return (
    <div id="top" className="flex min-h-full flex-col">
      <SiteHeader nav={NAV} homeHref="/" installHref="/#install" />
      <main className="mx-auto flex w-full max-w-7xl flex-1 px-5 sm:px-6">
        <DocsSidebar />
        {children}
      </main>
      <SiteFooter />
    </div>
  );
}
