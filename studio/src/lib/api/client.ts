import { API_BASE } from "@/lib/constants";

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
      const res = await fetch(`${API_BASE}/v1/health`, {
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
    const res = await fetch(`${API_BASE}${path}`, {
      method: opts.method ?? "GET",
      headers: opts.body ? { "content-type": "application/json" } : undefined,
      body: opts.body ? JSON.stringify(opts.body) : undefined,
      signal: opts.signal,
      cache: "no-store",
    });
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
  const res = await fetch(`${API_BASE}${path}`, { signal, cache: "no-store" });
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
