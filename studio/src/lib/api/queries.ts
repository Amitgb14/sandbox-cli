"use client";

/**
 * Every read and write Studio makes, as TanStack Query hooks. Reads refresh on
 * an interval suited to how fast the thing changes; writes invalidate what
 * they change, so a screen never shows the state from before your click.
 */

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiFetch } from "@/lib/api/client";
import type {
  Agent,
  AgentState,
  AuditEvent,
  DirEntry,
  EgressGroup,
  EgressRule,
  EgressSettings,
  Info,
  LaunchRequest,
  LaunchResult,
  MetricsList,
  NetworkPolicy,
  NodeStatus,
  Process,
  Sandbox,
  Snapshot,
  VmTemplate,
  Volume,
} from "@/lib/types";

const sbx = (id: string) => `/v1/sandboxes/${encodeURIComponent(id)}`;

export const keys = {
  info: ["info"] as const,
  node: ["node"] as const,
  sandboxes: ["sandboxes"] as const,
  sandbox: (id: string) => ["sandbox", id] as const,
  processes: (id: string) => ["processes", id] as const,
  events: (id: string) => ["events", id] as const,
  metrics: (id: string) => ["metrics", id] as const,
  dir: (id: string, path: string) => ["dir", id, path] as const,
  volumes: ["volumes"] as const,
  snapshots: ["snapshots"] as const,
  agents: ["agents"] as const,
  agentStates: ["agent-states"] as const,
  templates: ["templates"] as const,
  egress: ["egress"] as const,
};

// --- reads -------------------------------------------------------------------------

export function useInfo() {
  return useQuery({ queryKey: keys.info, queryFn: () => apiFetch<Info>("/info"), refetchInterval: 30_000 });
}

/**
 * The machine's capacity and what is free of it: a plain sandboxd's. A
 * gateway has no such endpoint, so callers enable this only once whoami says
 * this is not one, and a refusal is still an answer, not an error to retry.
 */
export function useNode(enabled = true) {
  return useQuery({
    queryKey: keys.node,
    queryFn: () => apiFetch<NodeStatus>("/v1/node"),
    enabled,
    refetchInterval: 10_000,
    retry: false,
  });
}

export function useSandboxes() {
  return useQuery({
    queryKey: keys.sandboxes,
    queryFn: async () => (await apiFetch<{ sandboxes: Sandbox[] }>("/v1/sandboxes")).sandboxes,
    refetchInterval: (q) => (q.state.data?.some((s) => s.snapshotting) ? 2_000 : 5_000),
  });
}

export function useSandbox(id: string) {
  return useQuery({
    queryKey: keys.sandbox(id),
    queryFn: () => apiFetch<Sandbox>(sbx(id)),
    enabled: !!id,
    // Every second while a snapshot is taken, for its progress.
    refetchInterval: (q) => (q.state.data?.snapshotting ? 1_000 : 5_000),
  });
}

export function useProcesses(id: string, enabled = true) {
  return useQuery({
    queryKey: keys.processes(id),
    queryFn: async () => (await apiFetch<{ processes: Process[] }>(`${sbx(id)}/processes`)).processes,
    enabled: !!id && enabled,
    refetchInterval: 3_000,
  });
}

export function useEvents(id: string, enabled = true) {
  return useQuery({
    queryKey: keys.events(id),
    queryFn: async () => (await apiFetch<{ events: AuditEvent[]; truncated?: boolean }>(`${sbx(id)}/events`)),
    enabled: !!id && enabled,
    refetchInterval: 5_000,
    retry: false,
  });
}

/** How long a file or directory read may take before the screen says so. */
export const FILES_TIMEOUT_MS = 20_000;

/** A guest's answer that never came, said as that rather than as an abort. */
export function noAnswer(e: unknown): Error {
  return e instanceof DOMException && (e.name === "TimeoutError" || e.name === "AbortError")
    ? new Error(`The sandbox did not answer within ${FILES_TIMEOUT_MS / 1000} seconds.`)
    : (e as Error);
}

/** The last hour of a sandbox's usage, read while something shows it. */
export function useMetrics(id: string, enabled = true) {
  return useQuery({
    queryKey: keys.metrics(id),
    queryFn: () => apiFetch<MetricsList>(`${sbx(id)}/metrics`),
    enabled: !!id && enabled,
    refetchInterval: 10_000,
    retry: false,
  });
}

export function useDir(id: string, path: string, enabled = true) {
  return useQuery({
    queryKey: keys.dir(id, path),
    // Bounded: a read the guest never answered left "reading…" up for good,
    // with nothing to do about it.
    queryFn: async ({ signal }) => {
      try {
        const out = await apiFetch<{ entries: DirEntry[] }>(`${sbx(id)}/dirs?path=${encodeURIComponent(path)}`, {
          signal: AbortSignal.any([signal, AbortSignal.timeout(FILES_TIMEOUT_MS)]),
        });
        return out.entries;
      } catch (e) {
        throw noAnswer(e);
      }
    },
    enabled: !!id && enabled,
    retry: false,
  });
}

