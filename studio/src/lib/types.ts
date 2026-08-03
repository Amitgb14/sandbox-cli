/**
 * The Studio domain model.
 *
 * Every type here mirrors something the CLI already owns, and the comments say
 * which — because the frontend must not invent a vocabulary the backend cannot
 * speak. Where the Go side draws a distinction (an id versus a path, a request
 * versus an outcome, absent versus zero) this file draws the same one, since
 * collapsing it in the types is how a UI ends up asserting something the daemon
 * never claimed.
 *
 * Source of truth, per group:
 *   Run              — runtime.ContainerInfo + audit.SessionMeta + sandbox labels
 *   NetworkPosture   — config.NetworkSpec (+ audit's enforcement *request*)
 *   Agent            — agents.Descriptor
 *   Worktree         — worktree.Info
 *   UsageSnapshot    — agentusage.Snapshot
 *   DoctorCheck      — cli/doctor.go
 *   ResolvedConfig   — config.Config
 */

// ---------------------------------------------------------------------------
// Primitives
// ---------------------------------------------------------------------------

/** Docker's own container states, as `runtime.ContainerInfo.State` reports them. */
export type RunState =
  | "created"
  | "running"
  | "paused"
  | "exited"
  | "dead"
  | "removing";

/**
 * `sandbox.fleet` — the label that separates a `fleet run` container from an
 * interactive `--detach` session in the same repository. It is load-bearing
 * everywhere else in the CLI (`fleet stop --all` does not reach an interactive
 * session, `max_parallel` does not count one), so the listing shows it too.
 */
export type RunKind = "interactive" | "fleet";

/** `config.NetworkSpec.Mode`. */
export type NetworkMode = "default" | "none" | "allowlist";

/**
 * `audit.SessionMeta.EgressEnforcementRequested`. Named for a *request*, not an
 * outcome, because that is all the host can honestly know: the container takes
 * the by-name proxy path only if `sandbox-egress-proxy` is on its PATH.
 * `null` means there was no allowlist at all.
 */
export type EgressEnforcement = "name" | "address" | null;

/** `--profile`. Both are secure; they differ in what they optimise. */
export type Profile = "dev" | "prod";

export type Engine = "docker" | "podman";

/** The fifteen adapters in `cli.agentCmds()`. */
export type AgentName =
  | "claude"
  | "codex"
  | "gemini"
  | "opencode"
  | "cline"
  | "goose"
  | "crush"
  | "aider"
  | "copilot"
  | "cursor"
  | "qwen"
  | "amp"
  | "continue"
  | "openhands"
  | "droid";

/** `config.MountSpec`. */
export interface MountSpec {
  host: string;
  container: string;
  mode: "ro" | "rw";
  /** Why this mount exists, for the one place the UI has to justify reach. */
  origin?: "workspace" | "worktree-git" | "persisted-home" | "history" | "statusline" | "share" | "cache";
}

/**
 * `config.NetworkSpec` plus the two facts only the run itself knows: which
 * network object it joined, and which enforcement regime was asked for.
 */
export interface NetworkPosture {
  mode: NetworkMode;
  /** `network.baseline` — tri-state on the Go side; resolved to a boolean here. */
  baseline: boolean;
  /** The resolved allowlist: baseline ∪ configured. Empty unless mode is allowlist. */
  allow: string[];
  networkName?: string;
  enforcement: EgressEnforcement;
  /** Container ports named by `--publish`, the only ingress carve-out. */
  ingressPorts?: number[];
}

/** `config.SecuritySpec`, as it ended up after the merge. */
export interface SecurityPosture {
  noNewPrivileges: boolean;
  capDrop: string[];
  capAdd: string[];
  pidsLimit: number;
  memory: string;
  cpus: string;
  seccomp: string;
  user: string;
  hardening: boolean;
}

// ---------------------------------------------------------------------------
// Runs
// ---------------------------------------------------------------------------

/**
 * One sandbox run. A live container and a finished audit line are the same
 * entity at two points in its life, so they are one type: `state` says which,
 * and the fields only one of them can fill are nullable rather than zeroed.
 */
export interface Run {
  /** Container id. Abbreviated for display, never for addressing. */
  id: string;
  /** `sandbox-<repo>-<branch>` for a detached run; docker's own name otherwise. */
  name: string;
  kind: RunKind;
  state: RunState;
  /** Meaningful once `state` is "exited". `null` while it is still running. */
  exitCode: number | null;

  createdAt: string;
  startedAt: string | null;
  finishedAt: string | null;
  /** Wall clock. Live runs report elapsed-so-far; the UI labels which. */
  durationMs: number | null;

  /** `sandbox.agent` — null for a plain `run`. */
  agent: AgentName | null;
  /** The guest argv, recorded verbatim (audit does the same, and says why). */
  command: string[];
  /** The prompt a fleet task was launched with, when there was one. */
  prompt?: string;

