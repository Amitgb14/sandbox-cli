// Sandbox Studio API — TypeScript contract mirror.
//
// GENERATED from internal/studioapi/types.go by `make contract`. Do not edit:
// the Go types are the contract, their doc comments are its documentation, and
// a change made only here is a claim no server makes. `TestContractMirrorIsInSync`
// fails when this file and those types disagree.
//
// Three things a client must get right, all enforced server-side (see
// internal/studioapi/guard.go, and the trust model in README.md):
//
//   1. Any request carrying a body must send `Content-Type: application/json`.
//      A bodiless POST (e.g. /runs/:id/stop with no options) needs no header.
//   2. Requests must reach the server by a name it answers to — a loopback name
//      by default, plus anything named with `-allow-host`, which is how a
//      proxied deployment's public name is allowed. A page served from another
//      origin must be named in `-cors-origin`, or it is refused outright rather
//      than merely prevented from reading the response.
//   3. With a token configured, send `Authorization: Bearer <token>`. The one
//      exception is the WebSocket log stream, where the browser API cannot set
//      headers: pass `?token=<token>` on that URL only.
//
// Timestamps are ISO-8601 (Go renders time.Time as RFC3339). A field marked
// optional here is one the server omits when empty — absent means absent, and
// never a zero standing in for an answer.

/**
 * ErrorResponse is the body of every non-2xx response.
 */
export interface ErrorResponse {
  error: string;
}

/**
 * EgressPosture is what a run launched by this daemon may reach.
 */
export interface EgressPosture {
  /**
   * Mode is "allowlist", "default" (unrestricted) or "none".
   */
  mode: string;
  /**
   * Baseline reports whether the built-in domains are part of an allowlist.
   */
  baseline: boolean;
  /**
   * Domains is how many the allowlist resolved to. Always present, because a
   * count discloses nothing about which hosts and is most of what a screen
   * renders anyway.
   */
  domains?: number;
  /**
   * Allow is the resolved list — baseline ∪ configured — which is what the
   * firewall is actually programmed with, rather than the configured half a
   * reader would have to add the other half to.
   *
   * Present only for an authenticated caller: see egressPosture.
   */
  allow?: string[];
}

/**
 * HealthResponse answers "is the control plane usable right now".
 */
export interface HealthResponse {
  status: string;  // "ok" | "degraded"
  version: string;
  engine: string;  // "docker" | "podman"
  engineVersion: string;
  dockerAvailable: boolean;
  project: string;  // the host directory this server manages
  profile: string;  // "dev" | "prod"
  /**
   * AuthRequired reports whether this daemon was started with a -token, so a
   * client can say "you need the token" instead of failing every request with
   * a 401 it cannot explain.
   *
   * Health is the one endpoint that answers unauthenticated, which is exactly
   * why the fact belongs here: it is the only thing a client without a token
   * can still ask. It reports *that* a token is required, never any part of
   * the token itself.
   */
  authRequired: boolean;
  /**
   * Egress is the posture this daemon will launch with, resolved from its own
   * config layers.
   *
   * Reported because a client cannot work it out and must not guess. The
   * network mode is **not expressible per request** — a launch may add domains
   * and may not loosen the posture, the same tighten-only rule
   * internal/config/trust.go applies to a project file — so a form that
   * rendered a mode selector was offering a control the request does not have,
   * initialised to a value nobody had asked for. Showing what the daemon *will*
   * do, and where to change it, is the honest version of that field.
   */
  egress: EgressPosture;
  /**
   * Host is what this machine is, as the engine and the Go runtime report it.
   * Always present: a client showing "where am I running" has nowhere to put
   * an absent object, and the zero values are honest — 0 bytes means the
   * engine would not say, which is the same answer `fleet` accepts when it
   * cannot size the host.
   */
  host: HostInfo;
}

/**
 * HostInfo is the daemon's view of the machine it runs on.
 */
export interface HostInfo {
  os: string;
  arch: string;
  cpus: number;
  memBytes: number;
}

/**
 * Project is one repository this control plane will answer about — the unit
 * every branch-addressed request is scoped to.
 *
 * ID, not Root, is what a request names. It is worktree.RepoID, the same id that
 * becomes a container's sandbox.repo label, which is what lets "the runs for
 * this repository" and "the worktrees for this repository" be the same question:
 * two clones sharing a directory name do not share an id, and a path is not
 * something a client is trusted to hand back. See internal/studioapi/projects.go.
 */
export interface Project {
  id: string;
  name: string;
  root: string;
  /**
   * Default marks the repository this daemon was started in — the one every
   * request that names no repo is about. Exactly one project carries it, and it
   * is the one that cannot be removed.
   */
  default?: boolean;
  /**
   * Missing reports a repository that is registered but cannot be read right
   * now: the directory is gone, is no longer a git repository, or sits on a
   * volume that is not mounted. Listed rather than dropped, because an absent
   * checkout is not the same as one the user never asked for — and a client
   * should show it greyed out rather than silently lose the row.
   */
  missing?: boolean;
}

/**
 * FileEntry is one row of a directory listing.
 *
 * Path is repository-relative and slash-separated, so a client feeds it straight
 * back as the next request's `path` without assembling anything itself — and
 * never learns a host path it did not already have from Project.Root.
 */
export interface FileEntry {
  name: string;
  path: string;
  dir?: boolean;
  size?: number;
  /**
   * Symlink marks a link rather than resolving it. It is reported because
   * opening one may well be refused: a link leaving the repository is not
   * readable through this API, which is the rule that keeps an agent-written
   * `notes.md -> ~/.ssh/id_ed25519` from being served over loopback.
   */
  symlink?: boolean;
  modifiedAt?: string;
}

/**
 * FilesResponse is the body of GET /files.
 */
export interface FilesResponse {
  /**
   * Path is the listed directory, repository-relative; "" is the root.
   */
  path: string;
  entries: FileEntry[];
  /**
   * Truncated reports a directory with more entries than one listing carries.
   * Said out loud rather than silently cut: a listing that stops without
   * saying so reads as "this is everything".
   */
  truncated?: boolean;
}

/**
 * FileContentResponse is the body of GET /files/content.
 */
export interface FileContentResponse {
  path: string;
  size: number;
  /**
   * Binary files are reported, never sent: their bytes rendered as text are
   * noise, and the size is the useful fact about them.
   */
  binary?: boolean;
  /**
   * Truncated reports that Content is the first part of a larger file.
   */
  truncated?: boolean;
  content?: string;
}

