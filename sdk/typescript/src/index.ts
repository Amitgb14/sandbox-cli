import { Transport } from "./transport.js";
import { ApiError, ConnectionError, NothingToSnapshotError, WaitError, abortError } from "./errors.js";
import { discoverToken, discoverUrl } from "./discover.js";
import { initRepo, localRepo, samePath, unbornWithFiles, wirePath, type LocalRepo } from "./local.js";
import type {
  AgentInfo,
  HealthResponse,
  LogEvent,
  LogLine,
  Project as ProjectRecord,
  ProjectsResponse,
  Run,
  RunCreateRequest,
  RunsResponse,
  RestoreMode,
  SnapshotCreateRequest,
  SnapshotInfo,
  SnapshotListResponse,
  SnapshotRestoreRequest,
  SnapshotRetentionRequest,
  SnapshotS3CheckResponse,
  SnapshotSettings,
  RunRecoverResponse,
  WorktreesResponse,
  SessionSummary,
  SessionListResponse,
} from "./contract.js";

export * from "./contract.js";
export { ApiError, ConnectionError, NothingToSnapshotError, TimeoutError, WaitError } from "./errors.js";
// The wire shape of a repository, under a name that does not collide with the
// Project *class* below. Without this a consumer cannot type a raw
// `GET /v1/projects` row at all: the class shadows the interface silently, so
// `{ id, name, root, default: true }` fails to typecheck against it.
export type { Project as ProjectRecord } from "./contract.js";

/**
 * A typed client for the sandbox-cli control plane.
 *
 * Three nouns, and they are the daemon's rather than this package's: a
 * **Studio** is a daemon, a **Project** is a repository it has been told about,
 * and a **Workspace** is a branch's worktree inside one. Runs happen in a
 * workspace, and each is a container — not a session you exec into repeatedly.
 * That is the model this tool actually has, and naming it here is deliberate:
 * borrowing "sandbox" from platforms whose sandbox is a machine you keep would
 * promise something no endpoint delivers.
 */

export interface ConnectOptions {
  /** Defaults to $SANDBOX_API_URL, then the port studio.sh recorded, then 8787. */
  url?: string;
  /** Defaults to $SANDBOX_STUDIO_TOKEN, then ~/.config/sandbox/studio/token. */
  token?: string;
  timeoutMs?: number;
  fetch?: typeof globalThis.fetch;
}

export interface AddProjectOptions {
  /**
   * Run `git init` in the directory when it is not in a repository yet.
   *
   * Opt-in, and it stays that way. Two reasons, and the second is the one that
   * cannot be designed around. Creating a repository is a larger side effect
   * than registering one, and this SDK already refuses to register what a lookup
   * failed to find — a mistyped path would otherwise leave an empty repository
   * somewhere nobody meant. And the path belongs to the **daemon's** machine
   * while `git init` necessarily runs on this one: against a remote daemon,
   * doing it automatically would create a repository here, silently, and still
   * fail to register there.
   *
   * No initial commit is made. Git creates an orphan worktree from a commitless
   * repository, so branch-addressed runs work immediately.
   */
  init?: boolean;
}

export interface RunOptions {
  /**
   * Remove a **finished** run still holding this branch's container name, and
   * launch anyway.
   *
   * Docker refuses a duplicate name, and that refusal is what enforces one agent
   * per branch — so a run that has exited keeps its name until somebody reaps
   * it, and running the same script twice is refused with a 409. That is the
   * right default: a finished run's logs are the evidence for what it did, and
   * removing them for you would discard that on every second run.
   *
   * This is how a caller says the evidence is spent. It refuses a run that is
   * still going: "finished" is the whole of the claim.
   */
  replaceFinished?: boolean;
  env?: Record<string, string>;
  /** Extra egress domains for this run. It can add to the daemon's posture and
   *  never loosen it — the same tighten-only rule a project file gets. */
  allow?: string[];
  memory?: string;
  cpus?: string;
  base?: string;
  publish?: string[];
  verify?: string;
  /** How long to wait for the run to finish. See Workspace.run. */
  timeoutMs?: number;
  signal?: AbortSignal;
}

export interface AgentOptions extends RunOptions {
  /** Agents to try when the first one's provider is not answering. */
  fallback?: string[];
}

/** How a run ended, and what it actually was. */
/** Options for {@link Workspace.snapshot} and {@link Workspace.snapshotRun}. */
export interface SnapshotOptions {
  /** What to call it. A checkpoint without one is a hex id in a list. */
  label?: string;
  /**
   * How long to keep this one, as a Go duration ("72h"). Omitted follows the
   * daemon's configured default — seven days for a snapshot somebody asked for.
   */
  retention?: string;
}

/** Options for {@link Workspace.restore}. */
export interface RestoreOptions {
  /** Defaults to "branch", the only mode that cannot destroy anything. */
  mode?: RestoreMode;
  /** Overrides the generated branch name; "branch" mode only. */
  branch?: string;
}

export interface Outcome {
  id: string;
  exitCode: number;
  stdout: string;
  stderr: string;
  /** The agent that ran, which is not always the one asked for. */
  agent?: string;
  /** Set when routing or a handoff put a different agent on the work. Reported
   *  on every outcome rather than behind an option: a script that cannot see a
   *  failover attributes one agent's output to another. */
  routedFrom?: string;
  routeReason?: string;
  handoffFrom?: string;
  /** True when the wait gave up and stopped the run rather than the run ending
   *  on its own terms. The exit code of a stopped container is not a verdict. */
  stopped: boolean;
  run: Run;
}

