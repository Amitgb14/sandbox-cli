import { cn } from "@/lib/utils";
import type { AgentState, AgentStateName, SandboxState } from "@/lib/types";

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

const ACTIVITY: Partial<Record<AgentStateName, { label: string; dot: string }>> = {
  working: { label: "Working", dot: "bg-status-running animate-pulse" },
  blocked: { label: "Waiting for you", dot: "bg-caution" },
  idle: { label: "Idle", dot: "bg-muted-foreground/60" },
  done: { label: "Done", dot: "bg-status-good" },
  failed: { label: "Failed", dot: "bg-destructive" },
  // Nothing to judge yet (no conversation written): the agent's name alone,
  // so the row still says it is an agent's sandbox.
  unknown: { label: "", dot: "bg-muted-foreground/40" },
};

/**
 * What a sandbox's agent is doing, under its state: a running sandbox says
 * only that the VM is up, and the question that brings someone to the list is
 * whether the agent in it needs them. Decided host-side by the same code as
 * `sandbox-cli agent state`; the reason is the tooltip. States that add
 * nothing to the sandbox's own (suspended, stopped) are not shown.
 */
export function AgentActivity({ agent, className }: { agent: AgentState; className?: string }) {
  const a = ACTIVITY[agent.state];
  if (!a) return null;
  return (
    <span
      title={`${agent.agent}: ${agent.why}`}
      className={cn("inline-flex items-center gap-1.5 text-xs whitespace-nowrap text-muted-foreground", agent.state === "blocked" && "text-caution", className)}
    >
      <span aria-hidden className={cn("size-1.5 shrink-0 rounded-full", a.dot)} />
      <span className="font-mono">{agent.agent}</span>
      {a.label ? a.label.toLowerCase() : null}
    </span>
  );
}