/**
 * BrowseEntry is one directory offered by the folder picker.
 *
 * Names and a path, and nothing else: no size, no modification time, no
 * contents. See internal/studioapi/browse.go for why this endpoint is
 * deliberately not a file browser.
 */
export interface BrowseEntry {
  name: string;
  path: string;
  /**
   * Repo marks a directory holding a .git — a hint, so the picker can point at
   * what is worth adding. POST /projects still decides.
   */
  repo?: boolean;
  /**
   * Registered marks a repository this Studio already manages, so the picker
   * can say so instead of letting somebody add it twice.
   */
  registered?: boolean;
}

/**
 * BrowseResponse is the body of GET /browse.
 */
export interface BrowseResponse {
  /**
   * Path is the directory being listed, absolute and symlink-resolved.
   */
  path: string;
  /**
   * Parent is the directory above, or "" at the filesystem root.
   */
  parent?: string;
  /**
   * Home is this user's home directory — where a picker should start, and the
   * one shortcut it can offer without guessing.
   */
  home?: string;
  /**
   * Repo reports whether Path itself is a repository, so "Use this folder" can
   * be offered for the directory you are standing in.
   */
  repo?: boolean;
  entries: BrowseEntry[];
  truncated?: boolean;
}

/**
 * ProviderStatus is one agent's provider, and whether it is answering.
 */
export interface ProviderStatus {
  agent: string;
  /**
   * Host is what was asked, empty for an agent with nothing to ask — opencode
   * is provider-agnostic, and an agent behind a proxy is not talking to the
   * vendor at all.
   */
  host?: string;
  /**
   * Probed distinguishes "asked and answered" from "never asked". Unknown is
   * not down: an unprobeable agent still works, it simply cannot be skipped in
   * advance.
   */
  probed: boolean;
  reachable: boolean;
  /**
   * Reason is why an unreachable provider is unreachable, in a phrase: "timed
   * out", "provider answered 503". It is also what tells an outage from a
   * laptop with no network, which this cannot distinguish on its own.
   */
  reason?: string;
  /**
   * Overridden reports a host the user chose rather than the one compiled into
   * the descriptor — which is the only way opencode gets probed at all, and the
   * right answer for anyone pointing an agent at a proxy.
   */
  overridden?: boolean;
  /**
   * Managed says the override came from the file Studio writes, rather than
   * from the user's own config.yaml — which outranks it and cannot be edited
   * from here.
   *
   * The distinction is not cosmetic: a client that rebuilds its save payload
   * from every overridden row copies config.yaml's values into Studio's file,
   * where they then persist after the config lines are deleted, and an edit to
   * an agent config.yaml also names appears to save and silently reverts on the
   * next daemon start. A row that is overridden but not managed is read-only,
   * and saying so is the only honest thing this API can do about a layer it
   * does not own.
   */
  managed?: boolean;
  /**
   * Routable is whether a chain may contain this agent at all — it needs a
   * verified non-interactive mode, or it would hang in the fallback slot where
   * nobody is looking.
   */
  routable: boolean;
}

/**
 * ProvidersRequest is the body of POST /routing/providers: the host to probe per
 * agent. An empty value is an explicit "do not probe this one".
 */
export interface ProvidersRequest {
  providers: Record<string, string>;
}

/**
 * ProbeBucket is one slot of a provider's uptime strip: how many probes in that
 * span answered and how many did not.
 *
 * Both counts rather than a state, because zero-and-zero is a third thing: the
 * daemon was not running, or was started with probing off, and nothing was
 * asked. A bucket that reported "down" for that would turn every night a laptop
 * was closed into an incident.
 */
export interface ProbeBucket {
  at: string;
  up: number;
  down: number;
  reason?: string;
}

/**
 * ProviderHistory is one agent's strip.
 */
export interface ProviderHistory {
  agent: string;
  buckets: ProbeBucket[];
  /**
   * Uptime is the fraction of *taken* samples that answered, and Samples is how
   * many there were. The pair travels together on purpose: 100% of two samples
   * is not the claim 100% of six hundred is, and a percentage with no count
   * behind it invites reading the first as the second.
   */
  uptime?: number;
  samples?: number;
}

/**
 * ProbeHistoryResponse is the body of GET /routing/history.
 */
export interface ProbeHistoryResponse {
  hours: number;
  /**
   * Interval is the sampling period in seconds, 0 when probing is off. A client
   * needs it to say what a gap means — with no prober running, every gap is
   * simply "not collected" rather than anything about the provider.
   */
  interval: number;
  providers: ProviderHistory[];
}

/**
 * RoutingResponse is the body of GET /routing.
 */
export interface RoutingResponse {
  providers: ProviderStatus[];
}

/**
 * ProjectsResponse is the body of GET /projects.
 */
export interface ProjectsResponse {
  projects: Project[];
}

/**
 * ProjectCreateRequest is the body of POST /projects, and the only place in this
 * contract where a client hands over a host path. Every refusal that applies to
 * a directory Studio will touch is applied here, once, so that every other
 * endpoint can take an id and be done.
 */
export interface ProjectCreateRequest {
  /**
   * Path is an absolute host directory inside the git repository to add. It is
   * resolved to the repository *root* before being recorded: Studio addresses
   * work by branch, and a branch belongs to a repository rather than to
   * whichever subdirectory somebody happened to type.
   */
  path: string;
}

/**
 * ProjectCloneRequest is the body of POST /projects/clone.
 *
 * The one request in this API that makes the daemon write to the host filesystem
 * and run a program, which is why the handler's refusals are the substance of it
 * — see internal/studioapi/clone.go.
 */
export interface ProjectCloneRequest {
  /**
   * URL is the repository to clone. https, ssh, or git@host:path; everything
   * else is refused, `ext::` above all, because it executes a command rather
   * than fetching a repository.
   */
  url: string;
  /**
   * Parent is the absolute directory to clone *into*. It must exist and pass
   * the same refusals a typed project path does.
   */
  parent: string;
  /**
   * Name is the directory to create inside it. Empty takes git's own answer:
   * the last path segment without .git.
   */
  name?: string;
}

/**
 * AgentInfo describes one agent adapter sandbox-cli knows how to launch
 * headlessly. Only agents with a verified non-interactive mode are ever listed —
 * see internal/agents' package doc — because a Studio-launched run is always
 * detached, and an agent that stops to ask permission would just hang.
 */
