import { ApiError, request } from "@/lib/api/client";
import {
  MOCK_AGENTS,
  MOCK_AUDIT,
  MOCK_DAEMON,
  MOCK_DOCTOR,
  MOCK_CONVERSATION,
  MOCK_RUNS,
  MOCK_USAGE,
  MOCK_WORKTREES,
  buildArgv,
  mockConfig,
  mockDiff,
  mockLogs,
  mockMetrics,
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
  MetricSeries,
  ResolvedConfig,
  Run,
  UsageSnapshot,
  Worktree,
  Conversation,
  SessionSummary,} from "@/lib/types";

/**
 * The daemon's surface, one function per endpoint.
 *
 * Paths are versioned (`/v1/...`) because Studio ships separately from the CLI
 * and the two will not always be the same age.
 */

export const api = {
  daemon: () =>
    request<DaemonInfo>("/v1/health", {
      fixture: () => MOCK_DAEMON,
      latencyMs: 120,
    }),

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

  /** Conversations this agent can be resumed from, newest first. */
  agentSessions: (agent: string) =>
    request<SessionSummary[]>(`/v1/agents/${agent}/sessions`, {
      fixture: () => [],
      unwrap: (b) => (b as { sessions: SessionSummary[] }).sessions ?? [],
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

  worktrees: () =>
    request<Worktree[]>("/v1/worktrees", {
      fixture: () => MOCK_WORKTREES,
      latencyMs: 240,
      unwrap: (b) => (b as { worktrees: Worktree[] }).worktrees,
    }),

  worktree: (branch: string) =>
    request<Worktree>(`/v1/worktrees/${encodeURIComponent(branch)}`, {
      fixture: () => {
        const w = MOCK_WORKTREES.find((x) => x.branch === branch);
        if (!w) throw new Error(`no worktree ${branch}`);
        return w;
      },
      latencyMs: 140,
    }),

  worktreeCommits: (branch: string) =>
    request<Commit[]>(`/v1/worktrees/${encodeURIComponent(branch)}/commits`, {
      fixture: () => [],
      latencyMs: 200,
      unwrap: (b) => (b as { commits: Commit[] }).commits,
    }),

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
  commitDiff: (sha: string) =>
    request<DiffFile[]>(`/v1/commits/${encodeURIComponent(sha)}/diff`, {
      fixture: () => [],
      latencyMs: 200,
    }),

  removeWorktree: (branch: string) =>
    request<void>(`/v1/worktrees/${encodeURIComponent(branch)}`, {
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
  audit: (branch?: string, limit?: number) =>
    request<AuditRecord[]>(
      `/v1/audit?limit=${limit ?? 200}${branch ? `&branch=${encodeURIComponent(branch)}` : ""}`,
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
export function localPreview(req: LaunchRequest): LaunchPreview {
  const refusals: string[] = [];
  const warnings: string[] = [];

  const allow = req.network.baseline
    ? [...BASELINE_EGRESS, ...req.network.allow]
    : [...req.network.allow];

  // The edge that matters is the empty one: the firewall is wired only when
  // there are domains to permit, so an allowlist that resolved to nothing is
  // refused rather than handed back as a container with no filtering at all.
  if (req.network.mode === "allowlist" && allow.length === 0) {
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
    if (req.network.baseline) {
      refusals.push(
        "prod requires network.baseline to be false: the baseline contains github.com, a write endpoint and so an exfiltration channel for any token the agent holds.",
      );
    }
    if (req.sync) {
      refusals.push(
        "prod does not mount the host's Claude history bucket: it is a host path outside the workspace.",
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

  if (req.network.mode === "allowlist") {
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
    argv: previewArgv(req, allow),
    hostPathsInReach,
    refusals,
    warnings,
  };
}

function previewArgv(req: LaunchRequest, allow: string[]): string[] {
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
      mode: req.network.mode,
      baseline: req.network.baseline,
      allow,
      networkName: req.network.mode === "allowlist" ? "sandbox-net" : undefined,
      enforcement: req.network.mode === "allowlist" ? "name" : null,
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
 *   share / publish   a mount and an inbound port — the two widest things a
 *                     launch option can add.
 *   persistAuth       whether an OAuth refresh token is mounted.
 *
 * Those are shown so you can see what you are about to get, and they come from
 * the daemon's own config; sending them back as instructions is what the
 * refused-key rule in internal/config/trust.go exists to prevent, one layer up.
 *
 * What does travel is the task: which agent, what to do, where, and the limits
 * that only ever narrow it.
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

  // A worktree is addressed by branch and replaces the workspace; only one of
  // the two ever reaches the daemon, which refuses both together.
  if (req.worktree) body.worktree = req.worktree;
  else if (req.workspace) body.project = req.workspace;

  if (req.base) body.base = req.base;
  // Console travels: it is a property of the task ("I intend to talk to this"),
  // not of the posture. It widens nothing — a pty and an open stdin change what
  // the container listens to, never what it can reach.
  if (req.console && req.agent) body.console = true;
  // Both are console-only and agent-only, and the daemon refuses them
  // otherwise — so the form does not send a pair it knows will 400.
  if (req.console && req.agent && req.skipPermissions) body.skipPermissions = true;
  if (req.console && req.agent && req.resume) body.resume = req.resume;
  // Refused together by the daemon, so the form does not send a pair it knows
  // will 400. Verify decides the exit code; an interactive session's exit code
  // is whenever you quit.
  if (req.verify.trim() && !req.console) body.verify = req.verify;
  if (req.memory) body.memory = req.memory;
  if (req.cpus) body.cpus = req.cpus;
  // Domains add to the baseline and cannot subtract from it, so this narrows or
  // does nothing — the one network field a request may carry.
  if (req.network.allow.length > 0) body.allow = req.network.allow;

  return body;
}
