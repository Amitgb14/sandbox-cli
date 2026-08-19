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
