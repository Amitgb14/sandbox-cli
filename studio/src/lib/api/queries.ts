"use client";

import {
  useMutation,
  useQuery,
  useQueryClient,
  type UseQueryOptions,
} from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { onTransportChange, reconnect, transportMode, type TransportMode } from "@/lib/api/client";
import { toast } from "sonner";
import { api } from "@/lib/api/endpoints";
import { formatRelative } from "@/lib/format";
import { useUi } from "@/lib/store";
import type { LaunchRequest, Project, UsageSnapshot } from "@/lib/types";

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
  agentSessions: (agent: string, scope?: string) =>
    ["agents", agent, "sessions", scope ?? "resumable"] as const,
  sessionTranscript: (agent: string, id: string) =>
    ["agents", agent, "sessions", id, "transcript"] as const,
  sessionRaw: (agent: string, id: string) =>
    ["agents", agent, "sessions", id, "raw"] as const,
  conversation: (id: string) => ["runs", id, "conversation"] as const,
  runConfig: (id: string) => ["runs", id, "config"] as const,
  agents: ["agents"] as const,
  projects: ["projects"] as const,
  browse: (path?: string) => ["browse", path ?? "home"] as const,
  // Repo-scoped keys carry the repo id. A key that did not would serve one
  // repository's worktrees under another's name the moment the picker moved —
  // the cache would hit, and the screen would be confidently wrong.
  worktrees: (repo?: string) => ["worktrees", repo ?? "default"] as const,
  worktree: (b: string, repo?: string) => ["worktrees", repo ?? "default", b] as const,
  worktreeCommits: (b: string, repo?: string) =>
    ["worktrees", repo ?? "default", b, "commits"] as const,
  branchRuns: (b: string) => ["runs", "branch", b] as const,
  commitDiff: (sha: string, repo?: string) => ["commits", repo ?? "default", sha, "diff"] as const,
  historyStats: (days: number) => ["stats", "history", days] as const,
  // The branch is part of the key: a worktree is its own directory, so the same
  // path in two branches is two different files, and a key that ignored the
  // branch would serve one under the other's name.
  files: (path: string, repo?: string, branch?: string) =>
    ["files", repo ?? "default", branch ?? "checkout", path] as const,
  fileContent: (path: string, repo?: string, branch?: string) =>
    ["files", repo ?? "default", branch ?? "checkout", path, "content"] as const,
  worktreeDiff: (branch: string, repo?: string) =>
    ["worktrees", repo ?? "default", branch, "diff"] as const,
  usage: ["usage"] as const,
  doctor: ["doctor"] as const,
  audit: (branch?: string, limit?: number, repo?: string) =>
    ["audit", repo ?? "all-repos", branch ?? "all", limit ?? 200] as const,
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

/**
 * The repositories this daemon answers about.
 *
 * Rarely changes and is read by nearly every screen, so it is cached long and
 * invalidated by the mutations below rather than polled.
 */
export function useProjects() {
  return useQuery({ queryKey: qk.projects, queryFn: api.projects, staleTime: 60_000 });
}

/**
 * Directories on the host, for the Add-repository folder picker. Enabled only
 * while the picker is open — this is the one query that reads outside a
 * repository, and it should not be running because a dialog exists.
 */
export function useBrowse(path: string | undefined, enabled: boolean) {
  return useQuery({
    queryKey: qk.browse(path),
    queryFn: () => api.browse(path),
    enabled,
    staleTime: 10_000,
  });
}

/**
 * The repository every repo-scoped query is about: whatever the sidebar picker
 * has scoped the app to, or nothing, which the daemon reads as the repository it
 * was started in.
 *
 * Read from the store here rather than threaded through a dozen call sites, and
 * that is a deliberate trade. "Scope every screen to one repository" is what the
 * picker promises, so the alternative is every screen remembering to pass it —
 * and the one that forgets does not fail, it shows another repository's
 * worktrees under this repository's name.
 */
function useScopedRepo(): string | undefined {
  return useUi((s) => s.repoFilter) ?? undefined;
}

/**
 * A repository's worktrees — the scoped one, or `override` for the one caller
 * that legitimately asks about a different repository than the app is scoped to:
 * the Launch form, where picking the repository to work in is the question being
 * answered rather than a filter already applied.
 */
export function useWorktrees(override?: string) {
  const scoped = useScopedRepo();
  const repo = override || scoped;
  return useQuery({
    queryKey: qk.worktrees(repo),
    queryFn: () => api.worktrees(repo),
    refetchInterval: 15_000,
  });
}

export function useWorktree(branch: string) {
  const repo = useScopedRepo();
  return useQuery({
    queryKey: qk.worktree(branch, repo),
    queryFn: () => api.worktree(branch, repo),
    refetchInterval: 15_000,
  });
}

export function useWorktreeCommits(branch: string) {
  const repo = useScopedRepo();
  return useQuery({
    queryKey: qk.worktreeCommits(branch, repo),
    queryFn: () => api.worktreeCommits(branch, repo),
    refetchInterval: 30_000,
  });
}