export class Studio {
  private readonly t: Transport;

  private constructor(t: Transport) {
    this.t = t;
  }

  /**
   * Point at a daemon. With no arguments it finds the one on this machine.
   */
  static async connect(opts: ConnectOptions = {}): Promise<Studio> {
    const t = new Transport({
      url: discoverUrl(opts.url),
      token: discoverToken(opts.token),
      timeoutMs: opts.timeoutMs,
      fetch: opts.fetch,
    });
    const studio = new Studio(t);
    // One round trip before anything else, so a wrong URL or a missing token is
    // reported here rather than as a failure of whatever ran first. /health is
    // the only route that answers without a token, which is what makes it able
    // to say that a token is what is missing.
    let health: HealthResponse;
    try {
      health = await studio.health();
    } catch (err) {
      // "fetch failed" is true and useless. Nothing answering on a loopback port
      // that this package *discovered from a file* almost always means the daemon
      // is not running — and a caller who never started one has no reason to
      // connect that to studio.sh.
      if (err instanceof ConnectionError) {
        throw new ConnectionError(
          t.url,
          "nothing is listening",
          `no daemon is running there. Start one from the repository you want to work in: ` +
            `curl -fsSL https://raw.githubusercontent.com/Amitgb14/sandbox-cli/main/studio.sh | sh — ` +
            `or pass { url } if yours is somewhere else.`,
        );
      }
      throw err;
    }
    if (health.authRequired && !t.token) {
      throw new ApiError(
        401,
        "GET /v1/health",
        `the daemon at ${t.url} requires a token; set SANDBOX_STUDIO_TOKEN, pass one, or start it with studio.sh which writes one`,
      );
    }
    return studio;
  }

  get url(): string {
    return this.t.url;
  }

  health(): Promise<HealthResponse> {
    return this.t.request<HealthResponse>("GET", "/v1/health");
  }

  async agents(): Promise<AgentInfo[]> {
    const res = await this.t.request<{ agents: AgentInfo[] }>("GET", "/v1/agents");
    return res.agents ?? [];
  }

  async projects(): Promise<Project[]> {
    const res = await this.t.request<ProjectsResponse>("GET", "/v1/projects");
    return (res.projects ?? []).map((p) => new Project(this.t, p));
  }

  /**
   * The snapshot configuration in force on the daemon: the two retention
   * windows, and the object storage snapshots are mirrored to.
   *
   * Daemon-wide rather than per repository, which is why it hangs off the client
   * and not off a {@link Workspace}: one bucket holds every repository's
   * snapshots, namespaced by repository id.
   *
   * The credential is reported as a *name* and a boolean — which variable is
   * read, and whether it currently resolves. There is nowhere in the response
   * for a value, deliberately.
   */
  snapshotSettings(): Promise<SnapshotSettings> {
    return this.t.request<SnapshotSettings>("GET", "/v1/snapshots/settings");
  }

  /**
   * Does the configured bucket answer, and does the named credential resolve?
   *
   * Asks about what the *daemon* is configured with; there is no way to hand it
   * a bucket to dial, because a check that took its host from the caller would
   * be a server-side request forgery with a friendly name.
   *
   * A bucket that refuses is a normal result with `ok: false`, not a thrown
   * error: the request succeeded, the storage did not.
   */
  checkSnapshotStorage(): Promise<SnapshotS3CheckResponse> {
    return this.t.request<SnapshotS3CheckResponse>("POST", "/v1/snapshots/s3/check", {});
  }

  /**
   * One repository, by id or by name — or, with no argument, the one this
   * script is standing in.
   *
   * The no-argument form walks up from `process.cwd()` to the git root and
   * matches it against the registry **by path**, which is a lookup rather than a
   * new way to name things: the daemon is still asked about repositories it
   * already knows, and a directory nobody registered is still refused. It is
   * therefore only meaningful when the daemon is on this machine, and says so
   * when the roots do not match instead of pretending the repository vanished.
   *
   * Refuses an ambiguous name rather than picking: two clones of a same-named
   * repo share a name and not an id.
   */
  async project(...args: [] | [string]): Promise<Project> {
    // `args.length`, not `=== undefined`: the two are different requests.
    // `project()` asks for the repository this script is in, while `project(x)`
    // with x undefined is a forgotten argument — `process.argv[2]` that was
    // never passed — and resolving *that* to whatever repository the script file
    // happens to sit in would launch agents somewhere nobody named.
    if (args.length === 0) return this.projectHere();
    const [idOrName] = args;
    if (idOrName === undefined || idOrName === "") {
      throw new Error(
        "project() was given an empty repository name — a missing argument rather than a request. " +
          "Call project() with no arguments to mean the repository this script is in.",
      );
    }
    const all = await this.projects();
    const byId = all.find((p) => p.id === idOrName);
    if (byId) return byId;
    const named = all.filter((p) => p.name === idOrName);
    if (named.length === 1) return named[0];
    if (named.length > 1) {
      throw new Error(
        `${named.length} repositories are called ${idOrName}; use an id: ${named.map((p) => p.id).join(", ")}`,
      );
    }
    // A path-shaped argument is answered as a path rather than reported as a
    // missing name: resolved on this machine and matched against the registry,
    // so nothing new is reachable — the daemon is still only asked about
    // repositories somebody added. Anything else is a name, and the list of
    // registered ones separates a typo from a repository nobody added without
    // needing a second call.
    if (looksLikePath(idOrName)) return this.projectAt(idOrName, all);
    const known = all.map((p) => p.name).join(", ") || "none";
    throw new Error(
      `no repository ${idOrName} is registered with the daemon at ${this.t.url}. Registered: ${known}. ` +
        `Add one with studio.addProject("/abs/path"), or in Studio.`,
    );
  }

