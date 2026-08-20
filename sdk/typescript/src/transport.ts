import { ApiError, ConnectionError, TimeoutError, abortError } from "./errors.js";
import type { ErrorResponse } from "./contract.js";

/**
 * The one way this package talks to a daemon.
 *
 * Everything the client can do goes through here, which is what keeps the
 * promise the design makes: an SDK is a *client*, never a second control plane.
 * There is no docker socket in this package, nothing shells out to sandbox-cli,
 * and no argv is assembled — because the gates that make a sandbox a sandbox are
 * applied where `sandbox.Options` are built, and a Node process cannot repeat
 * them. If this library wants a capability the daemon does not expose, the
 * daemon grows an endpoint and the gate is written once, in Go, with a test.
 */
export interface TransportOptions {
  url: string;
  token: string;
  /** Bounds a single request. The wait for a *run* is a different clock. */
  timeoutMs?: number;
  fetch?: typeof globalThis.fetch;
}

const DEFAULT_REQUEST_TIMEOUT_MS = 30_000;

export class Transport {
  readonly url: string;
  readonly token: string;
  private readonly timeoutMs: number;
  /** Exposed so every path uses the caller's fetch, `follow` included: an
   *  injected wrapper that eleven methods honour and the twelfth ignores is
   *  worse than none, because it is trusted. */
  readonly doFetch: typeof globalThis.fetch;

  constructor(opts: TransportOptions) {
    this.url = opts.url.replace(/\/+$/, "");
    this.token = opts.token;
    this.timeoutMs = opts.timeoutMs ?? DEFAULT_REQUEST_TIMEOUT_MS;
    this.doFetch = opts.fetch ?? globalThis.fetch;
  }

  headers(extra?: Record<string, string>): Record<string, string> {
    const h: Record<string, string> = { ...extra };
    // Sent only when there is one: a daemon started without -token refuses a
    // bearer it never issued on nothing, but an empty header is still a lie
    // about what this client holds.
    if (this.token) h.Authorization = `Bearer ${this.token}`;
    return h;
  }

  async request<T>(
    method: string,
    path: string,
    body?: unknown,
    signal?: AbortSignal,
  ): Promise<T> {
    const url = this.url + path;
    const init: RequestInit = { method, headers: this.headers() };
    if (body !== undefined) {
      // The daemon requires this on any request carrying a body, and refuses
      // the request rather than guessing — which is also what stops a
      // cross-origin POST from staying "simple" enough to skip a preflight.
      init.headers = this.headers({ "Content-Type": "application/json" });
      init.body = JSON.stringify(body);
    }
    const controller = new AbortController();
    let timedOut = false;
    const timer = setTimeout(() => {
      timedOut = true;
      controller.abort();
    }, this.timeoutMs);
    const onAbort = () => controller.abort();
    if (signal) {
      if (signal.aborted) controller.abort();
      else signal.addEventListener("abort", onAbort);
    }
    init.signal = controller.signal;

    let res: Response;
    try {
      res = await this.doFetch(url, init);
    } catch (cause) {
      // Three different failures arrive here as one rejection, and calling them
      // all "cannot reach the daemon" sends the reader to the network for two
      // of them: a slow but healthy daemon, and a cancel the caller asked for.
      if (timedOut) throw new TimeoutError(`${method} ${path}`, this.timeoutMs);
      if (signal?.aborted) throw abortError(`${method} ${path} was aborted`);
      throw new ConnectionError(url, cause);
    } finally {
      clearTimeout(timer);
      // Removed on every path. Left attached, one per request, these accumulate
      // on a signal the caller may hold for the life of the process.
      signal?.removeEventListener("abort", onAbort);
    }

    if (!res.ok) throw new ApiError(res.status, `${method} ${path}`, await errorText(res));
    if (res.status === 204) return undefined as T;
    const text = await res.text();
    if (text.length === 0) return undefined as T;
    return JSON.parse(text) as T;
  }

  /** The absolute URL of a streaming endpoint. The credential travels in the
   *  headers `headers()` builds, exactly as it does for every other request —
   *  the daemon's `?token=` carve-out is for browsers, which cannot set them. */
  streamUrl(path: string): string {
    return this.url + path;
  }
}

/**
 * The daemon's message, or the best available account of why it said no.
 *
 * Every refusal in this API is `{"error": "..."}`; anything else means the reply
 * did not come from the daemon at all — a proxy's error page, most often — and
 * the body is worth more than a constructed sentence in that case too.
 */
async function errorText(res: Response): Promise<string> {
  const text = await res.text().catch(() => "");
  if (!text) return "";
  try {
    const parsed = JSON.parse(text) as ErrorResponse;
    if (parsed && typeof parsed.error === "string") return parsed.error;
  } catch {
    // not JSON
  }
  return text.slice(0, 500);
}
