import { ApiError, request } from "@/lib/api/client";
import {
  MOCK_AGENTS,
  MOCK_AUDIT,
  MOCK_DAEMON,
  MOCK_DOCTOR,
  MOCK_CONVERSATION,
  MOCK_PROJECTS,
  MOCK_RUNS,
  MOCK_USAGE,
  MOCK_WORKTREES,
  buildArgv,
  mockConfig,
  mockBrowse,
  mockDiff,
  mockFileContent,
  mockFiles,
  mockLogs,
  mockMetrics,
  MOCK_SNAPSHOTS,
  MOCK_SNAPSHOT_SETTINGS,
} from "@/lib/mock/data";
import { BASELINE_EGRESS, RESERVED_ENV } from "@/lib/constants";
import type {
  Agent,
  Commit,
  HistoryStats,
  AuditRecord,
  DaemonInfo,
  DiffFile,
  DoctorCheck,
  LaunchPreview,
  LaunchRequest,
  LogLine,
  BrowseListing,
  FileContent,
  FileListing,
  MetricSeries,
  Project,
  ProviderStatus,
  ResolvedConfig,
  Run,
  UsageSnapshot,
  Worktree,
  Conversation,
  SessionRaw,
  SessionSummary,
  SessionTranscript,
  ProbeHistory,
  DaemonEgress,
  NetworkMode,
  Snapshot,
  SnapshotSettings,
  SnapshotS3Check,
  RestoreMode,
  RestoreResult,
} from "@/lib/types";

/**
 * The daemon's surface, one function per endpoint.
 *
 * Paths are versioned (`/v1/...`) because Studio ships separately from the CLI
 * and the two will not always be the same age.
 */

/**
 * `?repo=<id>` for the endpoints scoped to one repository, and nothing at all
 * when no repository was chosen.
 *
 * Nothing, rather than `?repo=`, on purpose: an empty parameter and an absent
 * one mean the same thing to the daemon ("the repository I was started in"), and
 * sending the empty form would make every cache key and every server log carry a
 * question that was never asked.
 */
function repoQuery(repo?: string): string {
  return repo ? `?repo=${encodeURIComponent(repo)}` : "";
}