  /** The repository this script is standing in. */
  private async projectHere(): Promise<Project> {
    const cwd = process.cwd();
    const here = await localRepo(cwd);
    if (!here) {
      throw new Error(
        `${cwd} is not inside a git repository, so there is nothing here to work on. ` +
          `Name one — studio.project("my-app") — or add a directory on the daemon's machine ` +
          `with studio.addProject("/abs/path").`,
      );
    }
    return this.match(here, await this.projects());
  }

  /**
   * The registered repository at a path on this machine.
   *
   * Matched by root, and *not* registered if it is missing: adding a repository
   * is a change to what this daemon will touch, and a lookup that quietly made
   * one would turn a typo into a permanent entry. `addProject` is the sentence
   * that asks for it.
   */
  private async projectAt(path: string, all: Project[]): Promise<Project> {
    const here = await localRepo(path);
    return this.match(here ?? { root: wirePath(path, process.cwd()), tree: "" }, all);
  }

  /**
   * The registry row for a local repository, or an error saying which of the two
   * reasons it is missing for.
   *
   * Both forms are compared, and the second is not redundant: a daemon started
   * *inside* a linked worktree registers its default project as that worktree
   * (`studio.sh` resolves `-project` with `--show-toplevel`), while every added
   * repository carries the main root. Matching the root alone would refuse the
   * one repository that cannot be removed.
   */
  private match(here: LocalRepo, all: Project[]): Project {
    const hit = all.find(
      (p) => samePath(p.root, here.root) || (here.tree !== "" && samePath(p.root, here.tree)),
    );
    if (hit) return hit;
    const known = all.map((p) => p.root).join(", ") || "none";
    throw new Error(
      `the daemon at ${this.t.url} lists no repository at ${here.root}. Either it has not been added — ` +
        `studio.addProject(${JSON.stringify(here.root)}) — or that daemon is on another machine, where this ` +
        `path means nothing. It knows: ${known}.`,
    );
  }

  /**
   * Register a directory on the **daemon's** machine as a repository.
   *
   * The one call in this SDK that hands over a path, mirroring the one endpoint
   * that accepts one: everything else names a repository by id, so the checks a
   * directory has to pass — absolute, on disk, a git repository, not your home
   * or an ancestor of it — are applied here, once, by the daemon.
   *
   * The path is resolved on that machine, not this one: what crosses is the
   * string, expanded but never symlink-resolved, since that resolution is a fact
   * about *this* disk and would rewrite `/tmp/api` into `/private/tmp/api` for a
   * daemon that has neither. With no argument this does read `process.cwd()`,
   * which is a local answer and therefore only meaningful for a daemon on this
   * machine — a remote one will say it has no such directory, which is honest.
   *
   * Adding a repository that is already registered is a no-op that returns the
   * existing row, so this is safe to call on every start.
   */
  async addProject(path?: string, opts: AddProjectOptions = {}): Promise<Project> {
    // No argument means "the repository I am in", and a relative path or a `~`
    // is expanded the way a shell would. Both are conveniences of *expression*:
    // what crosses is one absolute path, to a daemon that applies every check
    // before it agrees to touch the directory.
    const cwd = process.cwd();
    let asked: string;
    if (path === undefined) {
      let here = await localRepo(cwd);
      if (!here && opts.init) {
        await initRepo(cwd);
        here = await localRepo(cwd);
      }
      if (!here) {
        throw new Error(
          `${cwd} is not inside a git repository — Studio addresses work by branch, so there has to ` +
            `be one. Pass { init: true } to run \`git init\` here first, or give addProject a path.`,
        );
      }
      asked = here.root;
    } else {
      const typed = path.trim();
      if (!typed) throw new Error("addProject needs a path on the daemon's machine");
      asked = wirePath(typed, cwd);
      if (opts.init && !(await localRepo(asked))) await initRepo(asked);
    }
    // A repository with files and no commits is registerable and useless: every
    // worktree Studio makes from it is empty, so the agent starts in a
    // /workspace with none of this code in it and nothing says so. Caught here,
    // where the person is still at the keyboard, rather than in a run whose
    // output is "no such file".
    if (await unbornWithFiles(asked)) {
      throw new Error(
        `${asked} is a git repository with no commits yet, and Studio works from committed ` +
          `state — every worktree it makes would be empty, so an agent would see none of these ` +
          `files. Commit first (check what you are adding: a directory that was never a ` +
          `repository usually has no .gitignore), then add it:\n` +
          `  git add -A && git commit -m "initial commit"`,
      );
    }
    const rec = await this.t.request<ProjectRecord>("POST", "/v1/projects", { path: asked });
    return new Project(this.t, rec);
  }

