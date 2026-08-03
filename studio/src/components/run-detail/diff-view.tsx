"use client";

import { useState } from "react";
import { Columns2, FileDiff, FileMinus, FilePlus, FileText, Rows3 } from "lucide-react";
import { Skeleton } from "@/components/ui/skeleton";
import { ScrollArea } from "@/components/ui/scroll-area";
import { Badge } from "@/components/ui/badge";
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group";
import { EmptyState } from "@/components/common/empty-state";
import { CopyButton } from "@/components/common/copy-button";
import { useRunDiff } from "@/lib/api/queries";
import { useUi } from "@/lib/store";
import { pluralize } from "@/lib/format";
import { cn } from "@/lib/utils";
import type { DiffFile, DiffFileStatus, DiffLine, Run } from "@/lib/types";

const STATUS_ICON: Record<DiffFileStatus, typeof FileText> = {
  added: FilePlus,
  modified: FileText,
  deleted: FileMinus,
  renamed: FileDiff,
};

const STATUS_TONE: Record<DiffFileStatus, string> = {
  added: "text-status-good",
  modified: "text-chart-1",
  deleted: "text-status-critical",
  renamed: "text-chart-4",
};

/**
 * What the run changed in the workspace.
 *
 * This is the answer to the question a crash makes urgent: the files are on a
 * bind mount, so they are usually already on disk — the diff is how you find out
 * whether that is good news.
 */
export function DiffView({ run }: { run: Run }) {
  const { data, isPending } = useRunDiff(run.id, run.state === "running");
  const view = useUi((s) => s.diffView);
  const setView = useUi((s) => s.setDiffView);
  const [selected, setSelected] = useState<string | null>(null);

  if (isPending) {
    return (
      <div className="grid gap-4 lg:grid-cols-[16rem_1fr]">
        <Skeleton className="h-72 w-full" />
        <Skeleton className="h-72 w-full" />
      </div>
    );
  }

  if (!data || data.length === 0) {
    return (
      <EmptyState
        icon={FileDiff}
        title="Nothing changed"
        description="The workspace is as it was. For a run that failed early that is expected; for one that passed, it means the work was already committed."
      />
    );
  }

  const active = data.find((f) => f.path === selected) ?? data[0];
  const totals = data.reduce(
    (acc, f) => ({
      insertions: acc.insertions + f.insertions,
      deletions: acc.deletions + f.deletions,
    }),
    { insertions: 0, deletions: 0 },
  );

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <p className="text-sm text-muted-foreground">
          {pluralize(data.length, "file")} ·{" "}
          <span className="font-mono text-status-good">+{totals.insertions}</span>{" "}
          <span className="font-mono text-status-critical">-{totals.deletions}</span>
        </p>
        <ToggleGroup
          type="single"
          size="sm"
          value={view}
          onValueChange={(v) => v && setView(v as "unified" | "split")}
          variant="outline"
        >
          <ToggleGroupItem value="unified" className="h-8 px-2.5 text-xs">
            <Rows3 className="size-3.5" />
            Unified
          </ToggleGroupItem>
          <ToggleGroupItem value="split" className="h-8 px-2.5 text-xs">
            <Columns2 className="size-3.5" />
            Split
          </ToggleGroupItem>
        </ToggleGroup>
      </div>

      <div className="grid gap-4 lg:grid-cols-[17rem_1fr]">
        <div className="overflow-hidden rounded-lg border">
          <div className="border-b bg-muted/40 px-3 py-2 text-xs font-medium">Files</div>
          <ScrollArea className="h-[28rem]">
            <div className="p-1.5">
              {data.map((file) => {
                const Icon = STATUS_ICON[file.status];
                const isActive = file.path === active.path;
                return (
                  <button
                    key={file.path}
                    onClick={() => setSelected(file.path)}
                    className={cn(
                      "flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-xs transition-colors",
                      isActive ? "bg-accent" : "hover:bg-accent/50",
                    )}
                  >
                    <Icon className={cn("size-3.5 shrink-0", STATUS_TONE[file.status])} />
                    <span className="min-w-0 flex-1 truncate font-mono" title={file.path}>
                      {file.path.split("/").pop()}
                    </span>
                    <span className="shrink-0 font-mono text-[10px] tabular-nums">
                      <span className="text-status-good">+{file.insertions}</span>{" "}
                      <span className="text-status-critical">-{file.deletions}</span>
                    </span>
                  </button>
                );
              })}
            </div>
          </ScrollArea>
        </div>

        <FilePanel file={active} view={view} />
      </div>
    </div>
  );
}

/**
 * One file's changes. Exported so a commit's diff renders through the same
 * component a run's does — two renderers would drift, and the one thing a diff
 * viewer must not do is show the same change two different ways.
 */