export function useVolumes(enabled = true) {
  return useQuery({
    queryKey: keys.volumes,
    queryFn: async () => (await apiFetch<{ volumes: Volume[] }>("/v1/volumes")).volumes,
    enabled,
    refetchInterval: 10_000,
    retry: false,
  });
}

export function useSnapshots(enabled = true) {
  return useQuery({
    queryKey: keys.snapshots,
    queryFn: async () => (await apiFetch<{ snapshots: Snapshot[] }>("/v1/snapshots")).snapshots,
    enabled,
    retry: false,
    // A sandbox's schedule adds and removes them while a screen is open.
    refetchInterval: 10_000,
  });
}

/** Every live agent sandbox and what its agent is doing; reads conversations, so polled gently. */
export function useAgentStates() {
  return useQuery({
    queryKey: keys.agentStates,
    queryFn: () => apiFetch<AgentState[]>("/agents/state"),
    refetchInterval: 5_000,
  });
}

export function useAgents() {
  return useQuery({ queryKey: keys.agents, queryFn: async () => (await apiFetch<{ agents: Agent[] }>("/agents")).agents });
}

/** Sizes to launch at: the built-in ones, then this user's (~/.config/sandbox/studio.json). */
export function useTemplates() {
  return useQuery({ queryKey: keys.templates, queryFn: async () => (await apiFetch<{ templates: VmTemplate[] }>("/templates")).templates });
}

/** Allowlist groups and the deny rules every Studio launch carries. */
export function useEgress() {
  return useQuery({ queryKey: keys.egress, queryFn: () => apiFetch<EgressSettings>("/egress") });
}

// --- writes ------------------------------------------------------------------------

function useInvalidating<V, R>(fn: (v: V) => Promise<R>, invalidate: (v: V) => readonly unknown[][]) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: fn,
    onSuccess: (_r, v) => {
      for (const k of invalidate(v)) qc.invalidateQueries({ queryKey: k });
    },
  });
}

export function useLaunch() {
  return useInvalidating(
    (req: LaunchRequest) => apiFetch<LaunchResult>("/runs", { method: "POST", json: req }),
    () => [[...keys.sandboxes]],
  );
}

export function useKill() {
  return useInvalidating(
    (id: string) => apiFetch<void>(sbx(id), { method: "DELETE" }),
    (id) => [[...keys.sandboxes], [...keys.sandbox(id)], [...keys.volumes]],
  );
}

export function useSuspend() {
  return useInvalidating(
    ({ id, resume }: { id: string; resume?: boolean }) =>
      apiFetch<Sandbox>(`${sbx(id)}/${resume ? "resume" : "suspend"}`, { method: "POST" }),
    ({ id }) => [[...keys.sandboxes], [...keys.sandbox(id)]],
  );
}

export function useSnapshot() {
  return useInvalidating(
    (id: string) => apiFetch<Snapshot>(`${sbx(id)}/snapshots`, { method: "POST" }),
    (id) => [[...keys.snapshots], [...keys.sandbox(id)], [...keys.sandboxes]],
  );
}

export function useDeleteSnapshot() {
  return useInvalidating(
    (id: string) => apiFetch<void>(`/v1/snapshots/${encodeURIComponent(id)}`, { method: "DELETE" }),
    () => [[...keys.snapshots]],
  );
}

/** Renames, relabels or retimes a live sandbox; only what is given changes. */
export function useUpdateSandbox() {
  return useInvalidating(
    ({ id, ...req }: { id: string; name?: string; labels?: Record<string, string>; idle_timeout_secs?: number }) =>
      apiFetch<Sandbox>(sbx(id), { method: "PATCH", json: req }),
    ({ id }) => [[...keys.sandbox(id)], [...keys.sandboxes]],
  );
}

/** A copy at a new size in the sandbox's place (Studio's server: internal/studio/resize.go). */
export function useResize() {
  return useInvalidating(
    ({ id, ...req }: { id: string; cpus: number; memory_mb: number; disk_mb?: number; drop_env?: boolean; keep_snapshot?: boolean }) =>
      apiFetch<{ sandbox: Sandbox; replaced: string; snapshot?: string }>(`/sandboxes/${encodeURIComponent(id)}/resize`, { method: "POST", json: req }),
    ({ id }) => [[...keys.sandbox(id)], [...keys.sandboxes], [...keys.snapshots]],
  );
}

export function useUpdateNetwork() {
  return useInvalidating(
    ({ id, network }: { id: string; network: NetworkPolicy }) =>
      apiFetch<Sandbox>(sbx(id), { method: "PATCH", json: { network } }),
    ({ id }) => [[...keys.sandbox(id)], [...keys.sandboxes]],
  );
}

