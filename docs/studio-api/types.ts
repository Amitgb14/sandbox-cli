// Sandbox Studio API — TypeScript contract mirror.
//
// Hand-maintained alongside internal/studioapi/types.go, which is the source
// of truth. Every shape here is the exact JSON a client receives or must
// send — camelCase, ISO-8601 timestamps (Go's encoding/json renders time.Time
// as RFC3339), no server-only fields. Keep the two files in sync by hand;
// there is no generator wiring them together yet.

export interface ErrorResponse {
  error: string;
}

export interface HealthResponse {
  status: "ok" | "degraded";
  version: string;
  engine: "docker" | "podman";
  dockerAvailable: boolean;
  /** The host directory this server manages. */
  project: string;
  profile: "dev" | "prod";
}

export interface AgentInfo {
  name: string;
  persistDir: string;
  envAllow: string[];
}

export interface AgentsResponse {
  agents: AgentInfo[];
}

/** A fleet task vs. a run someone (or Studio) started directly. */
export type RunKind = "interactive" | "fleet";

/** Mirrors the docker container states runtime.ContainerInfo reports. */
export type RunState =
  | "created"
  | "running"
  | "paused"
  | "restarting"
  | "exited"
  | "dead"
  | "unknown";

export interface Run {
  /** Short id (12 chars) — what the rest of the API accepts back as :id. */
  id: string;
  containerId: string;
  name: string;
  kind: RunKind;
  state: RunState;
  /** Set once state is "exited". */
  exitCode?: number;
  detached: boolean;

  repo?: string;
  branch?: string;
  base?: string;
  agent?: string;
  verify?: string;

  createdAt: string;
  startedAt?: string;
  finishedAt?: string;

  /** What GET /runs/{id}/logs and interactive attach could do — Studio runs are always detached, so both are false. */
  openStdin: boolean;
  tty: boolean;
}

export interface RunsResponse {
  runs: Run[];
}

/**
 * GET /runs query parameters (not a JSON body — parsed from the URL):
 *   all=1        include finished runs (default: live only)
 *   repo=NAME    filter by sandbox.repo label
 *   branch=NAME  filter by sandbox.branch label
 *   agent=NAME   filter by sandbox.agent label
 *   fleet=1      only fleet-launched runs
 */
export interface RunListQuery {
  all?: boolean;
  repo?: string;
  branch?: string;
  agent?: string;
  fleet?: boolean;
}

/**
 * POST /runs always launches detached: an HTTP request/response cycle has no
 * foreground mode to offer. Set either `agent` (+ `prompt`) or `command`, not
 * both. Set either `project` or `worktree`, not both.
 */
export interface RunCreateRequest {
  project?: string;
  worktree?: string;

  agent?: string;
  prompt?: string;
  command?: string[];

  branch?: string;
  base?: string;
  verify?: string;

  image?: string;
  memory?: string;
  cpus?: string;
  allow?: string[];
  env?: Record<string, string>;
}

export interface RunStopRequest {
  /** SIGKILL immediately instead of asking the guest to exit first. */
  force?: boolean;
}

export type RestoreMode = "branch" | "patch" | "worktree";

export interface RunRecoverRequest {
  /** Default "branch". */
  mode?: RestoreMode;
  /** Override the generated branch name (mode "branch" only). */
  branch?: string;
}

export interface RunRecoverResponse {
  sessionId: string;
  mode: RestoreMode;
  /** Set for mode "branch". */
  branch?: string;
  /** The diff text, for mode "patch". */
  patch?: string;
  files: number;
  /**
   * The workspace on disk already held what the snapshot held — the common
   * case, since /workspace is a bind mount and the snapshot is the belt, not
   * the braces.
   */
  matchesWorkingTree: boolean;
}

/** One line streamed from GET /runs/{id}/logs as SSE `event: log`. */
export interface LogEvent {
  stream: "stdout" | "stderr";
  data: string;
}

/** A single resource sample — GET /runs/{id}/metrics, or one entry of GET /stats. */
export interface RunMetrics {
  id: string;
  memUsageBytes: number;
  memLimitBytes?: number;
  memPercent: number;
  cpuPercent: number;
  pids: number;
  sampledAt: string;
}

export interface StatsResponse {
  runs: RunMetrics[];
  sampledAt: string;
}

export interface Worktree {
  branch: string;
  path: string;
  /** Modified/untracked paths. */
  dirty?: string[];
  dirtyCount: number;
}

export interface WorktreesResponse {
  worktrees: Worktree[];
}

export interface WorktreeCreateRequest {
  branch: string;
}