/**
 * One directory of the scoped repository.
 *
 * Polled slowly rather than not at all: an agent is writing into this tree while
 * you look at it, and a browser that showed the state at page load would be
 * describing a directory that has since changed. Slowly, because a file listing
 * is not a live view and a git checkout mid-run would make it flicker.
 */
export function useFiles(path: string, branch?: string) {
  const repo = useScopedRepo();
  return useQuery({
    queryKey: qk.files(path, repo, branch),
    queryFn: () => api.files(path, repo, branch),
    refetchInterval: 30_000,
  });
}

/** One file's content, fetched only once something asks to see it. */
export function useFileContent(path: string | null, branch?: string) {
  const repo = useScopedRepo();
  return useQuery({
    queryKey: qk.fileContent(path ?? "", repo, branch),
    queryFn: () => api.fileContent(path as string, repo, branch),
    enabled: !!path,
    // Re-read on demand rather than on a timer: this is a file somebody opened,
    // and a viewer that swapped the text under a reader's eyes every few seconds
    // would be worse than one that is a minute out of date.
    staleTime: 15_000,
  });
}

/**
 * One branch's changes. Polled while you look at it, because an agent may be
 * writing into that worktree as you read — slowly, since a diff is a review
 * surface rather than a live view.
 */
export function useWorktreeDiff(branch: string | null) {
  const repo = useScopedRepo();
  return useQuery({
    queryKey: qk.worktreeDiff(branch ?? "", repo),
    queryFn: () => api.worktreeDiff(branch as string, repo),
    enabled: !!branch,
    refetchInterval: 20_000,
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
  const repo = useScopedRepo();
  return useQuery({
    queryKey: qk.commitDiff(sha, repo),
    queryFn: () => api.commitDiff(sha, repo),
    enabled,
    staleTime: Infinity, // a commit is immutable; refetching one is pure waste
  });
}

/**
 * The daemon's own aggregate of the run log. `null` means it has no index, and
 * the caller computes the same numbers client-side instead.
 */
export function useHistoryStats(days = 14, opts?: { enabled?: boolean }) {
  return useQuery({
    queryKey: qk.historyStats(days),
    queryFn: () => api.historyStats(days),
    staleTime: 30_000,
    // Disabled while a repository is scoped: this aggregate is machine-wide and
    // has no repo dimension to narrow, so its numbers would describe other
    // repositories' runs under this one's name.
    enabled: opts?.enabled ?? true,
  });
}

export function useUsage(enabled = true) {
  return useQuery({ queryKey: qk.usage, queryFn: api.usage, staleTime: 60_000, enabled });
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
/**
 * The run log, scoped to the picked repository like everything else.
 *
 * The daemon derives each record's repository from the workspace it recorded —
 * the log has no repo field, and stamping one now would leave every existing
 * line unfilterable, which on a machine with months of history reads as "this
 * repository has no runs".
 */
export function useAudit(branch?: string, limit?: number, opts?: { enabled?: boolean }) {
  const repo = useScopedRepo();
  return useQuery({
    queryKey: qk.audit(branch, limit, repo),
    queryFn: () => api.audit(branch, limit, repo),
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
      // Every repository's listing: a launch can create a worktree in a
      // repository other than the one currently scoped, since the Launch form
      // lets you pick which.
      qc.invalidateQueries({ queryKey: ["worktrees"] });
    },
  });
}

/**
 * Refresh the usage reading by making the agent fetch it.
 *
 * No client-side guard on `canRefresh`. There was one, and its justification
 * did not survive being read back: it claimed to spare the API log from a tab
 * left open on a deployment that cannot refresh — but such a tab is running
 * *older JavaScript*, so it has neither the guard nor the hidden button that
 * ships alongside it. The scenario it named was the one case it could not help.
 *
 * What actually prevents the pointless request is the control being absent:
 * UsageGauge renders the button only when the snapshot says canRefresh. A
 * second check behind a hidden control is unreachable code whose rejection
 * nothing surfaces, and the server answers 501 correctly for any caller that
 * finds another way in.
 */
export function useRefreshUsage() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () => api.refreshUsage(),
    onSuccess: (data) => {
      const before = qc.getQueryData<UsageSnapshot>(qk.usage);
      qc.setQueryData(qk.usage, data);

      // Say which of the two things happened, because they look identical.
      //
      // A refresh drives the agent and then re-reads; the agent decides whether
      // to refetch, and where it writes is not necessarily where these numbers
      // are read from — a host Claude Code with no usage cache of its own
      // leaves the daemon serving the sandbox agent's copy, unchanged. So a
      // *successful* refresh routinely moves nothing, and with no feedback the
      // button is indistinguishable from a broken one. It was reported as
      // broken three times before this line existed.
      if (data.fetchedAt && data.fetchedAt !== before?.fetchedAt) {
        toast.success(`Usage refreshed — the reading is now ${formatRelative(data.fetchedAt)}`);
        return;
      }
      toast.info("The agent reported no newer reading", {
        description: data.path
          ? `Still ${formatRelative(data.fetchedAt)}, from ${data.path}. These figures only move when the agent that owns that file talks to the server.`
          : "These figures only move when the agent that owns them talks to the server.",
      });
    },
    // The daemon's own words: 501 explains that this deployment has no agent to
    // drive, 502 that the agent ran and failed. Both are worth reading, and
    // neither reaches the user through a mutation that swallows its error.
    onError: (err) =>
      toast.error("Could not refresh usage", {
        description: err instanceof Error ? err.message : String(err),
      }),
  });
}

