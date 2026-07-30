import { cn } from "@/lib/utils";
import type { LucideIcon } from "lucide-react";

/**
 * An empty state that says *why* it is empty.
 *
 * "No results" is a dead end; "no runs match this filter" tells you what to
 * change. Where the daemon declined to answer rather than answered nothing, the
 * caller passes that reason through — an unanswerable question and an empty
 * answer are different things and the UI should not merge them.
 */
export function EmptyState({
  icon: Icon,
  title,
  description,
  action,
  className,
}: {
  icon: LucideIcon;
  title: string;
  description?: React.ReactNode;
  action?: React.ReactNode;
  className?: string;
}) {
  return (
    <div
      className={cn(
        "flex flex-col items-center justify-center gap-3 rounded-lg border border-dashed px-6 py-14 text-center",
        className,
      )}
    >
      <div className="flex size-10 items-center justify-center rounded-full bg-muted/60">
        <Icon className="size-5 text-muted-foreground" aria-hidden />
      </div>
      <div className="space-y-1">
        <p className="text-sm font-medium">{title}</p>
        {description && (
          <p className="mx-auto max-w-md text-sm text-muted-foreground">{description}</p>
        )}
      </div>
      {action}
    </div>
  );
}
