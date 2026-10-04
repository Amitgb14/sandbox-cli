/**
 * Renders a page of the repository's Markdown into HTML, at build time only.
 *
 * This runs in server components during `next build` (the site is a static
 * export, so there is no "later"): the HTML is in the exported files and no
 * Markdown, and no renderer, reaches a browser.
 *
 * The renderer is `marked`: one package with no dependencies of its own, GFM
 * tables and fenced code built in, and a renderer object small enough to
 * override the four things this site needs to decide itself:
 *
 *  - **Heading ids** in GitHub's style (`slug` below), so an anchor written
 *    against the file on GitHub — `fleet.md#giving-users-keys` — lands on the
 *    same heading here.
 *  - **Links.** A link to another rendered doc becomes its /docs address, a
 *    relative link to anything else in the repository becomes its GitHub URL,
 *    and a relative link to a file that does not exist fails the build.
 *  - **Raw HTML is never passed through.** The docs are our own, but nothing
 *    in them needs HTML, and a page that passed it through would be one
 *    careless paste from running script on the site. An HTML comment (the
 *    generated-file note in docs/cli.md) is dropped; anything else is shown
 *    as the text it is.
 *  - **Code** is set as text, with no highlighter: the docs' blocks are shell,
 *    YAML and JSON a reader copies, and colour would be decoration competing
 *    with the comments, as on the rest of the site (components/code-block.tsx).
 */

import fs from "node:fs";
import path from "node:path";
import { Marked, type Tokens } from "marked";
import { BLOB, TREE } from "@/lib/site";
import { pageBySource, pageHref, type DocPage } from "@/lib/docs";

/** The repository root: web/ is one level down, and `next build` runs there. */
export const REPO_ROOT = path.resolve(process.cwd(), "..");

export type DocHeading = { depth: number; id: string; text: string };

export type RenderedDoc = {
  html: string;
  /** The first h1's text, or the manifest's title. */
  title: string;
  /** Every h2 and h3, for "On this page". */
  toc: DocHeading[];
};

const ENTITIES: Record<string, string> = {
  "&amp;": "&",
  "&lt;": "<",
  "&gt;": ">",
  "&quot;": '"',
  "&#39;": "'",
};

function escapeHtml(s: string) {
  return s
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&#39;");
}

/** The text of rendered inline HTML: tags dropped, entities decoded. */
function plainText(html: string) {
  return html.replace(/<[^>]*>/g, "").replace(/&(amp|lt|gt|quot|#39);/g, (m) => ENTITIES[m] ?? m);
}

/**
 * GitHub's heading anchor: lowercase, every character that is not a letter,
 * a mark, a digit, a connector (`_`), a space or `-` dropped, spaces turned
 * into dashes. A repeated heading gets `-1`, `-2`, … in order. internal/cli's
 * `anchor` is the same rule for the ASCII command names it writes links to.
 */
export function slug(text: string) {
  return text
    .toLowerCase()
    .replace(/[^\p{L}\p{M}\p{N}\p{Pc} -]/gu, "")
    .replace(/ /g, "-");
}

/**
 * Where a link in `source` should go on the site. Throws for a relative link
 * to a file the repository does not have: that is a broken link in the docs,
 * and the build is where it should be found.
 */
export function resolveLink(source: string, href: string): { href: string; external: boolean } {
  if (/^[a-z][a-z0-9+.-]*:/i.test(href)) return { href, external: true };
  if (href.startsWith("#") || href === "") return { href, external: false };

  const hashAt = href.indexOf("#");
  const target = hashAt < 0 ? href : href.slice(0, hashAt);
  const anchor = hashAt < 0 ? "" : href.slice(hashAt + 1);
  const resolved = path.posix
    .normalize(target.startsWith("/") ? target.slice(1) : path.posix.join(path.posix.dirname(source), target))
    .replace(/\/$/, "");
  if (resolved.startsWith("..")) {
    throw new Error(`${source}: link ${href} leaves the repository`);
  }

  const page = pageBySource(resolved);
  if (page) return { href: pageHref(page, anchor || undefined), external: false };

  const onDisk = path.join(REPO_ROOT, resolved);
  if (!fs.existsSync(onDisk)) {
    throw new Error(`${source}: link ${href} names ${resolved}, which is not in the repository`);
  }
  const base = fs.statSync(onDisk).isDirectory() ? TREE : BLOB;
  return { href: `${base}/${resolved}${anchor ? `#${anchor}` : ""}`, external: true };
}

/** Renders one page's Markdown. */
export function renderMarkdown(page: DocPage, markdown: string): RenderedDoc {
  const seen = new Map<string, number>();
  const toc: DocHeading[] = [];
  let title = "";

  const marked = new Marked({
    gfm: true,
    async: false,
    renderer: {
      heading({ tokens, depth }: Tokens.Heading) {
        const inner = this.parser.parseInline(tokens);
        const text = plainText(inner);
        let id = slug(text);
        const n = seen.get(id) ?? 0;
        seen.set(id, n + 1);
        if (n > 0) id = `${id}-${n}`;
        if (depth === 1 && !title) title = text;
        if (depth === 2 || depth === 3) toc.push({ depth, id, text });
        return `<h${depth} id="${id}"><a class="doc-anchor" href="#${id}" aria-label="Link to this section">#</a>${inner}</h${depth}>\n`;
      },
      link({ href, title: linkTitle, tokens }: Tokens.Link) {
        const inner = this.parser.parseInline(tokens);
        const to = resolveLink(page.source, href);
        const attrs = [`href="${escapeHtml(to.href)}"`];
        if (linkTitle) attrs.push(`title="${escapeHtml(linkTitle)}"`);
        if (to.external) attrs.push('target="_blank"', 'rel="noopener noreferrer"');
        return `<a ${attrs.join(" ")}>${inner}</a>`;
      },
      html({ text }: Tokens.HTML | Tokens.Tag) {
        if (/^\s*<!--[\s\S]*?-->\s*$/.test(text)) return "";
        return escapeHtml(text);
      },
      code({ text, lang }: Tokens.Code) {
        const l = (lang ?? "").trim().split(/\s+/)[0];
        const label = l ? ` data-lang="${escapeHtml(l)}"` : "";
        return `<pre${label}><code>${escapeHtml(text.replace(/\n$/, ""))}</code></pre>\n`;
      },
      image({ href, text }: Tokens.Image) {
        // No doc has an image yet; one that does gets it from GitHub, as
        // every other non-doc file.
        const to = resolveLink(page.source, href);
        return `<img src="${escapeHtml(to.href.replace("/blob/", "/raw/"))}" alt="${escapeHtml(text)}" />`;
      },
    },
  });

  let html = marked.parse(markdown) as string;
  // A wide table scrolls inside its own box instead of widening the page.
  html = html.replace(/<table>/g, '<div class="doc-table"><table>').replace(/<\/table>/g, "</table></div>");
  return { html, title: title || page.title, toc };
}

/** Reads and renders a page from the repository. */
export function loadDoc(page: DocPage): RenderedDoc {
  const markdown = fs.readFileSync(path.join(REPO_ROOT, page.source), "utf8");
  return renderMarkdown(page, markdown);
}
