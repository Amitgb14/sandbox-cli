import { apiBase } from "@/lib/constants";

/**
 * The transport.
 *
 * Studio is built against the daemon's contract but has to be useful before the
 * daemon exists, so every endpoint carries a fixture. The rule that keeps that
 * from becoming a lie: **the UI always knows which one it got.** `useDaemon()`
 * reports the mode, the header shows it, and nothing silently presents a fixture
 * as a live reading — the same bargain the CLI makes when it prints the age of a
 * cached usage figure instead of the figure alone.
 *
 * The probe is cached rather than repeated per request: thirty fetches to a port
 * nobody is listening on is thirty seconds of a spinner. `reconnect()` is the
 * explicit retry, wired to the header's offline badge.
 */

export type TransportMode = "live" | "fixture" | "unknown";

let mode: TransportMode = "unknown";
let probe: Promise<TransportMode> | null = null;
const listeners = new Set<(m: TransportMode) => void>();

function setMode(next: TransportMode) {
  if (mode === next) return;
  mode = next;
  listeners.forEach((l) => l(next));
}

export function transportMode(): TransportMode {
  return mode;
}

export function onTransportChange(fn: (m: TransportMode) => void): () => void {
  listeners.add(fn);
  return () => listeners.delete(fn);
}

/** Forget the cached probe so the next request tries the daemon again. */
export function reconnect(): void {
  probe = null;
  setMode("unknown");
}

const PROBE_TIMEOUT_MS = 1200;

async function probeDaemon(): Promise<TransportMode> {
  if (typeof window === "undefined") return "fixture";
  if (probe) return probe;
  probe = (async () => {
    try {
      const ctl = new AbortController();
      const timer = setTimeout(() => ctl.abort(), PROBE_TIMEOUT_MS);
      const res = await fetch(`${apiBase()}/v1/health`, {
        signal: ctl.signal,
        cache: "no-store",
      });
      clearTimeout(timer);
      const next: TransportMode = res.ok ? "live" : "fixture";
      setMode(next);
      return next;
    } catch {
      setMode("fixture");
      return "fixture";
    }
  })();
  return probe;
}

export class ApiError extends Error {
  constructor(
    message: string,
    readonly status: number,
  ) {
    super(message);
    this.name = "ApiError";
  }
}

interface RequestOptions<T> {
  method?: "GET" | "POST" | "DELETE" | "PATCH";
  body?: unknown;
  /**
   * The fixture for this endpoint. Required — an endpoint with no fixture is one
   * that breaks the app the moment the daemon is not running, which during
   * frontend development is always.
   */
  fixture: () => T | Promise<T>;
  /** Simulated latency for the fixture path, so loading states are real. */
  latencyMs?: number;
  signal?: AbortSignal;
  /**
   * Pull the payload out of the daemon's envelope, for the endpoints that have
   * one — `GET /v1/agents` answers `{"agents": [...]}`, not a bare array.
   *
   * Applied to live responses only. The fixtures are the shape the components
   * consume, which is the point of them: a fixture that mirrored the wire
   * envelope would make every screen unwrap it twice, and the envelope is the
   * daemon's business rather than the UI's.
   *
   * This exists because the two halves of Studio were built to a contract
   * nobody arbitrated, and `agents.filter is not a function` is what that looks
   * like at runtime — a crash three screens in, not a type error at build time.
   */
  unwrap?: (body: unknown) => T;
}

export async function request<T>(path: string, opts: RequestOptions<T>): Promise<T> {
  const resolved = await probeDaemon();

  if (resolved === "live") {
    const headers: Record<string, string> = {};
    if (opts.body) headers["content-type"] = "application/json";
    const token = apiToken();
    if (token) headers.authorization = `Bearer ${token}`;

    const res = await fetch(`${apiBase()}${path}`, {
      method: opts.method ?? "GET",
      headers,
      body: opts.body ? JSON.stringify(opts.body) : undefined,
      signal: opts.signal,
      cache: "no-store",
    });
    noteAuthResult(res.status);
    if (!res.ok) {
      // The daemon's own words, not just the status line. Every non-2xx body is
      // an ErrorResponse saying what it refused and usually how to fix it —
      // "an agent is already running on X; stop it first", "no Go toolchain in
      // this image". Reporting `502 Bad Gateway` instead means the one thing
      // that would have answered the question was fetched and thrown away.
      throw new ApiError(await errorText(res, opts.method ?? "GET", path), res.status);
    }
    if (res.status === 204) return undefined as T;
    const body: unknown = await res.json();
    return (opts.unwrap ? opts.unwrap(body) : body) as T;
  }

  await sleep(opts.latencyMs ?? 180);
  return opts.fixture();
}