  /** Clone a repository onto the daemon's machine and register it. */
  async clone(opts: { url: string; parent: string; name?: string }): Promise<Project> {
    const rec = await this.t.request<ProjectRecord>("POST", "/v1/projects/clone", opts);
    return new Project(this.t, rec);
  }

  /** Every run the daemon knows about, newest first. */
  async runs(opts: { all?: boolean; repo?: string } = {}): Promise<Run[]> {
    const q = new URLSearchParams();
    if (opts.all) q.set("all", "1");
    if (opts.repo) q.set("repo", opts.repo);
    const suffix = q.toString() ? `?${q}` : "";
    const res = await this.t.request<RunsResponse>("GET", `/v1/runs${suffix}`);
    return res.runs ?? [];
  }

  /** The conversations an agent has on the daemon's machine. */
  async sessions(agent: string, opts: { all?: boolean } = {}): Promise<SessionSummary[]> {
    const suffix = opts.all ? "?scope=all" : "";
    const res = await this.t.request<SessionListResponse>(
      "GET",
      `/v1/agents/${encodeURIComponent(agent)}/sessions${suffix}`,
    );
    return res.sessions ?? [];
  }
}

export class Project {
  private readonly t: Transport;
  readonly id: string;
  readonly name: string;
  readonly root: string;
  readonly missing: boolean;

  constructor(t: Transport, rec: ProjectRecord) {
    this.t = t;
    this.id = rec.id;
    this.name = rec.name;
    this.root = rec.root;
    this.missing = rec.missing ?? false;
  }

  /**
   * A branch's worktree, created if it does not exist.
   *
   * The isolation unit: one branch, one tree, many runs. Two agents working in
   * one tree is a data race with a filesystem in the middle, which is why this
   * is the only way to get somewhere to run.
   */
  async workspace(branch: string): Promise<Workspace> {
    await this.t.request("POST", "/v1/worktrees", { branch, repo: this.id });
    return new Workspace(this.t, this, branch);
  }

  /** The repository itself, for a run that should touch the checked-out tree. */
  rootWorkspace(): Workspace {
    return new Workspace(this.t, this, undefined);
  }
}

const POLL_MIN_MS = 250;
const POLL_MAX_MS = 2_000;
const DEFAULT_RUN_TIMEOUT_MS = 30 * 60_000;

export class Workspace {
  private readonly t: Transport;
  readonly project: Project;
  readonly branch?: string;
  /**
   * The run this object last returned an Outcome for.
   *
   * A finished run keeps its branch's container name — docker refuses a
   * duplicate, which is what enforces one agent per branch — so a second step on
   * the same workspace is refused until something clears the first. This is the
   * one run that can be cleared without discarding anything: its exit code,
   * stdout and stderr were handed to the caller before it was recorded here.
   * Anything else holding the name belongs to somebody else and still refuses.
   */
  private delivered?: string;

  constructor(t: Transport, project: Project, branch?: string) {
    this.t = t;
    this.project = project;
    this.branch = branch;
  }

  /**
   * Run a command and wait for it.
   *
   * Each run is its own container over this worktree, so state lives in the
   * tree rather than in a process: `npm ci` then `npm test` works because
   * node_modules was written to disk, not because anything stayed alive.
   *
   * The wait is bounded, and when it expires the run is **stopped** and the
   * outcome says so. A deadline that only stopped waiting would leave a
   * container holding a CPU with nobody watching it — and reporting a stopped
   * run as a failed one would put a verdict on a container that was interrupted.
   */
  // `async` rather than a plain function returning a promise, and the same for
  // agent() and handoff() below: a validation that threw *synchronously* from a
  // method typed Promise<Outcome> would escape a caller's .catch() and surface
  // as an uncaught exception, which is a different failure from the one the
  // check exists to report.
  async run(argv: string[], opts: RunOptions = {}): Promise<Outcome> {
    if (argv.length === 0) throw new Error("run needs a command");
    return this.launchAndWait({ command: argv, ...this.common(opts) }, opts);
  }

  /** Hand the work to an agent instead of a command. */
  async agent(name: string, prompt: string, opts: AgentOptions = {}): Promise<Outcome> {
    if (!prompt.trim()) throw new Error("an agent run needs a prompt: it is the whole instruction");
    const body: RunCreateRequest = { agent: name, prompt, ...this.common(opts) };
    if (opts.fallback?.length) body.fallback = opts.fallback;
    return this.launchAndWait(body, opts);
  }

  /**
   * Start an agent on somebody else's conversation.
   *
   * A briefing, not a resume: what crosses is HANDOFF.md, a vendor-neutral
   * transcript and a file ledger derived from git, mounted read-only, with a
   * prompt that tells the target it is reading a briefing rather than its own
   * history. The daemon refuses a source it has no verified reader for.
   */
  async handoff(
    to: string,
    from: { agent: string; sessionId: string },
    prompt: string,
    opts: AgentOptions = {},
  ): Promise<Outcome> {
    if (!prompt.trim()) {
      throw new Error(
        "a handoff needs a prompt: the briefing says what happened before, the prompt says what to do now",
      );
    }
    return this.launchAndWait(
      { agent: to, prompt, handoffFrom: from, ...this.common(opts) },
      opts,
    );
  }

