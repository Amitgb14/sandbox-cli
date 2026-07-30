import { CopyButton } from "@/components/copy-button";
import { cn } from "@/lib/utils";

/**
 * The dark terminal block the rest of the page already speaks in, extracted so
 * the multi-agent doc can use it six times without six copies of the same
 * markup and the same comment-dimming rule.
 *
 * `lang` decides one thing only: whether a leading `$` is drawn. A shell block
 * gets a prompt on each command; a YAML block gets none, because a `$` in front
 * of a config file is a lie about what you do with it.
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
  className,
}: {
  code: string;
  lang?: "sh" | "yaml";
  className?: string;
}) {
  const lines = code.split("\n");
  // Copy what you would actually run or paste: the comments go too for YAML
  // (they are part of the file) but never the rendered `$` prompt.
  const copyable = lang === "yaml" ? code : lines.filter((l) => !l.trimStart().startsWith("#")).join("\n");

  return (
    <div
      className={cn(
        "group relative flex items-start gap-3 overflow-hidden rounded-xl bg-[#0b0b0d] px-4 py-4",
        className,
      )}
    >
      <pre className="no-scrollbar min-w-0 flex-1 overflow-x-auto font-mono text-[0.78rem] leading-relaxed text-[#e7e7ea]">
        {lines.map((line, i) => {
          const comment = line.trimStart().startsWith("#");
          const continued = i > 0 && lines[i - 1].trimEnd().endsWith("\\");
          const prompt = lang === "sh" && !comment && !continued && line.trim() !== "";
          return (
            <div key={i} className="whitespace-pre">
              {lang === "sh" ? (
                <span className="pr-2 text-[#6ee7b7] select-none">{prompt ? "$" : " "}</span>
              ) : null}
              <span className={comment ? "text-[#8a8a94]" : undefined}>{line}</span>
            </div>
          );
        })}
      </pre>
      <CopyButton value={copyable} className="text-[#a1a1aa] hover:bg-white/10 hover:text-white" />
    </div>
  );
}
