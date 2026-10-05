import { cn } from "@/lib/utils";
import type { SandboxState } from "@/lib/types";

const STATE: Record<SandboxState, { label: string; dot: string }> = {
  pending: { label: "Starting", dot: "bg-status-running/60 animate-pulse" },
  running: { label: "Running", dot: "bg-status-running" },
  suspended: { label: "Suspended", dot: "bg-caution" },
  terminated: { label: "Terminated", dot: "bg-muted-foreground/40" },
};

/**
 * A sandbox's state as a dot and a word: quieter than a badge in a table full
 * of them, and the colour is never the only signal.
 */
export function StateDot({ state, className }: { state: SandboxState; className?: string }) {
  const s = STATE[state] ?? { label: state, dot: "bg-muted-foreground/40" };
  return (
    <span className={cn("inline-flex items-center gap-2 text-sm whitespace-nowrap", className)}>
      <span aria-hidden className={cn("size-2 shrink-0 rounded-full", s.dot)} />
      {s.label}
    </span>
  );
}
