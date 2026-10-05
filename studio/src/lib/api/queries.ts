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
  Info,
  LaunchRequest,
  LaunchResult,
  NetworkPolicy,
  NodeStatus,
  Process,
  Sandbox,
  Snapshot,
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
  dir: (id: string, path: string) => ["dir", id, path] as const,
  volumes: ["volumes"] as const,
  snapshots: ["snapshots"] as const,
  agents: ["agents"] as const,
  agentStates: ["agent-states"] as const,
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
    refetchInterval: 5_000,
  });
}

export function useSandbox(id: string) {
  return useQuery({
    queryKey: keys.sandbox(id),
    queryFn: () => apiFetch<Sandbox>(sbx(id)),
    enabled: !!id,
    refetchInterval: 5_000,
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

export function useDir(id: string, path: string, enabled = true) {
  return useQuery({
    queryKey: keys.dir(id, path),
    queryFn: async () =>
      (await apiFetch<{ entries: DirEntry[] }>(`${sbx(id)}/dirs?path=${encodeURIComponent(path)}`)).entries,
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
    () => [[...keys.snapshots]],
  );
}

export function useDeleteSnapshot() {
  return useInvalidating(
    (id: string) => apiFetch<void>(`/v1/snapshots/${encodeURIComponent(id)}`, { method: "DELETE" }),
    () => [[...keys.snapshots]],
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
export function useOpenShell() {
  return useInvalidating(
    ({ id, rows, cols }: { id: string; rows: number; cols: number }) =>
      apiFetch<Process>(`${sbx(id)}/processes`, {
        method: "POST",
        json: {
          argv: ["/bin/sh", "-c", "if command -v bash >/dev/null 2>&1; then exec bash -l; else exec sh -l; fi"],
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

