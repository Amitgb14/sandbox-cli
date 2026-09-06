"use client";

import { useMemo, type ReactNode } from "react";
import { CopyButton } from "@/components/common/copy-button";
import { cn } from "@/lib/utils";

/**
 * An agent's own words, formatted.
 *
 * This used to be `whitespace-pre-wrap` and the literal characters, and the
 * comment that stood there was right about why: transcript text is written by an
 * agent working in a repository whose contents *it* does not control either, so
 * it is untrusted twice over, and rendering it as markup is how a prompt
 * injection reaches the browser. What changed is not the threat but the weight —
 * the console is now how a Studio run is *read*, the terminal being for driving
 * one, so "safe but unreadable" costs more than it did when this was the
 * fallback view. Issue #151.
 *
 * So the rule is kept and the rendering is made safe rather than avoided, and
 * the way it is made safe is **structural, not configured**:
 *
 *   - **React elements only.** There is no `dangerouslySetInnerHTML` anywhere in
 *     this file, and no code path that turns a string into markup. An agent that
 *     writes `<img onerror=…>` gets those characters back as text, because there
 *     is nothing here that could do anything else with them. This is the whole
 *     security argument, and it is one grep away from being checked — which is
 *     why it is a hand-written parser rather than a library configured to
 *     disallow HTML: a configuration can be changed by someone who does not know
 *     what it was for, and the absence of a code path cannot.
 *
 *   - **No remote fetch, ever.** `![alt](url)` renders as *text*, not an image.
 *     An agent that writes one into its reply would otherwise make the browser
 *     fetch that URL, which is a beacon reporting when — and whether — somebody
 *     read the transcript. Nothing in a rendered reply causes a request.
 *
 *   - **Two schemes, and no autolinking.** A `[label](url)` becomes a link only
 *     for `http:` and `https:`; `javascript:`, `data:`, `file:` and everything
 *     else render as text showing both halves, so a reader sees what it was
 *     rather than a label that lies about where it goes. A bare URL in prose is
 *     left alone — autolinking is how a plausible-looking address becomes
 *     clickable without anyone writing a link at all.
 *
 * What it does not do: tables, footnotes, reference links, images, raw HTML.
 * Those degrade to their own source text rather than disappearing, which is the
 * same bargain the old renderer made for everything. The supported set — fenced
 * code, headings, lists, quotes, rules, bold, italic, inline code, links — is
 * what agents actually emit.
 */
export function AgentMarkdown({
  text,
  className,
}: {
  text: string;
  className?: string;
}) {
  // Transcripts are polled, so this runs on every refresh of a conversation that
  // usually has not changed. The parse is cheap per message and there can be
  // hundreds of them.
  const blocks = useMemo(() => parseBlocks(text), [text]);

  return (
    <div
      // A hook for the test that proves the security properties: the assertions
      // are about what this renderer emits, and scoping them to `main` picked up
      // the page's own chrome instead.
      data-agent-markdown=""
      className={cn("space-y-2 text-sm leading-relaxed", className)}
    >
      {blocks.map((b, i) => (
        <Block key={i} block={b} />
      ))}
    </div>
  );
}

/* ------------------------------------------------------------------ blocks */

type Block =
  | { kind: "p"; text: string }
  | { kind: "heading"; level: number; text: string }
  | { kind: "code"; lang: string; code: string }
  | { kind: "list"; ordered: boolean; items: string[]; start: number }
  | { kind: "quote"; text: string }
  | { kind: "hr" };

function Block({ block }: { block: Block }) {
  switch (block.kind) {
    case "code":
      return <CodeBlock lang={block.lang} code={block.code} />;

    case "heading": {
      // One element for every level, with size carrying the level rather than a
      // tag name: this renders inside a card in a run detail, so an <h1> here
      // would outrank the page's own heading in the document outline.
      const size =
        block.level <= 2
          ? "text-base"
          : block.level === 3
            ? "text-sm"
            : "text-xs";
      return (
        <p
          className={cn("font-semibold", size)}
          role="heading"
          aria-level={block.level}
        >
          <Inline text={block.text} />
        </p>
      );
    }

    case "list": {
      const cls =
        "ms-5 space-y-1 " + (block.ordered ? "list-decimal" : "list-disc");
      const items = block.items.map((it, i) => (
        <li key={i}>
          <Inline text={it} />
        </li>
      ));
      return block.ordered ? (
        <ol className={cls} start={block.start}>
          {items}
        </ol>
      ) : (
        <ul className={cls}>{items}</ul>
      );
    }

    case "quote":
      return (
        <blockquote className="border-s-2 ps-3 text-muted-foreground">
          <Inline text={block.text} />
        </blockquote>
      );

    case "hr":
      return <hr className="border-border" />;

    default:
      // Line breaks inside a paragraph are kept: an agent's numbered question
      // arrives as one block of lines, and losing them was what made the
      // unformatted version readable enough to ship in the first place.
      return (
        <p className="whitespace-pre-wrap break-words">
          <Inline text={block.text} />
        </p>
      );
  }
}

