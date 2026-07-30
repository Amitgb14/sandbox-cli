"use client";

import { useMemo, useState } from "react";
import { ScrollText, Search } from "lucide-react";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import { EmptyState } from "@/components/common/empty-state";
import { CopyButton } from "@/components/common/copy-button";
import { useRunLogs } from "@/lib/api/queries";
import { stripAnsi } from "@/lib/ansi";
import { formatClock, pluralize } from "@/lib/format";
import { cn } from "@/lib/utils";
import type { Run } from "@/lib/types";

/**
 * The same output as the terminal tab, as plain searchable text.
 *
 * Two views of one stream, because they answer different questions: the terminal
 * shows what the agent *drew*, this shows what it *wrote*. Escape codes are
 * stripped here, so a search for a word is not defeated by a colour code sitting
 * in the middle of it.
 */
export function LogsView({ run }: { run: Run }) {
  const { data, isPending } = useRunLogs(run.id);
  const [query, setQuery] = useState("");
  const [errorsOnly, setErrorsOnly] = useState(false);

  const lines = useMemo(
    () => (data ?? []).map((l) => ({ ...l, plain: stripAnsi(l.text) })),
    [data],
  );

  const filtered = useMemo(
    () =>
      lines.filter((l) => {
        if (errorsOnly && l.stream !== "stderr") return false;
        if (query && !l.plain.toLowerCase().includes(query.toLowerCase())) return false;
        return true;
      }),
    [lines, errorsOnly, query],
  );

  const stderrCount = lines.filter((l) => l.stream === "stderr").length;

  if (isPending) {
    return (
      <div className="space-y-2">
        {Array.from({ length: 12 }).map((_, i) => (
          <Skeleton key={i} className="h-4 w-full" />
        ))}
      </div>
    );
  }

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-3">
        <div className="relative">
          <Search className="absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2 text-muted-foreground" />
          <Input
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="Search output…"
            className="h-8 w-full pl-8 sm:w-72"
          />
        </div>
        <div className="flex items-center gap-2">
          <Switch id="stderr-only" checked={errorsOnly} onCheckedChange={setErrorsOnly} />
          <Label htmlFor="stderr-only" className="text-xs font-normal">
            stderr only
          </Label>
          <Badge variant="outline" className="text-[10px] tabular-nums">
            {stderrCount}
          </Badge>
        </div>
        <div className="ml-auto flex items-center gap-2">
          <span className="text-xs text-muted-foreground tabular-nums">
            {filtered.length === lines.length
              ? pluralize(lines.length, "line")
              : `${filtered.length} of ${lines.length} lines`}
          </span>
          <CopyButton
            size="sm"
            value={filtered.map((l) => l.plain).join("\n")}
            label="Copy shown"
          />
        </div>
      </div>

      <div className="scrollbar-thin h-[30rem] overflow-auto rounded-lg border bg-card/40 font-mono text-[12.5px] leading-[1.6]">
        {filtered.length === 0 ? (
          <EmptyState
            icon={ScrollText}
            title={lines.length === 0 ? "No output captured" : "Nothing matches"}
            description={
              lines.length === 0
                ? "This container wrote nothing, or its output was not captured. A detached run's logs are its whole supervision story, which is why --rm is off for those."
                : "Try a shorter search, or turn off the stderr filter."
            }
            className="border-0"
          />
        ) : (
          <table className="w-full border-collapse">
            <tbody>
              {filtered.map((line) => (
                <tr key={line.seq} className="align-top hover:bg-accent/40">
                  <td className="w-14 py-px pr-2 pl-3 text-right tabular-nums text-muted-foreground/40 select-none">
                    {line.seq + 1}
                  </td>
                  <td className="w-20 py-px pr-3 tabular-nums text-muted-foreground/60 select-none">
                    {formatClock(line.ts)}
                  </td>
                  <td className="w-14 py-px pr-3 text-[10px] tracking-wide uppercase select-none">
                    <span
                      className={cn(
                        line.stream === "stderr"
                          ? "text-status-warning"
                          : "text-muted-foreground/40",
                      )}
                    >
                      {line.stream === "stderr" ? "err" : "out"}
                    </span>
                  </td>
                  <td className="py-px pr-3 whitespace-pre-wrap break-words">
                    <Highlight text={line.plain} query={query} />
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </div>
    </div>
  );
}

function Highlight({ text, query }: { text: string; query: string }) {
  if (!query) return <>{text || " "}</>;
  const idx = text.toLowerCase().indexOf(query.toLowerCase());
  if (idx === -1) return <>{text}</>;
  return (
    <>
      {text.slice(0, idx)}
      <mark className="rounded-sm bg-chart-4/30 px-0.5 text-foreground">
        {text.slice(idx, idx + query.length)}
      </mark>
      {text.slice(idx + query.length)}
    </>
  );
}