export interface AgentInfo {
  name: string;
  label: string;
  persistDir: string;
  envAllow: string[];
  /**
   * Env is what sandbox-cli itself sets in the container for this agent — the
   * keyring droid must not look for, and so on. Distinct from EnvAllow, which
   * is host values forwarded by *name* only if the host has them.
   */
  env: string[];
  /**
   * HeadlessVerified is true for every agent listed here, and saying so
   * explicitly is not redundant: internal/agents only registers adapters with a
   * confirmed non-interactive argv (TestEveryAgentHasAVerifiedHeadlessArgv is
   * where that stops being a convention), because a Studio-launched run is
   * always detached and an agent that stops to ask permission does not fail —
   * it hangs. A client that cannot see the flag cannot warn about the agents
   * that would, so the field is sent rather than inferred from membership.
   */
  headlessVerified: boolean;
  /**
   * CanSkipPermissions is whether this agent's approval prompts can be turned
   * off with a *flag*, which is what an interactive run would need. False for
   * the agents whose non-interactive mode is a subcommand instead (`codex
   * exec`, `opencode run`, `droid exec`): there is nothing to add to a console
   * session, and a control that silently did nothing would be worse than one
   * that is not offered.
   */
  canSkipPermissions: boolean;
  /**
   * SkipPermissionArgs is that flag, verbatim — `--dangerously-skip-permissions`,
   * `--yolo`. Sent rather than left for the client to know, because a control
   * that turns off an agent's approval prompts should be able to name what it
   * adds, and a UI carrying its own copy of two flag strings is a second
   * definition of a security-relevant argv. Empty exactly when
   * CanSkipPermissions is false.
   */
  skipPermissionArgs?: string[];
  /**
   * CanResume is whether a conversation of this agent's can be reopened by its
   * native session id — `claude --resume`, `codex resume`, `opencode --session`.
   * **Gemini and droid declare none**, so for them "carry this conversation on"
   * is not expressible at all, and the only honest continuation is a fresh run
   * with a briefing.
   *
   * Sent rather than inferred for the same reason CanSkipPermissions is: a
   * client that cannot see the flag cannot warn about the agents that lack it,
   * and a picker offering "resume" where the argv has no way to say it would be
   * offering a control the request does not have. Read from internal/agentctx's
   * store table — the same table resumeArgsFor consults when the run is built,
   * so the offer and the launch cannot disagree.
   */
  canResume: boolean;
  /**
   * AutonomousInvocation is the argv a fleet task or a detached run would start
   * this agent with, prompt elided — the same string `fleet run --dry-run`
   * prints, so a launch preview and a dry run cannot disagree about what is
   * about to happen.
   */
  autonomousInvocation: string[];
  /**
   * Delivery is how the binary reaches the container. Four adapters are baked
   * into the base image and the rest are installed lazily into the persisted
   * HOME on first use — baking every adapter would put hundreds of megabytes in
   * front of every user for agents most will never run. This is a fact about
   * assets/Dockerfile rather than about the descriptor, which is why it is a
   * list here and cannot be read off agents.Descriptor.
   */
  delivery: string;  // "baked" | "npm"
  /**
   * Auth reports whether this agent has logged in yet: the sandbox-owned HOME
   * mounted for it, whether it exists, and when it last changed. Never its
   * contents — the persisted directory holds an OAuth refresh token.
   */
  auth: AgentAuth;
  /**
   * StatusLine and HistorySync are true for claude alone, and that is a
   * deliberate limit rather than an oversight: no other agent has a status-line
   * hook, and only claude mounts the host's per-project history bucket.
   */
  statusLine: boolean;
  historySync: boolean;
  /**
   * Sessions and ContextStore come from the persisted record of what has
   * actually been confirmed on this machine. An agent with no verified
   * descriptor is reported untracked rather than guessed at.
   */
  sessions: number;
  contextStore: string;  // "verified" | "empty" | "missing" | "untracked"
}

/**
 * UsageWindow is one subscription window: how much of it is spent, and when it
 * resets.
 *
 * Utilization is a pointer because absent and zero are different answers, and
 * this is the field where confusing them matters most. A window past its reset
 * has a cached figure describing the period that already ended, so it reports
 * null — "we cannot honestly say" — rather than a number that is merely wrong.
 */
export interface UsageWindow {
  kind: string;  // "five_hour" | "seven_day"
  label: string;  // for display: "5-hour", "Weekly"
  utilization?: number;
  resetsAt?: string;
  scope?: string;  // the model a per-model allowance covers
  /**
   * Active is whether the agent reported this window as the one currently in
   * force. Null when it said nothing — a window described only by the
   * five_hour/seven_day fields carries no such flag, and rendering "not in
   * force" from a missing one would state the absence of a field as a fact.
   */
  active?: boolean;
}

/**
 * UsageSnapshot is one reading of an agent's usage cache.
 *
 * FetchedAt is when the *agent* last refreshed these numbers from the server,
 * not when this server read the file — and it is always sent, because these
 * figures refresh only when the agent talks to the server and an unlabelled
 * percentage can be hours stale.
 */
export interface UsageSnapshot {
  agent: string;
  windows: UsageWindow[];
  /**
   * CanRefresh is whether the agent that owns this cache is on this machine's
   * PATH. The figures are readable without it — they come from a file, and the
   * sandbox keeps its own copy — so "there are numbers" and "they can be made
   * current" are different questions, and a client that offered a refresh it
   * cannot perform would be answering the second with the first.
   */
  canRefresh: boolean;
  fetchedAt?: string;
  path?: string;
  /**
   * Source is which kind of file answered: "statusline" for the recording
   * sandbox-statusline writes from the hook payload, "cache" for Claude Code's
   * own ~/.claude.json. A client needs it to say how a newer reading is
   * obtained — driving the agent advances the cache and nothing else, so a
   * refresh control belongs to one of these and not the other.
   */
  source?: string;
  /**
   * Abandoned reports that the file carrying these figures is being written
   * while the reading inside it is not — the agent is running and no longer
   * recording usage there.
   *
   * The distinction matters because the remedies are opposite. An old reading
   * on an idle machine is fixed by using the agent, or by the refresh button.
   * An abandoned one cannot be fixed at all: refreshing drives the agent, the
   * agent rewrites the file, and the reading stays where it was. A client that
   * cannot tell them apart offers a button that does nothing, which is what
   * this field exists to stop.
   */
  abandoned?: boolean;
}

/**
 * DoctorCheck is one host property, as `sandbox-cli doctor` reports it.
 *
 * UnderDev and UnderProd both travel because the same fact means different
 * things to the two profiles — a control the host cannot provide warns under
 * dev and refuses under prod — and a reader deciding whether this machine is
 * ready for unattended work should not have to switch profiles and ask again.
 */
