/**
 * The shapes Studio reads, mirroring the Go types they come from:
 *
 *   Sandbox API v1 (proxied at /api/v1)   internal/api/types.go, docs/api/v1.md
 *   Studio's own API (/api)               internal/studio/*.go
 *
 * Field names are the wire's, snake_case included, so a value can be passed
 * from a response to a component without a translation layer to drift.
 */

export type NetworkMode = "none" | "allowlist" | "open";

export interface NetworkPolicy {
  mode: NetworkMode;
  allow?: string[] | null;
  deny?: string[];
}

export interface VolumeMount {
  name: string;
  path: string;
  read_only?: boolean;
}

export type SandboxState = "pending" | "running" | "suspended" | "terminated";

export interface Sandbox {
  id: string;
  name?: string;
  state: SandboxState;
  image: string;
  cpus: number;
  memory_mb: number;
  disk_mb: number;
  env_names?: string[];
  network: NetworkPolicy;
  created_at: string;
  idle_timeout_secs: number;
  bind?: { host_path: string; read_only?: boolean };
  labels?: Record<string, string>;
  volumes?: VolumeMount[];
}

export interface Capabilities {
  api_version: string;
  backend: string;
  capabilities: Record<string, boolean>;
  limits: { max_cpus: number; max_memory_mb: number; max_disk_mb: number; max_idle_timeout_secs: number };
  network: { default: NetworkPolicy; ceiling: NetworkMode; may_allow: string[] | null };
}

export interface Process {
  pid: number;
  tty?: boolean;
  argv: string[];
  state: "running" | "exited";
  exit_code: number | null;
  started_at: string;
}

export interface AuditEvent {
  time: string;
  type: string;
  sandbox: string;
  name?: string;
  image?: string;
  labels?: Record<string, string>;
  network?: NetworkPolicy;
  env_names?: string[];
  bind?: string;
  snapshot?: string;
  volumes?: VolumeMount[];
  pid?: number;
  /** A process is audited by program, argument count and a hash of the arguments, never their text. */
  program?: string;
  arg_count?: number;
  args_sha256?: string;
  cwd?: string;
  exit_code?: number;
  duration_ms?: number;
  path?: string;
  bytes?: number;
  port?: number;
  reason?: string;
}

export interface Volume {
  name: string;
  size_mb: number;
  created_at: string;
  attached_to?: string;
}

export interface DirEntry {
  name: string;
  type: "file" | "dir";
  size: number;
}

export interface Snapshot {
  id: string;
  sandbox: string;
  image: string;
  bytes: number;
  created_at: string;
}

/** GET /api/info */
export interface Info {
  context: string;
  version: string;
  capabilities?: Capabilities;
  error?: string;
}

export interface Repo {
  id: string;
  path: string;
  name: string;
  added: string;
  missing?: boolean;
}

/** A session record joined with its sandbox's state ("gone" once it is not). */
export interface Run {
  sandbox: string;
  repo: string;
  repo_id: string;
  agent?: string;
  started: string;
  state: SandboxState | "gone";
  labels?: Record<string, string>;
  done: boolean;
  brought_back?: string;
  checkpoint?: string;
  checkpoint_at?: string;
}

export interface SandboxRef {
  ref: string;
  commit: string;
  subject: string;
  author: string;
  date: string;
  ahead: number;
}

export interface Diff {
  ref: string;
  files: { path: string; added: number; removed: number }[];
  patch: string;
  truncated: boolean;
}

export type TaskStateName = "running" | "verified" | "failed" | "rejected" | "lost";

export interface TaskState {
  branch: string;
  agent: string;
  sandbox: string;
  state: TaskStateName;
  exit_code: number;
  ref?: string;
  /** The latest checkpoint taken while it ran: where a lost task's work is. */
  checkpoint?: string;
  log: string;
  error?: string;
}

export interface FleetState {
  repo: string;
  base_branch: string;
  base_commit: string;
  started_at: string;
  tasks: Record<string, TaskState>;
}

/** An agent Studio can run: only those with a verified headless mode are listed. */
export interface Agent {
  name: string;
  login: "saved" | "-" | "not kept";
}

export interface LaunchRequest {
  /** A registered repository's id; absent starts with an empty /workspace. */
  repo?: string;
  agent?: string;
  prompt?: string;
  console?: boolean;
  command?: string[];
  name?: string;
  network?: "" | NetworkMode;
  allow?: string[];
  labels?: Record<string, string>;
  volumes?: VolumeMount[];
  git?: boolean;
  profile?: "" | "dev" | "prod";
  rows?: number;
  cols?: number;
}

export interface LaunchResult {
  sandbox: string;
  pid: number;
}

/** One line of GET /v1/sandboxes/{ref}/processes/{pid}/output. */
export type OutputEvent =
  | { stream: "stdout" | "stderr"; data: string }
  | { exit_code: number };

/**
 * What an agent is doing, decided host-side from evidence rather than from its
 * wording (internal/agentstate): `blocked` is quiet with a terminal somebody can
 * answer at, `idle` quiet with none, `unknown` where the evidence runs out.
 */
export type AgentStateName = "working" | "blocked" | "idle" | "done" | "failed" | "suspended" | "stopped" | "unknown";

export interface AgentState {
  sandbox: string;
  name?: string;
  agent: string;
  state: AgentStateName;
  why: string;
}