  image: string;
  engine: Engine;
  /** The host directory mounted at `workdir` — the thing this run could change. */
  workspace: string;
  workdir: string;

  /** `sandbox.repo` is `worktree.RepoID`: an id, not a path. */
  repoId: string;
  /** Display name only. Two clones of a same-named repo share it; `repoId` not. */
  repoName: string;
  /** `sandbox.branch` — the branch the workspace was on at launch. */
  branch: string | null;
  /** `sandbox.base` — the branch the work is expected to land on. */
  base: string | null;
  /**
   * `sandbox.verify` — the task's definition of done, when it declared one.
   * Its *presence* is what tells "no check" from "passed its check"; the
   * verdict itself is the exit code.
   */
  verify: string | null;

  profile: Profile;
  network: NetworkPosture;
  security: SecurityPosture;
  mounts: MountSpec[];
  /** Forwarded host variables, **by name only** — audit records no values. */
  envNames: string[];

  detached: boolean;
  /** Together these say what attaching can do. A detached run has neither. */
  tty: boolean;
  openStdin: boolean;

  /** The most recent sample, for the listing. Absent when never sampled. */
  latestMetrics?: MetricSample | null;
  /** Cheap summary of the run's effect on the workspace. */
  diffStat?: DiffStat;
}

/** The verdict a row shows, derived rather than stored. */
export type RunOutcome =
  | "running"
  | "passed"
  | "failed"
  | "verify-failed"
  | "stopped"
  | "created";

/** `fleet.VerifyFailedExit` — a verify that ran and said no. */
export const VERIFY_FAILED_EXIT = 91;

/**
 * The one place a run's exit code becomes a word. Kept as a function so the
 * table, the detail header and the charts cannot disagree about what "failed"
 * means — and so `verify` being *present* is what distinguishes a rejected
 * check from an agent that merely crashed.
 */
export function runOutcome(run: Run): RunOutcome {
  if (run.state === "running" || run.state === "paused") return "running";
  if (run.state === "created") return "created";
  if (run.exitCode === null) return "stopped";
  if (run.exitCode === 0) return "passed";
  if (run.verify && run.exitCode === VERIFY_FAILED_EXIT) return "verify-failed";
  if (run.exitCode === 137 || run.exitCode === 143) return "stopped";
  return "failed";
}

// ---------------------------------------------------------------------------
// Metrics
// ---------------------------------------------------------------------------

/** One reading of one container, in the shape `docker stats` gives it. */
export interface MetricSample {
  /** ISO timestamp. */
  t: string;
  cpuPct: number;
  memBytes: number;
  /** 0 means unlimited — `--memory` was not set. */
  memLimitBytes: number;
  netRxBytes: number;
  netTxBytes: number;
  blockReadBytes: number;
  blockWriteBytes: number;
  pids: number;
}

export interface MetricSeries {
  runId: string;
  samples: MetricSample[];
  /** Peak values over the window, which is what the CLI's footer summary prints. */
  peak: { cpuPct: number; memBytes: number };
}

// ---------------------------------------------------------------------------
// Logs, diffs
// ---------------------------------------------------------------------------

export interface LogLine {
  seq: number;
  ts: string;
  stream: "stdout" | "stderr";
  text: string;
}

export interface DiffStat {
  files: number;
  insertions: number;
  deletions: number;
}

export type DiffFileStatus = "added" | "modified" | "deleted" | "renamed";

export interface DiffLine {
  kind: "add" | "del" | "ctx" | "meta";
  oldNo: number | null;
  newNo: number | null;
  content: string;
}

export interface DiffHunk {
  header: string;
  lines: DiffLine[];
}

export interface DiffFile {
  path: string;
  previousPath?: string;
  status: DiffFileStatus;
  insertions: number;
  deletions: number;
  binary?: boolean;
  hunks: DiffHunk[];
}

// ---------------------------------------------------------------------------
// Agents
// ---------------------------------------------------------------------------

/** How the adapter gets into the container. */
export type AgentDelivery = "baked" | "npm" | "installer" | "pip";

/**
 * `agents.Descriptor`, plus the host-side facts a descriptor deliberately does
 * **not** carry (anything producing a host path: the persisted HOME, the
 * history mount, the status-line mount). Keeping them in separate groups here
 * mirrors why they are separate there.
 */
