"use client";

import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { COLUMNS, ROWS, type Tone } from "@/lib/comparison";
import { cn } from "@/lib/utils";

const DOT: Record<Tone, string> = {
  strong: "bg-contained",
  ok: "bg-contained/45",
  weak: "bg-caution/60",
  none: "bg-exposed/70",
  neutral: "bg-muted-foreground/35",
};

const TEXT: Record<Tone, string> = {
  strong: "text-foreground",
  ok: "text-foreground",
  weak: "text-muted-foreground",
  none: "text-muted-foreground",
  neutral: "text-muted-foreground",
};

export function ComparisonTable({ className }: { className?: string }) {
  return (
    <div className={cn("overflow-hidden rounded-2xl border bg-card", className)}>
      <div className="overflow-x-auto">
        <Table className="min-w-[62rem]">
          <TableHeader>
            <TableRow className="hover:bg-transparent">
              <TableHead className="sticky left-0 z-10 w-[15rem] bg-card" />
              {COLUMNS.map((c) => (
                <TableHead
                  key={c.id}
                  className={cn(
                    "h-auto py-3 align-bottom",
                    c.highlight && "bg-contained-soft",
                  )}
                >
                  <span className="flex flex-col gap-0.5">
                    <span
                      className={cn(
                        "text-[0.85rem] font-semibold",
                        c.highlight ? "font-mono text-contained" : "text-foreground",
                      )}
                    >
                      {c.name}
                    </span>
                    <span className="text-[0.7rem] font-normal text-muted-foreground">
                      {c.sub}
                    </span>
                  </span>
                </TableHead>
              ))}
            </TableRow>
          </TableHeader>

          <TableBody>
            {ROWS.map((r) => (
              <TableRow key={r.label} className="align-top">
                <TableCell className="sticky left-0 z-10 bg-card">
                  <span className="flex flex-col gap-0.5">
                    <span className="text-[0.82rem] font-medium">{r.label}</span>
                    {r.note ? (
                      <span className="text-[0.7rem] text-muted-foreground">{r.note}</span>
                    ) : null}
                  </span>
                </TableCell>
                {COLUMNS.map((c) => {
                  const cell = r.cells[c.id];
                  return (
                    <TableCell
                      key={c.id}
                      className={cn(
                        "text-[0.78rem] leading-snug",
                        TEXT[cell.tone],
                        c.highlight && "bg-contained-soft/60 font-medium",
                      )}
                    >
                      <span className="flex items-start gap-2">
                        <span
                          aria-hidden="true"
                          className={cn("mt-1.5 size-1.5 shrink-0 rounded-full", DOT[cell.tone])}
                        />
                        {cell.text}
                      </span>
                    </TableCell>
                  );
                })}
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>

      <p className="border-t bg-surface px-4 py-3 text-xs leading-relaxed text-muted-foreground sm:px-5">
        This is the project&rsquo;s own read of the landscape, and the ratings for other tools are a
        snapshot that will age — check their docs before choosing. sandbox-cli&rsquo;s edge is code
        quality and a focused feature set, not a hard security boundary; for that, reach for microVM
        tooling (or run sandbox-cli on top of one with{" "}
        <code className="font-mono">--runtime</code>).
      </p>
    </div>
  );
}