export function FilePanel({ file, view }: { file: DiffFile; view: "unified" | "split" }) {
  const Icon = STATUS_ICON[file.status];
  const text = file.hunks
    .flatMap((h) => [
      h.header,
      ...h.lines.map(
        (l) => `${l.kind === "add" ? "+" : l.kind === "del" ? "-" : " "}${l.content}`,
      ),
    ])
    .join("\n");

  return (
    <div className="surface-sheen overflow-hidden rounded-lg border">
      <div className="flex flex-wrap items-center gap-2 border-b bg-muted/40 px-3 py-2">
        <Icon className={cn("size-3.5", STATUS_TONE[file.status])} />
        <span className="min-w-0 flex-1 truncate font-mono text-xs">{file.path}</span>
        <Badge variant="outline" className="text-[10px] capitalize">
          {file.status}
        </Badge>
        <CopyButton value={text} label="Copy diff" />
      </div>

      <ScrollArea className="h-[28rem]">
        {file.binary ? (
          <p className="p-4 text-sm text-muted-foreground">
            Binary file — no textual diff to show.
          </p>
        ) : (
          <div className="font-mono text-[12.5px] leading-[1.6]">
            {file.hunks.map((hunk, hi) => (
              <div key={hi}>
                <div className="bg-chart-7/10 px-3 py-1 text-chart-7 select-none">
                  {hunk.header}
                </div>
                {view === "unified"
                  ? hunk.lines.map((line, li) => <UnifiedLine key={li} line={line} />)
                  : splitRows(hunk.lines).map((row, li) => <SplitRow key={li} row={row} />)}
              </div>
            ))}
          </div>
        )}
      </ScrollArea>
    </div>
  );
}

const LINE_TONE: Record<DiffLine["kind"], string> = {
  add: "bg-status-good/[0.09]",
  del: "bg-status-critical/[0.09]",
  ctx: "",
  meta: "text-muted-foreground",
};

function UnifiedLine({ line }: { line: DiffLine }) {
  return (
    <div className={cn("flex", LINE_TONE[line.kind])}>
      <Gutter n={line.oldNo} />
      <Gutter n={line.newNo} />
      {/* The marker column carries the add/remove sign, so the diff is readable
          without the background tint — the tint is the second channel, not the
          only one. */}
      <span
        className={cn(
          "w-4 shrink-0 select-none text-center",
          line.kind === "add"
            ? "text-status-good"
            : line.kind === "del"
              ? "text-status-critical"
              : "text-muted-foreground/40",
        )}
      >
        {line.kind === "add" ? "+" : line.kind === "del" ? "-" : " "}
      </span>
      <span className="min-w-0 flex-1 pr-3 whitespace-pre-wrap break-words">
        {line.content || " "}
      </span>
    </div>
  );
}

interface SplitPair {
  left: DiffLine | null;
  right: DiffLine | null;
}

/**
 * Pair deletions with the additions that replaced them.
 *
 * A run of `n` deletions followed by `m` additions becomes `max(n, m)` rows, so
 * a rename or a rewritten block lines up side by side instead of drifting.
 */
function splitRows(lines: DiffLine[]): SplitPair[] {
  const rows: SplitPair[] = [];
  let i = 0;
  while (i < lines.length) {
    const line = lines[i];
    if (line.kind === "ctx" || line.kind === "meta") {
      rows.push({ left: line, right: line });
      i++;
      continue;
    }
    const dels: DiffLine[] = [];
    const adds: DiffLine[] = [];
    while (i < lines.length && lines[i].kind === "del") dels.push(lines[i++]);
    while (i < lines.length && lines[i].kind === "add") adds.push(lines[i++]);
    const n = Math.max(dels.length, adds.length);
    for (let k = 0; k < n; k++) {
      rows.push({ left: dels[k] ?? null, right: adds[k] ?? null });
    }
  }
  return rows;
}

function SplitRow({ row }: { row: SplitPair }) {
  return (
    <div className="flex divide-x">
      <Half line={row.left} side="left" />
      <Half line={row.right} side="right" />
    </div>
  );
}

function Half({ line, side }: { line: DiffLine | null; side: "left" | "right" }) {
  if (!line) {
    return <div className="w-1/2 bg-muted/30" />;
  }
  const tone =
    side === "left" && line.kind === "del"
      ? "bg-status-critical/[0.09]"
      : side === "right" && line.kind === "add"
        ? "bg-status-good/[0.09]"
        : "";
  return (
    <div className={cn("flex w-1/2 min-w-0", tone)}>
      <Gutter n={side === "left" ? line.oldNo : line.newNo} />
      <span className="min-w-0 flex-1 px-2 whitespace-pre-wrap break-words">
        {line.content || " "}
      </span>
    </div>
  );
}

function Gutter({ n }: { n: number | null }) {
  return (
    <span className="w-11 shrink-0 pr-2 text-right tabular-nums text-muted-foreground/40 select-none">
      {n ?? ""}
    </span>
  );
}
