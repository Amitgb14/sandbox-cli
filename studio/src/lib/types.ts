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
  labels?: Record<string, string>;
  volumes?: VolumeMount[];
  /** The snapshot schedule: one every so many seconds while it runs, the newest kept. */
  snapshot_every_secs?: number;
  snapshot_keep?: number;
}

export interface Capabilities {
  api_version: string;
  backend: string;
  capabilities: Record<string, boolean>;
  limits: {
    max_cpus: number;
    max_memory_mb: number;
    max_disk_mb: number;
    max_idle_timeout_secs: number;
    /** The shortest snapshot schedule allowed, and the most scheduled snapshots kept. */
    min_snapshot_every_secs?: number;
    max_snapshot_keep?: number;
  };
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
  /** memory: memory, processes and disk; disk: the files only, forks boot afresh. */
  kind?: "memory" | "disk";
  /** Taken by the sandbox's schedule, whose retention may remove it. */
  scheduled?: boolean;
  bytes: number;
  created_at: string;
}

/** GET /api/info */
/** What a sandboxd has for sandboxes (GET /v1/node): allocations, not live usage. */
export interface NodeResources {
  cpus: number;
  memory_mb: number;
  disk_mb: number;
}

export interface NodeStatus {
  node: string;
  version: string;
  capacity: NodeResources;
  /** Capacity less what running and suspended sandboxes were given. */
  free: NodeResources;
  running: number;
  /** Images whose root disk is already built here, so a sandbox of one starts without a pull. */
  images?: string[];
  cordoned: boolean;
}

export interface Info {
  context: string;
  version: string;
  /** The client's built-in allowlist, which --allow adds to under a non-allowlist default. */
  baseline_egress?: string[];
  /** The context's own organisation (sandbox-cli org use), which Studio starts in. */
  org?: string;
  capabilities?: Capabilities;
  error?: string;
}

/** An agent Studio can run: only those with a verified headless mode are listed. */
export interface Agent {
  name: string;
  login: "saved" | "-" | "not kept";
  /** The agent's API, which its runs may always reach. */
  provider_host?: string;
  /** What is kept of its login between runs, relative to the sandbox user's home. */
  login_files?: string[];
  /** The variables it reads, and whether each is set where Studio runs. Names only. */
  env?: { name: string; set: boolean }[];
}