/**
 * A shell in a running sandbox, on a terminal the browser then attaches to:
 * the same process `sandbox-cli shell` starts — bash where the image has it,
 * sh otherwise, in the sandbox user's home.
 */
/** The shell Studio opens: bash if the image has it, else sh, as a login shell. */
export const SHELL_ARGV = ["/bin/sh", "-c", "if command -v bash >/dev/null 2>&1; then exec bash -l; else exec sh -l; fi"];

export function useOpenShell() {
  return useInvalidating(
    ({ id, rows, cols }: { id: string; rows: number; cols: number }) =>
      apiFetch<Process>(`${sbx(id)}/processes`, {
        method: "POST",
        json: {
          argv: SHELL_ARGV,
          cwd: "/sandbox/home",
          tty: true,
          rows,
          cols,
          env: { TERM: "xterm-256color" },
        },
      }),
    ({ id }) => [[...keys.processes(id)]],
  );
}

/** The command images/desktop provides: a desktop served over VNC on the guest's loopback. */
export const DESKTOP_COMMAND = "sandbox-desktop";

export function useStartDesktop() {
  return useInvalidating(
    ({ id }: { id: string }) =>
      apiFetch<Process>(`${sbx(id)}/processes`, { method: "POST", json: { argv: [DESKTOP_COMMAND], cwd: "/sandbox/home" } }),
    ({ id }) => [[...keys.processes(id)]],
  );
}

export function useSetSnapshotSchedule() {
  return useInvalidating(
    ({ id, every_secs, keep }: { id: string; every_secs: number; keep: number }) =>
      apiFetch<Sandbox>(`${sbx(id)}/snapshot-schedule`, { method: "PUT", json: { every_secs, keep } }),
    ({ id }) => [[...keys.sandbox(id)], [...keys.sandboxes], [...keys.snapshots]],
  );
}

export function useSignal() {
  return useMutation({
    mutationFn: ({ id, pid, signal }: { id: string; pid: number; signal: string }) =>
      apiFetch<void>(`${sbx(id)}/processes/${pid}/signal`, { method: "POST", json: { signal } }),
  });
}

export function useCreateVolume() {
  return useInvalidating(
    ({ name, size_mb }: { name: string; size_mb?: number }) =>
      apiFetch<Volume>("/v1/volumes", { method: "POST", json: size_mb ? { name, size_mb } : { name } }),
    () => [[...keys.volumes]],
  );
}

export function useDeleteVolume() {
  return useInvalidating(
    (name: string) => apiFetch<void>(`/v1/volumes/${encodeURIComponent(name)}`, { method: "DELETE" }),
    () => [[...keys.volumes]],
  );
}


export function useSaveTemplate() {
  return useInvalidating(
    (t: VmTemplate) =>
      apiFetch<VmTemplate>(`/templates/${encodeURIComponent(t.name)}`, {
        method: "PUT",
        json: { description: t.description ?? "", cpus: t.cpus, memory_mb: t.memory_mb, disk_mb: t.disk_mb ?? 0 },
      }),
    () => [[...keys.templates]],
  );
}

export function useDeleteTemplate() {
  return useInvalidating(
    (name: string) => apiFetch<void>(`/templates/${encodeURIComponent(name)}`, { method: "DELETE" }),
    () => [[...keys.templates]],
  );
}

export function useSaveEgressGroup() {
  return useInvalidating(
    (g: EgressGroup) =>
      apiFetch<EgressGroup>(`/egress/groups/${encodeURIComponent(g.name)}`, {
        method: "PUT",
        json: { description: g.description ?? "", hosts: g.hosts, default: !!g.default },
      }),
    () => [[...keys.egress]],
  );
}

export function useDeleteEgressGroup() {
  return useInvalidating(
    (name: string) => apiFetch<void>(`/egress/groups/${encodeURIComponent(name)}`, { method: "DELETE" }),
    () => [[...keys.egress]],
  );
}

/** Replaces the deny rules whole, as the server stores them. */
export function useSaveEgressRules() {
  return useInvalidating(
    (rules: EgressRule[]) => apiFetch<{ rules: EgressRule[] }>("/egress", { method: "PUT", json: { rules } }),
    () => [[...keys.egress]],
  );
}

/** Saves an agent's key. Write-only: no call returns it. */
export function useSaveAgentKey() {
  return useInvalidating(
    ({ agent, name, value }: { agent: string; name: string; value: string }) =>
      apiFetch<void>(`/agents/${encodeURIComponent(agent)}/keys/${encodeURIComponent(name)}`, { method: "PUT", json: { value } }),
    () => [[...keys.agents]],
  );
}

export function useDeleteAgentKey() {
  return useInvalidating(
    ({ agent, name }: { agent: string; name: string }) =>
      apiFetch<void>(`/agents/${encodeURIComponent(agent)}/keys/${encodeURIComponent(name)}`, { method: "DELETE" }),
    () => [[...keys.agents]],
  );
}