export interface Agent {
  name: AgentName;
  label: string;
  /** Separate from `name` so renaming a subcommand cannot orphan a login. */
  persistDir: string;
  /** Forwarded **only if set on the host**. Suggested, opt-in, narrow. */
  envAllow: string[];
  /** NAME=VALUE settings sandbox-cli itself puts in the container. */
  env: string[];
  delivery: AgentDelivery;
  /**
   * Whether the adapter has a *verified* headless argv. Only these may appear
   * in a `fleet.yaml`: a fleet is unattended, and an agent that stops to ask
   * permission does not fail — it hangs.
   */
  headlessVerified: boolean;
  /**
   * Whether this agent's approval prompts can be turned off with a flag, which
   * is what an interactive run needs. False where the non-interactive mode is a
   * subcommand instead, so the control is not offered rather than offered and
   * silently doing nothing.
   */
  canSkipPermissions?: boolean;
  /** The argv a fleet would start it with, for the dry-run preview. */
  autonomousInvocation?: string[];

  // Host-side, not from the descriptor:
  /** `~/.config/sandbox/agents/<persistDir>` — present once the agent logged in. */
  auth: { persisted: boolean; path: string; lastSeen: string | null };
  /** Only claude has a status-line hook; the others show nothing, on purpose. */
  statusLine: boolean;
  /** Only claude mounts the host's per-project history bucket. */
  historySync: boolean;
  /** Sessions found in this agent's verified transcript store. */
  sessions: number;
  /** An agent with no verified store descriptor is reported untracked, not guessed. */
  contextStore: "verified" | "untracked" | "empty";
  docs?: string;
}

// ---------------------------------------------------------------------------
// Worktrees
// ---------------------------------------------------------------------------

/**
 * `worktree.Info`. A worktree is addressed by **branch**, never by a directory
 * name derived from one — an agent that runs `git checkout -b` inside its
 * worktree puts the two out of sync.
 */
/** One commit on a branch. Subject and author are text from the repository. */
export interface Commit {
  sha: string;
  shortSha: string;
  subject: string;
  author: string;
  date: string;
  files: number;
  insertions: number;
  deletions: number;
}

export interface Worktree {
  branch: string;
  /** Symlink-resolved, so the string here is the one git reports. */
  path: string;
  head: string;
  repoId: string;
  /** Paths with uncommitted changes, truncated by the daemon. */
  dirty: string[];
  ahead: number;
  behind: number;
  /** The base branch the work is meant to land on. */
  base: string | null;
  /** The run currently working this branch, if one is live. */
  runId: string | null;
  createdAt: string;
  /**
   * Whether the last run on this branch passed its verify. `land` refuses a
   * branch that never did, so the UI has to be able to say which.
   */
  verified: boolean | null;
  /** True when this is the repository's main checkout rather than a worktree. */
  primary?: boolean;
}

// ---------------------------------------------------------------------------
// Usage
// ---------------------------------------------------------------------------

/** `agentusage.Window`. */
export interface UsageWindow {
  kind: "five_hour" | "seven_day";
  label: string;
  /** 0–100. `null` when the cached figure cannot honestly be shown. */
  utilization: number | null;
  resetsAt: string | null;
  /** The model a per-model allowance applies to. Empty covers the account. */
  scope?: string;
}

/**
 * `agentusage.Snapshot`. Two rules travel with it and the UI keeps both:
 * a shape the parser no longer recognises yields **no windows** rather than a
 * zero, and every reading is **aged** — these refresh only when the agent talks
 * to the server, so an unlabelled percentage can be hours stale.
 */
export interface UsageSnapshot {
  agent: AgentName;
  windows: UsageWindow[];
  /** When the *agent* last refreshed from the server, not when we read it. */
  fetchedAt: string | null;
  path: string | null;
}

// ---------------------------------------------------------------------------
// Doctor, config, audit
// ---------------------------------------------------------------------------

export type CheckResult = "pass" | "warn" | "fail" | "unknown";

/**
 * One `sandbox-cli doctor` check. `unknown` is not `pass`: under prod a
 * question that could not be *asked* counts as a failure, because it does not
 * get to assume the answer it would prefer.
 */
export interface DoctorCheck {
  id: string;
  title: string;
  result: CheckResult;
  detail: string;
  /** What this becoming `fail` costs under each profile. */
  underDev: "warn" | "fail";
  underProd: "warn" | "fail";
}

/** Where a resolved setting came from. Precedence, later wins. */
export type ConfigLayer =
  | "default"
  | "profile"
  | "user"
  | "project"
  | "explicit"
  | "flag";

export interface ResolvedField {
  key: string;
  value: string;
  layer: ConfigLayer;
  /** Set when a nearer layer asked for this and was refused as privileged. */
  refusedFrom?: ConfigLayer;
}

export interface ResolvedConfig {
  profile: Profile;
  image: string;
  workdir: string;
  user: string;
  home: string;
  engine: Engine;
  network: NetworkPosture;
  security: SecurityPosture;
  mounts: MountSpec[];
  envAllow: string[];
  persistAuth: boolean;
  sync: boolean;
  fields: ResolvedField[];
  /** The docker argv `runtime.BuildArgs` would emit. Display only. */
  argv: string[];
}

