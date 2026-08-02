import {
  CheckCircle2,
  CircleDashed,
  CircleSlash,
  Loader2,
  ShieldAlert,
  XCircle,
  type LucideIcon,
} from "lucide-react";
import { cn } from "@/lib/utils";
import type { RunOutcome } from "@/lib/types";

/**
 * A run's outcome, as a word.
 *
 * Status colour is never the only channel: every variant carries an icon and a
 * label, because four of these are red-ish or green-ish to somebody. That is the
 * same rule the CLI follows when it prints a reason next to a refusal rather
 * than relying on an exit code.
 */
const VARIANTS: Record<
  RunOutcome,
  { label: string; icon: LucideIcon; className: string; spin?: boolean }
> = {
  running: {
    label: "Running",
    icon: Loader2,
    className: "text-status-running border-status-running/30 bg-status-running/10",
    spin: true,
  },
  passed: {
    label: "Passed",
    icon: CheckCircle2,
    className: "text-status-good border-status-good/30 bg-status-good/10",
  },
  failed: {
    label: "Failed",
    icon: XCircle,
    className: "text-status-critical border-status-critical/30 bg-status-critical/10",
  },
  "verify-failed": {
    label: "Verify failed",
    icon: ShieldAlert,
    className: "text-status-serious border-status-serious/30 bg-status-serious/10",
  },
  stopped: {
    label: "Stopped",
    icon: CircleSlash,
    className: "text-muted-foreground border-border bg-muted/40",
  },
  created: {
    label: "Created",
    icon: CircleDashed,
    className: "text-muted-foreground border-border bg-muted/40",
  },
};

export function StatusBadge({
  outcome,
  exitCode,
  className,
  size = "default",
}: {
  outcome: RunOutcome;
  /** Shown for a non-zero exit: "Failed · 1" says more than "Failed". */
  exitCode?: number | null;
  className?: string;
  size?: "default" | "sm";
}) {
  const v = VARIANTS[outcome];
  const Icon = v.icon;
  const showCode =
    exitCode !== null &&
    exitCode !== undefined &&
    exitCode !== 0 &&
    (outcome === "failed" || outcome === "verify-failed" || outcome === "stopped");

  return (
    <span
      className={cn(
        "inline-flex shrink-0 items-center gap-1.5 rounded-md border font-medium whitespace-nowrap",
        size === "sm" ? "px-1.5 py-0.5 text-[11px]" : "px-2 py-0.5 text-xs",
        v.className,
        className,
      )}
    >
      <Icon
        className={cn(size === "sm" ? "size-3" : "size-3.5", v.spin && "animate-spin")}
        aria-hidden
      />
      {v.label}
      {showCode && <span className="tabular-nums opacity-70">· {exitCode}</span>}
    </span>
  );
}

/** The one moving thing on a live row. */
export function LiveDot({ className }: { className?: string }) {
  return (
    <span className={cn("relative inline-flex size-2", className)}>
      <span className="live-dot absolute inset-0 rounded-full bg-status-running" />
    </span>
  );
}