export const api = {
  daemon: () =>
    request<DaemonInfo>("/v1/health", {
      fixture: () => MOCK_DAEMON,
      latencyMs: 120,
    }),

  /**
   * Which agent providers are answering right now.
   *
   * The question a routing chain is configured against, and until this existed
   * the only way to ask it was to launch a run and see.
   */
  routing: (refresh = false) =>
    request<ProviderStatus[]>(`/v1/routing${refresh ? "?refresh=1" : ""}`, {
      fixture: () => [],
      latencyMs: 200,
      unwrap: (b) => (b as { providers: ProviderStatus[] }).providers ?? [],
    }),

  /**
   * Whether each provider has been answering, over time.
   *
   * The one thing on the Routing screen that is collected rather than derived:
   * nothing records a provider's health at a moment nobody asked, so the daemon
   * samples on a timer. Its `interval` comes back with the data because a gap
   * means different things with a prober running and without one.
   */
  probeHistory: (hours = 24) =>
    request<ProbeHistory>(`/v1/routing/history?hours=${hours}`, {
      fixture: () => ({ hours, interval: 0, providers: [] }),
      latencyMs: 200,
    }),

  /**
   * Set which host is probed for an agent.
   *
   * Narrow on purpose: it writes one map into a file of its own, never the
   * user's config.yaml — a value typed there by hand outranks this and keeps its
   * comments. The daemon validates each value as a host and answers with a fresh
   * probe, because the point of setting one is to find out whether it answers.
   */
  setProviders: (providers: Record<string, string>) =>
    request<ProviderStatus[]>("/v1/routing/providers", {
      method: "POST",
      body: { providers },
      liveOnly: true,
      unwrap: (b) => (b as { providers: ProviderStatus[] }).providers ?? [],
    }),

  /**
   * The repositories this daemon will answer about: the one it was started in,
   * plus every one added since.
   *
   * This is the only source of a repository list. A screen that hardcodes one is
   * a screen offering repositories the daemon has never heard of — which is what
   * this endpoint was added to stop.
   */
  projects: () =>
    request<Project[]>("/v1/projects", {
      fixture: () => MOCK_PROJECTS,
      latencyMs: 140,
      unwrap: (b) => (b as { projects: Project[] }).projects,
    }),

  /**
   * Directories on the host, for the folder picker.
   *
   * It runs in the daemon because a browser cannot answer it: a directory input
   * yields relative paths and `showDirectoryPicker()` yields a handle with no
   * path, and the daemon needs the absolute one. Directories only, names only,
   * dot-directories never — see internal/studioapi/browse.go.
   */
  browse: (path?: string) =>
    request<BrowseListing>(`/v1/browse${path ? `?path=${encodeURIComponent(path)}` : ""}`, {
      fixture: () => mockBrowse(path),
      latencyMs: 120,
    }),

  /**
   * Add a repository by host path — the one request in this client that carries
   * one, matching the one endpoint that accepts one. The daemon decides whether
   * it is acceptable (absolute, on disk, a git repository, not your home
   * directory) and answers with the repository *root* it recorded, which is why
   * the caller must use what comes back rather than what it sent.
   */
  addProject: (path: string) =>
    request<Project>("/v1/projects", {
      method: "POST",
      body: { path },
      // No fixture, deliberately. Only the daemon can say whether a path is a
      // repository it may touch, and only the daemon can remember it — a
      // fabricated answer here reported "Added" and then vanished from the very
      // list the dialog was opened from, because the list is fixtures too.
      liveOnly: true,
    }),

  /**
   * Clone a repository and register it.
   *
   * The one call in this client that makes the daemon write to the host and run
   * a program. Every refusal lives there — the transport allowlist (`ext::`
   * executes a command rather than fetching), the target's own checks, and no
   * stored credential being spent — so this only carries the answer back.
   */
  cloneProject: (url: string, parent: string, name?: string) =>
    request<Project>("/v1/projects/clone", {
      method: "POST",
      body: { url, parent, ...(name ? { name } : {}) },
      liveOnly: true,
    }),

  /** Forget a repository. Nothing on disk is touched. */
  removeProject: (id: string) =>
    request<void>(`/v1/projects/${encodeURIComponent(id)}`, {
      method: "DELETE",
      liveOnly: true, // same reason as addProject: the list is read back
    }),

  /**
   * One directory of a repository, from the daemon.
   *
   * `path` is repository-relative and comes from a previous listing — never
   * assembled here. The daemon resolves it and refuses anything that lands
   * outside the repository, symlinks included, which is the whole reason a
   * client does not get to name a host path.
   */
  files: (path: string, repo?: string, branch?: string) =>
    request<FileListing>(
      `/v1/files?${new URLSearchParams({
        ...(repo ? { repo } : {}),
        // A branch is a different directory on disk — its own worktree — not a
        // ref rendered into a tree. Absent means the repository's own checkout.
        ...(branch ? { branch } : {}),
        ...(path ? { path } : {}),
      })}`,
      {
        fixture: () => mockFiles(path),
        latencyMs: 160,
      },
    ),

  fileContent: (path: string, repo?: string, branch?: string) =>
    request<FileContent>(
      `/v1/files/content?${new URLSearchParams({
        ...(repo ? { repo } : {}),
        ...(branch ? { branch } : {}),
        path,
      })}`,
      {
        fixture: () => mockFileContent(path),
        latencyMs: 200,
      },
    ),

  /**
   * What one branch has that its base does not, plus whatever is uncommitted in
   * its worktree — the same shape a run's diff answers with, because it is the
   * same question asked of the branch rather than of a container. Useful after
   * the container is gone, which is when reviewing usually happens.
   */
  worktreeDiff: (branch: string, repo?: string) =>
    request<DiffFile[]>(
      `/v1/worktrees/${encodeURIComponent(branch)}/diff${repoQuery(repo)}`,
      { fixture: () => mockDiff(branch), latencyMs: 260 },
    ),

  /**
   * Every run, finished ones included — `?all=1`.
   *
   * The daemon defaults to live-only, matching `sandbox-cli list`, which is the
   * right default for a terminal: you ask what is running now. It is the wrong
   * one for this UI. The Runs screen exists to say how runs *ended*, the
   * dashboard buckets fourteen days of them, and both were empty on any machine
   * where nothing happened to be running at that moment — which is most of them,
   * most of the time. Callers that want only the live ones filter, and the
   * dashboard already does.
   */
  runs: () =>
    request<Run[]>("/v1/runs?all=1", {
      fixture: () => MOCK_RUNS,
      latencyMs: 260,
      unwrap: (b) => (b as { runs: Run[] }).runs,
    }),

  run: (id: string) =>
    request<Run>(`/v1/runs/${id}`, {
      fixture: () => {
        const run = MOCK_RUNS.find((r) => r.id === id);
        if (!run) throw new Error(`no run ${id}`);
        return run;
      },
      latencyMs: 160,
    }),

  runMetrics: (id: string) =>
    request<MetricSeries>(`/v1/runs/${id}/metrics`, {
      fixture: () => mockMetrics(id),
      latencyMs: 220,
    }),

  runLogs: (id: string) =>
    request<LogLine[]>(`/v1/runs/${id}/logs`, {
      fixture: () => mockLogs(id),
      latencyMs: 200,
    }),

  runDiff: (id: string) =>
    request<DiffFile[]>(`/v1/runs/${id}/diff`, {
      fixture: () => mockDiff(id),
      latencyMs: 280,
    }),

  runConfig: (id: string) =>
    request<ResolvedConfig>(`/v1/runs/${id}/config`, {
      fixture: () => mockConfig(id),
      latencyMs: 180,
    }),

  /**
   * Stop asks the guest to exit; kill does not wait. The difference is whether
   * the agent closed the file it was editing, which is why the UI makes you pick
   * one by name rather than offering a single "end run".
   */
  stopRun: (id: string) =>
    request<void>(`/v1/runs/${id}/stop`, {
      method: "POST",
      fixture: () => undefined,
    }),

  // Kill is stop with force, not a separate endpoint: the daemon exposes one
  // route because the difference is a flag on the same act, and a second path to
  // "end this run" is a second place for the two to disagree about what they
  // reach.
  killRun: (id: string) =>
    request<void>(`/v1/runs/${id}/stop`, {
      method: "POST",
      body: { force: true },
      fixture: () => undefined,
    }),

  /**
   * Reap a finished run's container. The work is untouched — that lives in the
   * workspace, which outlives every container that wrote to it; what goes is the
   * container's logs and exit code, which for a detached run are the whole
   * record that it happened.
   */
  removeRun: (id: string) =>
    request<void>(`/v1/runs/${id}`, {
      method: "DELETE",
      fixture: () => undefined,
    }),

  /**
   * What a run has said, and whether it can be answered.
   *
   * Read from the agent's transcript rather than its terminal output: a console
   * run draws a full-screen TUI, and text scraped out of a repaint looks like an
   * answer without being one.
   */
  conversation: (id: string) =>
    request<Conversation>(`/v1/runs/${id}/conversation`, {
      fixture: () => ({ messages: MOCK_CONVERSATION, writable: true }),
    }),

  /**
   * Send keystrokes to a running agent's stdin.
   *
   * `enter` appends the carriage return that submits — \r rather than \n,
   * because the container's stdin is a pty in raw mode where a line feed is not
   * a submit and the text would simply sit in the agent's input box.
   */
  sendConsoleInput: (id: string, data: string, enter = true) =>
    request<void>(`/v1/runs/${id}/console/input`, {
      method: "POST",
      body: { data, enter },
      fixture: () => undefined,
    }),

  /**
   * Tell a container how big the attached terminal is.
   *
   * Looks cosmetic and is not: a full-screen agent renders nothing until it
   * knows the size, so without this an attached console is a blank rectangle
   * over a perfectly healthy run. `docker attach` sends one from the client
   * terminal's dimensions, which is why attaching from a real terminal always
   * worked and the first version of this did not.
   */
  resizeConsole: (id: string, rows: number, cols: number) =>
    request<void>(`/v1/runs/${id}/console/resize`, {
      method: "POST",
      body: { rows, cols },
      fixture: () => undefined,
    }),

  /**
   * Conversations for an agent, newest first.
   *
   * The default is the resume picker's question — only the sandbox-owned store,
   * because those are the ones a container can reopen. `all` is the reading
   * question, which includes your own ~/.claude history; every row says which
   * store it came from and whether it can be resumed.
   */
  agentSessions: (agent: string, opts?: { scope?: "all"; limit?: number }) =>
    request<SessionSummary[]>(
      `/v1/agents/${agent}/sessions?${new URLSearchParams({
        ...(opts?.scope ? { scope: opts.scope } : {}),
        ...(opts?.limit ? { limit: String(opts.limit) } : {}),
      })}`,
      {
        fixture: () => [],
        unwrap: (b) => (b as { sessions: SessionSummary[] }).sessions ?? [],
      },
    ),

  /** One conversation, parsed into turns. Named by id; the daemon finds the file. */
  sessionTranscript: (agent: string, id: string) =>
    request<SessionTranscript>(`/v1/agents/${agent}/sessions/${encodeURIComponent(id)}`, {
      fixture: () => ({
        session: { id, turns: 0, modified: new Date(0).toISOString() },
        messages: MOCK_CONVERSATION,
      }),
      latencyMs: 240,
    }),

  /**
   * The transcript file as it is on disk — the answer to "is the parsed view
   * telling me everything", which for a format with a dozen line kinds is a
   * question worth being able to ask.
   */
  sessionRaw: (agent: string, id: string) =>
    request<SessionRaw>(`/v1/agents/${agent}/sessions/${encodeURIComponent(id)}/raw`, {
      fixture: () => ({
        session: { id, turns: 0, modified: new Date(0).toISOString() },
        size: 0,
        content: '{"type":"user","message":{"content":"fixture line"}}\n',
      }),
      latencyMs: 260,
    }),

  launch: (req: LaunchRequest) =>
    request<{ id: string }>("/v1/runs", {
      method: "POST",
      body: toRunCreate(req),
      fixture: () => ({ id: MOCK_RUNS[0].id }),
      latencyMs: 700,
    }),

  /**
   * The dry-run preview. On the fixture path it is computed locally so the form
   * stays responsive while typing; the daemon's answer is authoritative because
   * only it runs the real `BuildSpec`.
   */
  preview: (req: LaunchRequest) =>
    request<LaunchPreview>("/v1/runs/preview", {
      method: "POST",
      body: req,
      fixture: () => localPreview(req),
      latencyMs: 90,
    }),

  agents: () =>
    request<Agent[]>("/v1/agents", {
      fixture: () => MOCK_AGENTS,
      latencyMs: 200,
      unwrap: (b) => (b as { agents: Agent[] }).agents,
    }),

  /**
   * A repository's worktrees. `repo` is a repo id from `projects()` — absent
   * means the repository the daemon was started in, which is what this asked
   * before repositories were plural.
   */
  /**
   * The snapshots recorded for a repository, newest first.
   *
   * Baselines are filtered out by the daemon: one is recorded before every
   * launch and holds the workspace as it was *before* the agent touched it, so
   * offering to restore one would hand back a run's starting point looking like
   * success.
   */
  snapshots: (repo?: string, branch?: string) => {
    // `repo=all` when nothing is scoped, the same spelling worktrees uses: the
    // daemon's *absent* parameter means the one repository it was started in,
    // so "All repositories" cannot be said by leaving it out.
    const params = new URLSearchParams({ repo: repo ?? "all" });
    if (branch) params.set("branch", branch);
    const q = params.toString();
    return request<Snapshot[]>(`/v1/snapshots${q ? `?${q}` : ""}`, {
      fixture: () =>
        MOCK_SNAPSHOTS.filter(
          (s) => (!repo || s.repoId === repo) && (!branch || s.branch === branch),
        ),
      latencyMs: 200,
      unwrap: (b) => (b as { snapshots: Snapshot[] }).snapshots ?? [],
    });
  },

  /**
   * Checkpoint a workspace now.
   *
   * liveOnly, like every write whose point is to change what a later read
   * returns: a fixture here would report a snapshot that the listing — served by
   * the same fixtures — cannot then show, which reads as a broken feature rather
   * than as a missing daemon.
   */
  createSnapshot: (body: { repo?: string; branch?: string; label?: string; retention?: string }) =>
    request<Snapshot>("/v1/snapshots", { method: "POST", body, liveOnly: true }),

  /**
   * Put a snapshot back.
   *
   * The daemon refuses this for a snapshot taken through the SDK — a script
   * mid-way through something is not a thing to undo from a browser tab — so the
   * button is disabled on those rather than left to fail.
   */
  restoreSnapshot: (id: string, body: { mode?: RestoreMode; branch?: string; repo?: string }) =>
    request<RestoreResult>(`/v1/snapshots/${encodeURIComponent(id)}/restore`, {
      method: "POST",
      body,
      liveOnly: true,
    }),

  /** How long one snapshot is kept; "" returns it to the default. */
  setSnapshotRetention: (id: string, retention: string, repo?: string) =>
    request<Snapshot>(`/v1/snapshots/${encodeURIComponent(id)}/retention`, {
      method: "POST",
      body: { retention, repo },
      liveOnly: true,
    }),

  /**
   * Mirror one snapshot to object storage now.
   *
   * For the two cases the automatic path leaves behind: an upload that failed
   * while the network was down, and a snapshot taken before a bucket was
   * configured. There is deliberately no "unmirror" — deleting a backup is not
   * something a button should do.
   */
  uploadSnapshot: (id: string, repo?: string) =>
    request<Snapshot>(`/v1/snapshots/${encodeURIComponent(id)}/upload`, {
      method: "POST",
      body: { repo },
      liveOnly: true,
    }),

  /**
   * Ask the bucket whether a snapshot's object is really there.
   *
   * `snapshot.remote` records what the upload did; a lifecycle rule or somebody
   * tidying a bucket leaves a snapshot reading as mirrored when it is not. Per
   * row and on demand, never for a whole listing.
   */
  verifySnapshot: (id: string, repo?: string) =>
    request<SnapshotS3Check>(`/v1/snapshots/${encodeURIComponent(id)}/verify`, {
      method: "POST",
      body: { repo },
      liveOnly: true,
    }),

  /**
   * Does the configured bucket answer, and does the named credential resolve?
   *
   * Sends no body: the daemon checks what *it* is configured with. A check that
   * dialled a host from the request would be a server-side request forgery with
   * a Test button in front of it.
   *
   * liveOnly, because a fixture answering "connected" is the one lie this
   * particular button must never tell.
   */
  checkSnapshotStorage: () =>
    request<SnapshotS3Check>("/v1/snapshots/s3/check", {
      method: "POST",
      body: {},
      liveOnly: true,
    }),

  snapshotSettings: () =>
    request<SnapshotSettings>("/v1/snapshots/settings", {
      fixture: () => MOCK_SNAPSHOT_SETTINGS,
      latencyMs: 120,
    }),

  setSnapshotSettings: (body: SnapshotSettings) =>
    request<SnapshotSettings>("/v1/snapshots/settings", {
      method: "POST",
      body,
      liveOnly: true,
    }),

  worktrees: (repo?: string) =>
    // `repo=all` when nothing is scoped, because that is what the picker's "All
    // repositories" means — and the daemon's *absent* parameter means something
    // else, the one repository it was started in. Runs never needed this: docker
    // lists containers across every repository already, so a dashboard showing
    // all repositories' runs beside one repository's worktrees was comparing two
    // different questions and looked like missing worktrees.
    request<Worktree[]>(`/v1/worktrees?repo=${encodeURIComponent(repo ?? "all")}`, {
      fixture: () => (repo ? MOCK_WORKTREES.filter((w) => w.repoId === repo) : MOCK_WORKTREES),
      latencyMs: 240,
      unwrap: (b) => (b as { worktrees: Worktree[] }).worktrees,
    }),

  worktree: (branch: string, repo?: string) =>
    request<Worktree>(`/v1/worktrees/${encodeURIComponent(branch)}${repoQuery(repo)}`, {
      fixture: () => {
        const w = MOCK_WORKTREES.find((x) => x.branch === branch);
        if (!w) throw new Error(`no worktree ${branch}`);
        return w;
      },
      latencyMs: 140,
    }),

  worktreeCommits: (branch: string, repo?: string) =>
    request<Commit[]>(
      `/v1/worktrees/${encodeURIComponent(branch)}/commits${repoQuery(repo)}`,
      {
        fixture: () => [],
        latencyMs: 200,
        unwrap: (b) => (b as { commits: Commit[] }).commits,
      },
    ),

  /** Every run that worked this branch, finished ones included. */
  branchRuns: (branch: string) =>
    request<Run[]>(`/v1/runs?all=1&branch=${encodeURIComponent(branch)}`, {
      fixture: () => MOCK_RUNS.filter((r) => r.branch === branch),
      latencyMs: 200,
      unwrap: (b) => (b as { runs: Run[] }).runs,
    }),

  /**
   * The run log aggregated by the daemon, when it has an index to aggregate in.
   *
   * Returns null on 501, which is the daemon saying "no index configured" — not
   * a failure. The caller then computes the same numbers from /v1/audit as it
   * always did. That fallback is the point: the index is a faster path to the
   * same answer, never a requirement.
   */
  historyStats: async (days = 14): Promise<HistoryStats | null> => {
    try {
      return await request<HistoryStats>(`/v1/stats/history?days=${days}`, {
        // In fixture mode there is no daemon to aggregate anything; the caller
        // falls back to computing from the fixture records.
        fixture: () => null as unknown as HistoryStats,
        latencyMs: 120,
      });
    } catch (e) {
      if (e instanceof ApiError && e.status === 501) return null;
      throw e;
    }
  },

  /** What one commit changed. Scoped to this daemon's project. */
  commitDiff: (sha: string, repo?: string) =>
    request<DiffFile[]>(`/v1/commits/${encodeURIComponent(sha)}/diff${repoQuery(repo)}`, {
      fixture: () => [],
      latencyMs: 200,
    }),

  removeWorktree: (branch: string, repo?: string) =>
    request<void>(`/v1/worktrees/${encodeURIComponent(branch)}${repoQuery(repo)}`, {
      method: "DELETE",
      fixture: () => undefined,
    }),

  landWorktree: (branch: string, onto?: string) =>
    request<{ merged: boolean; message: string }>(
      `/v1/worktrees/${encodeURIComponent(branch)}/land`,
      {
        method: "POST",
        body: { onto },
        fixture: () => ({
          merged: true,
          message: `Merged ${branch} into ${onto ?? "its recorded base"}.`,
        }),
        latencyMs: 600,
      },
    ),

  usage: () =>
    request<UsageSnapshot>("/v1/usage", {
      fixture: () => MOCK_USAGE,
      latencyMs: 200,
    }),

  refreshUsage: () =>
    request<UsageSnapshot>("/v1/usage/refresh", {
      method: "POST",
      fixture: () => MOCK_USAGE,
      latencyMs: 1400,
    }),

  doctor: () =>
    request<DoctorCheck[]>("/v1/doctor", {
      fixture: () => MOCK_DOCTOR,
      latencyMs: 900,
      // `{profile, checks}` on the wire: the profile the checks were run against
      // is part of the answer, since the same host passes dev and fails prod.
      unwrap: (b) => (b as { checks: DoctorCheck[] }).checks,
    }),

  /**
   * The run log. Durable in a way the runs list is not: a container carries its
   * own history until it is reaped, and then that history is gone — docker is
   * the state store. This survives, which makes it the only answer to "what has
   * run here" once the containers are cleaned up.
   */
  audit: (branch?: string, limit?: number, repo?: string) =>
    request<AuditRecord[]>(
      `/v1/audit?limit=${limit ?? 200}` +
        (branch ? `&branch=${encodeURIComponent(branch)}` : "") +
        (repo ? `&repo=${encodeURIComponent(repo)}` : ""),
      {
        fixture: () =>
          branch ? MOCK_AUDIT.filter((a) => a.branch === branch) : MOCK_AUDIT,
        latencyMs: 300,
        unwrap: (b) => (b as { records: AuditRecord[] }).records,
      },
    ),
};

