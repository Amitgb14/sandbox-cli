import {
  CheckCircle2,
  CircleDashed,
  CircleSlash,
  CircleHelp,
  Hand,
  Loader2,
  Moon,
  PauseCircle,
  XCircle,
  type LucideIcon,
} from "lucide-react";
import { cn } from "@/lib/utils";

/**
 * A state, as a word.
 *
 * Status colour is never the only channel: every variant carries an icon and a
 * label, because several of these are red-ish or green-ish to somebody. That is
 * the same rule the CLI follows when it prints a reason next to a refusal
 * rather than relying on an exit code.
 *
 * One map for the kinds of state Studio shows — a sandbox's and an agent's —
 * since they share words.
 */
const VARIANTS: Record<string, { label: string; icon: LucideIcon; className: string; spin?: boolean }> = {
  running: {
    label: "Running",
    icon: Loader2,
    className: "text-status-running border-status-running/30 bg-status-running/10",
    spin: true,
  },
  pending: { label: "Starting", icon: CircleDashed, className: "text-muted-foreground border-border bg-muted/40" },
  suspended: { label: "Suspended", icon: PauseCircle, className: "text-caution border-caution/30 bg-caution/10" },
  terminated: { label: "Terminated", icon: CircleSlash, className: "text-muted-foreground border-border bg-muted/40" },
  failed: {
    label: "Failed",
    icon: XCircle,
    className: "text-status-critical border-status-critical/30 bg-status-critical/10",
  },
  exited: { label: "Exited", icon: CheckCircle2, className: "text-muted-foreground border-border bg-muted/40" },
  // An agent's state (internal/agentstate). `blocked` is the one that asks for
  // somebody, so it is the one that stands out.
  working: {
    label: "Working",
    icon: Loader2,
    className: "text-status-running border-status-running/30 bg-status-running/10",
    spin: true,
  },
  blocked: { label: "Waiting for you", icon: Hand, className: "text-caution border-caution/40 bg-caution/15" },
  idle: { label: "Idle", icon: Moon, className: "text-muted-foreground border-border bg-muted/40" },
  done: { label: "Done", icon: CheckCircle2, className: "text-status-good border-status-good/30 bg-status-good/10" },
  stopped: { label: "Stopped", icon: CircleSlash, className: "text-muted-foreground border-border bg-muted/40" },
  unknown: { label: "Unknown", icon: CircleHelp, className: "text-muted-foreground border-border" },
  // A gateway's jobs and runs (internal/api/jobs_types.go) and service
  // replicas (services_types.go).
  queued: { label: "Queued", icon: CircleDashed, className: "text-muted-foreground border-border bg-muted/40" },
  succeeded: { label: "Succeeded", icon: CheckCircle2, className: "text-status-good border-status-good/30 bg-status-good/10" },
  cancelled: { label: "Cancelled", icon: CircleSlash, className: "text-muted-foreground border-border bg-muted/40" },
  timed_out: {
    label: "Timed out",
    icon: XCircle,
    className: "text-status-critical border-status-critical/30 bg-status-critical/10",
  },
  starting: { label: "Starting", icon: CircleDashed, className: "text-muted-foreground border-border bg-muted/40" },
  healthy: { label: "Healthy", icon: CheckCircle2, className: "text-status-good border-status-good/30 bg-status-good/10" },
  unhealthy: {
    label: "Unhealthy",
    icon: XCircle,
    className: "text-status-critical border-status-critical/30 bg-status-critical/10",
  },
  lost: { label: "Lost", icon: CircleHelp, className: "text-caution border-caution/30 bg-caution/10" },
  cordoned: { label: "Cordoned", icon: PauseCircle, className: "text-caution border-caution/30 bg-caution/10" },
  down: {
    label: "Not answering",
    icon: XCircle,
    className: "text-status-critical border-status-critical/30 bg-status-critical/10",
  },
};

export function StatusBadge({
  outcome,
  exitCode,
  className,
  size = "default",
}: {
  outcome: string;
  /** Shown for a non-zero exit: "Failed · 1" says more than "Failed". */
  exitCode?: number | null;
  className?: string;
  size?: "default" | "sm";
}) {
  const v = VARIANTS[outcome] ?? { label: outcome, icon: CircleDashed, className: "text-muted-foreground border-border" };
  const Icon = v.icon;
  const showCode =
    exitCode !== null &&
    exitCode !== undefined &&
    exitCode !== 0 &&
    (outcome === "failed" || outcome === "exited");

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
