"use client";

import { cn } from "@/lib/utils";
import { CopyButton } from "@/components/common/copy-button";
import { formatArgv } from "@/lib/format";

/**
 * The terminal block. One shape, used by the argv previews, the config tab and
 * the launch form, so a command never renders three different ways.
 */
export function CodeBlock({
  children,
  value,
  title,
  className,
  copy = true,
}: {
  children: React.ReactNode;
  /** What the copy button puts on the clipboard. Defaults to nothing copyable. */
  value?: string;
  title?: React.ReactNode;
  className?: string;
  copy?: boolean;
}) {
  return (
    <div
      className={cn(
        "surface-sheen overflow-hidden rounded-lg border bg-[#0c0c0f] text-[13px]",
        className,
      )}
    >
      {(title || (copy && value)) && (
        <div className="flex items-center justify-between gap-2 border-b border-white/5 px-3 py-1.5">
          <span className="truncate font-mono text-[11px] tracking-wide text-white/40 uppercase">
            {title}
          </span>
          {copy && value && <CopyButton value={value} className="hover:bg-white/10" />}
        </div>
      )}
      <pre className="scrollbar-thin overflow-x-auto p-3 font-mono leading-relaxed text-zinc-200">
        {children}
      </pre>
    </div>
  );
}

/**
 * A docker argv, one flag per line in the order `runtime.BuildArgs` emits them.
 *
 * Rendered line-per-flag rather than wrapped, because the order *is* the
 * information: the mounts, then the network, then the security options, then the
 * image. A wrapped blob hides which flag a value belongs to.
 */
export function ArgvBlock({
  argv,
  title = "docker argv",
  className,
  highlight,
}: {
  argv: string[];
  title?: string;
  className?: string;
  /** Flags to mark as widening the boundary. */
  highlight?: (flag: string, value: string | undefined) => "widen" | "tighten" | null;
}) {
  const lines = groupArgv(argv);
  return (
    <div
      className={cn(
        "surface-sheen overflow-hidden rounded-lg border bg-[#0c0c0f] text-[13px]",
        className,
      )}
    >
      <div className="flex items-center justify-between gap-2 border-b border-white/5 px-3 py-1.5">
        <span className="font-mono text-[11px] tracking-wide text-white/40 uppercase">
          {title}
        </span>
        <CopyButton value={formatArgv(argv)} label="Copy command" className="hover:bg-white/10" />
      </div>
      <div className="scrollbar-thin max-h-[28rem] overflow-auto p-3 font-mono">
        {lines.map((line, i) => {
          const kind = highlight?.(line.flag, line.value) ?? null;
          return (
            <div
              key={i}
              className={cn(
                "flex items-baseline gap-2 rounded px-1 py-px whitespace-pre",
                kind === "widen" && "bg-exposed/10",
                kind === "tighten" && "bg-contained/10",
              )}
            >
              <span
                className={cn(
                  i === 0 ? "text-primary" : "text-sky-300/90",
                  kind === "widen" && "text-exposed",
                  kind === "tighten" && "text-contained",
                )}
              >
                {line.flag}
              </span>
              {line.value !== undefined && (
                <span className="break-all text-zinc-300">{line.value}</span>
              )}
              {line.continues && <span className="text-white/25">\</span>}
            </div>
          );
        })}
      </div>
    </div>
  );
}

interface ArgvLine {
  flag: string;
  value?: string;
  continues: boolean;
}

/**
 * Pair each flag with its value so the preview can show one setting per line.
 * A flag whose next token is another flag takes no value — `--rm` and
 * `--security-opt no-new-privileges` must not be rendered the same way.
 */
function groupArgv(argv: string[]): ArgvLine[] {
  const out: ArgvLine[] = [];
  let i = 0;
  // The engine name and subcommand lead, on one line.
  if (argv.length >= 2 && !argv[0].startsWith("-")) {
    out.push({ flag: `${argv[0]} ${argv[1]}`, continues: true });
    i = 2;
  }
  while (i < argv.length) {
    const tok = argv[i];
    if (tok.startsWith("-")) {
      const next = argv[i + 1];
      if (next !== undefined && !next.startsWith("-")) {
        out.push({ flag: tok, value: next, continues: i + 2 < argv.length });
        i += 2;
        continue;
      }
      out.push({ flag: tok, continues: i + 1 < argv.length });
      i += 1;
      continue;
    }
    // Past the flags: the image, then the guest command, which is one unit.
    out.push({ flag: tok, continues: i + 1 < argv.length });
    if (out.length && i + 1 < argv.length) {
      const rest = argv.slice(i + 1);
      out.push({ flag: rest.join(" "), continues: false });
    }
    break;
  }
  return out;
}