  /** Launch without waiting, for a caller that wants to follow the logs itself. */
  async start(body: RunCreateRequest): Promise<Run> {
    return this.t.request<Run>("POST", "/v1/runs", { ...body, ...this.common({}) });
  }

  /** Ask the run to exit. Not the same as removing it. */
  async stop(id: string, force = false): Promise<void> {
    await this.t.request("POST", `/v1/runs/${encodeURIComponent(id)}/stop`, { force });
  }

  /**
   * Remove the container.
   *
   * Deliberately separate from stop, and never called for you: a finished run's
   * logs are the evidence for what it did, and a helper that tidied up on the
   * way out would throw that away on every happy path.
   */
  async remove(id: string): Promise<void> {
    await this.t.request("DELETE", `/v1/runs/${encodeURIComponent(id)}`);
  }

  /**
   * Free this branch's container name by removing the finished run that holds
   * it, and report what was removed.
   *
   * Null when nothing was holding it. Refuses when the holder is still running —
   * stopping somebody else's agent is not a side effect a helper should have.
   */
  async clearFinished(): Promise<Run | null> {
    const branch = await this.branchName();
    const res = await this.t.request<RunsResponse>(
      "GET",
      `/v1/runs?all=1&repo=${encodeURIComponent(this.project.id)}`,
    );
    const holder = (res.runs ?? []).find((r) => r.branch === branch);
    if (!holder) return null;
    // Finished is the *positive* claim, and it is made about two states only.
    // Listing the live ones instead fails open on the states nobody thought
    // about: `paused` holds the name and holds unwritten work, and `unknown`
    // means the engine would not say — both of which would have been reaped.
    if (holder.state !== "exited" && holder.state !== "dead") {
      throw new Error(
        `run ${holder.id} on ${branch} is ${holder.state}, not finished; stop it first — ` +
          `two agents in one checkout overwrite each other's work`,
      );
    }
    await this.remove(holder.id);
    return holder;
  }

  /**
   * Checkpoint this workspace now, and return the snapshot.
   *
   * A snapshot is a commit of the working tree under
   * `refs/sandbox/snapshots/`, written through a private index so the
   * repository's own index, HEAD, branches and working tree are untouched. It
   * holds files and nothing else — no container, no image, no credential.
   *
   * Throws {@link NothingToSnapshotError} when the tree is exactly what is
   * already committed, which is the case a "checkpoint before the risky step"
   * script wants to skip rather than fail on.
   */
  async snapshot(opts: SnapshotOptions = {}): Promise<SnapshotInfo> {
    const body: SnapshotCreateRequest = { repo: this.project.id };
    if (this.branch) body.branch = this.branch;
    if (opts.label) body.label = opts.label;
    if (opts.retention) body.retention = opts.retention;
    return this.capture("/v1/snapshots", body);
  }

  /**
   * Checkpoint the workspace a run is working in.
   *
   * The run answers which repository and which worktree from the labels it was
   * stamped with, so neither is sent — and the daemon refuses them in this body
   * rather than letting a second answer decide where files are written.
   */
  async snapshotRun(id: string, opts: SnapshotOptions = {}): Promise<SnapshotInfo> {
    const body: SnapshotCreateRequest = {};
    if (opts.label) body.label = opts.label;
    if (opts.retention) body.retention = opts.retention;
    return this.capture(`/v1/runs/${encodeURIComponent(id)}/snapshot`, body);
  }

  private async capture(path: string, body: SnapshotCreateRequest): Promise<SnapshotInfo> {
    try {
      return await this.t.request<SnapshotInfo>("POST", path, body);
    } catch (err) {
      // 422 covers every reason a capture can fail, so the message is what
      // distinguishes them. Matching on it here, once, is the alternative to
      // every caller doing it — and the daemon's sentence is carried through
      // either way, so a wording change costs this branch and nothing else.
      if (err instanceof ApiError && err.status === 422 && /nothing to snapshot/i.test(err.message)) {
        throw new NothingToSnapshotError(err.message, this.branch);
      }
      throw err;
    }
  }

  /**
   * The snapshots recorded for this workspace, newest first.
   *
   * Scoped to this workspace's branch, because that is what a Workspace *is*.
   * Pass `allBranches` for the repository's whole set.
   */
  async snapshots(opts: { allBranches?: boolean } = {}): Promise<SnapshotInfo[]> {
    const params = new URLSearchParams({ repo: this.project.id });
    if (this.branch && !opts.allBranches) params.set("branch", this.branch);
    const res = await this.t.request<SnapshotListResponse>("GET", `/v1/snapshots?${params}`);
    return res.snapshots ?? [];
  }

  /**
   * Put a snapshot back.
   *
   * Defaults to `branch` mode, the only one that cannot destroy anything: it
   * points a new branch at the snapshot and leaves the working tree alone.
   * `worktree` writes the files back and is refused on a dirty tree; `patch`
   * returns a diff and touches nothing.
   */
  async restore(id: string, opts: RestoreOptions = {}): Promise<RunRecoverResponse> {
    const body: SnapshotRestoreRequest = { repo: this.project.id };
    if (opts.mode) body.mode = opts.mode;
    if (opts.branch) body.branch = opts.branch;
    return this.t.request<RunRecoverResponse>(
      "POST",
      `/v1/snapshots/${encodeURIComponent(id)}/restore`,
      body,
    );
  }