export interface DoctorCheck {
  id: string;
  title: string;
  result: string;  // "pass" | "warn" | "fail" | "unknown"
  detail: string;
  remedy?: string;
  underDev: string;  // "warn" | "fail"
  underProd: string;  // "warn" | "fail"
}

/**
 * DoctorResponse is the body of GET /v1/doctor.
 */
export interface DoctorResponse {
  profile: string;
  checks: DoctorCheck[];
}

/**
 * AgentAuth is where an agent's login is persisted, and whether it is there yet.
 */
export interface AgentAuth {
  persisted: boolean;
  path: string;
  lastSeen?: string;  // RFC3339, or absent when never
}

/**
 * AgentsResponse is the body of GET /agents.
 */
export interface AgentsResponse {
  agents: AgentInfo[];
}

/**
 * RunKind separates a fleet task from a run someone (or Studio) started directly
 * — the same distinction `sandbox-cli list`'s KIND column makes, carried into the
 * API for the same reason: a client deciding what it may stop or reap needs it.
 */
export type RunKind =
  "interactive" |
  "fleet";

/**
 * RunState mirrors the docker container states runtime.ContainerInfo reports,
 * spelled out as a closed set so a TypeScript client can switch over them
 * exhaustively instead of guessing at docker's vocabulary.
 */
export type RunState =
  "created" |
  "running" |
  "paused" |
  "restarting" |
  "exited" |
  "dead" |
  "unknown";

/**
 * Run is a container sandbox-cli started, addressed the same way
 * `sandbox-cli list`/`kill`/`logs` addresses one: by id, name, or branch. It is
 * assembled from runtime.ContainerInfo plus the sandbox.* labels stamped on the
 * container — never re-derived, since docker is the state store.
 */
export interface Run {
  id: string;  // short id (12 chars) — what the rest of the API accepts back
  containerId: string;  // full id
  name: string;
  kind: RunKind;
  state: RunState;
  exitCode?: number;  // set once State is "exited"
  detached: boolean;
  /**
   * RepoID is `sandbox.repo`: worktree.RepoID, an id and not a path.
   *
   * Spelled `repoId` to match Worktree, and that is a fix rather than a
   * preference. This field was `repo` while Worktree's was `repoId` — one fact
   * under two names in one contract — and Studio was written against the other
   * spelling, so filtering runs by repository compared every row against
   * `undefined` and quietly produced nothing. It looked like an empty
   * repository rather than like a broken field, which is why it survived: the
   * only way to see it was to select a repository that definitely had runs.
   */
  repoId?: string;
  /**
   * RoutedFrom is the agent that was asked for, when routing fell through to a
   * different one; empty when the run used what it was given. RouteReason says
   * why. Read from the container's labels rather than from the audit log,
   * because a detached run's audit line is written when it *ends* — long after
   * somebody looks at the listing and asks why it says codex.
   */
  routedFrom?: string;
  routeReason?: string;
  /**
   * RouteID is the episode, and RouteAttempt the position in it — 1 for the
   * agent first asked for, 2 for the one the supervisor started after it
   * failed. Both from labels, for the reason above.
   *
   * The attempt is what separates the two kinds of switch, which look identical
   * through RoutedFrom alone: a *preflight* skip is attempt 1 and carries no
   * conversation, because the agent it names never ran, while attempt 2 or more
   * is a run that failed and handed its work over with a briefing. Telling a
   * user "it did not inherit the conversation" about the second case is simply
   * untrue.
   */
  routeId?: string;
  routeAttempt?: number;
  /**
   * HandoffFrom is the agent whose conversation this run was briefed with, and
   * HandoffSession the session it came from. Empty for a run that started from
   * its own prompt.
   *
   * Read from labels, and reported separately from RoutedFrom even though a
   * failover sets both: routing says a provider stopped answering, a handoff
   * says a person chose. A listing that collapsed them would answer "why is
   * codex doing this" with the wrong story half the time.
   */
  handoffFrom?: string;
  handoffSession?: string;
  /**
   * RepoName is the display half of that id. Two clones of a same-named repo
   * share it and do not share RepoID, so it is for showing and never for
   * matching — which is exactly the mistake this pair exists to keep separate.
   */
  repoName?: string;
  branch?: string;
  base?: string;
  agent?: string;
  verify?: string;
  /**
   * Profile is the posture the run was launched under, and Prompt what the
   * agent was asked to do. Both are read from labels stamped at launch: a
   * container says what confinement it got but not which profile chose it, and
   * a prompt otherwise survives only inside an agent-specific argv. Absent for
   * containers started before either label existed, which is why both omit
   * rather than send an empty string.
   */
  profile?: string;
  prompt?: string;
  createdAt: string;
  startedAt?: string;
  finishedAt?: string;
  /**
   * OpenStdin/TTY say what `attach` could do with this run — see
   * runtime.ContainerInfo. A Studio-launched run always has both false: it is
   * always detached.
   */
  openStdin: boolean;
  tty: boolean;
  /**
   * What the run actually was, read back off the container rather than
   * re-derived from a config file that may have been edited since. The labels
   * above say what the launcher *intended*; these say what docker gave it,
   * which is the question someone reviewing a finished run is asking.
   */
  image: string;
  command: string[];
  workdir: string;
  workspace: string;  // the host path mounted at /workspace
  engine: string;
  durationMs?: number;
  mounts: RunMount[];
  network: RunNetwork;
  security: RunSecurity;
  /**
   * EnvNames is names only, never values. The credential broker exists to keep
   * secret values off the argv and out of config files; an API response is one
   * more file, in a browser's cache. internal/audit makes the same trade and
   * has nowhere to put a value on purpose.
   */
  envNames: string[];
}

/**
 * RunMount is one host path a run could reach. Named host/container/mode rather
 * than docker's source/destination/rw because that is the vocabulary the rest of
 * sandbox-cli uses — a `mounts:` entry in a config file reads the same way.
 */
export interface RunMount {
  host: string;
  container: string;
  mode: string;  // "ro" | "rw"
  origin?: string;
}

/**
 * RunNetwork is the egress posture this container actually ran with, read back
 * off the container rather than from the config that asked for it.
 *
 * Allow is the *resolved* list — baseline ∪ configured — because that is what
 * the entrypoint was handed and therefore what the firewall and proxy actually
 * enforce. Baseline says whether the built-in set is part of it, which is the
 * difference between "these nine hosts plus mine" and "only mine".
 */
