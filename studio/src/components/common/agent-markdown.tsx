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
  | { kind: "list"; ordered: boolean; items: Block[][]; start: number }
  | { kind: "quote"; blocks: Block[] }
  | { kind: "hr" };

function Blocks({ blocks }: { blocks: Block[] }) {
  return (
    <>
      {blocks.map((b, i) => (
        <Block key={i} block={b} />
      ))}
    </>
  );
}

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
          className={cn("break-words font-semibold", size)}
          role="heading"
          aria-level={block.level}
        >
          <Inline text={block.text} />
        </p>
      );
    }

    case "list": {
      const cls = cn(
        "ms-5 space-y-1 break-words",
        block.ordered ? "list-decimal" : "list-disc",
      );
      const items = block.items.map((blocks, i) => (
        <li key={i} className="space-y-2">
          {/* A one-paragraph item renders its text directly. Wrapping it in a
              <p> would space every bullet like a paragraph, which is what makes
              a short list look like an essay. */}
          {blocks.length === 1 && blocks[0].kind === "p" ? (
            // whitespace-pre-wrap here too: the paragraph case keeps line breaks
            // deliberately, and a one-paragraph item taking this shortcut was
            // quietly dropping them.
            <span className="whitespace-pre-wrap">
              <Inline text={blocks[0].text} />
            </span>
          ) : (
            <Blocks blocks={blocks} />
          )}
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
      // Block-parsed, not inlined. The quote used to collect its stripped lines
      // into one string and hand them to the inline parser, so a fenced block
      // inside a quote was not a fence — its backticks rendered literally and
      // whatever it contained went through the emphasis rules, which is the one
      // thing parseBlocks promises never happens. A list inside a quote came out
      // as literal "- a" for the same reason.
      return (
        <blockquote className="space-y-2 break-words border-s-2 ps-3 text-muted-foreground">
          <Blocks blocks={block.blocks} />
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

const FENCE = /^(\s*)(`{3,}|~{3,})\s*([^\s`]*)/;
const HEADING = /^\s{0,3}(#{1,6})\s+(.*)$/;
const HR = /^\s{0,3}([-*_])(?:\s*\1){2,}\s*$/;
const QUOTE = /^\s{0,3}>\s?(.*)$/;
/** A list line, with its indent, its marker and the content after it. */
const ITEM = /^(\s*)([-*+]|\d{1,9}[.)])\s+(.*)$/;

/** Deeper than any real reply, and the bound that keeps nesting from recursing. */
const MAX_DEPTH = 6;

/**
 * Split text into blocks, line by line.
 *
 * A fence swallows everything up to its closing run *including* what would
 * otherwise be syntax, which is the one rule that has to hold: an agent
 * explaining markdown inside a code block must not have the explanation parsed.
 * An unterminated fence — the common case while a reply is still streaming in —
 * runs to the end of the message rather than being abandoned, so a half-arrived
 * code block reads as code rather than as a stray paragraph of source.
 *
 * A list item holds **blocks**, not a string, and it is parsed by calling this
 * function again on the item's own dedented lines. That is what makes a nested
 * list nest and a fenced block inside a numbered step stay code: the first
 * version treated an item as text, so `1. Run:` followed by an indented ```sh
 * fence put the fence through the *inline* parser — the exact thing the
 * paragraph above says must not happen, in the shape agents write most often.
 */
function parseBlocks(input: string, depth = 0): Block[] {
  const lines = input.replace(/\r\n?/g, "\n").split("\n");
  const out: Block[] = [];
  let para: string[] = [];
  let quote: string[] = [];

  const flushPara = () => {
    if (para.length) out.push({ kind: "p", text: para.join("\n") });
    para = [];
  };
  const flushQuote = () => {
    if (quote.length) {
      out.push({
        kind: "quote",
        blocks: parseBlocks(quote.join("\n"), depth + 1),
      });
    }
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
      const indent = fence[1].length;
      // Hoisted out of the scan: recompiling this per line inside a long block
      // is work proportional to the block for no reason.
      const close = new RegExp(
        `^\\s*${fence[2][0] === "\`" ? "`" : "~"}{${fence[2].length},}\\s*$`,
      );
      const body: string[] = [];
      i++;
      for (; i < lines.length; i++) {
        if (close.test(lines[i])) break;
        // Dedented by the fence's own indent, so a block inside a list item is
        // not rendered with the indentation that put it there.
        body.push(lines[i].slice(Math.min(indent, leadingSpaces(lines[i]))));
      }
      out.push({ kind: "code", lang: fence[3] ?? "", code: body.join("\n") });
      continue;
    }

    if (!line.trim()) {
      flushAll();
      continue;
    }

    const q = depth < MAX_DEPTH ? QUOTE.exec(line) : null;
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

    const item = ITEM.exec(line);
    if (item && depth < MAX_DEPTH) {
      flushPara();
      i = readList(lines, i, out, depth);
      continue;
    }

    para.push(line);
  }
  flushAll();
  return out;
}

function leadingSpaces(s: string): number {
  return s.length - s.trimStart().length;
}

/**
 * Read one list starting at `start`, and return the index of its last line.
 *
 * A line belongs to this list if it is a sibling item at the same indent, or if
 * it is indented past the marker — which is how a nested list, a wrapped
 * sentence and an indented code fence all reach the item they belong to rather
 * than ending it. Each item is then parsed as its own document, dedented, so
 * every block kind works inside one without this function knowing what they are.
 */
function readList(
  lines: string[],
  start: number,
  out: Block[],
  depth: number,
): number {
  const first = ITEM.exec(lines[start])!;
  const indent = first[1].length;
  const ordered = /\d/.test(first[2]);
  const startNum = ordered ? Number(first[2].replace(/\D/g, "")) : 1;

  const items: string[][] = [];
  let current: string[] = [first[3]];
  // How far in this item's *content* sits, so continuation lines can be
  // dedented to match it.
  let contentIndent = indent + first[2].length + 1;
  let i = start;
  let blanks = 0;

  for (i = start + 1; i < lines.length; i++) {
    const line = lines[i];

    if (!line.trim()) {
      // One blank line inside a list is a loose item; two ends the list.
      if (++blanks > 1) break;
      current.push("");
      continue;
    }

    const sib = ITEM.exec(line);
    if (sib && sib[1].length <= indent) {
      // A sibling, or a marker outdented past this list — either way this item
      // is finished. A different kind of marker at the same indent ends the
      // list rather than joining it, so a bulleted aside under numbered steps
      // does not become step 3.
      if (sib[1].length < indent || /\d/.test(sib[2]) !== ordered) break;
      items.push(current);
      current = [sib[3]];
      contentIndent = sib[1].length + sib[2].length + 1;
      blanks = 0;
      continue;
    }

    if (leadingSpaces(line) > indent) {
      current.push(line.slice(Math.min(contentIndent, leadingSpaces(line))));
      blanks = 0;
      continue;
    }
    break;
  }
  items.push(current);

  out.push({
    kind: "list",
    ordered,
    start: startNum,
    items: items.map((lines) => parseBlocks(lines.join("\n"), depth + 1)),
  });
  // The loop above stops *on* the line that ends the list, which the caller has
  // not seen yet; hand back the one before it.
  return i - 1;
}

/* ------------------------------------------------------------------ inline */

/**
 * Code spans, found first and in a pass of their own.
 *
 * "Code first" used to mean *first in an alternation*, which is not the same
 * thing and was not true: the regex engine tries alternatives per **position**,
 * not per priority, so an emphasis marker earlier in the line won against a code
 * span later in it. `2*3 and \`x*y\` are different` matched `*3 and \`x*` as
 * italic, swallowing the opening backtick and destroying the code span. Doing it
 * as a separate pass makes the guarantee structural: nothing else ever sees the
 * inside of a code span, because the emphasis scanner is only ever handed the
 * gaps between them.
 */
const CODE = /`([^`\n]+)`/g;

/**
 * Everything else: links, then bold, then italic.
 *
 * **Every class is bounded**, and the two caps are the point rather than
 * tidiness. A label written `[^\]]*` is unbounded in both directions — it
 * matches newlines, so an unclosed `[` scans to the end of the entire message,
 * fails, and does it again at the next `[`. That is quadratic, and it was
 * measured on this exact pattern: 20 000 `[` took 146 ms, 40 000 took 548 ms,
 * 80 000 took 2.2 s — so a 200 KB reply is ~14 s of blocked main thread, and the
 * conversation refetches every three seconds, so the tab never comes back.
 *
 * That is the same denial of service the code-span rewrite closed, surviving in
 * the one alternative that was left unbounded. A cap makes each failed attempt
 * cost the cap rather than the message, which is linear again. 200 characters of
 * link text and 2 000 of URL are both far past anything real; past them the
 * construct renders as its own source, which is what this file does with
 * everything it does not understand.
 */
const LINK_LABEL_MAX = 200;
const LINK_HREF_MAX = 2000;
const REST = new RegExp(
  `(!?)\\[([^\\]\\n]{0,${LINK_LABEL_MAX}})\\]\\(([^\\s)]{0,${LINK_HREF_MAX}})\\)` +
    `|(\\*\\*|__)([^\\s](?:[^\\n]*?[^\\s])?)\\4` +
    `|(\\*|_)([^\\s*_](?:[^\\n]*?[^\\s])?)\\6`,
  "g",
);

function Inline({ text }: { text: string }): ReactNode {
  return <>{renderInline(text)}</>;
}

/** A character that makes a `_` around it part of a word rather than a marker. */
function isWordChar(c: string | undefined): boolean {
  return c !== undefined && /[\p{L}\p{N}_]/u.test(c);
}

/**
 * Emphasis has to contain something that is not punctuation.
 *
 * `ls -la **\/*.go` is a glob, and the italic rule found `*\/*` inside it: the
 * content is `/`, which satisfies "starts and ends with a non-space", so the
 * reader was shown `ls -la *​/.go` — a path that does not exist, with the
 * markers silently deleted. That is the same failure the intraword-`_` rule
 * exists to prevent, one character class over.
 *
 * Requiring a letter or a digit somewhere inside costs nothing real — emphasis
 * on pure punctuation is not a thing anybody writes — and it rules out the glob
 * and path shapes agents produce constantly.
 */
function hasWordChar(s: string): boolean {
  return /[\p{L}\p{N}]/u.test(s);
}

function renderInline(text: string, depth = 0): ReactNode[] {
  // Emphasis and link labels recurse into their own content, so a hostile string
  // of markers cannot be made to nest without bound.
  if (depth > 4) return [text];

  const out: ReactNode[] = [];
  const code = new RegExp(CODE.source, "g");
  const key = { n: 0 };
  let last = 0;
  let m: RegExpExecArray | null;

  while ((m = code.exec(text)) !== null) {
    if (m.index > last) pushRest(out, text.slice(last, m.index), depth, key);
    out.push(
      <code
        key={key.n++}
        className="rounded bg-muted px-1 py-0.5 font-mono text-[0.9em]"
      >
        {m[1]}
      </code>,
    );
    last = code.lastIndex;
  }
  if (last < text.length) pushRest(out, text.slice(last), depth, key);
  return out;
}

/** Links and emphasis, over one stretch of text known to contain no code span. */
function pushRest(
  out: ReactNode[],
  text: string,
  depth: number,
  key: { n: number },
): void {
  const re = new RegExp(REST.source, "g");
  let last = 0;
  let m: RegExpExecArray | null;

  while ((m = re.exec(text)) !== null) {
    const marker = m[4] ?? m[6];
    const content = m[5] ?? m[7];

    if (marker !== undefined) {
      // `SANDBOX_EGRESS_ALLOW` is not italic `EGRESS`. CommonMark forbids
      // intraword `_` emphasis for exactly this reason, and without the rule
      // every snake_case identifier came out mangled — with the underscores
      // *deleted*, so the reader saw a name that does not exist. `*` keeps no
      // such rule, because intraword `*` is how emphasis inside a word is
      // written.
      const underscore = marker === "__" || marker === "_";
      const badFlank =
        underscore &&
        (isWordChar(text[m.index - 1]) ||
          isWordChar(text[m.index + m[0].length]));

      if (badFlank || !hasWordChar(content)) {
        // Not a marker after all. Resume one character in, so a genuine
        // delimiter later on the same line is still found.
        re.lastIndex = m.index + 1;
        continue;
      }
    }

    if (m.index > last) out.push(text.slice(last, m.index));

    if (m[2] !== undefined) {
      out.push(
        <Link
          key={key.n++}
          image={m[1] === "!"}
          label={m[2]}
          href={m[3]}
          depth={depth}
        />,
      );
    } else if (m[4]) {
      out.push(<strong key={key.n++}>{renderInline(m[5], depth + 1)}</strong>);
    } else {
      out.push(<em key={key.n++}>{renderInline(m[7], depth + 1)}</em>);
    }
    last = re.lastIndex;
  }
  if (last < text.length) out.push(text.slice(last));
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
 * **The destination is shown either way**, and that is the correction to the
 * first version of this file. It showed the URL only for the schemes it refused
 * to link, which is backwards: an unclickable `javascript:` link is the one a
 * reader is least likely to follow, and a clickable
 * `[https://github.com/…](https://evil.example/login)` — a label that is itself
 * a trusted-looking URL — was rendered with the real destination hidden. So a
 * link that goes somewhere its label does not already name carries the host
 * beside it. The label is the agent's words; the host is where they go.
 */
function Link({
  image,
  label,
  href,
  depth,
}: {
  image: boolean;
  label: string;
  href: string;
  depth: number;
}) {
  // An image is never fetched — see the file comment. It renders as its source.
  if (image) {
    return (
      <span className="text-muted-foreground">
        ![{label}]({href})
      </span>
    );
  }

  let url: URL | null = null;
  try {
    const u = new URL(href);
    if (u.protocol === "http:" || u.protocol === "https:") url = u;
  } catch {
    url = null;
  }

  if (!url) {
    return (
      <span>
        {label} <span className="text-muted-foreground">({href})</span>
      </span>
    );
  }

  // Suppressed only when the label already names where it goes, so the common
  // case — a link whose text *is* the URL — does not print the host twice.
  const names = label.includes(url.host) || label.trim() === "";
  return (
    <>
      <a
        href={url.href}
        target="_blank"
        // noopener so the opened page cannot reach back through window.opener;
        // noreferrer so it is not told which run detail somebody was reading.
        rel="noopener noreferrer nofollow"
        className="underline underline-offset-2 hover:text-foreground"
      >
        {label ? renderInline(label, depth + 1) : url.href}
      </a>
      {!names && <span className="text-muted-foreground"> ({url.host})</span>}
    </>
  );
}
