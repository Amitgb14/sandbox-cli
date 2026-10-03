"use client";

import { useEffect, useMemo, useState } from "react";
import { followOutput } from "@/lib/api/client";
import { parseAnsi } from "@/lib/ansi";
import { cn } from "@/lib/utils";

/**
 * A process's output from its first byte, followed until it exits. The server
 * keeps every process's output, so opening this late — or after a reconnect —
 * still shows it from the start.
 */
export function ProcessOutput({ sandbox, pid }: { sandbox: string; pid: number }) {
  const [text, setText] = useState("");
  const [exit, setExit] = useState<number | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    const ac = new AbortController();
    setText("");
    setExit(null);
    setError(null);
    followOutput(
      sandbox,
      pid,
      (ev) => {
        if ("exit_code" in ev) setExit(ev.exit_code);
        else setText((t) => (t.length > 2_000_000 ? t.slice(-1_000_000) : t) + ev.data);
      },
      ac.signal,
    ).catch((e) => {
      if (!ac.signal.aborted) setError(e instanceof Error ? e.message : String(e));
    });
    return () => ac.abort();
  }, [sandbox, pid]);

  const lines = useMemo(() => text.split("\n"), [text]);
  return (
    <div className="flex flex-col gap-2">
      <pre className="max-h-[32rem] min-h-40 overflow-auto rounded-md border bg-[#0b0b0c] p-3 font-mono text-xs leading-relaxed text-[#e7e7ea]">
        {lines.map((line, i) => (
          <div key={i} className="whitespace-pre-wrap">
            {parseAnsi(line).map((span, j) => (
              <span
                key={j}
                className={cn(span.bold && "font-semibold", span.dim && "opacity-60", span.italic && "italic", span.underline && "underline")}
                style={span.color ? { color: span.color } : undefined}
              >
                {span.text}
              </span>
            ))}
          </div>
        ))}
      </pre>
      <p className="text-xs text-muted-foreground">
        {error ? `could not follow: ${error}` : exit === null ? "following…" : `exited ${exit}`}
      </p>
    </div>
  );
}
