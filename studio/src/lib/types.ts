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

/** The twelve adapters in `cli.agentCmds()`. */
export type AgentName =
  | "claude"
  | "codex"
  | "gemini"
  | "opencode"
  | "cline"
  | "goose"
  | "copilot"
  | "cursor"
  | "qwen"
  | "openhands"
  | "devin"
  | "kilocode";

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

  /**
   * The agent that was *asked* for, when routing fell through to a different
   * one; absent when the run used what it was given. `routeReason` says why.
   *
   * Read from the container's labels rather than the run log, because a detached
   * run's audit line is written when it ends — long after somebody looks at the
   * listing and asks why it says codex when they picked claude.
   */
  routedFrom?: string;
  /**
   * The agent whose conversation this run was briefed with, and the session it
   * came from — set when somebody handed the work over, rather than when
   * routing did. Both look like "codex, after claude" in a listing and answer
   * different questions, which is why they are separate fields.
   */
  handoffFrom?: string;
  handoffSession?: string;
  routeReason?: string;

  /**
   * The episode, and where in it.
   *
   * `routeAttempt` is what separates the two kinds of switch, which `routedFrom`
   * alone cannot: attempt 1 is a *preflight* skip — the named agent never ran,
   * so there is no conversation to carry — while attempt 2 or more is a run that
   * failed and had its work handed over with a briefing.
   */
  routeId?: string;
  routeAttempt?: number;

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
  /**
   * Whether a conversation of this agent's can be reopened by id. False for
   * gemini, whose CLI has no resume argv — the conversations panel
   * reads this before offering to carry one on, rather than offering a control
   * the launch would refuse.
   */
  canResume?: boolean;
  /**
   * That flag, verbatim — `--dangerously-skip-permissions`, `--yolo`. From the
   * daemon rather than kept here, so the control can name what it adds without
   * this file becoming a second copy of a security-relevant argv.
   */
  skipPermissionArgs?: string[];
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
  /**
   * Whether the agent reported this as the window currently in force
   * (`limits[].is_active`). `null` when it said nothing — the five_hour and
   * seven_day fields carry no such flag, and rendering "not in force" from a
   * missing one would state the absence of a field as a fact.
   */
  active: boolean | null;
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
  /**
   * Whether the agent that owns this cache is on the daemon's PATH. The numbers
   * are readable without it — the sandbox keeps its own copy of the cache, and
   * the daemon may be running in a container with no claude binary — so having
   * figures and being able to make them current are different questions.
   */
  canRefresh: boolean;
  /**
   * Which file answered: `"statusline"` for the recording written from the
   * hook payload while an agent runs, `"cache"` for Claude Code's own
   * ~/.claude.json. It decides how a newer reading is obtained — driving the
   * agent advances the cache and nothing else — so the refresh control belongs
   * to one of them and not the other.
   */
  source?: "statusline" | "cache";
  /**
   * Whether the file carrying these figures is being written while the reading
   * inside it is not — the agent is running and no longer recording usage
   * there. An old reading on an idle machine is fixed by using the agent; this
   * one cannot be fixed at all, and the two look identical without this flag.
   */
  abandoned?: boolean;
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
  /**
   * Routing, when this run was part of an episode. Runs sharing a `routeId` are
   * one attempt at one task — the agent that failed, and the one that ran
   * instead — which is the only thing that tells a rescue from two unrelated
   * runs.
   */
  routedFrom?: string;
  routeReason?: string;
  routeId?: string;
  routeAttempt?: number;
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
  /**
   * Whether `exitCode` is a result or a placeholder, and the id that pairs a
   * detached run's launch line with the line written when it ended.
   *
   * A detached run has no exit code to wait for, so its launch line carries 0 —
   * and every Studio run is detached. Reading that as success is what made the
   * Routing screen report a 100% rescue rate. The daemon collapses the pair, so
   * a record arriving here with `finished: false` is a run still going, or one
   * whose ending nobody was around to see.
   */
  finished?: boolean;
  runId?: string;
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
  /**
   * Agents to try, in order, when the chosen one's provider is not answering.
   *
   * Studio probes before launching and takes the first that answers. It does not
   * retry a run that failed after starting: a launch here is detached, so
   * nothing is left watching the exit code. The Run that comes back says which
   * agent it actually got.
   */
  fallback: string[];
  /**
   * Another agent's conversation to start *from*, as a briefing rather than a
   * resume. Null for an ordinary launch.
   *
   * Separate from `resume` because they are opposites: resume reopens a
   * conversation with the agent that wrote it, this starts a new one carrying
   * evidence about an old. The daemon refuses them together, so the form never
   * holds both — picking a conversation to hand over clears the resume, and
   * vice versa.
   */
  handoffFrom: { agent: string; sessionId: string } | null;

  /**
   * Which registered repository this run is about, by `Project.id`. Empty means
   * the one the daemon was started in.
   *
   * It is what the daemon is sent, and `workspace` below is what the form
   * displays: an id is resolved against the list of repositories somebody
   * deliberately added, while a path is whatever a screen put in a field. The
   * two used to be one thing, which is how a repository root from a fixture
   * ended up in a real launch request.
   */
  repo: string;
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
  /**
   * Container ports to bind on the daemon's host, in docker's syntax. A bare
   * port binds 127.0.0.1 there — not 0.0.0.0, which is where sandbox-cli
   * deliberately differs from `docker -p`.
   */
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
// Repositories
// ---------------------------------------------------------------------------

