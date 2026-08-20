import { CopyButton } from "@/components/copy-button";
import { cn } from "@/lib/utils";

/**
 * The dark terminal block the rest of the page already speaks in, extracted so
 * the multi-agent doc can use it six times without six copies of the same
 * markup and the same comment-dimming rule.
 *
 * `lang` decides one thing only: whether a leading `$` is drawn. A shell block
 * gets a prompt on each command; a YAML or TypeScript block gets none, because a
 * `$` in front of a file is a lie about what you do with it — a forty-line
 * example rendered as shell reads as forty commands to run, which is how the SDK
 * page shipped its flagship snippet looking like a terminal session.
 *
 * "Each command" is not "each line": a line following one that ends in `\` is
 * the same command continued, and a second `$` there would render a command
 * nobody can copy.
 *
 * Comments are dimmed rather than syntax-highlighted. The page is arguing about
 * behaviour, and a full highlighter would be decoration competing with the one
 * distinction that matters in these snippets — what the command is, and what
 * someone wrote next to it to explain why.
 */
export function CodeBlock({
  code,
  lang = "sh",
  title,
  className,
}: {
  code: string;
  lang?: "sh" | "yaml" | "ts";
  /**
   * What this block *is*, shown in a title bar with the window buttons: a file
   * name for a file, or where you are typing for a shell.
   *
   * Opt-in rather than automatic. The chrome earns its two rems on a block you
   * are meant to recognise — the file you will copy, the terminal you will type
   * in — and spends them for nothing on the one-line snippets that sit inside a
   * sentence.
   */
  title?: string;
  className?: string;
}) {
  const lines = code.split("\n");
  // Copy what you would actually run or paste: the comments go too for YAML
  // (they are part of the file) but never the rendered `$` prompt.
  // Only a shell block drops its comments on copy — they are annotations on a
  // command you are about to run. A file's comments are part of the file.
  const copyable =
    lang === "sh" ? lines.filter((l) => !l.trimStart().startsWith("#")).join("\n") : code;

  const body = (
    <div className="group relative flex items-start gap-3 px-4 py-4">
      <pre className="no-scrollbar min-w-0 flex-1 overflow-x-auto font-mono text-[0.78rem] leading-relaxed text-[#e7e7ea]">
        {lines.map((line, i) => {
          const comment =
            lang === "ts"
              ? line.trimStart().startsWith("//") || line.trimStart().startsWith("*") || line.trimStart().startsWith("/*")
              : line.trimStart().startsWith("#");
          const continued = i > 0 && lines[i - 1].trimEnd().endsWith("\\");
          const prompt = lang === "sh" && !comment && !continued && line.trim() !== "";
          return (
            <div key={i} className="whitespace-pre">
              {lang === "sh" ? (
                <span className="pr-2 text-[#6ee7b7] select-none">{prompt ? "$" : " "}</span>
              ) : null}
              {/* A blank line is a line. Rendered as an empty span it collapses
                  to nothing, which silently reflows a file into something denser
                  than the one you would copy — the paragraphs a reader uses to
                  find their place disappear. */}
              <span className={comment ? "text-[#8a8a94]" : undefined}>
                {line === "" ? "\u00A0" : line}
              </span>
            </div>
          );
        })}
      </pre>
      <CopyButton value={copyable} className="text-[#a1a1aa] hover:bg-white/10 hover:text-white" />
    </div>
  );

  if (!title) {
    return <div className={cn("overflow-hidden rounded-xl bg-[#0b0b0d]", className)}>{body}</div>;
  }

  return (
    <div className={cn("overflow-hidden rounded-xl bg-[#0b0b0d]", className)}>
      {/* The window buttons are decoration and are told so: they are not
          controls, so they are hidden from assistive technology rather than
          announced as three unlabelled somethings. */}
      <div className="flex items-center gap-2 border-b border-white/10 bg-white/[0.04] px-4 py-2.5">
        <span aria-hidden className="flex items-center gap-1.5">
          <span className="size-2.5 rounded-full bg-[#ff5f57]" />
          <span className="size-2.5 rounded-full bg-[#febc2e]" />
          <span className="size-2.5 rounded-full bg-[#28c840]" />
        </span>
        <span className="truncate font-mono text-[0.7rem] text-[#8a8a94]">{title}</span>
      </div>
      {body}
    </div>
  );
}
