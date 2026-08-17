"use client";

import { cn } from "@/lib/utils";

/**
 * Why chains fired, commonest first.
 *
 * The stats above count *that* routing happened; this says what happened to
 * cause it, and the two lead to different actions. A column of "provider
 * answered 503" is an outage you waited out correctly. A column of "exited 1
 * having changed nothing" is not an outage at all — it is an agent failing
 * before it writes anything, which a chain will keep paying for until somebody
 * looks at why.
 *
 * The reason strings come from the run log unchanged, because they are the
 * words the run itself recorded; grouping them into categories here would be
 * this screen inventing a taxonomy the audit line does not have.
 */
export function FailoverReasons({
  reasons,
}: {
  reasons: Array<{ reason: string; count: number }>;
}) {
  if (reasons.length === 0) return null;
  const max = Math.max(...reasons.map((r) => r.count));

  return (
    <ul className="space-y-1.5">
      {reasons.slice(0, 6).map((r) => (
        <li key={r.reason} className="flex items-center gap-2 text-xs">
          <div className="h-2 w-24 shrink-0 overflow-hidden rounded-sm bg-muted">
            <div
              className={cn(
                "h-full rounded-sm",
                // A provider that answered badly and an agent that failed on its
                // own are different problems, and the colour says which without
                // asking anyone to read the string first.
                r.reason.includes("exit") ? "bg-status-serious" : "bg-status-critical",
              )}
              style={{ width: `${Math.max(6, (r.count / max) * 100)}%` }}
            />
          </div>
          <span className="w-6 shrink-0 tabular-nums text-[10px] text-muted-foreground">
            {r.count}
          </span>
          <span className="min-w-0 flex-1 truncate text-muted-foreground">{r.reason}</span>
        </li>
      ))}
      {reasons.length > 6 && (
        <li className="text-[10px] text-muted-foreground">
          and {reasons.length - 6} more — the log has all of them.
        </li>
      )}
    </ul>
  );
}