/**
 * One repository this daemon will answer about, from `GET /v1/projects`.
 *
 * `id` is what every request names — a repo id from the daemon, never a path a
 * screen assembled. Two clones sharing a directory name do not share an id, and
 * the same id is what containers are labelled with, which is what makes "the
 * runs for this repository" and "the worktrees for this repository" the same
 * question.
 */
export interface Project {
  id: string;
  name: string;
  root: string;
  /**
   * The repository the daemon was started in: what every request naming no repo
   * is about, and the one that cannot be removed. Changing it is a restart
   * (`studio.sh up --project DIR`).
   */
  default?: boolean;
  /**
   * Registered, but not readable right now — the directory is gone, is no
   * longer a git repository, or sits on a volume that is not mounted. Shown
   * rather than hidden: a row the user added that quietly disappeared is worse
   * than one they can see is unavailable.
   */
  missing?: boolean;
}

/** One row of a repository's directory listing, from `GET /v1/files`. */
export interface FileEntry {
  name: string;
  /**
   * Repository-relative and slash-separated — the daemon's own spelling, sent
   * straight back as the next request's `path`. A client that assembled paths
   * itself would be inventing the one string the containment check is about.
   */
  path: string;
  dir?: boolean;
  size?: number;
  /**
   * A symlink, reported rather than followed. Opening one may be refused: a link
   * leaving the repository is not readable through this API, which is what stops
   * an agent-written `notes.md -> ~/.ssh/id_ed25519` from being served.
   */
  symlink?: boolean;
  modifiedAt?: string;
}

export interface FileListing {
  /** The listed directory, repository-relative; "" is the repository root. */
  path: string;
  entries: FileEntry[];
  /** More entries than one listing carries — said out loud, never silently cut. */
  truncated?: boolean;
}

export interface FileContent {
  path: string;
  size: number;
  /** Binary files are reported, never sent. */
  binary?: boolean;
  /** `content` is the first part of a larger file. */
  truncated?: boolean;
  content?: string;
}

/** One directory offered by the folder picker, from `GET /v1/browse`. */
export interface BrowseEntry {
  name: string;
  /** Absolute host path — what `POST /v1/projects` takes. */
  path: string;
  /** Holds a .git. A hint for the picker; the daemon still decides on add. */
  repo?: boolean;
  /** Already managed by this Studio, so adding it again would be a no-op. */
  registered?: boolean;
}

export interface BrowseListing {
  path: string;
  /** The directory above, absent at the filesystem root. */
  parent?: string;
  /** This user's home directory — where the picker starts. */
  home?: string;
  /** Whether `path` itself is a repository, so it can be picked directly. */
  repo?: boolean;
  entries: BrowseEntry[];
  truncated?: boolean;
}

