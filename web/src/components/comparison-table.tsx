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
        This is the project&rsquo;s own read of the landscape, by kind of tool rather than by
        product, and a kind varies more than a table can show — check the tool you are weighing
        before choosing. What sandbox-cli adds is a VM boundary that runs where your code already
        is, with the same API on your laptop, your server and a cloud.
      </p>

      <p className="border-t bg-surface px-4 py-3 text-xs leading-relaxed text-muted-foreground sm:px-5">
        <span className="font-medium text-foreground/80">What this table does not claim.</span>{" "}
        A secret handed to a sandbox reaches its environment, where the agent can read it with{" "}
        <code className="font-mono">printenv</code> — a broker that injects the credential so the
        agent never holds it is not built, and the posture is to make a leaked one cheap instead:
        short-lived values, an allowlist, prod&rsquo;s refusal to copy logins in. A row that cannot
        be defended against the code gets changed here.
      </p>
    </div>
  );
}
