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
  Diff,
  DirEntry,
  FleetState,
  Info,
  LaunchRequest,
  LaunchResult,
  NetworkPolicy,
  Process,
  Repo,
  Run,
  Sandbox,
  SandboxRef,
  Snapshot,
  Volume,
} from "@/lib/types";

const sbx = (id: string) => `/v1/sandboxes/${encodeURIComponent(id)}`;

export const keys = {
  info: ["info"] as const,
  sandboxes: ["sandboxes"] as const,
  sandbox: (id: string) => ["sandbox", id] as const,
  processes: (id: string) => ["processes", id] as const,
  events: (id: string) => ["events", id] as const,
  dir: (id: string, path: string) => ["dir", id, path] as const,
  volumes: ["volumes"] as const,
  snapshots: ["snapshots"] as const,
  repos: ["repos"] as const,
  runs: ["runs"] as const,
  refs: (repo: string) => ["refs", repo] as const,
  diff: (repo: string, ref: string) => ["diff", repo, ref] as const,
  fleet: (repo: string) => ["fleet", repo] as const,
  agents: ["agents"] as const,
  agentStates: ["agent-states"] as const,
};

// --- reads -------------------------------------------------------------------------

export function useInfo() {
  return useQuery({ queryKey: keys.info, queryFn: () => apiFetch<Info>("/info"), refetchInterval: 30_000 });
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

export function useRepos() {
  return useQuery({ queryKey: keys.repos, queryFn: async () => (await apiFetch<{ repos: Repo[] }>("/repos")).repos });
}

/** Every live agent sandbox and what its agent is doing; reads conversations, so polled gently. */
export function useAgentStates() {
  return useQuery({
    queryKey: keys.agentStates,
    queryFn: () => apiFetch<AgentState[]>("/agents/state"),
    refetchInterval: 5_000,
  });
}

export function useRuns() {
  return useQuery({
    queryKey: keys.runs,
    queryFn: async () => (await apiFetch<{ runs: Run[] }>("/runs")).runs,
    refetchInterval: 5_000,
  });
}

export function useRefs(repo: string) {
  return useQuery({
    queryKey: keys.refs(repo),
    queryFn: async () => (await apiFetch<{ refs: SandboxRef[] }>(`/repos/${repo}/refs`)).refs,
    enabled: !!repo,
    refetchInterval: 15_000,
  });
}

export function useDiff(repo: string, ref: string) {
  return useQuery({
    queryKey: keys.diff(repo, ref),
    queryFn: () => apiFetch<Diff>(`/repos/${repo}/diff?ref=${encodeURIComponent(ref)}`),
    enabled: !!repo && !!ref,
  });
}

export function useFleet(repo: string) {
  return useQuery({
    queryKey: keys.fleet(repo),
    queryFn: () => apiFetch<FleetState>(`/repos/${repo}/fleet`),
    enabled: !!repo,
    refetchInterval: 5_000,
    retry: false,
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
    () => [[...keys.sandboxes], [...keys.runs]],
  );
}

export function useKill() {
  return useInvalidating(
    (id: string) => apiFetch<void>(sbx(id), { method: "DELETE" }),
    (id) => [[...keys.sandboxes], [...keys.sandbox(id)], [...keys.runs], [...keys.volumes]],
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

export function useUpdateNetwork() {
  return useInvalidating(
    ({ id, network }: { id: string; network: NetworkPolicy }) =>
      apiFetch<Sandbox>(sbx(id), { method: "PATCH", json: { network } }),
    ({ id }) => [[...keys.sandbox(id)], [...keys.sandboxes]],
  );
}

export function useSignal() {
  return useMutation({
    mutationFn: ({ id, pid, signal }: { id: string; pid: number; signal: string }) =>
      apiFetch<void>(`${sbx(id)}/processes/${pid}/signal`, { method: "POST", json: { signal } }),
  });
}

export function useBringBack() {
  return useInvalidating(
    (sandbox: string) => apiFetch<{ ref: string }>(`/runs/${sandbox}/bring-back`, { method: "POST" }),
    () => [[...keys.runs], ["refs"]],
  );
}

export function useForgetRun() {
  return useInvalidating(
    (sandbox: string) => apiFetch<void>(`/runs/${sandbox}`, { method: "DELETE" }),
    () => [[...keys.runs]],
  );
}

export function useAddRepo() {
  return useInvalidating(
    (path: string) => apiFetch<Repo>("/repos", { method: "POST", json: { path } }),
    () => [[...keys.repos]],
  );
}

export function useRemoveRepo() {
  return useInvalidating(
    (id: string) => apiFetch<void>(`/repos/${id}`, { method: "DELETE" }),
    () => [[...keys.repos]],
  );
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

export function useLand() {
  return useInvalidating(
    ({ repo, ...body }: { repo: string; branch?: string; all?: boolean; unverified?: boolean; onto?: string }) =>
      apiFetch<{ landed?: string[]; skipped?: { Branch: string; Reason: string }[]; error?: string }>(
        `/repos/${repo}/fleet/land`,
        { method: "POST", json: body },
      ),
    ({ repo }) => [[...keys.fleet(repo)], [...keys.refs(repo)]],
  );
}
