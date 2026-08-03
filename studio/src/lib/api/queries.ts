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
import type { LaunchRequest, UsageSnapshot } from "@/lib/types";

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
  agentSessions: (agent: string) => ["agents", agent, "sessions"] as const,
  conversation: (id: string) => ["runs", id, "conversation"] as const,
  runConfig: (id: string) => ["runs", id, "config"] as const,
  agents: ["agents"] as const,
  worktrees: ["worktrees"] as const,
  worktree: (b: string) => ["worktrees", b] as const,
  worktreeCommits: (b: string) => ["worktrees", b, "commits"] as const,
  branchRuns: (b: string) => ["runs", "branch", b] as const,
  commitDiff: (sha: string) => ["commits", sha, "diff"] as const,
  historyStats: (days: number) => ["stats", "history", days] as const,
  usage: ["usage"] as const,
  doctor: ["doctor"] as const,
  audit: (branch?: string, limit?: number) => ["audit", branch ?? "all", limit ?? 200] as const,
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

/**
 * One commit's diff, fetched only once its row is opened: a branch can carry a
 * hundred commits and none of them is worth a git invocation until someone asks
 * to see it.
 */
export function useCommitDiff(sha: string, enabled: boolean) {
  return useQuery({
    queryKey: qk.commitDiff(sha),
    queryFn: () => api.commitDiff(sha),
    enabled,
    staleTime: Infinity, // a commit is immutable; refetching one is pure waste
  });
}

/**
 * The daemon's own aggregate of the run log. `null` means it has no index, and
 * the caller computes the same numbers client-side instead.
 */
export function useHistoryStats(days = 14) {
  return useQuery({
    queryKey: qk.historyStats(days),
    queryFn: () => api.historyStats(days),
    staleTime: 30_000,
  });
}

export function useUsage() {
  return useQuery({ queryKey: qk.usage, queryFn: api.usage, staleTime: 60_000 });
}

export function useDoctor(opts?: Partial<UseQueryOptions<Awaited<ReturnType<typeof api.doctor>>>>) {
  return useQuery({ queryKey: qk.doctor, queryFn: api.doctor, staleTime: 5 * 60_000, ...opts });
}

/**
 * The run log. `limit` matters for the dashboard: its window is fourteen days,
 * and the default 200 is the newest 200 records — on a busy machine that is two
 * days, and the chart's older columns come back empty because nothing was
 * fetched for them, not because nothing ran.
 */
export function useAudit(branch?: string, limit?: number, opts?: { enabled?: boolean }) {
  return useQuery({
    queryKey: qk.audit(branch, limit),
    queryFn: () => api.audit(branch, limit),
    staleTime: 30_000,
    enabled: opts?.enabled ?? true,
  });
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

/**
 * Refresh the usage reading by making the agent fetch it.
 *
 * Refuses locally when the snapshot already said the agent is not on the
 * daemon's PATH, rather than sending a request that can only come back 501.
 * The server answers correctly either way — this is about not asking a question
 * whose answer is already in hand, and about the one deployment where it is
 * always no: under `docker compose --profile api` the daemon is a container
 * with no claude binary, so an older tab left open on that setup would
 * otherwise fill the API log with refusals.
 */
export function useRefreshUsage() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => {
      const known = qc.getQueryData<UsageSnapshot>(qk.usage);
      if (known && !known.canRefresh) {
        return Promise.reject(
          new Error(
            "This server cannot refresh usage: the agent that records these numbers is not on its PATH.",
          ),
        );
      }
      return api.refreshUsage();
    },
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

/**
 * A run's conversation, polled while the run is live.
 *
 * Polled rather than streamed, and that is the honest shape: the source is a
 * transcript file the agent appends to, so there is no event to subscribe to —
 * a watcher would be polling underneath anyway. Three seconds is fast enough to
 * notice a question and slow enough that a long session is not re-read
 * constantly.
 */
export function useConversation(id: string, live: boolean) {
  return useQuery({
    queryKey: qk.conversation(id),
    queryFn: () => api.conversation(id),
    refetchInterval: live ? 3000 : false,
  });
}

/**
 * Send a reply to a running agent.
 *
 * The conversation is invalidated on success but the answer does not appear at
 * once: the agent has to read it, think, and write its turn. The poll above is
 * what surfaces it, which is why this does not try to show anything optimistic —
 * a message that appeared instantly and then vanished on the next refetch would
 * be worse than one that took a moment.
 */
export function useSendConsoleInput(id: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ data, enter }: { data: string; enter?: boolean }) =>
      api.sendConsoleInput(id, data, enter ?? true),
    onSuccess: () => qc.invalidateQueries({ queryKey: qk.conversation(id) }),
  });
}

/** Conversations an agent can be resumed from. */
export function useAgentSessions(agent: string | null) {
  return useQuery({
    queryKey: qk.agentSessions(agent ?? ""),
    queryFn: () => api.agentSessions(agent!),
    enabled: !!agent,
  });
}