  /**
   * Change how long one snapshot is kept — a Go duration ("72h"), or "" to
   * return it to the configured default.
   */
  async setSnapshotRetention(id: string, retention: string): Promise<SnapshotInfo> {
    const body: SnapshotRetentionRequest = { retention, repo: this.project.id };
    return this.t.request<SnapshotInfo>(
      "POST",
      `/v1/snapshots/${encodeURIComponent(id)}/retention`,
      body,
    );
  }

  /**
   * Mirror a snapshot to the daemon's configured object storage, now.
   *
   * With a bucket configured, {@link Workspace.snapshot} already does this on
   * the way out — so this is for the two cases it leaves behind: an upload that
   * failed while the network was down, and a snapshot taken before a bucket
   * existed. There is deliberately no way to *un*-mirror one from here; deleting
   * a backup is not a thing an API should make easy.
   *
   * Which bucket is the daemon's decision and never this call's. A client able
   * to name one would be choosing where a repository's contents are sent.
   */
  async uploadSnapshot(id: string): Promise<SnapshotInfo> {
    return this.t.request<SnapshotInfo>(
      "POST",
      `/v1/snapshots/${encodeURIComponent(id)}/upload`,
      { repo: this.project.id },
    );
  }

  /**
   * Ask the bucket whether a snapshot's object is really there.
   *
   * `snapshot.remote` records what the *upload* did, which is a different claim:
   * a lifecycle rule or somebody tidying a bucket leaves a snapshot reading as
   * mirrored when it is not. This is the call that asks — worth making before
   * relying on a checkpoint you have not touched in a while, and not worth
   * making per row of a listing.
   */
  async verifySnapshot(id: string): Promise<SnapshotS3CheckResponse> {
    return this.t.request<SnapshotS3CheckResponse>(
      "POST",
      `/v1/snapshots/${encodeURIComponent(id)}/verify`,
      { repo: this.project.id },
    );
  }

  /**
   * The branch this workspace's runs are named after.
   *
   * A worktree knows its own; the repository's root does not, so it is asked —
   * the container name is derived from whatever is checked out there, and
   * guessing "main" would free the wrong name on a repository that is on
   * anything else.
   */
  private async branchName(): Promise<string> {
    if (this.branch) return this.branch;
    // The generated shape, not a hand-written one: a rename of `primary` in
    // types.go would then be a compile error here rather than a runtime "cannot
    // tell which branch" — and this is the one place the claim that the types
    // are generated has to hold for itself.
    const res = await this.t.request<WorktreesResponse>(
      "GET",
      `/v1/worktrees?repo=${encodeURIComponent(this.project.id)}`,
    );
    const primary = (res.worktrees ?? []).find((w) => w.primary);
    if (!primary) {
      throw new Error(`cannot tell which branch ${this.project.name} has checked out`);
    }
    return primary.branch;
  }

  /** The output of a finished run, as a document. */
  async logs(id: string): Promise<LogLine[]> {
    return this.t.request<LogLine[]>("GET", `/v1/runs/${encodeURIComponent(id)}/logs`);
  }

  /**
   * A live run's output, line by line, until it ends.
   *
   * Server-sent events rather than a WebSocket: the daemon offers both carrying
   * the identical payload, and SSE needs nothing this runtime does not already
   * have.
   */
  async *follow(id: string, signal?: AbortSignal): AsyncGenerator<LogEvent> {
    const url = this.t.streamUrl(`/v1/runs/${encodeURIComponent(id)}/logs?follow=1`);
    // Its own controller, chained to the caller's. Leaving the stream open when
    // the consumer stops is not merely untidy: the daemon's read loop is the
    // only thing that notices a gone client, and it is what stops the
    // `docker logs --follow` behind this endpoint. A generator that returned
    // without closing would leak a socket here and a process there — and the
    // symptom is a test suite that never exits, which is how this was found.
    const controller = new AbortController();
    const onAbort = () => controller.abort();
    if (signal) {
      if (signal.aborted) controller.abort();
      else signal.addEventListener("abort", onAbort, { once: true });
    }

    const res = await this.t.doFetch(url, {
      headers: this.t.headers({ Accept: "text/event-stream" }),
      signal: controller.signal,
    });
    if (!res.ok || !res.body) {
      controller.abort();
      throw new ApiError(res.status, `GET /v1/runs/${id}/logs?follow=1`, await res.text().catch(() => ""));
    }
    const reader = res.body.getReader();
    const decoder = new TextDecoder();
    let buf = "";
    try {
      for (;;) {
        const { done, value } = await reader.read();
        if (done) return;
        buf += decoder.decode(value, { stream: true });
        // Frames are separated by a blank line; a partial one stays in the
        // buffer rather than being parsed as a whole event. One read can carry
        // several — the loop drains the buffer rather than assuming a frame per
        // chunk.
        let sep = buf.indexOf("\n\n");
        while (sep >= 0) {
          const frame = buf.slice(0, sep);
          buf = buf.slice(sep + 2);
          const event = parseFrame(frame);
          if (event) {
            yield event;
            // The stream says when a run's output ends; the connection may stay
            // open after it. Waiting for the socket instead would hang on
            // exactly the runs that finished.
            if (event.type === "end") return;
          }
          sep = buf.indexOf("\n\n");
        }
      }
    } finally {
      // Runs on `return`, on `break` in the caller's for-await, and on a throw.
      signal?.removeEventListener("abort", onAbort);
      await reader.cancel().catch(() => undefined);
      controller.abort();
    }
  }

