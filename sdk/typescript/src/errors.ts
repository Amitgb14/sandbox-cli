/**
 * What went wrong, in the daemon's own words.
 *
 * The refusals in this API are the product of a lot of thought — "resume and
 * handoff_from cannot be combined: one reopens a conversation, the other starts
 * a new one from a briefing about it" — and wrapping them in "request failed"
 * throws that away at the moment somebody needs it. So the message is carried
 * verbatim, with the status and the endpoint beside it.
 */
export class ApiError extends Error {
  readonly status: number;
  readonly endpoint: string;

  constructor(status: number, endpoint: string, message: string) {
    super(message || `${endpoint} failed with ${status}`);
    this.name = "ApiError";
    this.status = status;
    this.endpoint = endpoint;
  }

  /** The daemon refused the credential, rather than the request. */
  get unauthorized(): boolean {
    return this.status === 401 || this.status === 403;
  }
}

/** The daemon could not be reached at all — a different thing from a refusal. */
export class ConnectionError extends Error {
  readonly url: string;

  constructor(url: string, cause: unknown) {
    super(`cannot reach the sandbox daemon at ${url}: ${String(cause)}`);
    this.name = "ConnectionError";
    this.url = url;
  }
}

/**
 * A request outlived its own clock.
 *
 * Distinct from ConnectionError, and the distinction is the point: a daemon that
 * is slow — a large log fetch, a stop waiting out the SIGTERM grace — is
 * reachable, and reporting it as unreachable sends the reader to the network
 * when the answer is the timeout on this side.
 */
export class TimeoutError extends Error {
  readonly endpoint: string;
  readonly ms: number;

  constructor(endpoint: string, ms: number) {
    super(`${endpoint} did not answer within ${ms}ms`);
    this.name = "TimeoutError";
    this.endpoint = endpoint;
    this.ms = ms;
  }
}

/** The conventional shape, so `err.name === "AbortError"` works as it does everywhere else. */
export function abortError(message = "aborted"): Error {
  const err = new Error(message);
  err.name = "AbortError";
  return err;
}

/**
 * A run was launched and then something went wrong while waiting for it.
 *
 * It carries the run, and that is the whole reason it exists: the container is
 * still out there holding its name — `sandbox-<repo>-<branch>`, which docker
 * refuses to duplicate — so a caller that lost the id cannot stop it, cannot
 * remove it, and cannot launch on that branch again. Throwing a bare poll error
 * would have made a transient blip into a blocked branch with no handle.
 */
export class WaitError extends Error {
  readonly run: { id: string; name?: string };

  constructor(run: { id: string; name?: string }, cause: unknown) {
    super(`run ${run.id} was started but waiting for it failed: ${String(cause)}`);
    this.name = "WaitError";
    this.run = run;
    this.cause = cause;
  }
}