function sleep(ms: number) {
  return new Promise((r) => setTimeout(r, ms));
}

/**
 * A stream of newline-delimited JSON, which is how the daemon will ship logs and
 * metric samples. Falls back to replaying a fixture on a timer so the live views
 * can be built and reviewed without a backend.
 */
export async function* streamNdjson<T>(
  path: string,
  fixture: () => AsyncIterable<T>,
  signal?: AbortSignal,
): AsyncGenerator<T> {
  const resolved = await probeDaemon();
  if (resolved !== "live") {
    yield* fixture();
    return;
  }
  const res = await fetch(`${apiBase()}${path}`, {
    signal,
    cache: "no-store",
    headers: authHeaders(),
  });
  noteAuthResult(res.status);
  if (!res.ok || !res.body) throw new ApiError(`stream ${path} failed`, res.status);
  const reader = res.body.getReader();
  const decoder = new TextDecoder();
  let buf = "";
  while (true) {
    const { done, value } = await reader.read();
    if (done) break;
    buf += decoder.decode(value, { stream: true });
    const parts = buf.split("\n");
    buf = parts.pop() ?? "";
    for (const line of parts) {
      if (!line.trim()) continue;
      yield JSON.parse(line) as T;
    }
  }
}

/**
 * Pull the daemon's message out of a failed response, falling back to the status
 * line when there is nothing better.
 *
 * Defensive on purpose: this runs on the error path, where the body may be
 * empty, truncated, or HTML from something that is not the daemon at all — and a
 * parse failure here would replace a useful message with a stack trace about
 * JSON.
 */
async function errorText(res: Response, method: string, path: string): Promise<string> {
  const fallback = `${method} ${path} failed: ${res.status} ${res.statusText}`;
  try {
    const body = (await res.json()) as { error?: unknown };
    if (typeof body?.error === "string" && body.error.trim()) {
      return body.error;
    }
  } catch {
    // Not JSON, or no body. The status line is all there is.
  }
  return fallback;
}


/**
 * The bearer token, when the daemon was started with one.
 *
 * `sandbox-studio-api -token` is optional and off by default, so most local
 * setups send nothing and every request is accepted — the loopback bind is what
 * holds the line there. The console is the exception: typing at a *running*
 * agent requires a token whatever the rest of the server is doing, so this has
 * to exist before that screen can work.
 *
 * Three sources, in this order:
 *
 *   localStorage        set from the console panel, which is where the need
 *                       appears. Wins, because it is the one a person can change
 *                       without restarting anything.
 *   window.__SANDBOX_…  injected per request from the server's environment
 *                       (`SANDBOX_STUDIO_TOKEN`), so a scripted install can
 *                       generate a token, hand it to both halves and leave
 *                       nobody a value to copy between two terminals.
 *   NEXT_PUBLIC_…       baked in at build time, for a Studio that builds its own
 *                       bundle next to a daemon whose token never changes.
 *
 * The injected one is same-origin script content, which is the same exposure the
 * localStorage copy already has: a page on another origin can neither read this
 * document nor that key. What it is not is a *cookie* — this goes to a daemon on
 * another origin, and a cookie would be attached by the browser to requests this
 * code did not make.
 */
export const TOKEN_STORAGE_KEY = "sandbox-studio-token";

export function apiToken(): string {
  if (typeof window !== "undefined") {
    const stored = window.localStorage.getItem(TOKEN_STORAGE_KEY);
    if (stored) return stored;
    if (window.__SANDBOX_TOKEN__) return window.__SANDBOX_TOKEN__;
  }
  return process.env.NEXT_PUBLIC_STUDIO_TOKEN ?? "";
}

