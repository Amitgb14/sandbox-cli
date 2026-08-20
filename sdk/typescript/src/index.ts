import { Transport } from "./transport.js";
import { ApiError, ConnectionError, WaitError, abortError } from "./errors.js";
import { discoverToken, discoverUrl } from "./discover.js";
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
  WorktreesResponse,
  SessionSummary,
  SessionListResponse,
} from "./contract.js";

export * from "./contract.js";
export { ApiError, ConnectionError, TimeoutError, WaitError } from "./errors.js";
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

  /** One repository, by id or by name. Refuses an ambiguous name rather than
   *  picking: two clones of a same-named repo share a name and not an id. */
  async project(idOrName: string): Promise<Project> {
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
    // Name what *is* registered. This throws for two different mistakes — a
    // typo, and asking for a repository nobody added — and the list separates
    // them without a second call. A path-shaped argument gets its own sentence
    // because it is not a typo at all but a wrong model of where this script
    // runs: the daemon may be on another machine, so a directory here means
    // nothing to it. `addProject` is the one place a path crosses, which is
    // exactly the shape of the daemon's own rule.
    const known = all.map((p) => p.name).join(", ") || "none";
    const pathish = idOrName === "." || idOrName === ".." || /^[./~]|^[A-Za-z]:[\\/]/.test(idOrName);
    throw new Error(
      pathish
        ? `${idOrName} is a path, and repositories are named rather than located: this script's own directory ` +
          `is not what the agent works on, and the daemon at ${this.t.url} may not even be on this machine. ` +
          `Registered: ${known}. To add a directory on the daemon's machine, call studio.addProject("/abs/path").`
        : `no repository ${idOrName} is registered with the daemon at ${this.t.url}. Registered: ${known}. ` +
          `Add one with studio.addProject("/abs/path"), or in Studio.`,
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
   * The path is resolved on that machine, not this one. Against a remote daemon
   * `process.cwd()` is a local answer to a question about somewhere else, which
   * is why nothing here defaults to it.
   *
   * Adding a repository that is already registered is a no-op that returns the
   * existing row, so this is safe to call on every start.
   */
  async addProject(path: string): Promise<Project> {
    if (!path.trim()) throw new Error("addProject needs a path on the daemon's machine");
    const rec = await this.t.request<ProjectRecord>("POST", "/v1/projects", { path });
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

  private common(opts: RunOptions): Partial<RunCreateRequest> {
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
      if (!opts.replaceFinished) {
        // The daemon's own remedy is "GET /v1/runs/<id>/logs, then DELETE
        // /v1/runs/<id>" — correct, and addressed to a client holding a URL.
        // Somebody holding *this* client has two better moves, and being told
        // to reach for curl from inside a typed API is how a library teaches
        // people to work around it. The daemon's sentence is kept whole; this
        // adds the sentence it cannot know to write.
        throw new ApiError(
          err.status,
          err.endpoint,
          `${err.message}\n  From here: pass { replaceFinished: true } to run this anyway, ` +
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
        throw new ApiError(err.status, err.endpoint, `${err.message}\n  ${String(clearErr)}`);
      }
      started = await this.t.request<Run>("POST", "/v1/runs", body, opts.signal);
    }
    try {
      return await this.wait(started.id, opts);
    } catch (cause) {
      // The launch already succeeded, so the container exists whatever went
      // wrong here — a daemon restart mid-poll, a 502, an abort. Rejecting with
      // a bare error would leave the caller with no id to stop or remove, and a
      // detached run holds `sandbox-<repo>-<branch>`, which docker refuses to
      // duplicate: the branch would be blocked by something nobody can name.
      throw new WaitError(started, cause);
    }
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
