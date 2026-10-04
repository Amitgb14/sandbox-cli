import Link from "next/link";
import { ArrowLeft, ArrowRight, Pencil } from "lucide-react";
import { DocsMobileNav } from "@/components/docs/docs-nav";
import { editHref, neighbours, pageHref, segmentOf, type DocPage } from "@/lib/docs";
import { loadDoc } from "@/lib/markdown";

/**
 * One page of the docs: the rendered Markdown in a reading-width column, "On
 * this page" beside it from `xl` up, and previous / next in sidebar order.
 *
 * The HTML comes from our own repository's Markdown, rendered during the build
 * with raw HTML escaped (lib/markdown.ts); nothing is rendered at runtime.
 */
export function DocArticle({ page }: { page: DocPage }) {
  const doc = loadDoc(page);
  const { prev, next } = neighbours(page);
  const segment = segmentOf(page);

  return (
    <div className="flex min-w-0 flex-1 gap-10 py-8 lg:pl-8">
      <div className="min-w-0 flex-1">
        <DocsMobileNav current={page.title} />
        {segment ? <p className="eyebrow mb-3">{segment.title}</p> : null}
        <article
          className="doc-prose max-w-3xl"
          // Rendered at build time from the repository's own docs; raw HTML in
          // them is escaped, never passed through (lib/markdown.ts).
          dangerouslySetInnerHTML={{ __html: doc.html }}
        />

        <div className="mt-12 flex max-w-3xl flex-wrap items-center justify-between gap-3 border-t pt-5 text-sm text-muted-foreground">
          {page.generatedFrom ? (
            <a
              href={page.generatedFrom.href}
              target="_blank"
              rel="noopener noreferrer"
              className="inline-flex items-center gap-1.5 transition-colors hover:text-foreground"
            >
              <Pencil className="size-3.5" />
              {page.generatedFrom.label}
            </a>
          ) : (
            <a
              href={editHref(page)}
              target="_blank"
              rel="noopener noreferrer"
              className="inline-flex items-center gap-1.5 transition-colors hover:text-foreground"
            >
              <Pencil className="size-3.5" />
              Edit on GitHub
            </a>
          )}
          <span className="font-mono text-xs">{page.source}</span>
        </div>

        <nav aria-label="Previous and next" className="mt-6 grid max-w-3xl grid-cols-1 gap-3 sm:grid-cols-2">
          {prev ? (
            <Link
              href={pageHref(prev)}
              className="group flex flex-col gap-1 rounded-xl border bg-card p-4 transition-colors hover:border-foreground/30"
            >
              <span className="inline-flex items-center gap-1 text-xs text-muted-foreground">
                <ArrowLeft className="size-3" /> Previous
              </span>
              <span className="text-sm font-medium">{prev.title}</span>
            </Link>
          ) : (
            <span />
          )}
          {next ? (
            <Link
              href={pageHref(next)}
              className="group flex flex-col items-end gap-1 rounded-xl border bg-card p-4 text-right transition-colors hover:border-foreground/30"
            >
              <span className="inline-flex items-center gap-1 text-xs text-muted-foreground">
                Next <ArrowRight className="size-3" />
              </span>
              <span className="text-sm font-medium">{next.title}</span>
            </Link>
          ) : null}
        </nav>
      </div>

      {doc.toc.length > 0 ? (
        <aside className="sticky top-14 hidden h-[calc(100vh-3.5rem)] w-52 shrink-0 overflow-y-auto py-1 xl:block">
          <p className="eyebrow pb-2">On this page</p>
          <ul className="flex flex-col gap-1 border-l text-[0.8rem]">
            {doc.toc.map((h) => (
              <li key={h.id}>
                <a
                  href={`#${h.id}`}
                  className={
                    h.depth === 3
                      ? "-ml-px block border-l border-transparent py-0.5 pl-6 text-muted-foreground transition-colors hover:border-foreground/40 hover:text-foreground"
                      : "-ml-px block border-l border-transparent py-0.5 pl-3 text-muted-foreground transition-colors hover:border-foreground/40 hover:text-foreground"
                  }
                >
                  {h.text}
                </a>
              </li>
            ))}
          </ul>
        </aside>
      ) : null}
    </div>
  );
}