export interface RunNetwork {
  mode: string;  // "default" | "none" | "allowlist"
  baseline: boolean;
  allow: string[];
  networkName?: string;
  /**
   * Enforcement names how the allowlist is applied, and null when there is no
   * allowlist at all. "name" means the in-container proxy decided on the
   * hostname; "address" would mean IP rules alone, which cannot tell two hosts
   * sharing an address apart.
   */
  enforcement?: string;
  ingressPorts?: number[];
}

/**
 * RunSecurity is the confinement docker applied, read back rather than assumed.
 */
export interface RunSecurity {
  noNewPrivileges: boolean;
  capDrop: string[];
  capAdd: string[];
  pidsLimit: number;
  /**
   * Memory and CPUs are the strings a config file would carry ("2g", "1.5"),
   * empty when unlimited — not "0", which reads as a limit of nothing.
   */
  memory: string;
  cpus: string;
  seccomp: string;
  user: string;
  /**
   * Hardening is whether the confinement this tool applies by default is
   * actually in force, so a client can say "this run was hardened" without
   * re-deriving the rule from four fields.
   */
  hardening: boolean;
  /**
   * Runtime is the OCI runtime the engine reported for this container
   * ("runc", "runsc", "kata-runtime", …), empty when the engine named none.
   * StrongerIsolation says whether that runtime gives the container a kernel
   * of its own — a separate field because an unrecognised name is shown and
   * deliberately not characterised, and a client should not have to keep its
   * own copy of that list to find out.
   */
  runtime?: string;
  stronger_isolation: boolean;
}

/**
 * LogLine is one line of a run's output, as GET /runs/{id}/logs returns it
 * without follow.
 *
 * Stream is kept rather than merged because which one a line came from is how a
 * reader tells the agent's own output from the egress proxy's DENY lines
 * interleaved with it — and TS is empty when docker recorded none, not a
 * substituted "now", since a log's value is that it says what happened when.
 */
export interface LogLine {
  seq: number;
  ts: string;
  stream: string;  // "stdout" | "stderr"
  text: string;
}

/**
 * MetricSample is one reading of a running container's resource use, in the
 * units a chart wants: bytes and percentages, with the time it was taken.
 */
export interface MetricSample {
  t: string;
  cpuPct: number;
  memBytes: number;
  memLimitBytes: number;  // 0 means unlimited
  netRxBytes: number;
  netTxBytes: number;
  blockReadBytes: number;
  blockWriteBytes: number;
  pids: number;
}

/**
 * MetricSeries is the body of GET /runs/{id}/metrics.
 *
 * A series of one, for now: docker reports what a container is using *now* and
 * keeps no history, so anything longer has to be accumulated by a client that
 * stays connected to ?stream=1. Shaped as a series anyway because that is what
 * the reading is — a point on a chart — and a client should not have to change
 * its type when a second point arrives.
 */
export interface MetricSeries {
  runId: string;
  samples: MetricSample[];
  peak: MetricPeak;
}

/**
 * MetricPeak is the high-water mark over the samples, which is what the CLI's
 * footer summary prints when a run ends.
 */
export interface MetricPeak {
  cpuPct: number;
  memBytes: number;
}

/**
 * AuditRecord is one line of the run log: what ran, how it was confined, and how
 * it ended.
 *
 * EnvNames carries names and never values, which is the rule the log itself
 * keeps — the credential broker exists so secret values stay off the argv and
 * out of files, and this is one more file.
 */
export interface AuditRecord {
  time: string;
  /**
   * RepoID is which repository this run belonged to, derived from Workspace
   * rather than recorded — the log predates repositories being plural and has
   * no such field. Empty means "no repository this daemon knows about", which
   * is a true statement about a run in a checkout nobody registered.
   * See repoIDForWorkspace.
   */
  repoId?: string;
  /**
   * RunID identifies the container, and Finished says whether the outcome below
   * is a result or a placeholder.
   *
   * A detached run is written twice — once when it launches, once when it ends —
   * because at launch there is no exit code to wait for. Two lines, one run: a
   * client that counted them as two would double every Studio run in every
   * total on every screen, so `GET /v1/audit` collapses the pair and keeps the
   * finished half.
   */
  runId?: string;
  finished?: boolean;
  /**
   * Routing, when this run was part of an episode. Runs sharing a RouteID are
   * one attempt at one task — the agent that failed and the one that ran
   * instead — which is the only way to tell a rescue from two unrelated runs.
   */
  routedFrom?: string;
  routeReason?: string;
  routeId?: string;
  routeAttempt?: number;
  image: string;
  workspace: string;
  workdir: string;
  agent?: string;
  branch?: string;
  command: string[];
  engine: string;
  network: string;
  networkName: string;
  /**
   * EgressEnforcementRequested is named for a *request* rather than an
   * outcome, because that is all the host can honestly know: the container
   * programs its own firewall, and this says what it was asked to do.
   */
  egressEnforcementRequested?: string;
  egressAllow: string[];
  envNames: string[];
  exitCode: number;
  durationMs: number;
  detached: boolean;
}

/**
 * HistoryStatsResponse is the body of GET /v1/stats/history: what the run log
 * says about outcomes, aggregated in the index rather than in the client.
 */
export interface HistoryStatsResponse {
  stats: Stats;
  days: DayBucket[];
}

/**
 * AuditResponse is the body of GET /v1/audit.
 */
export interface AuditResponse {
  records: AuditRecord[];
}

/**
 * DiffFile is one file's change in a run's work.
 *
 * Hunks are empty for now and that is stated rather than hidden: this reports
 * *what* changed and by how much, which is the question a reviewer asks first,
 * and rendering the content is a second call the client can make against git
 * itself. An empty list is not a claim that the file has no content.
 */
export interface DiffFile {
  path: string;
  previousPath?: string;
  status: string;  // "added" | "modified" | "deleted" | "renamed"
  insertions: number;
  deletions: number;
  binary?: boolean;
  hunks: DiffHunk[];
}

/**
 * DiffHunk is a contiguous run of changed lines.
 */
export interface DiffHunk {
  header: string;
  lines: DiffLine[];
}

/**
 * DiffLine is one line of a hunk.
 */
export interface DiffLine {
  kind: string;  // "add" | "del" | "ctx" | "meta"
  oldNo?: number;
  newNo?: number;
  content: string;
}

/**
 * ResolvedConfig is the configuration a run actually got, read off its
 * container.
 */
