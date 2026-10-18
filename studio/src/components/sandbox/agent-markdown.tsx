"use client";

import { memo } from "react";
import ReactMarkdown, { type Components } from "react-markdown";
import remarkGfm from "remark-gfm";

/**
 * What the agent writes is Markdown, and was shown as its source: tables as
 * rows of pipes, **bold** with its stars. It is the guest's text, so it is
 * drawn as Markdown and nothing more. react-markdown builds React elements and
 * never raw HTML (an HTML tag in it stays text). A link is kept only when it is
 * an absolute web or mail address (safeURL), and opens in a new tab, without
 * this page as its opener or referrer. An image is its alt text: drawing it would have the
 * browser fetch whatever address the guest wrote, telling it who is looking
 * and when; what the agent saw arrives as its tool calls' own images instead.
 */
/**
 * The only links a reply keeps: absolute http, https and mailto addresses.
 * react-markdown's own check stops only unsafe schemes, so a link with none —
 * "/#key=sgk_…", "#token=…" — passed, and opened Studio itself with a
 * fragment it acts on: a gateway key to sign in with, a token to use. A link
 * dropped here is its text alone. It drops footnotes' in-page links too, which
 * opened a second Studio rather than scrolling.
 */
export function safeURL(url: string): string | undefined {
  try {
    const u = new URL(url);
    if (u.protocol === "http:" || u.protocol === "https:" || u.protocol === "mailto:") return url;
  } catch {
    // Relative, or not a URL at all.
  }
  return undefined;
}

const components: Components = {
  // In Markdown a single newline is a space; an agent's plain lines ("Step 1…",
  // "Step 2…") are kept as lines, as they were shown before.
  p: ({ children }) => <p className="my-2 whitespace-pre-wrap first:mt-0 last:mb-0">{children}</p>,
  h1: ({ children }) => <h3 className="mt-3 mb-1.5 text-base font-semibold first:mt-0">{children}</h3>,
  h2: ({ children }) => <h3 className="mt-3 mb-1.5 text-base font-semibold first:mt-0">{children}</h3>,
  h3: ({ children }) => <h4 className="mt-3 mb-1 font-semibold first:mt-0">{children}</h4>,
  h4: ({ children }) => <h5 className="mt-2 mb-1 font-semibold first:mt-0">{children}</h5>,
  h5: ({ children }) => <h6 className="mt-2 mb-1 font-semibold first:mt-0">{children}</h6>,
  h6: ({ children }) => <h6 className="mt-2 mb-1 font-semibold first:mt-0">{children}</h6>,
  ul: ({ children }) => <ul className="my-2 list-disc space-y-0.5 pl-5">{children}</ul>,
  ol: ({ children }) => <ol className="my-2 list-decimal space-y-0.5 pl-5">{children}</ol>,
  blockquote: ({ children }) => <blockquote className="my-2 border-l-2 pl-3 text-muted-foreground">{children}</blockquote>,
  hr: () => <hr className="my-3" />,
  a: ({ href, children }) =>
    href ? (
      <a href={href} target="_blank" rel="noopener noreferrer nofollow" className="text-primary underline underline-offset-2">
        {children}
      </a>
    ) : (
      <span>{children}</span>
    ),
  img: ({ alt }) => <span className="text-muted-foreground">[image{alt ? `: ${alt}` : ""}]</span>,
  // A block's code drops the inline box, whether or not it names a language.
  pre: ({ children }) => (
    <pre className="my-2 overflow-x-auto rounded-md border bg-muted/40 p-2.5 font-mono text-xs [&>code]:rounded-none [&>code]:bg-transparent [&>code]:p-0 [&>code]:text-xs">
      {children}
    </pre>
  ),
  code: ({ className, children }) => <code className={`rounded bg-muted px-1 py-0.5 font-mono text-[0.85em] ${className ?? ""}`}>{children}</code>,
  table: ({ children }) => (
    <div className="my-2 overflow-x-auto">
      <table className="w-max min-w-full border-collapse text-xs">{children}</table>
    </div>
  ),
  th: ({ children, style }) => (
    <th style={style} className="border bg-muted/50 px-2 py-1 text-left font-medium">
      {children}
    </th>
  ),
  td: ({ children, style }) => (
    <td style={style} className="border px-2 py-1 align-top">
      {children}
    </td>
  ),
};

const plugins = [remarkGfm];

/**
 * Memoised: the tab draws every entry again on each streamed chunk, and a
 * reply that is not changing need not be parsed again.
 */
export const AgentMarkdown = memo(function AgentMarkdown({ text }: { text: string }) {
  return (
    <div className="break-words text-sm">
      <ReactMarkdown remarkPlugins={plugins} components={components} urlTransform={safeURL}>
        {text}
      </ReactMarkdown>
    </div>
  );
});
