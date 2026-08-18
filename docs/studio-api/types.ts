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
  /**
   * The host directory this server was started in — its *default* repository,
   * the one every request naming no repo is about. Others are added at runtime;
   * see GET /projects.
   */
  project: string;
  profile: "dev" | "prod";
}

/**
 * One repository this daemon will answer about.
 *
 * `id` is what a request names — never a path. It is worktree.RepoID, the same
 * id containers carry as their `sandbox.repo` label, which is what makes "the
 * runs for this repository" and "the worktrees for this repository" one
 * question.
 */
export interface Project {
  id: string;
  name: string;
  root: string;
  /** The repository the daemon was started in. Cannot be removed. */
  default?: boolean;
  /** Registered but unreadable now — gone, no longer a repository, or unmounted. */
  missing?: boolean;
}

/** One directory offered by the folder picker (`GET /browse`). Names only. */
export interface BrowseEntry {
  name: string;
  /** Absolute host path — what POST /projects takes. */
  path: string;
  /** Holds a .git. A hint; the add endpoint still decides. */
  repo?: boolean;
  /** Already managed by this Studio. */
  registered?: boolean;
}

export interface BrowseResponse {
  path: string;
  /** The directory above; absent at the filesystem root. */
  parent?: string;
  /** This user's home directory — where a picker should start. */
  home?: string;
  /** Whether `path` itself is a repository. */
  repo?: boolean;
  entries: BrowseEntry[];
  truncated?: boolean;
}

/** One row of a directory listing. `path` is repository-relative, slash-separated. */
export interface FileEntry {
  name: string;
  path: string;
  dir?: boolean;
  size?: number;
  /** Reported, never followed: a link out of the repository is not readable. */
  symlink?: boolean;
  modifiedAt?: string;
}

export interface FilesResponse {
  /** The listed directory; "" is the repository root. */
  path: string;
  entries: FileEntry[];
  /** More entries than one listing carries. */
  truncated?: boolean;
}

export interface FileContentResponse {
  path: string;
  size: number;
  /** Binary content is reported, not sent. */
  binary?: boolean;
  /** `content` is the first part of a larger file. */
  truncated?: boolean;
  content?: string;
}

/** One line of the run log. */
export interface AuditRecord {
  time: string;
  /**
   * Which repository this run belonged to — derived from the recorded
   * workspace, not stored, since the log has no repo field. "" (absent) means
   * no repository this daemon knows about.
   */
  repoId?: string;
  workspace: string;
  branch?: string | null;
  agent?: string | null;
  exitCode: number;
  durationMs: number;
}

export interface ProjectsResponse {
  projects: Project[];
}

/**
 * POST /projects — the only request in this contract carrying a host path, so
 * the only one where every path refusal applies. Resolved to the repository
 * root before it is recorded.
 */
export interface ProjectCreateRequest {
  path: string;
}

export interface AgentInfo {
  name: string;
  persistDir: string;
  envAllow: string[];
  /**
   * Whether a conversation of this agent's can be reopened by its native session
   * id (`claude --resume`, `codex resume`, `opencode --session`). False for
   * gemini and droid, which declare none — for those, "carry this conversation
   * on" is not expressible and a client must not offer it.
   */
  canResume: boolean;
}

/**
 * The conversation a run is briefed with — the agent that held it and its
 * session id, by id and never by path.
 *
 * This is **not** a resume and the two are refused together. A session id is a
 * primary key into one vendor's private store, so claude's cannot be handed to
 * codex; what crosses is internal/handoff's export (HANDOFF.md, a
 * vendor-neutral transcript.jsonl, a git-derived files.md), mounted read-only
 * at /sandbox/context, with a prompt that tells the target it is reading a
 * briefing rather than its own history. Requires an agent and a prompt: the
 * briefing says what happened before, the prompt says what to do now.
 */
export interface HandoffRef {
  agent: string;
  sessionId: string;
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

  /**
   * `sandbox.repo` — worktree.RepoID, an id and not a path. Spelled the same as
   * Worktree.repoId: one fact, one name. (It was `repo` here and `repoId`
   * there, and a client filtering both by repository could only be right about
   * one of them.)
   */
  repoId?: string;
  /** The display half of that id. For showing, never for matching. */
  repoName?: string;
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
 * both. Set either `project` or `worktree`, not both, and never `repo` with
 * `project` — they are two answers to "which repository".
 */
export interface RunCreateRequest {
  /**
   * Which registered repository, by `Project.id`. Absent means the one the
   * daemon was started in. This is what a UI should send: an id is resolved
   * against the registry, a path is not.
   */
  repo?: string;
  /** A host directory, for callers that already know the path they mean. */
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

/**
 * Worktree.primary marks the repository's *own checkout* — the directory
 * -project names, the one branch with no worktree of its own. Listed because a
 * branch picker has to know about it; marked because the operations differ (it
 * cannot be removed, and `land` merges into it).
 */
export interface WorktreesResponse {
  worktrees: Worktree[];
}

export interface WorktreeCreateRequest {
  branch: string;
  /** Which registered repository, by `Project.id`. Absent means the default. */
  repo?: string;
}