export interface ResolvedConfig {
  profile: string;
  image: string;
  workdir: string;
  user: string;
  home: string;
  engine: string;
  network: RunNetwork;
  security: RunSecurity;
  mounts: RunMount[];
  envAllow: string[];
  persistAuth: boolean;
  sync: boolean;
  /**
   * Fields is the layered provenance — which of default/user/project/flag
   * supplied each value. Empty here, deliberately: a container records the
   * resolved answer and not the layers behind it, and a guessed layer is worse
   * than none when the entire point of the view is to say where a value came
   * from.
   */
  fields: ResolvedField[];
  /**
   * Argv is what the container was started with. Display only.
   */
  argv: string[];
}

/**
 * ResolvedField is one setting and the layer that supplied it.
 */
export interface ResolvedField {
  key: string;
  value: string;
  layer: string;
  refusedFrom?: string;
}

/**
 * Commit is one commit on a branch.
 *
 * Subject and Author are text from the repository, exactly like a branch name:
 * render them, never interpret them.
 */
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

/**
 * CommitsResponse is the body of GET /v1/worktrees/{branch}/commits.
 */
export interface CommitsResponse {
  base: string;
  commits: Commit[];
}

/**
 * RunsResponse is the body of GET /runs.
 */
export interface RunsResponse {
  runs: Run[];
}

/**
 * RunCreateRequest is the body of POST /runs.
 *
 * It always launches detached: an HTTP request/response cycle has nowhere to
 * hold an interactive terminal, so unlike the CLI's `run` there is no foreground
 * mode here — every Studio run is what `sandbox-cli run --detach` or a fleet task
 * would produce. The one variation is Console, which asks for a run somebody
 * intends to attach to and type at; it changes the agent's argv and the
 * container's stdin, and nothing about what either can reach.
 */
export interface RunCreateRequest {
  /**
   * Repo names which registered repository this run is about, by id from GET
   * /projects. Empty means the repository this daemon was started in.
   *
   * This is the field a UI should send, and the difference from Project below
   * is the trust model rather than convenience: an id is resolved against the
   * registry — a list of directories somebody deliberately added — while a path
   * is a directory named by whoever composed the request. With no worktree, the
   * repository root is itself the workspace; with one, the worktree is resolved
   * inside this repository.
   */
  repo?: string;
  /**
   * Project is a host directory to mount at /workspace. Defaults to the
   * server's configured project root. Mutually exclusive with Worktree and
   * with Repo.
   *
   * It predates the registry and is kept for callers that are not a browser —
   * a script that already knows the path it means. Prefer Repo: it is the one
   * that cannot name a directory nobody registered.
   */
  project?: string;
  /**
   * Worktree, when set, resolves (creating if needed) a git worktree for this
   * branch under sandbox-cli's managed worktree directory and mounts *that* as
   * the workspace instead — the same mechanism `--worktree` and `fleet` use, so
   * several runs can work in parallel without colliding. Branch defaults to
   * this value when Branch is empty.
   */
  worktree?: string;
  /**
   * Fallback are the agents to try, in order, when Agent's provider is not
   * answering — the chain from internal/routing.
   *
   * Two mechanisms, as in the CLI. The daemon probes before launching and takes
   * the first agent that answers — the Run it answers with says which one that
   * was. And a launch with somewhere left to fall through to is *supervised*:
   * when it exits non-zero having left the workspace untouched, the next agent
   * is started with a briefing of the conversation so far. See supervisor.go
   * for the two limits that carries.
   */
  fallback?: string[];
  /**
   * Agent is one of the names from GET /agents. Required unless Command is set.
   * When set, Prompt is run through the agent's autonomous/headless mode,
   * unless Console asks for the interactive one.
   */
  agent?: string;
  prompt?: string;
  /**
   * HandoffFrom starts this run with a briefing built from another
   * conversation — the answer to "my claude conversation, run it via codex".
   *
   * It is **not** a resume and the two are refused together. A session id is a
   * primary key into one vendor's private store and the schemas differ
   * entirely, so handing claude's id to codex cannot work; what crosses instead
   * is internal/handoff's export — HANDOFF.md, a vendor-neutral
   * transcript.jsonl, and a files.md derived from git — mounted read-only, with
   * a prompt that tells the target it is reading a briefing rather than its own
   * history. docs/proposals/shared-context.md argues why the other direction is
   * refused: an agent told it is resuming answers as though a fabricated
   * history were its own, confidently, with file-writing tools.
   *
   * The source agent may be the *same* agent, and that is not a degenerate
   * case: gemini and droid declare no resume argv, so a briefing from itself is
   * the only way to carry one of their conversations on.
   */
  handoffFrom?: HandoffRef;
  /**
   * Console starts the agent in its *interactive* mode on a container that
   * keeps a pty and stdin open, so `sandbox-cli attach` from any terminal can
   * answer it. Prompt, when set, seeds the first turn instead of being the
   * whole run.
   *
   * It is one field for both halves deliberately. A console without the
   * interactive argv is a keyboard wired to a headless agent that will never
   * ask anything; the interactive argv without a console is an agent waiting on
   * stdin that does not exist. Neither half is useful alone, so neither is
   * separately requestable.
   *
   * Refused with Verify: verify's exit code is the answer it exists to give,
   * and an interactive session's exit code is whenever the person quit.
   */
  console?: boolean;
  /**
   * SkipPermissions turns off the agent's approval prompts on a console run.
   *
   * Headless runs always have it — an agent that stops to ask does not fail,
   * it hangs — but a console run is one somebody is attached to, where being
   * asked is the point. So it is opt-in here, for the case where you want to
   * watch a run that does not wait for you. The container is the blast-radius
   * boundary either way; this changes what the agent asks, not what it can
   * reach.
   *
   * Only meaningful for agents whose non-interactive mode is a flag rather
   * than a subcommand (claude, gemini).
   */
  skipPermissions?: boolean;
  /**
   * Resume carries on an existing conversation instead of starting one, by
   * the agent's own session id. Requires Console: resuming is something you
   * do interactively, and a headless resume would replay one prompt into an
   * old conversation and exit.
   */
  resume?: string;
  /**
   * Command is a plain guest argv, for a run with no agent (mutually exclusive
   * with Agent).
   */
  command?: string[];
  /**
   * Branch/Base become the sandbox.branch/sandbox.base labels. Branch defaults
   * to Worktree (or the project's current git branch) when empty.
   */
  branch?: string;
  base?: string;
  /**
   * Verify is a shell command run after the agent; its exit code becomes the
   * container's, same as a fleet task's `verify:`.
   */
  verify?: string;
  image?: string;
  memory?: string;
  cpus?: string;
  /**
   * Allow adds egress domains and switches the allowlist on for this run, same
   * as --allow.
   */
  allow?: string[];
  /**
   * Env sets literal KEY=VALUE pairs in the container. Reserved control
   * variables (config.IsReservedEnv) are refused, same as the CLI.
   */
  env?: Record<string, string>;
  /**
   * Publish binds container ports on the daemon's host, in docker's syntax —
   * "8000", "8080:8000", "0.0.0.0:8000:8000" — so an agent's dev server can be
   * opened in a browser.
   *
   * A bare port binds **127.0.0.1**, which is where sandbox-cli deliberately
   * differs from `docker -p`: you asked to see the port from your machine, not
   * to serve it to the network. Writing an address out still does exactly what
   * it says.
   *
   * This is the one launch option that opens a way *in*, so it is worth being
   * clear about who may ask. A project `.sandbox.yaml` may not — trust.go
   * refuses `ports:` with the reasoning that declaring a dev-server port is a
   * real use but a decision about the boundary, so it belongs to the user. A
   * request carrying this *is* the user, driving their own daemon, which is the
   * same act as typing `--publish`. What it is not is a repository choosing for
   * them.
   *
   * Under an allowlist the firewall's default-deny INPUT chain gains a carve-out
   * for exactly these ports (SANDBOX_INGRESS_PORTS), which is what makes a
   * published port reachable at all.
   *
   * That variable is also where RunNetwork.IngressPorts is read from, so a run
   * on an *unrestricted* daemon publishes its ports and reports none — there
   * was no inbound chain to carve, so nothing recorded them. Worth knowing
   * before reading an empty field as "nothing was published".
   */
  publish?: string[];
}