export function useLandWorktree() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ branch, onto }: { branch: string; onto?: string }) =>
      api.landWorktree(branch, onto),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["worktrees"] }),
  });
}

export function useRemoveWorktree() {
  const qc = useQueryClient();
  const repo = useScopedRepo();
  return useMutation({
    mutationFn: (branch: string) => api.removeWorktree(branch, repo),
    // Every repository's listing, not just this one's: the key is scoped, and a
    // removal invalidating only the scoped key leaves a stale row behind for
    // anyone who switches back.
    onSuccess: () => qc.invalidateQueries({ queryKey: ["worktrees"] }),
  });
}

/**
 * Add a repository by host path. The daemon validates it — this only reports
 * what it said, because "is that a repository, and may Studio touch it" is a
 * question about the host rather than about the form.
 */
export function useAddProject() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (path: string) => api.addProject(path),
    onSuccess: (project) => {
      const known = qc.getQueryData<Project[]>(qk.projects) ?? [];
      const already = known.some((p) => p.id === project.id);

      // Written into the cache *and* invalidated. The invalidation is the
      // authority — the daemon may have recorded a different root than the path
      // that was typed — but it is a round trip, and the row appearing only
      // after it lands is what "it said added and the list did not change" looks
      // like on a slow answer.
      //
      // An id already present is left exactly as it is rather than overwritten:
      // the listing's copy carries `default`, and the POST's does not, so
      // replacing it would drop the "started here" marker off the one repository
      // that cannot be removed until the refetch put it back.
      if (!already) {
        qc.setQueryData<Project[]>(qk.projects, (prev) =>
          prev ? [...prev, project] : [project],
        );
      }
      qc.invalidateQueries({ queryKey: qk.projects });

      // Adding a repository Studio already manages is a no-op, and saying
      // "Added" for it is how you end up hunting a list for a second row that
      // was never going to appear — a repository is one row, addressed by id,
      // and adding it twice cannot make two.
      if (already) {
        toast.info(`${project.name} is already managed`, { description: project.root });
        return;
      }
      toast.success(`Added ${project.name}`, { description: project.root });
    },
    onError: (err) =>
      toast.error("Could not add that repository", {
        description: err instanceof Error ? err.message : String(err),
      }),
  });
}

/** Forget a repository. The checkout on disk is untouched — this is a list. */
export function useRemoveProject() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => api.removeProject(id),
    onSuccess: (_void, id) => {
      qc.setQueryData<Project[]>(qk.projects, (prev) =>
        prev?.filter((p) => p.id !== id),
      );
      qc.invalidateQueries({ queryKey: qk.projects });
      qc.invalidateQueries({ queryKey: ["worktrees"] });
    },
    onError: (err) =>
      toast.error("Could not remove that repository", {
        description: err instanceof Error ? err.message : String(err),
      }),
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
  // Every screen is holding fixtures at this point, and they do not stop being
  // fixtures because the transport changed its mind — the cached answers are
  // still the fabricated ones. Invalidating on the transition is what turns the
  // background recheck into screens that actually update, rather than a badge
  // that quietly changes while the table underneath still shows six agents that
  // have been running since January.
  useEffect(
    () =>
      onTransportChange((next) => {
        setM(next);
        if (next === "live") qc.invalidateQueries();
      }),
    [qc],
  );
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
export function useAgentSessions(
  agent: string | null,
  opts?: { scope?: "all"; limit?: number },
) {
  return useQuery({
    queryKey: qk.agentSessions(agent ?? "", opts?.scope),
    queryFn: () => api.agentSessions(agent!, opts),
    enabled: !!agent,
  });
}

/** One conversation, parsed. Fetched only when something opens it. */
export function useSessionTranscript(agent: string | null, id: string | null) {
  return useQuery({
    queryKey: qk.sessionTranscript(agent ?? "", id ?? ""),
    queryFn: () => api.sessionTranscript(agent!, id!),
    enabled: !!agent && !!id,
    // A finished conversation does not change. A live one is being appended to,
    // but this is a reader rather than a follower — the console view is what
    // polls, and two pollers on one file is one too many.
    staleTime: 30_000,
  });
}

/** The same conversation, unparsed. Fetched only when the raw view is opened. */
export function useSessionRaw(agent: string | null, id: string | null, enabled: boolean) {
  return useQuery({
    queryKey: qk.sessionRaw(agent ?? "", id ?? ""),
    queryFn: () => api.sessionRaw(agent!, id!),
    enabled: enabled && !!agent && !!id,
    staleTime: 30_000,
  });
}
