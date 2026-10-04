import type { Metadata } from "next";
import { notFound } from "next/navigation";
import { DocArticle } from "@/components/docs/doc-article";
import { DOC_PAGES, pageBySlug } from "@/lib/docs";

/**
 * Every page of the manifest but the overview, which is /docs itself. The
 * export builds exactly these: an address the manifest does not list is not a
 * page, so there is nothing for a static host to answer it with.
 */
export const dynamicParams = false;

export function generateStaticParams() {
  return DOC_PAGES.filter((p) => p.slug !== "").map((p) => ({ slug: p.slug.split("/") }));
}

type Props = { params: Promise<{ slug: string[] }> };

export async function generateMetadata({ params }: Props): Promise<Metadata> {
  const page = pageBySlug((await params).slug.join("/"));
  if (!page) return {};
  const title = `${page.title} — sandbox-cli docs`;
  return {
    title,
    description: page.description,
    openGraph: { title, description: page.description, type: "article" },
  };
}

export default async function DocPageRoute({ params }: Props) {
  const page = pageBySlug((await params).slug.join("/"));
  if (!page || page.slug === "") notFound();
  return <DocArticle page={page} />;
}