/**
 * HandoffRef names the conversation a run is briefed with: the agent that held
 * it, and its session id from GET /agents/{agent}/sessions.
 *
 * By id, never by path — the same rule the projects registry and the session
 * endpoints keep. SessionSummary reports a `path` so a raw view can say what it
 * is showing; it is not accepted back, here least of all, since this one is
 * read and mounted into a container.
 */
export interface HandoffRef {
  agent: string;
  sessionId: string;
}

/**
 * RunStopRequest is the body of POST /runs/:id/stop.
 */
export interface RunStopRequest {
  /**
   * Force kills immediately (SIGKILL) instead of asking the guest to exit
   * first. Same distinction as `sandbox-cli kill --force`.
   */
  force?: boolean;
}

/**
 * RestoreMode selects what a recover call does with the recovered snapshot —
 * mirrors rescue.RestoreMode.
 */
export type RestoreMode =
  /**
   * RestoreModeBranch points a new branch at the snapshot. The default: it is
   * the only mode that cannot destroy anything already on disk.
   */
  "branch" |
  /**
   * RestoreModePatch returns the snapshot as a unified diff instead of touching
   * any branch or working tree.
   */
  "patch" |
  /**
   * RestoreModeWorktree writes the snapshot's files back into the run's
   * workspace. Refused if that workspace has uncommitted changes.
   */
  "worktree";

/**
 * RunRecoverRequest is the body of POST /runs/:id/recover.
 *
 * It restores the crash-recovery snapshot (internal/rescue) most recently
 * associated with this run's workspace — the same correlation
 * `sandbox-cli recover` performs by agent, project and time window. There may be
 * none: a run that finished cleanly, or one that never wrote a snapshot, has
 * nothing to recover.
 */
export interface RunRecoverRequest {
  mode?: RestoreMode;  // default RestoreModeBranch
  /**
   * Branch overrides the generated branch name (RestoreModeBranch only).
   */
  branch?: string;
}

/**
 * RunRecoverResponse is the body of a successful POST /runs/:id/recover.
 */
export interface RunRecoverResponse {
  sessionId: string;  // the rescue.Session this snapshot came from
  mode: RestoreMode;
  branch?: string;  // set for RestoreModeBranch
  patch?: string;  // the diff text, for RestoreModePatch
  files: number;
  /**
   * MatchesWorkingTree reports that the workspace on disk already holds what
   * the snapshot holds — the common case, since /workspace is a bind mount and
   * the snapshot is the belt, not the braces. See rescue.RestoreResult.
   */
  matchesWorkingTree: boolean;
}

/**
 * LogEventType discriminates a LogEvent. A client switching on it exhaustively
 * knows the difference between "the run's output ended" and "the connection
 * did", which is the one thing a log viewer must not guess: an incomplete
 * stream that renders as a complete one is how a half-finished agent run reads
 * as a finished one.
 */
export type LogEventType =
  "log" |
  /**
   * LogEventError carries a failure of the stream itself (docker unreachable
   * mid-follow, say), not anything the container printed to stderr.
   */
  "error" |
  /**
   * LogEventEnd is the last event of a stream that finished on its own terms.
   */
  "end";

/**
 * LogEvent is one event of GET /runs/:id/logs, identical on both transports: a
 * WebSocket text frame carries exactly this object, and an SSE `data:` line
 * carries exactly this object with `event:` repeating its Type.
 */
export interface LogEvent {
  type: LogEventType;
  stream?: string;  // "stdout" | "stderr", on Type "log"
  /**
   * Data is one line with its newline stripped, and is deliberately *not*
   * omitempty: a blank line is ordinary log output, and omitting the field for
   * it would make `data` optional in the contract — forcing every consumer to
   * coalesce a missing string on the hottest path in a log viewer.
   */
  data: string;
  error?: string;  // on Type "error"
}

/**
 * RunMetrics is a single point-in-time resource sample for one run — the same
 * numbers the CLI's live gauge and `sandbox-cli stats` sample from `docker
 * stats`, as parsed numbers rather than docker's formatted strings.
 */
export interface RunMetrics {
  id: string;
  memUsageBytes: number;
  memLimitBytes?: number;
  memPercent: number;
  cpuPercent: number;
  pids: number;
  sampledAt: string;
}

/**
 * StatsResponse is the body of GET /stats: one sample per live sandbox
 * container, host-wide (the API equivalent of `sandbox-cli stats --once`).
 */
export interface StatsResponse {
  runs: RunMetrics[];
  sampledAt: string;
}

/**
 * Worktree describes one git worktree sandbox-cli manages for the project.
 */
