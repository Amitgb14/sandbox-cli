import { Check, Minus, X } from "lucide-react";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { ROWS, type Level } from "@/lib/comparison";
import { cn } from "@/lib/utils";

function Mark({ level, text }: { level: Level; text: string }) {
  const Icon = level === "good" ? Check : level === "bad" ? X : Minus;
  return (
    <span
      className={cn(
        "flex items-center gap-1.5 font-mono text-xs",
        level === "good" && "text-contained",
        level === "bad" && "text-exposed",
        level === "partial" && "text-signal",
      )}
    >
      <Icon className="size-4 shrink-0" strokeWidth={2.4} />
      {text}
    </span>
  );
}

/** The highlighted column is what sits inside the boundary. */
export function ComparisonTable() {
  return (
    <div className="overflow-x-auto rounded-xl border bg-card shadow-sm">
      <Table className="min-w-[780px]">
        <TableHeader>
          <TableRow className="hover:bg-transparent">
            <TableHead className="font-mono text-[0.68rem] uppercase tracking-[0.12em]">
              Property
            </TableHead>
            <TableHead className="font-mono text-[0.68rem] uppercase tracking-[0.12em]">
              Bare host
            </TableHead>
            <TableHead className="border-x border-contained/30 bg-contained/[0.07] font-mono text-[0.68rem] uppercase tracking-[0.12em] text-contained">
              sandbox-cli
            </TableHead>
            <TableHead className="font-mono text-[0.68rem] uppercase tracking-[0.12em]">
              Dev Container
            </TableHead>
            <TableHead className="font-mono text-[0.68rem] uppercase tracking-[0.12em]">
              Full VM
            </TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {ROWS.map((r) => (
            <TableRow key={r.property}>
              <TableCell className="font-medium">{r.property}</TableCell>
              <TableCell>
                <Mark {...r.host} />
              </TableCell>
              <TableCell className="border-x border-contained/30 bg-contained/[0.07]">
                <Mark {...r.sandbox} />
              </TableCell>
              <TableCell>
                <Mark {...r.devcontainer} />
              </TableCell>
              <TableCell>
                <Mark {...r.vm} />
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  );
}