/** One line of `~/.config/sandbox/audit/sessions.jsonl`. */
/** `history.Stats` — the run log aggregated by the daemon. */
export interface HistorySummary {
  total: number;
  decided: number;
  passed: number;
  /** Percent, or null when nothing has been decided. */
  passRate: number | null;
  medianDurationMs: number | null;
  finishedToday: number;
}

/** `history.DayBucket`. */
export interface HistoryDay {
  date: string;
  total: number;
  passed: number;
  failed: number;
  verifyFailed: number;
  stopped: number;
}

export interface HistoryStats {
  stats: HistorySummary;
  days: HistoryDay[];
}

export interface AuditRecord {
  time: string;
  image: string;
  workspace: string;
  workdir: string;
  agent: AgentName | null;
  branch: string | null;
  command: string[];
  engine: Engine;
  network: NetworkMode;
  networkName: string;
  egressEnforcementRequested: EgressEnforcement;
  egressAllow: string[];
  /** By name only. There is nowhere here to put a value, deliberately. */
  envNames: string[];
  exitCode: number;
  durationMs: number;
  detached: boolean;
}

// ---------------------------------------------------------------------------
// Launching
// ---------------------------------------------------------------------------

/** What the Launch screen submits. Mirrors `sandbox.Options`. */
export interface LaunchRequest {
  agent: AgentName | null;
  /** Free command, for a plain `run`. Ignored when `agent` is set. */
  command: string;
  prompt: string;
  workspace: string;
  /** `--worktree <branch>`: addressed by branch, never by directory. */
  worktree: string | null;
  base: string | null;
  profile: Profile;
  network: { mode: NetworkMode; baseline: boolean; allow: string[] };
  memory: string;
  cpus: string;
  detach: boolean;
  /**
   * Start the agent in its interactive mode on a container that keeps a
   * terminal, so `sandbox-cli attach` can answer it. The prompt seeds the first
   * turn rather than being the whole run.
   */
  console: boolean;
  /**
   * Add the agent's skip-permissions flag to a console run, so it works without
   * stopping to ask. Headless runs always have it; an interactive session is
   * where being asked is the point, so here it is opt-in.
   */
  skipPermissions: boolean;
  /** Carry on an existing conversation by its session id, instead of starting one. */
  resume: string | null;
  persistAuth: boolean;
  sync: boolean;
  statusline: boolean;
  verify: string;
  envAllow: string[];
  share: string[];
  publish: string[];
}

export interface LaunchPreview {
  argv: string[];
  /** Host paths this configuration puts in the container's reach. */
  hostPathsInReach: string[];
  /** Refusals `BuildSpec`/`ValidateProfile` would raise for this request. */
  refusals: string[];
  warnings: string[];
}

// ---------------------------------------------------------------------------
// Daemon
// ---------------------------------------------------------------------------

export interface DaemonInfo {
  version: string;
  /**
   * The host directory this daemon manages — one server, one project, the same
   * "which project" question every sandbox-cli invocation answers. The Launch
   * form defaults to it, because the alternative is defaulting to a fixture.
   */
  project?: string;
  engine: Engine;
  engineVersion: string;
  /** The daemon's own view of the host, as `doctor` asks it. */
  host: { os: string; arch: string; cpus: number; memBytes: number };
  profile: Profile;
  /**
   * Whether this daemon requires a bearer token. Reported by /v1/health, which
   * is the one endpoint that answers without one — so it is the only thing a
   * client lacking a token can still ask, and the only way the UI can explain a
   * 401 instead of just showing one on every panel.
   */
  authRequired?: boolean;
  /** True when Studio is reading fixtures because no daemon answered. */
  mock?: boolean;
}

/** One turn of a run's conversation, from the agent's transcript. */
export interface ConversationMessage {
  role: "user" | "assistant";
  text: string;
  at?: string;
}

export interface Conversation {
  messages: ConversationMessage[];
  /** The agent's own id for this conversation, whole rather than abbreviated. */
  sessionId?: string;
  /**
   * The exact line to type on the host to carry this conversation on after the
   * container is gone. Built by the daemon, because the flags that make it work
   * are not guessable from the id — a Studio session lives in the sandbox-owned
   * agent HOME, which the claude wrapper's default history mount hides.
   */
  resume?: string;
  /**
   * Whether this run can be typed at right now: running *and* launched with a
   * console. The daemon decides it, because the two facts behind it (container
   * state, how stdin was created) both live there.
   */
  writable: boolean;
}

/** One conversation that a run can be resumed from. */
export interface SessionSummary {
  id: string;
  title?: string;
  turns: number;
  modified: string;
  /**
   * Listed from the file alone, because there is no verified reader for this
   * agent's transcript format: the id and dates are real, the title and turn
   * count are unknown and shown as unknown rather than as zero.
   */
  partial?: boolean;
}