export interface Worktree {
  branch: string;
  path: string;
  /**
   * Dirty is the modified/untracked paths, and is always present — never
   * `omitempty`. A clean worktree is the common case, and omitting the field
   * for it sent no key at all, which every client then reads as `undefined`
   * rather than as "nothing dirty". A list-valued field that vanishes when
   * empty makes the ordinary case the one that crashes.
   */
  dirty: string[];
  dirtyCount: number;
  head: string;  // the abbreviated commit this branch points at
  repoId: string;  // the id every container of this project is labelled with
  /**
   * Primary marks the repository's **own checkout** rather than a managed
   * worktree — the directory `-project` names, the one branch that has no
   * worktree of its own, and where a run launched without `--worktree` works.
   *
   * It is listed at all because a client asking "which branches can I look at"
   * has to be told about it: internal/worktree.List deliberately reports only
   * the worktrees sandbox-cli manages, which is right for `worktree list` (they
   * are the ones it created and can remove) and wrong for a branch picker,
   * where its absence meant `main` appeared nowhere. Marked rather than mixed
   * in, because the operations differ: it cannot be removed, and `land` merges
   * *into* it.
   */
  primary?: boolean;
  /**
   * Ahead and Behind are counted against Base. "3 ahead" says there is
   * something to land; "3 ahead, 40 behind" says landing it will be a merge.
   */
  ahead: number;
  behind: number;
  /**
   * Base is the branch this work is meant to land on, taken from the label the
   * launching run stamped rather than from whatever is checked out now — the
   * label is the intent, and `land` treats a disagreement between the two as a
   * refusal rather than a preference. Null when nothing recorded one.
   */
  base?: string;
  /**
   * RunID is the run currently working this branch, if one is live.
   */
  runId?: string;
  /**
   * Verified is what the branch's last run said about its own definition of
   * done: true if it passed, false if it failed or died before reaching its
   * verify, and **null when nothing checked it** — no container left to ask, or
   * a run that declared no verify at all. Null is not false. `land` refuses a
   * branch that never passed, so a client showing "unverified" and "failed" the
   * same way would be misreporting the one distinction that decides the merge.
   */
  verified?: boolean;
  /**
   * CreatedAt is when the checkout appeared on disk. git records no creation
   * time for a worktree, so this is the directory's own — accurate for the
   * managed worktrees this lists, all of which sandbox-cli created.
   */
  createdAt: string;
}

/**
 * WorktreesResponse is the body of GET /worktrees.
 */
export interface WorktreesResponse {
  worktrees: Worktree[];
}

/**
 * WorktreeCreateRequest is the body of POST /worktrees.
 */
export interface WorktreeCreateRequest {
  branch: string;
  /**
   * Repo names which registered repository the worktree belongs to, by id from
   * GET /projects. Empty means the repository this daemon was started in.
   */
  repo?: string;
}

/**
 * ConversationResponse is what a run said, and whether it can be answered.
 */
export interface ConversationResponse {
  messages: Message[];
  /**
   * Writable reports whether this run can be typed at right now: it is running
   * *and* was launched with a console. Sent rather than inferred client-side,
   * because the two facts that decide it (container state, how stdin was
   * created) both live here.
   */
  writable: boolean;
  /**
   * SessionID is the agent's own id for this conversation, whole rather than
   * abbreviated — Claude Code rejects anything that is not a complete UUID.
   */
  sessionId?: string;
  /**
   * Resume is the exact line to type on the host to carry the conversation on
   * after the container is gone. Built here rather than by a client, because
   * the flags that make it work are not guessable from the id.
   */
  resume?: string;
}

/**
 * ConsoleInputRequest is one delivery of keystrokes to a run's stdin.
 */
export interface ConsoleInputRequest {
  data: string;
  /**
   * Enter appends the carriage return that submits it. Separate from Data so a
   * client can send a partial line — and because \r is not what a caller would
   * guess: the pty is in raw mode, where a \n arrives as a literal line feed
   * and the agent's input box simply holds it.
   */
  enter?: boolean;
}

/**
 * ConsoleResizeRequest is a terminal's dimensions, in character cells.
 */
export interface ConsoleResizeRequest {
  rows: number;
  cols: number;
}

/**
 * SessionSummary is one resumable conversation.
 */
export interface SessionSummary {
  id: string;
  title?: string;
  turns: number;
  modified: string;
  started?: string;
  /**
   * Partial marks a session listed from its file alone, because sandbox-cli
   * has no verified reader for this agent's format. The id and the dates are
   * real; the title and turn count are unknown, and are reported as unknown
   * rather than as zero.
   */
  partial?: boolean;
  /**
   * Project is the working directory the transcript recorded. It is the one
   * field that tells a sandbox conversation from a host one at a glance: a
   * container's cwd is always /workspace, a host session's is the real path.
   */
  project?: string;
  /**
   * Path is where the transcript lives, reported so a raw view can say what it
   * is showing. It is never accepted *back* — a request names a session by id,
   * and the daemon resolves it.
   */
  path?: string;
  size?: number;
  /**
   * RepoID is the repository this conversation belongs to, or "" when it
   * cannot be attributed — a session pooled in the shared bucket records only
   * `/workspace` and nothing on disk says which project that was. Empty is an
   * answer, not a failure: it lets a client hide those rather than file them
   * under a repository they may not belong to.
   */
  repoId?: string;
  /**
   * Store is "sandbox" (the agent HOME containers get) or "host" (the user's
   * own history). Both are readable; only the first is this daemon's to
   * resume, since resuming the other would mean mounting the host's history
   * into a container that was not asked to have it.
   */
  store?: string;
  resumable?: boolean;
}

/**
 * SessionTranscriptResponse is one conversation, parsed into turns.
 */
export interface SessionTranscriptResponse {
  session: SessionSummary;
  messages: Message[];
}

/**
 * SessionRawResponse is the transcript file as it is on disk.
 *
 * The *tail* when it is long, because a conversation is appended to and the end
 * is what somebody opening it wants — and Truncated says so, since a client
 * showing half a file as though it were the file makes a claim nobody checked.
 */
export interface SessionRawResponse {
  session: SessionSummary;
  size: number;
  truncated?: boolean;
  content: string;
}

export interface SessionListResponse {
  sessions: SessionSummary[];
}

// ---------------------------------------------------------------------------
// Shapes owned by other packages, reached from the types above.
// ---------------------------------------------------------------------------

/**
 * Message is one turn of a conversation, as read from a transcript.
 */
export interface Message {
  role: string;  // "user" | "assistant"
  text: string;
  at?: string;
}

/**
 * DayBucket is one day's runs, split by how they ended.
 */
export interface DayBucket {
  date: string;  // YYYY-MM-DD
  total: number;
  passed: number;
  failed: number;
  verifyFailed: number;
  stopped: number;
}

/**
 * Stats is what the whole window says about outcomes.
 */
export interface Stats {
  total: number;
  decided: number;
  passed: number;
  passRate?: number;  // percent; null when nothing decided
  medianDurationMs?: number;
  finishedToday: number;
}
