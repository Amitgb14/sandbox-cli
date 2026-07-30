import { request } from "@/lib/api/client";
import {
  MOCK_AGENTS,
  MOCK_AUDIT,
  MOCK_DAEMON,
  MOCK_DOCTOR,
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
} from "@/lib/types";

/**
 * The daemon's surface, one function per endpoint.
 *
 * Paths are versioned (`/v1/...`) because Studio ships separately from the CLI
 * and the two will not always be the same age.
 */

export const api = {
  daemon: () =>
    request<DaemonInfo>("/v1/health", { fixture: () => MOCK_DAEMON, latencyMs: 120 }),

  runs: () => request<Run[]>("/v1/runs", { fixture: () => MOCK_RUNS, latencyMs: 260 }),

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
    request<void>(`/v1/runs/${id}/stop`, { method: "POST", fixture: () => undefined }),

  killRun: (id: string) =>
    request<void>(`/v1/runs/${id}/kill`, { method: "POST", fixture: () => undefined }),

  launch: (req: LaunchRequest) =>
    request<{ id: string }>("/v1/runs", {
      method: "POST",
      body: req,
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
    request<Agent[]>("/v1/agents", { fixture: () => MOCK_AGENTS, latencyMs: 200 }),

  worktrees: () =>
    request<Worktree[]>("/v1/worktrees", { fixture: () => MOCK_WORKTREES, latencyMs: 240 }),

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
    request<UsageSnapshot>("/v1/usage", { fixture: () => MOCK_USAGE, latencyMs: 200 }),

  refreshUsage: () =>
    request<UsageSnapshot>("/v1/usage/refresh", {
      method: "POST",
      fixture: () => MOCK_USAGE,
      latencyMs: 1400,
    }),

  doctor: () =>
    request<DoctorCheck[]>("/v1/doctor", { fixture: () => MOCK_DOCTOR, latencyMs: 900 }),

  audit: () =>
    request<AuditRecord[]>("/v1/audit", { fixture: () => MOCK_AUDIT, latencyMs: 300 }),
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
    ...(req.persistAuth && req.agent ? [`~/.config/sandbox/agents/${req.agent}`] : []),
    ...(req.sync && req.agent === "claude" ? ["~/.claude/projects/<this project>"] : []),
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
      { host: req.workspace, container: "/workspace", mode: "rw", origin: "workspace" },
      ...(req.persistAuth && req.agent
        ? ([
            {
              host: `~/.config/sandbox/agents/${req.agent}`,
              container: "/sandbox/home",
              mode: "rw" as const,
              origin: "persisted-home" as const,
            },
          ])
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