  /**
   * Every option `run` and `agent` accept.
   *
   * TypeScript's excess-property check catches a typo in an object literal, but
   * this package ships JavaScript too, and there a misspelling is silent —
   * `{ alow: ["api.example.com"] }` launches with the daemon's default egress
   * posture and reports success. That is a misspelling of the one option that is
   * a security control, so it is worth a runtime check rather than a type.
   */
  private static readonly KNOWN_OPTIONS = new Set([
    "env", "allow", "memory", "cpus", "base", "publish", "verify",
    "timeoutMs", "replaceFinished", "signal", "fallback",
  ]);

  private common(opts: RunOptions): Partial<RunCreateRequest> {
    const unknown = Object.keys(opts).filter((k) => !Workspace.KNOWN_OPTIONS.has(k));
    if (unknown.length > 0) {
      throw new Error(
        `unknown run option(s): ${unknown.join(", ")}. Known: ` +
          [...Workspace.KNOWN_OPTIONS].sort().join(", "),
      );
    }
    const body: Partial<RunCreateRequest> = { repo: this.project.id };
    if (this.branch) body.worktree = this.branch;
    if (opts.env) body.env = opts.env;
    if (opts.allow?.length) body.allow = opts.allow;
    if (opts.memory) body.memory = opts.memory;
    if (opts.cpus) body.cpus = opts.cpus;
    if (opts.base) body.base = opts.base;
    if (opts.publish?.length) body.publish = opts.publish;
    if (opts.verify) body.verify = opts.verify;
    return body;
  }

  private async launchAndWait(body: RunCreateRequest, opts: RunOptions): Promise<Outcome> {
    let started: Run;
    try {
      started = await this.t.request<Run>("POST", "/v1/runs", body, opts.signal);
    } catch (err) {
      if (!(err instanceof ApiError) || err.status !== 409) throw err;
      // Held separately because the retry below may replace it, and a reassigned
      // `err` loses the narrowing that proved it was an ApiError.
      let conflict: ApiError = err;
      // A second step in the same script is not a second script. The name is
      // held by the run *this object already returned an Outcome for* — its
      // exit code, stdout and stderr are in the caller's hands — so clearing it
      // discards no evidence anybody is still waiting for, and the alternative
      // is that `run()` twice on one workspace cannot work at all.
      //
      // Scoped to this object's own delivered run and nothing else: a run
      // launched by another script, an earlier session, or one still going is
      // somebody else's, and those still get the 409 and the sentence below.
      if (this.delivered && !opts.replaceFinished) {
        try {
          await this.remove(this.delivered);
          this.delivered = undefined;
          started = await this.t.request<Run>("POST", "/v1/runs", body, opts.signal);
          return await this.awaitOutcome(started, opts);
        } catch (retryErr) {
          if (!(retryErr instanceof ApiError) || retryErr.status !== 409) throw retryErr;
          conflict = retryErr;
        }
      }
      if (!opts.replaceFinished) {
        // The daemon's own remedy is "GET /v1/runs/<id>/logs, then DELETE
        // /v1/runs/<id>" — correct, and addressed to a client holding a URL.
        // Somebody holding *this* client has two better moves, and being told
        // to reach for curl from inside a typed API is how a library teaches
        // people to work around it. The daemon's sentence is kept whole; this
        // adds the sentence it cannot know to write.
        throw new ApiError(
          conflict.status,
          conflict.endpoint,
          `${conflict.message}\n  From here: pass { replaceFinished: true } to run this anyway, ` +
            `or call workspace.clearFinished() first — both refuse a run that is still going.`,
        );
      }
      // Retried once, and only once: a second conflict means something else took
      // the name, and looping would be a race with whatever that is.
      //
      // The daemon answers 409 for two different things — a finished run holding
      // the name, and *an agent still running on this branch*. Only the first is
      // clearable, and when clearFinished refuses the second the caller must
      // still get the ApiError it would have got without the flag: a `catch (e)
      // { if (e instanceof ApiError && e.status === 409) }` that stops matching
      // the moment you pass an option is worse than no option.
      try {
        await this.clearFinished();
      } catch (clearErr) {
        throw new ApiError(conflict.status, conflict.endpoint, `${conflict.message}\n  ${String(clearErr)}`);
      }
      started = await this.t.request<Run>("POST", "/v1/runs", body, opts.signal);
    }
    return this.awaitOutcome(started, opts);
  }

  /**
   * Wait for a launched run and hand back its outcome, remembering that this
   * object delivered it.
   *
   * That memory is what lets the *next* step on this workspace clear the name —
   * see launchAndWait. It is set only after the outcome is returned, so a run
   * whose result nobody received is never treated as spent.
   */
  private async awaitOutcome(started: Run, opts: RunOptions): Promise<Outcome> {
    let out: Outcome;
    try {
      out = await this.wait(started.id, opts);
    } catch (cause) {
      // The launch already succeeded, so the container exists whatever went
      // wrong here — a daemon restart mid-poll, a 502, an abort. Rejecting with
      // a bare error would leave the caller with no id to stop or remove, and a
      // detached run holds `sandbox-<repo>-<branch>`, which docker refuses to
      // duplicate: the branch would be blocked by something nobody can name.
      throw new WaitError(started, cause);
    }
    this.delivered = started.id;
    return out;
  }

