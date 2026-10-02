"use client";

import { Suspense, useState } from "react";
import { useSearchParams } from "next/navigation";
import { GitMerge } from "lucide-react";
import { PageHeader } from "@/components/common/page-header";
import { EmptyState } from "@/components/common/empty-state";
import { RepoGate } from "@/components/common/repo-gate";
import { CopyButton } from "@/components/common/copy-button";
import { useDiff, useRefs } from "@/lib/api/queries";
import type { Repo } from "@/lib/types";
import { formatRelative } from "@/lib/format";
import { cn } from "@/lib/utils";

/**
 * Work that came back: every ref under refs/sandbox/ — bring-backs,
 * checkpoints, fleet tasks — and its diff against HEAD. Read-only by design:
 * merging is a git command you run, because which branch it lands on is your
 * decision and not a button's.
 */
function Review({ repo }: { repo: Repo }) {
  const initial = useSearchParams().get("ref") ?? "";
  const { data: refs, isLoading } = useRefs(repo.id);
  const [picked, setPicked] = useState(initial);
  const ref = picked || refs?.[0]?.ref || "";
  const { data: diff, error } = useDiff(repo.id, ref);

  if (!isLoading && (refs ?? []).length === 0)
    return <EmptyState icon={GitMerge} title="Nothing has come back yet" description="When a run ends, or you bring one back, its commits appear here under refs/sandbox/." />;

  return (
    <div className="grid gap-4 lg:grid-cols-[22rem_minmax(0,1fr)]">
      <ul className="flex max-h-[75vh] flex-col gap-1 overflow-auto">
        {(refs ?? []).map((r) => (
          <li key={r.ref}>
            <button
              onClick={() => setPicked(r.ref)}
              className={cn("w-full rounded-md border px-3 py-2 text-left hover:bg-muted", r.ref === ref && "border-primary bg-muted")}
            >
              <div className="truncate font-mono text-xs">{r.ref.replace("refs/sandbox/", "")}</div>
              <div className="truncate text-sm">{r.subject}</div>
              <div className="text-xs text-muted-foreground">
                {r.ahead} commit{r.ahead === 1 ? "" : "s"} not in HEAD · {formatRelative(r.date)}
              </div>
            </button>
          </li>
        ))}
      </ul>
      <div className="flex min-w-0 flex-col gap-3">
        {ref && (
          <div className="flex flex-wrap items-center gap-2 rounded-md border bg-muted/40 px-3 py-2 font-mono text-xs">
            <span className="text-muted-foreground">merge it:</span>
            <span>git merge {ref}</span>
            <CopyButton value={`git merge ${ref}`} />
          </div>
        )}
        {error ? <p className="text-sm text-destructive">{error.message}</p> : null}
        {diff && (
          <>
            <ul className="flex flex-col gap-0.5 font-mono text-xs">
              {diff.files.map((f) => (
                <li key={f.path} className="flex gap-3">
                  <span className="w-20 text-right">
                    {f.added < 0 ? "binary" : (
                      <>
                        <span className="text-status-good">+{f.added}</span> <span className="text-status-critical">-{f.removed}</span>
                      </>
                    )}
                  </span>
                  <span className="truncate">{f.path}</span>
                </li>
              ))}
              {diff.files.length === 0 && <li className="text-muted-foreground">No changes against HEAD.</li>}
            </ul>
            <pre className="max-h-[65vh] overflow-auto rounded-md border bg-[#0b0b0c] p-3 font-mono text-xs leading-relaxed text-[#e7e7ea]">
              {diff.patch.split("\n").map((line, i) => (
                <div
                  key={i}
                  className={cn(
                    "whitespace-pre",
                    line.startsWith("+") && !line.startsWith("+++") && "bg-status-good/10 text-[#86efac]",
                    line.startsWith("-") && !line.startsWith("---") && "bg-status-critical/10 text-[#fca5a5]",
                    line.startsWith("@@") && "text-[#93c5fd]",
                    line.startsWith("diff --git") && "mt-2 font-semibold",
                  )}
                >
                  {line || " "}
                </div>
              ))}
            </pre>
            {diff.truncated ? <p className="text-xs text-muted-foreground">The patch is longer than shown; git diff HEAD...{ref} has all of it.</p> : null}
          </>
        )}
      </div>
    </div>
  );
}

export default function ReviewPage() {
  return (
    <div className="flex flex-col gap-4">
      <PageHeader title="Review" description="Commits that came back from sandboxes, against what you have checked out." />
      <RepoGate>
        {(repo) => (
          <Suspense fallback={null}>
            <Review repo={repo} />
          </Suspense>
        )}
      </RepoGate>
    </div>
  );
}
