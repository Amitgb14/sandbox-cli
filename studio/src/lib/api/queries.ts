"use client";

import {
  useMutation,
  useQuery,
  useQueryClient,
  type UseQueryOptions,
} from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { onTransportChange, reconnect, transportMode, type TransportMode } from "@/lib/api/client";
import { api } from "@/lib/api/endpoints";
import type { LaunchRequest } from "@/lib/types";

/**
 * Query keys, in one place. A key spelled two ways is a cache that never
 * invalidates and a table that never refreshes.
 */
export const qk = {
  daemon: ["daemon"] as const,
  runs: ["runs"] as const,
  run: (id: string) => ["runs", id] as const,
  runMetrics: (id: string) => ["runs", id, "metrics"] as const,
  runLogs: (id: string) => ["runs", id, "logs"] as const,
  runDiff: (id: string) => ["runs", id, "diff"] as const,
  runConfig: (id: string) => ["runs", id, "config"] as const,
  agents: ["agents"] as const,
  worktrees: ["worktrees"] as const,
  worktree: (b: string) => ["worktrees", b] as const,
  worktreeCommits: (b: string) => ["worktrees", b, "commits"] as const,
  branchRuns: (b: string) => ["runs", "branch", b] as const,
  usage: ["usage"] as const,
  doctor: ["doctor"] as const,
  audit: ["audit"] as const,
};

/** Live data is polled; everything else is fetched once and invalidated. */
const LIVE_MS = 4_000;

export function useDaemon() {
  return useQuery({ queryKey: qk.daemon, queryFn: api.daemon, staleTime: 30_000 });
}

export function useRuns(opts?: { live?: boolean }) {
  return useQuery({
    queryKey: qk.runs,
    queryFn: api.runs,
    refetchInterval: opts?.live === false ? false : LIVE_MS,
  });
}

export function useRun(id: string) {
  return useQuery({
    queryKey: qk.run(id),
    queryFn: () => api.run(id),
    refetchInterval: LIVE_MS,
  });
}

export function useRunMetrics(id: string, enabled = true) {
  return useQuery({
    queryKey: qk.runMetrics(id),
    queryFn: () => api.runMetrics(id),
    enabled,
    refetchInterval: LIVE_MS,
  });
}

/**
 * A run's output. Polled while the run is live, fetched once when it is not.
 *
 * Without the poll the terminal showed whatever the agent had written by the
 * time the page opened and never moved again — so a run that was working looked
 * identical to one that had stopped. `claude -p` buffers its result until the
 * turn ends, which makes this worse rather than better: the interesting output
 * arrives all at once, and arrives after the only fetch.
 */
export function useRunLogs(id: string, live = false, enabled = true) {
  return useQuery({
    queryKey: qk.runLogs(id),
    queryFn: () => api.runLogs(id),
    enabled,
    refetchInterval: live ? LIVE_MS : false,
  });
}

/** The work so far. Polled while the agent is still producing it. */
export function useRunDiff(id: string, live = false, enabled = true) {
  return useQuery({
    queryKey: qk.runDiff(id),
    queryFn: () => api.runDiff(id),
    enabled,
    refetchInterval: live ? LIVE_MS : false,
  });
}

export function useRunConfig(id: string, enabled = true) {
  return useQuery({ queryKey: qk.runConfig(id), queryFn: () => api.runConfig(id), enabled });
}

export function useAgents() {
  return useQuery({ queryKey: qk.agents, queryFn: api.agents, staleTime: 60_000 });
}

export function useWorktrees() {
  return useQuery({ queryKey: qk.worktrees, queryFn: api.worktrees, refetchInterval: 15_000 });
}

export function useWorktree(branch: string) {
  return useQuery({
    queryKey: qk.worktree(branch),
    queryFn: () => api.worktree(branch),
    refetchInterval: 15_000,
  });
}

export function useWorktreeCommits(branch: string) {
  return useQuery({
    queryKey: qk.worktreeCommits(branch),
    queryFn: () => api.worktreeCommits(branch),
    refetchInterval: 30_000,
  });
}

/** Every run that worked a branch — the agent history behind one worktree. */
export function useBranchRuns(branch: string) {
  return useQuery({
    queryKey: qk.branchRuns(branch),
    queryFn: () => api.branchRuns(branch),
    refetchInterval: LIVE_MS,
  });
}

export function useUsage() {
  return useQuery({ queryKey: qk.usage, queryFn: api.usage, staleTime: 60_000 });
}

export function useDoctor(opts?: Partial<UseQueryOptions<Awaited<ReturnType<typeof api.doctor>>>>) {
  return useQuery({ queryKey: qk.doctor, queryFn: api.doctor, staleTime: 5 * 60_000, ...opts });
}

export function useAudit() {
  return useQuery({ queryKey: qk.audit, queryFn: api.audit, staleTime: 30_000 });
}

// ---------------------------------------------------------------------------
// Mutations
// ---------------------------------------------------------------------------

export function useStopRun() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api.stopRun(id),
    onSuccess: (_d, id) => {
      qc.invalidateQueries({ queryKey: qk.runs });
      qc.invalidateQueries({ queryKey: qk.run(id) });
    },
  });
}

export function useKillRun() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api.killRun(id),
    onSuccess: (_d, id) => {
      qc.invalidateQueries({ queryKey: qk.runs });
      qc.invalidateQueries({ queryKey: qk.run(id) });
    },
  });
}

export function useRemoveRun() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api.removeRun(id),
    onSuccess: (_d, id) => {
      qc.invalidateQueries({ queryKey: qk.runs });
      qc.invalidateQueries({ queryKey: qk.run(id) });
    },
  });
}

export function useLaunchRun() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (req: LaunchRequest) => api.launch(req),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: qk.runs });
      qc.invalidateQueries({ queryKey: qk.worktrees });
    },
  });
}

export function useRefreshUsage() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => api.refreshUsage(),
    onSuccess: (data) => qc.setQueryData(qk.usage, data),
  });
}

export function useLandWorktree() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ branch, onto }: { branch: string; onto?: string }) =>
      api.landWorktree(branch, onto),
    onSuccess: () => qc.invalidateQueries({ queryKey: qk.worktrees }),
  });
}

export function useRemoveWorktree() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (branch: string) => api.removeWorktree(branch),
    onSuccess: () => qc.invalidateQueries({ queryKey: qk.worktrees }),
  });
}

// ---------------------------------------------------------------------------
// Transport mode
// ---------------------------------------------------------------------------

/**
 * Whether the data on screen came from the daemon or from a fixture. The header
 * shows it because a control plane that cannot say which it is showing is worse
 * than one that shows nothing.
 */
export function useTransportMode(): { mode: TransportMode; retry: () => void } {
  const [m, setM] = useState<TransportMode>(() => transportMode());
  const qc = useQueryClient();
  useEffect(() => onTransportChange(setM), []);
  return {
    mode: m,
    retry: () => {
      reconnect();
      setM("unknown");
      qc.invalidateQueries();
    },
  };
}