function CodeBlock({ lang, code }: { lang: string; code: string }) {
  return (
    <div className="group relative">
      {/* Copyable is most of why rendering code as code is worth doing: the
          thing a reader wants from a fenced block is its contents, in the
          clipboard, without selecting around the prose that surrounds it. */}
      <div className="absolute end-1.5 top-1.5 opacity-0 transition-opacity focus-within:opacity-100 group-hover:opacity-100">
        <CopyButton value={code} label="Copy code" />
      </div>
      {lang && (
        <span className="absolute start-3 top-1.5 text-[10px] uppercase tracking-wide text-muted-foreground">
          {lang}
        </span>
      )}
      <pre
        className={cn(
          "overflow-x-auto rounded-md border bg-muted/50 p-3 text-xs",
          lang && "pt-6",
        )}
      >
        <code>{code}</code>
      </pre>
    </div>
  );
}

const FENCE = /^\s{0,3}(`{3,}|~{3,})\s*([^\s`]*)/;
const HEADING = /^\s{0,3}(#{1,6})\s+(.*)$/;
const HR = /^\s{0,3}([-*_])(?:\s*\1){2,}\s*$/;
const UL = /^\s{0,3}[-*+]\s+(.*)$/;
const OL = /^\s{0,3}(\d{1,9})[.)]\s+(.*)$/;
const QUOTE = /^\s{0,3}>\s?(.*)$/;

/**
 * Split text into blocks, line by line.
 *
 * A fence swallows everything up to its closing run *including* what would
 * otherwise be syntax, which is the one rule that has to hold: an agent
 * explaining markdown inside a code block must not have the explanation parsed.
 * An unterminated fence — the common case while a reply is still streaming in —
 * runs to the end of the message rather than being abandoned, so a half-arrived
 * code block reads as code rather than as a stray paragraph of source.
 */
function parseBlocks(input: string): Block[] {
  const lines = input.replace(/\r\n?/g, "\n").split("\n");
  const out: Block[] = [];
  let para: string[] = [];
  let quote: string[] = [];

  const flushPara = () => {
    if (para.length) out.push({ kind: "p", text: para.join("\n") });
    para = [];
  };
  const flushQuote = () => {
    if (quote.length) out.push({ kind: "quote", text: quote.join("\n") });
    quote = [];
  };
  const flushAll = () => {
    flushPara();
    flushQuote();
  };

  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];

    const fence = FENCE.exec(line);
    if (fence) {
      flushAll();
      const marker = fence[1][0];
      const len = fence[1].length;
      const body: string[] = [];
      i++;
      for (; i < lines.length; i++) {
        const close = new RegExp(
          `^\\s{0,3}${marker === "`" ? "`" : "~"}{${len},}\\s*$`,
        );
        if (close.test(lines[i])) break;
        body.push(lines[i]);
      }
      out.push({ kind: "code", lang: fence[2] ?? "", code: body.join("\n") });
      continue;
    }

    if (!line.trim()) {
      flushAll();
      continue;
    }

    const q = QUOTE.exec(line);
    if (q) {
      flushPara();
      quote.push(q[1]);
      continue;
    }
    flushQuote();

    if (HR.test(line)) {
      flushPara();
      out.push({ kind: "hr" });
      continue;
    }

    const h = HEADING.exec(line);
    if (h) {
      flushPara();
      out.push({ kind: "heading", level: h[1].length, text: h[2] });
      continue;
    }

    const ul = UL.exec(line);
    const ol = OL.exec(line);
    if (ul || ol) {
      flushPara();
      const ordered = !!ol;
      const items: string[] = [ordered ? ol![2] : ul![1]];
      const start = ordered ? Number(ol![1]) : 1;
      // Consume the rest of the run, and continuation lines with it: a wrapped
      // bullet is one item, not an item and a paragraph.
      while (i + 1 < lines.length) {
        const next = lines[i + 1];
        const nu = UL.exec(next);
        const no = OL.exec(next);
        if (ordered ? no : nu) {
          items.push(ordered ? no![2] : nu![1]);
          i++;
          continue;
        }
        if (next.trim() && /^\s{2,}\S/.test(next) && !nu && !no) {
          items[items.length - 1] += "\n" + next.trim();
          i++;
          continue;
        }
        break;
      }
      out.push({ kind: "list", ordered, items, start });
      continue;
    }

    para.push(line);
  }
  flushAll();
  return out;
}