/** One agent's provider, and whether it is answering. From `GET /v1/routing`. */
/**
 * One slot of a provider's uptime strip.
 *
 * Two counts rather than a state, because zero-and-zero is a third thing: the
 * daemon was not running, or was started with `-probe-interval 0`, and nothing
 * was asked. A strip that painted that as "down" would turn every night a laptop
 * was closed into an incident.
 */
export interface ProbeBucket {
  at: string;
  up: number;
  down: number;
  reason?: string;
}

export interface ProviderHistory {
  agent: string;
  buckets: ProbeBucket[];
  /** Fraction of *taken* samples that answered, with the count behind it. */
  uptime?: number;
  samples?: number;
}

export interface ProbeHistory {
  hours: number;
  /** Sampling period in seconds; 0 when the daemon was asked not to probe. */
  interval: number;
  providers: ProviderHistory[];
}

export interface ProviderStatus {
  agent: string;
  /** What was asked. Absent for an agent with nothing to ask. */
  host?: string;
  /** "asked and answered" vs "never asked" — unknown is not down. */
  probed: boolean;
  reachable: boolean;
  /** Why it is unreachable, in a phrase. Also what tells an outage from no network. */
  reason?: string;
  /**
   * The host came from your config rather than the descriptor — which is the
   * only way a provider-agnostic agent like opencode gets probed at all, and the
   * right answer for anyone pointing an agent at a proxy.
   */
  overridden?: boolean;
  /**
   * Whether the override is the one Studio writes, rather than a value in the
   * user's own config.yaml — which outranks it and is not editable from here.
   *
   * The save payload is rebuilt from *this*, never from `overridden`: the
   * endpoint writes a whole map, so building it from every set value copied
   * config.yaml's hosts into Studio's file, where they outlived the config lines
   * they came from.
   */
  managed?: boolean;
  /** Whether a chain may contain it: it needs a verified non-interactive mode. */
  routable: boolean;
}

// ---------------------------------------------------------------------------
// Daemon
// ---------------------------------------------------------------------------

export interface DaemonInfo {
  version: string;
  /**
   * The host directory this daemon was started in — its default project, the
   * one every request naming no repository is about. Other repositories are
   * added at runtime and listed by `GET /v1/projects`; this is the one that
   * cannot be.
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

  /**
   * What a run launched by this daemon may reach, resolved from its own config.
   *
   * Reported rather than requested: the mode is tighten-only from a client, so a
   * form shows this and offers extra domains, never a mode.
   */
  egress?: DaemonEgress;
  /** True when Studio is reading fixtures because no daemon answered. */
  mock?: boolean;
}

/** What a run launched by a daemon may reach, as /v1/health reports it. */
export interface DaemonEgress {
  mode: NetworkMode;
  baseline: boolean;
  /** How many domains the allowlist resolved to. Always present. */
  domains?: number;
  /** The names, for an authenticated caller only — /health answers without a
   *  token, and a list of internal hostnames is not for anything that can open
   *  a socket. */
  allow?: string[];
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
  started?: string;
  /** No verified reader for this format: id and dates are real, the rest unknown. */
  partial?: boolean;
  /**
   * The working directory the transcript recorded. The one field that tells a
   * sandbox conversation from a host one at a glance: a container's cwd is
   * always /workspace, a host session's is the real path.
   */
  project?: string;
  /** Where the transcript lives. Reported, never sent back — requests name an id. */
  path?: string;
  size?: number;
  /**
   * The repository this conversation belongs to, or absent when it cannot be
   * attributed — a session pooled in the shared bucket records only
   * `/workspace`, and nothing on disk says which project that was. Absent is an
   * answer, not a gap: it is why the panel can hide them rather than file them
   * under a repository they may not belong to.
   */
  repoId?: string;
  /** "sandbox" (the agent HOME containers get) or "host" (your own history). */
  store?: "sandbox" | "host";
  /** Only the sandbox-owned store is this daemon's to resume. */
  resumable?: boolean;
}

export interface SessionTranscript {
  session: SessionSummary;
  messages: ConversationMessage[];
}

export interface SessionRaw {
  session: SessionSummary;
  size: number;
  /** `content` is the *tail* of a longer file — the end is what you opened it for. */
  truncated?: boolean;
  content: string;
}
