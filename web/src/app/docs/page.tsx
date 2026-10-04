import type { Metadata } from "next";
import { DocArticle } from "@/components/docs/doc-article";
import { pageBySlug } from "@/lib/docs";

/** /docs itself: the overview, docs/README.md. */
const page = pageBySlug("")!;

export const metadata: Metadata = {
  title: "Documentation — sandbox-cli",
  description: page.description,
  openGraph: { title: "Documentation — sandbox-cli", description: page.description, type: "article" },
};

export default function DocsIndex() {
  return <DocArticle page={page} />;
}