export interface LaunchRequest {
  agent?: string;
  prompt?: string;
  console?: boolean;
  command?: string[];
  name?: string;
  network?: "" | NetworkMode;
  allow?: string[];
  labels?: Record<string, string>;
  volumes?: VolumeMount[];
  profile?: "" | "dev" | "prod";
  rows?: number;
  cols?: number;
  /** An image instead of the server's default, or a snapshot to start from: one or neither. */
  image?: string;
  snapshot?: string;
  /** A snapshot schedule: one every so many seconds while it runs, the newest kept. */
  snapshot_every_secs?: number;
  snapshot_keep?: number;
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

// --- a gateway's own endpoints (internal/api/gateway.go, jobs_types.go,
// services_types.go; docs/api/v1.md "Gateway"). A plain sandboxd answers each
// 404. The admin-only shapes are in lib/admin, which a build without admin
// screens leaves out.

export type Scope = "sandbox:read" | "sandbox:create" | "sandbox:delete" | "sandbox:ssh" | "secrets:write" | "org:create" | "admin";

/** GET /v1/whoami: the caller as the gateway sees its key. */
export interface Whoami {
  user: string;
  tenant: string;
  key_id: string;
  scopes: string[];
  /** The organisation the request acted in, after X-Sandbox-Org; "default" for the default tenant. */
  org?: string;
}

/** GET /v1/orgs: one organisation the caller may act in. */
export interface Org {
  name: string;
  role: "owner" | "member";
  created?: string;
  current: boolean;
}

/** GET /v1/orgs/{name}/members. tenant is the tenant of the member's own keys; absent is the default one. */
export interface OrgMember {
  user: string;
  tenant?: string;
  role: "owner" | "member";
  added?: string;
}

export interface SSHInfo {
  host: string;
  port: number;
  host_keys: string[];
  fingerprint: string;
}

export interface SSHKey {
  id: string;
  fingerprint: string;
  key: string;
  sandbox?: string;
  created: string;
}

/** POST /v1/sandboxes/{ref}/ssh-access. `user` is the whole credential until it expires. */
export interface SSHAccess {
  user: string;
  host: string;
  port: number;
  expires_at: string;
  command: string;
}

export interface SecretInfo {
  name: string;
  updated_at: string;
}

export type JobState = "running" | "succeeded" | "failed" | "cancelled";
export type RunState = "queued" | "running" | "succeeded" | "failed" | "timed_out" | "cancelled";

export interface JobSpec {
  name?: string;
  image?: string;
  command?: string[];
  agent?: string;
  prompt?: string;
  prompts?: string[];
  parallelism?: number;
  completions?: number;
  retries?: number;
  timeout_secs?: number;
  env?: Record<string, string>;
  secrets?: string[];
  network?: NetworkPolicy;
  resources?: { cpus?: number; memory_mb?: number; disk_mb?: number };
  from_snapshot?: string;
  keep?: { output?: boolean; files?: string[] };
  notify?: string;
}

export interface JobFile {
  path: string;
  size: number;
  truncated?: boolean;
  error?: string;
}

export interface JobRun {
  n: number;
  state: RunState;
  sandbox?: string;
  pid?: number;
  attempts: number;
  exit_code?: number;
  started_at?: string;
  finished_at?: string;
  error?: string;
  output_truncated?: boolean;
  files?: JobFile[];
}

export interface Job {
  id: string;
  name?: string;
  state: JobState;
  spec: JobSpec;
  env_names?: string[];
  created_at: string;
  finished_at?: string;
  expires_at?: string;
  queued: number;
  running: number;
  succeeded: number;
  failed: number;
  runs?: JobRun[];
  error?: string;
}

/** What was kept of a run's output, base64. */
export interface JobOutput {
  stdout: string | null;
  stderr: string | null;
  truncated: boolean;
}

export interface ServiceHealth {
  http?: string;
  command?: string[];
  every_secs?: number;
  timeout_secs?: number;
  failures?: number;
}

export interface ServiceSpec {
  name: string;
  image?: string;
  command?: string[];
  replicas: number;
  resources?: { cpus?: number; memory_mb?: number; disk_mb?: number };
  port?: number;
  health?: ServiceHealth;
  env?: Record<string, string>;
  network?: NetworkPolicy;
  placement?: { spread?: string };
  public?: boolean;
  secrets?: string[];
}

export type ReplicaState = "starting" | "healthy" | "unhealthy" | "lost";

export interface ServiceReplica {
  sandbox: string;
  node: string;
  revision: number;
  state: ReplicaState;
  healthy: boolean;
  last_check?: string;
  last_error?: string;
  restarts: number;
  created_at: string;
}

export interface Service {
  spec: ServiceSpec;
  env_names?: string[];
  owner: string;
  tenant?: string;
  revision: number;
  serving: number;
  desired: number;
  ready: number;
  restarts: number;
  rollout?: { state: "in_progress" | "done" | "failed"; from: number; to: number; reason?: string };
  error?: string;
  url?: string;
  replicas: ServiceReplica[];
  created_at: string;
  updated_at: string;
}

/** One reading of a sandbox's usage, taken on the host (GET …/metrics). */
export interface MetricSample {
  time: string;
  /** Share of the sandbox's vCPUs used since the previous sample, 0–100. */
  cpu_percent: number;
  memory_bytes: number;
  memory_limit_bytes: number;
  /** Counters from the sandbox's start. */
  net_rx_bytes: number;
  net_tx_bytes: number;
  disk_read_bytes: number;
  disk_write_bytes: number;
  processes?: number;
}

export interface MetricsList {
  interval_secs: number;
  samples: MetricSample[];
}
