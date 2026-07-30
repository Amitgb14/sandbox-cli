// Sandbox Studio API — TypeScript contract mirror.
//
// Hand-maintained alongside internal/studioapi/types.go, which is the source
// of truth. Every shape here is the exact JSON a client receives or must
// send — camelCase, ISO-8601 timestamps (Go's encoding/json renders time.Time
// as RFC3339), no server-only fields. Keep the two files in sync by hand;
// there is no generator wiring them together yet.
//
// Three things a client must get right, all enforced server-side (see
// internal/studioapi/guard.go, and the trust model in README.md):
//
//   1. Any request carrying a body must send `Content-Type: application/json`.
//      A bodiless POST (e.g. /runs/:id/stop with no options) needs no header.
//   2. Requests must reach the server by a loopback name — `localhost` or
//      `127.0.0.1`, matching whatever `-addr` it was started on. A page served
//      from another origin must be named in `-cors-origin`, or it is refused
//      outright rather than merely prevented from reading the response.
//   3. With a token configured, send `Authorization: Bearer <token>`. The one
//      exception is the WebSocket log stream, where the browser API cannot set
//      headers: pass `?token=<token>` on that URL only.

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

export type LogEventType = "log" | "error" | "end";

/**
 * One event of GET /runs/{id}/logs, identical on both transports: a WebSocket
 * text frame carries exactly this object, and an SSE `data:` line carries
 * exactly this object with `event:` repeating its `type`.
 *
 * A discriminated union, so narrowing on `type` gives you the fields that
 * event actually has. The "end" case is the one worth handling explicitly:
 * without it you cannot tell a stream that finished from a connection that
 * dropped, and an incomplete log rendered as a complete one is how a
 * half-finished agent run reads as a finished one.
 */
export type LogEvent =
  | { type: "log"; stream: "stdout" | "stderr"; data: string }
  | { type: "error"; data: string; error: string }
  | { type: "end"; data: string };

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