/* ------------------------------------------------------------------ inline */

/**
 * Inline spans, in the one order that works: code first.
 *
 * A backtick span suppresses everything inside it, so `**not bold**` written in
 * code has to be matched before the emphasis rules ever see those asterisks.
 * Links come next so their label can still carry emphasis, then bold before
 * italic — `**` is a prefix of `*`, and testing the shorter one first would read
 * every bold marker as two empty italics.
 */
const INLINE =
  /(`+)([\s\S]*?[^`])\1(?!`)|(!?)\[([^\]]*)\]\(([^()\s]*)\)|(\*\*|__)([\s\S]+?)\6|(\*|_)([^\s*_][\s\S]*?)\8/;

function Inline({ text }: { text: string }): ReactNode {
  return <>{renderInline(text)}</>;
}

function renderInline(text: string, depth = 0): ReactNode[] {
  const out: ReactNode[] = [];
  let rest = text;
  let key = 0;

  // Emphasis recurses into its own content, so a hostile string of markers
  // cannot be made to nest without bound.
  if (depth > 4) return [text];

  while (rest.length > 0) {
    const m = INLINE.exec(rest);
    if (!m) {
      out.push(rest);
      break;
    }
    if (m.index > 0) out.push(rest.slice(0, m.index));

    if (m[1]) {
      // Inline code: the contents are text and nothing else looks at them.
      out.push(
        <code
          key={key++}
          className="rounded bg-muted px-1 py-0.5 font-mono text-[0.9em]"
        >
          {m[2]}
        </code>,
      );
    } else if (m[4] !== undefined) {
      out.push(
        <Link key={key++} image={m[3] === "!"} label={m[4]} href={m[5]} />,
      );
    } else if (m[6]) {
      out.push(<strong key={key++}>{renderInline(m[7], depth + 1)}</strong>);
    } else {
      out.push(<em key={key++}>{renderInline(m[9], depth + 1)}</em>);
    }
    rest = rest.slice(m.index + m[0].length);
  }
  return out;
}

/**
 * A link, or an honest rendering of something that only looks like one.
 *
 * The scheme test is an allowlist of two, applied to the parsed URL rather than
 * to the string: `javascript:alert(1)` and its whitespace-and-entity variants
 * are all the same URL once parsed, and a `startsWith` check is what misses
 * them. A relative href has no scheme to check and no meaning here — this text
 * did not come from a page — so it is not a link either.
 *
 * What fails the test is shown as `label (url)`, both halves visible: the label
 * is the agent's own words and the URL is the claim it is making about where
 * they go, and a reader deciding whether to trust a transcript should see both.
 */
function Link({
  image,
  label,
  href,
}: {
  image: boolean;
  label: string;
  href: string;
}) {
  // An image is never fetched — see the file comment. It renders as its source.
  if (image) {
    return (
      <span className="text-muted-foreground">
        ![{label}]({href})
      </span>
    );
  }

  let safe = false;
  try {
    const u = new URL(href);
    safe = u.protocol === "http:" || u.protocol === "https:";
  } catch {
    safe = false;
  }

  if (!safe) {
    return (
      <span>
        {label} <span className="text-muted-foreground">({href})</span>
      </span>
    );
  }
  return (
    <a
      href={href}
      target="_blank"
      // noopener so the opened page cannot reach back through window.opener;
      // noreferrer so it is not told which run detail somebody was reading.
      rel="noopener noreferrer nofollow"
      className="underline underline-offset-2 hover:text-foreground"
    >
      {label || href}
    </a>
  );
}