  /**
   * Poll until the run is over.
   *
   * Polling rather than the follow stream, and the reason is which failure each
   * has: a dropped stream would leave a caller waiting on a run that had already
   * finished, while a poll that misses an update simply asks again. The interval
   * backs off so a long agent run is not a request every quarter second.
   */
  private async wait(id: string, opts: RunOptions): Promise<Outcome> {
    const deadline = Date.now() + (opts.timeoutMs ?? DEFAULT_RUN_TIMEOUT_MS);
    let delay = POLL_MIN_MS;
    let stopped = false;
    for (;;) {
      const run = await this.t.request<Run>(
        "GET",
        `/v1/runs/${encodeURIComponent(id)}`,
        undefined,
        // Passed so a cancel is noticed during the request rather than only
        // when the next sleep begins — up to a whole request timeout later.
        opts.signal,
      );
      if (run.state === "exited" || run.state === "dead") {
        // The daemon may have routed this to another agent: on failover it
        // renames the failed container and starts a new one. Returning here
        // would credit the agent that *failed* and leave the retry running —
        // and the next run on this branch would then conflict with a live
        // container this object cannot clear.
        const successor = await this.failoverOf(id, run);
        if (successor) {
          id = successor;
          continue;
        }
        return this.outcome(run, stopped);
      }
      if (Date.now() >= deadline) {
        if (stopped) return this.outcome(run, true); // asked once; do not loop forever
        // The stop is the thing that makes a deadline mean something, so its
        // failure cannot be swallowed. Reporting `stopped: true` after a refused
        // stop would claim the container was ended while it is still running and
        // still holding its branch's name — the exact outcome the deadline
        // exists to prevent, announced as if it had been prevented.
        await this.stop(id);
        stopped = true;
        continue;
      }
      await sleep(Math.min(delay, Math.max(0, deadline - Date.now())), opts.signal);
      delay = Math.min(delay * 2, POLL_MAX_MS);
    }
  }

  /**
   * The run the daemon started to replace this one, if it did.
   *
   * Asked only when a run ended non-zero, because it costs a listing. The link
   * is `routedFrom`, which the daemon stamps on the replacement — the same field
   * the audit line and the container label carry, so this reads the record
   * rather than inferring from timing.
   */
  private async failoverOf(id: string, finished: Run): Promise<string | null> {
    if (!finished.exitCode) return null;
    try {
      const res = await this.t.request<RunsResponse>(
        "GET",
        `/v1/runs?all=1&repo=${encodeURIComponent(this.project.id)}`,
      );
      const next = (res.runs ?? []).find((r) => r.routedFrom === id);
      return next?.id ?? null;
    } catch {
      // A listing that fails says nothing about a failover, and the outcome in
      // hand is real. Reporting it beats throwing away a finished run.
      return null;
    }
  }

  private async outcome(run: Run, stopped: boolean): Promise<Outcome> {
    const lines = await this.logs(run.id).catch(() => [] as LogLine[]);
    const of = (stream: string) =>
      lines.filter((l) => l.stream === stream).map((l) => l.text).join("\n");
    return {
      id: run.id,
      exitCode: run.exitCode ?? -1,
      stdout: of("stdout"),
      stderr: of("stderr"),
      agent: run.agent,
      routedFrom: run.routedFrom,
      routeReason: run.routeReason,
      handoffFrom: run.handoffFrom,
      stopped,
      run,
    };
  }
}

function parseFrame(frame: string): LogEvent | null {
  for (const line of frame.split("\n")) {
    if (!line.startsWith("data:")) continue;
    try {
      return JSON.parse(line.slice(5).trim()) as LogEvent;
    } catch {
      return null;
    }
  }
  return null;
}

/**
 * Wait, cancellably, without leaving anything behind.
 *
 * The listener is removed on the resolved path too. `wait` calls this in a loop,
 * so without that a thirty-minute run leaves hundreds of retained closures on a
 * signal the caller may hold for the life of the process — measured at nineteen
 * after thirty seconds.
 *
 * It rejects with an error named AbortError, which is the shape every other
 * cancellable API in this ecosystem uses: `err.name === "AbortError"` is the
 * check callers already write, and a plain Error would never match it.
 */
function sleep(ms: number, signal?: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    if (signal?.aborted) return reject(abortError());
    let onAbort: (() => void) | undefined;
    const timer = setTimeout(() => {
      if (onAbort) signal?.removeEventListener("abort", onAbort);
      resolve();
    }, ms);
    onAbort = () => {
      clearTimeout(timer);
      signal?.removeEventListener("abort", onAbort!);
      reject(abortError());
    };
    signal?.addEventListener("abort", onAbort, { once: true });
  });
}

/**
 * Whether a repository argument was meant as a location.
 *
 * Deliberately narrow — a leading `.`, `/`, `~`, a Windows drive, or an embedded
 * separator. A repository name with a slash in it would be caught, and that is
 * the right trade: the daemon's names come from a directory basename, which
 * cannot contain one.
 */
function looksLikePath(s: string): boolean {
  return /^[./~]|^[A-Za-z]:[\\/]|[/\\]/.test(s);
}