export function setApiToken(token: string) {
  if (typeof window === "undefined") return;
  if (token) window.localStorage.setItem(TOKEN_STORAGE_KEY, token);
  else window.localStorage.removeItem(TOKEN_STORAGE_KEY);
}


/**
 * The Authorization header, or nothing when this browser has no token.
 *
 * Shared by request() and the streams, because a stream that skipped it would
 * 401 on exactly the daemons where it matters — and a live view failing while
 * the rest of the page works is the hardest kind of gap to place.
 */
function authHeaders(): Record<string, string> {
  const token = apiToken();
  return token ? { authorization: `Bearer ${token}` } : {};
}

/**
 * A container's raw terminal output.
 *
 * SSE, but read with fetch rather than EventSource, and that is forced rather
 * than preferred: EventSource cannot set a request header, so it cannot carry
 * the bearer token this endpoint needs. A token in the query string would be a
 * credential in the daemon's logs and in the browser's history, which is a
 * worse trade than writing the reader by hand.
 *
 * Each frame is base64 — the payload is pty output carrying escape sequences
 * and can split a UTF-8 rune mid-read, so the transport does not try to
 * understand it. What comes back out is bytes, for a terminal emulator to
 * interpret.
 */
export async function* streamConsole(
  path: string,
  signal?: AbortSignal,
): AsyncGenerator<Uint8Array> {
  const resolved = await probeDaemon();
  if (resolved !== "live") return;

  const res = await fetch(`${apiBase()}${path}`, {
    signal,
    cache: "no-store",
    headers: authHeaders(),
  });
  noteAuthResult(res.status);
  if (!res.ok || !res.body) {
    throw new ApiError(await errorText(res, "GET", path), res.status);
  }
  const reader = res.body.getReader();
  const decoder = new TextDecoder();
  let buf = "";
  while (true) {
    const { done, value } = await reader.read();
    if (done) break;
    buf += decoder.decode(value, { stream: true });
    // SSE frames are separated by a blank line; a frame we have only half of
    // stays in the buffer until the rest arrives.
    const frames = buf.split("\n\n");
    buf = frames.pop() ?? "";
    for (const frame of frames) {
      for (const line of frame.split("\n")) {
        if (!line.startsWith("data:")) continue;
        const payload = line.slice(5).trim();
        if (!payload) continue;
        let text: string;
        try {
          // The daemon JSON-encodes the base64 string, so it arrives quoted.
          text = JSON.parse(payload) as string;
        } catch {
          continue;
        }
        const bin = atob(text);
        const out = new Uint8Array(bin.length);
        for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
        yield out;
      }
    }
  }
}


/**
 * Whether the daemon is rejecting the token this browser holds.
 *
 * This exists because "no token" and "wrong token" looked identical to the UI
 * and only one of them was recoverable. The bar that asks for a token was shown
 * when none was stored — so a stale or mistyped one hid it, every request
 * 401'd, and there was no way left in the interface to correct the value.
 * Observed: a wrong token gave 401s on /usage, /worktrees and /agents with no
 * prompt anywhere on the page.
 *
 * A module-level signal rather than a per-query one, because any authenticated
 * request answers the question and nothing should have to make an extra one to
 * ask it.
 */
let authRejected = false;
const authListeners = new Set<() => void>();

function noteAuthResult(status: number) {
  // Latched: set by a 401 and never cleared by a success.
  //
  // Clearing on any 2xx looked tidier and was wrong — /v1/health answers 200
  // without a token by design, and it is polled, so every rejection was wiped
  // within a second and the bar never appeared. The flag is only interesting
  // until somebody supplies a token, and saving one reloads the page, which
  // resets this anyway.
  //
  // 403 is deliberately not counted. It is what the console endpoints answer
  // when the *daemon* has no token — a different problem with a different fix,
  // and treating it as a bad credential would ask for a token that cannot help.
  if (status !== 401 || authRejected) return;
  authRejected = true;
  authListeners.forEach((cb) => cb());
}

export function isAuthRejected(): boolean {
  return authRejected;
}

export function onAuthChange(cb: () => void): () => void {
  authListeners.add(cb);
  return () => authListeners.delete(cb);
}