/**
 * A local model of the refusals `sandbox.BuildSpec` and `config.ValidateProfile`
 * would raise, so the Launch form can explain itself before it submits.
 *
 * It is deliberately a *subset*: these are the refusals whose reasons are
 * documented and stable. Anything else the daemon decides comes back from the
 * real preview — a form that guessed at the full rule set would eventually
 * disagree with the thing that actually enforces it.
 */
export function localPreview(req: LaunchRequest, egress?: DaemonEgress): LaunchPreview {
  const refusals: string[] = [];
  const warnings: string[] = [];

  // The posture comes from the *daemon*, because a request cannot set it: mode
  // and baseline are its config, and `req.network.allow` holds only the extra
  // domains this launch adds. Computing the list from the form's own copy of
  // baseline was how a daemon with `baseline: false` and its own configured
  // domains produced an empty allowlist here — and then a refusal, and a Launch
  // button disabled with no control that could change it.
  const mode = egress?.mode ?? req.network.mode;
  const baseline = egress?.baseline ?? req.network.baseline;
  const configured = egress?.allow ?? (baseline ? BASELINE_EGRESS : []);
  const allow = [...new Set([...configured, ...req.network.allow])];

  // What the run will *actually* get. Adding domains to an unrestricted daemon
  // switches the allowlist on for that run — the one narrowing a request may
  // make — so a preview that echoed the daemon's mode would describe open egress
  // for a container that is about to be firewalled.
  const effectiveMode: NetworkMode =
    mode === "default" && req.network.allow.length > 0 ? "allowlist" : mode;

  // The edge that matters is the empty one: the firewall is wired only when
  // there are domains to permit, so an allowlist that resolved to nothing is
  // refused rather than handed back as a container with no filtering at all.
  if (mode === "allowlist" && allow.length === 0) {
    refusals.push(
      "An allowlist that resolved to no domains is refused: the firewall is only wired when there is something to permit, so this would run with no filtering at all. Use network mode “none” to ask to reach nothing.",
    );
  }

  if (req.profile === "prod") {
    if (req.persistAuth) {
      refusals.push(
        "prod does not mount the persisted agent HOME — the default auth path is an OAuth refresh token the agent can read, and prod's answer is that there is nothing there to steal.",
      );
    }
    // A *warning*, not a refusal, and the difference is who can act on it. The
    // baseline is the daemon's config; a launch cannot change it, and
    // ValidateProfile has already asserted it against the profile the daemon
    // actually runs. Refusing here disabled Launch with no control that could
    // satisfy the rule — a dead end rather than a check.
    if (baseline) {
      warnings.push(
        "prod expects network.baseline to be false: the baseline contains github.com, a write endpoint and so an exfiltration channel for any token the agent holds. It is set where the daemon reads its config.",
      );
    }
    if (req.sync) {
      refusals.push(
        "prod does not mount the host's Claude history bucket: it is a host path outside the workspace.",
      );
    }
    if (req.publish.length > 0) {
      refusals.push(
        "prod refuses published ports: publishing opens the boundary inward, and prod is the profile for runs nobody is watching. The daemon says the same thing, and would say it after the launch rather than before.",
      );
    }
  }

  for (const name of req.envAllow) {
    if (RESERVED_ENV.has(name)) {
      refusals.push(
        `${name} is an instruction, not a setting — it cannot be forwarded from outside. Supplying it would turn a control the root phase reads into an off switch.`,
      );
    }
  }

  if (!req.agent && !req.command.trim()) {
    refusals.push("Nothing to run: pick an agent or give a command.");
  }

  // The daemon's handoff rules, said before the launch rather than after it.
  // Each is a request it refuses outright, so showing them here is the
  // difference between a control that explains itself and a 400.
  if (req.resume && !req.console) {
    // The daemon refuses a headless resume, and the form drops the field rather
    // than sending one — so without this the run launches as a brand-new
    // conversation with whatever prompt is in the box, and nothing on screen
    // says the conversation was dropped. Arriving from a row's Continue makes
    // that one click away: the link ticks the console, and unticking it is an
    // ordinary thing to try.
    refusals.push(
      "Resuming a conversation needs the console: a headless resume would replay one prompt into an old conversation and exit. Keep the console ticked, or clear the conversation to start a new one.",
    );
  }

  if (req.handoffFrom) {
    if (!req.agent) {
      refusals.push(
        "A briefing is something an agent reads: pick one, or drop the conversation you are handing over.",
      );
    }
    if (!req.prompt.trim()) {
      refusals.push(
        "A handoff needs a prompt: the briefing says what happened before, and the prompt says what to do now. Without one the agent is handed evidence and no instruction.",
      );
    }
    if (req.resume) {
      refusals.push(
        "Resume and handoff are opposites: one reopens a conversation with the agent that wrote it, the other starts a new one carrying a briefing about it. Pick one.",
      );
    }
  }

  if (req.detach && !req.worktree) {
    warnings.push(
      "A detached run is named sandbox-<repo>-<branch>, and docker's duplicate-name refusal is what enforces one agent per branch. Without a worktree this will collide with another run on the same branch.",
    );
  }

  if (req.verify && !req.detach) {
    warnings.push(
      "verify is what makes a run autonomous rather than merely headless. It is wired for attached runs too, but its exit code is the container's — you will see it as the run's outcome, not on screen.",
    );
  }

  if (req.share.length > 0) {
    warnings.push(
      `--share widens the boundary deliberately: ${req.share.length} extra host ${req.share.length === 1 ? "directory is" : "directories are"} in reach.`,
    );
  }

  // Louder than share, because it is the only option here that opens a way *in*
  // rather than widening what goes out — and a preview that warned about a mount
  // and said nothing about an inbound port would rank them backwards.
  if (req.publish.length > 0) {
    const bound = req.publish.filter((p) => !/^(127\.0\.0\.1|localhost|\[::1\])[:.]/.test(p) && p.includes(":") && /^\d+\.|^\[/.test(p));
    warnings.push(
      bound.length > 0
        ? `--publish ${bound.join(", ")} binds an address you named rather than loopback: anything that can reach that address can reach the container.`
        : `--publish opens the way in for ${req.publish.length} port${req.publish.length === 1 ? "" : "s"}, bound to 127.0.0.1 on the machine running the daemon.`,
    );
  }

  if (effectiveMode === "allowlist") {
    warnings.push(
      "The allowlist decides on the hostname when the in-container proxy is present, and on the resolved address when it is not. The audit line records which regime was *requested*.",
    );
  }

  const hostPathsInReach = [
    req.worktree ? `${req.workspace} (worktree)` : req.workspace,
    ...(req.persistAuth && req.agent
      ? [`~/.config/sandbox/agents/${req.agent}`]
      : []),
    ...(req.sync && req.agent === "claude"
      ? ["~/.claude/projects/<this project>"]
      : []),
    ...req.share,
  ];

  return {
    argv: previewArgv(req, allow, effectiveMode, baseline),
    hostPathsInReach,
    refusals,
    warnings,
  };
}

function previewArgv(
  req: LaunchRequest,
  allow: string[],
  effectiveMode: NetworkMode,
  baseline: boolean,
): string[] {
  const branch = req.worktree ?? "main";
  const repoName = req.workspace.split("/").filter(Boolean).pop() ?? "repo";
  return buildArgv({
    ...MOCK_RUNS[0],
    name: `sandbox-${repoName}-${branch.replace(/\//g, "-")}`,
    kind: req.verify ? "fleet" : "interactive",
    agent: req.agent,
    command: req.agent ? [req.agent] : req.command.split(/\s+/).filter(Boolean),
    detached: req.detach,
    branch,
    base: req.base,
    verify: req.verify || null,
    profile: req.profile,
    workspace: req.workspace,
    network: {
      mode: effectiveMode,
      baseline,
      allow,
      // Container ports, as the daemon would resolve them: a spec may be
      // "8080:8000", and what the firewall carves out is the container half.
      ingressPorts: req.publish
        .map((p) => Number(p.split(":").pop()?.split("/")[0]))
        .filter((n) => Number.isFinite(n) && n > 0),
      networkName: effectiveMode === "allowlist" ? "sandbox-net" : undefined,
      enforcement: effectiveMode === "allowlist" ? "name" : null,
    },
    security: {
      ...MOCK_RUNS[0].security,
      memory: req.memory,
      cpus: req.cpus,
    },
    mounts: [
      {
        host: req.workspace,
        container: "/workspace",
        mode: "rw",
        origin: "workspace",
      },
      ...(req.persistAuth && req.agent
        ? [
            {
              host: `~/.config/sandbox/agents/${req.agent}`,
              container: "/sandbox/home",
              mode: "rw" as const,
              origin: "persisted-home" as const,
            },
          ]
        : []),
      ...req.share.map((s) => ({
        host: s,
        container: s,
        mode: "rw" as const,
        origin: "share" as const,
      })),
    ],
  });
}

/**
 * The launch form's state is not the daemon's request body, and the difference
 * is deliberate rather than an oversight to paper over.
 *
 * `RunCreateRequest` rejects unknown fields, so posting the form verbatim was a
 * 400 — and it was right to be. Most of what the form holds is a *display* of
 * the posture a run will have, not a choice the request gets to make:
 *
 *   profile           the server's, fixed by whoever started it. A request that
 *                     could pick its own profile would drop a run out of prod.
 *   network.mode      tighten-only, and not expressible per-request at all.
 *   envAllow          decides which host variables cross into the container.
 *   share             a mount, the widest thing a launch option can add.
 *   persistAuth       whether an OAuth refresh token is mounted.
 *
 * Those are shown so you can see what you are about to get, and they come from
 * the daemon's own config; sending them back as instructions is what the
 * refused-key rule in internal/config/trust.go exists to prevent, one layer up.
 *
 * What does travel is the task: which agent, what to do, where, and the limits
 * that only ever narrow it.
 *
 * **Published ports do travel**, and they are the one thing here that opens a
 * way *in* rather than narrowing what goes out. That is deliberate: an agent
 * running a dev server is a real reason to want one, and `trust.go` already
 * draws the line in the right place — a repository may not declare `ports:`,
 * because it is a decision about the boundary that belongs to the user, and a
 * request from this form *is* the user making it on their own daemon. A bare
 * port binds loopback on the daemon's host, so the default reach is the machine
 * you are already on.
 */
function toRunCreate(req: LaunchRequest): Record<string, unknown> {
  const body: Record<string, unknown> = {};

  if (req.agent) {
    body.agent = req.agent;
    if (req.prompt.trim()) body.prompt = req.prompt;
  } else if (req.command.trim()) {
    // The form is one text field and the API takes an argv. `sh -c` is the
    // honest reading of a command line someone typed: it is what makes quotes
    // and pipes behave the way they look, and splitting on whitespace here
    // would silently mangle any argument containing a space.
    body.command = ["sh", "-c", req.command];
  }

  // Which repository, by id. Sent alongside a worktree (the branch is resolved
  // *inside* this repository) but never alongside a project path, which the
  // daemon refuses as two answers to one question.
  if (req.repo) body.repo = req.repo;
  // Only when there is somewhere to fall through to. An empty chain and no chain
  // are the same request, and sending the empty form would put a field in every
  // launch that means nothing.
  if (req.fallback.length > 0) body.fallback = req.fallback;

  // A worktree is addressed by branch and replaces the workspace; only one of
  // the two ever reaches the daemon, which refuses both together. The workspace
  // path is sent only when no repo id was chosen — a repo id already says which
  // directory, and says it in the form the daemon can check against its registry.
  if (req.worktree) body.worktree = req.worktree;
  else if (!req.repo && req.workspace) body.project = req.workspace;

  if (req.base) body.base = req.base;
  // Console travels: it is a property of the task ("I intend to talk to this"),
  // not of the posture. It widens nothing — a pty and an open stdin change what
  // the container listens to, never what it can reach.
  if (req.console && req.agent) body.console = true;
  // Both are console-only and agent-only, and the daemon refuses them
  // otherwise — so the form does not send a pair it knows will 400.
  if (req.console && req.agent && req.skipPermissions) body.skipPermissions = true;
  if (req.console && req.agent && req.resume) body.resume = req.resume;
  // A briefing, and only where the daemon accepts one: an agent to read it, no
  // resume alongside it (refused together), and a prompt, since the briefing
  // says what happened before and the prompt says what to do now. Unlike resume
  // it is not console-only — a handoff starts a *new* conversation, so running
  // it headless is a legitimate thing to want.
  if (req.agent && req.handoffFrom && !req.resume && req.prompt.trim()) {
    body.handoffFrom = req.handoffFrom;
  }
  // Refused together by the daemon, so the form does not send a pair it knows
  // will 400. Verify decides the exit code; an interactive session's exit code
  // is whenever you quit.
  if (req.verify.trim() && !req.console) body.verify = req.verify;
  if (req.memory) body.memory = req.memory;
  if (req.cpus) body.cpus = req.cpus;
  // Domains add to the baseline and cannot subtract from it, so this narrows or
  // does nothing — the one network field a request may carry.
  if (req.network.allow.length > 0) body.allow = req.network.allow;
  // Ports travel: publishing is a decision about the boundary, and a request is
  // the user making it on their own daemon — the same act as typing --publish.
  // What may not make it is a repository, which trust.go refuses `ports:` from.
  if (req.publish.length > 0) body.publish = req.publish;

  return body;
}
