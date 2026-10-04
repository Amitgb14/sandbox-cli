/**
 * Studio's transport. Everything goes to the server that served this page —
 * `sandbox-cli studio` — under /api: its own endpoints, and /api/v1, which it
 * proxies to the sandboxd of the current context with that sandboxd's token.
 * The browser never holds that token.
 *
 * It holds Studio's own, which `sandbox-cli studio` prints in the URL's
 * fragment (`#token=…`). A fragment is never sent to a server, so it is read
 * here once, kept in sessionStorage — this tab, not every tab, and not after
 * the browser closes — and wiped from the address bar so it is not copied
 * along with a link.
 */

import type { OutputEvent } from "@/lib/types";
import { currentOrg } from "@/lib/org";

/** The header that selects an organisation on a gateway (lib/org.ts). */
export const ORG_HEADER = "X-Sandbox-Org";

const TOKEN_KEY = "sandbox-studio-token";

function readToken(): string {
  if (typeof window === "undefined") return "";
  const m = window.location.hash.match(/(?:^#|&)token=([0-9a-f]+)/);
  if (m) {
    try {
      sessionStorage.setItem(TOKEN_KEY, m[1]);
    } catch {
      // A browser that refuses storage still gets this page's requests.
    }
    window.history.replaceState(null, "", window.location.pathname + window.location.search);
    return m[1];
  }
  try {
    return sessionStorage.getItem(TOKEN_KEY) ?? "";
  } catch {
    return "";
  }
}

let token: string | null = null;

export function getToken(): string {
  if (token === null || token === "") token = readToken();
  return token;
}

export function setToken(t: string) {
  token = t.trim();
  try {
    sessionStorage.setItem(TOKEN_KEY, token);
  } catch {
    // see readToken
  }
}

/** A failed request, with the server's own message. */
export class ApiError extends Error {
  constructor(
    public readonly status: number,
    message: string,
    public readonly code?: string,
  ) {
    super(message);
    this.name = "ApiError";
  }
}

/**
 * Studio's token, and the selected organisation unless noOrg: the headers
 * every request to Studio's server carries. The token is Studio's own, never
 * the endpoint's, which sandbox-cli puts on as it proxies.
 */
export function authHeaders(noOrg = false): Record<string, string> {
  const h: Record<string, string> = {};
  const t = getToken();
  if (t) h.Authorization = `Bearer ${t}`;
  const org = noOrg ? null : currentOrg();
  if (org) h[ORG_HEADER] = org;
  return h;
}

export async function apiFetch<T>(path: string, opts: RequestInit & { json?: unknown; noOrg?: boolean } = {}): Promise<T> {
  const { noOrg, ...init } = opts;
  const headers = new Headers(init.headers);
  for (const [k, v] of Object.entries(authHeaders(noOrg))) headers.set(k, v);
  let body = init.body;
  if (init.json !== undefined) {
    headers.set("Content-Type", "application/json");
    body = JSON.stringify(init.json);
  }
  const resp = await fetch(`/api${path}`, { ...init, headers, body });
  if (!resp.ok) {
    let message = resp.statusText;
    let code: string | undefined;
    try {
      const e = (await resp.json()).error;
      message = e?.message ?? message;
      code = e?.code;
    } catch {
      // not JSON
    }
    throw new ApiError(resp.status, message, code);
  }
  if (resp.status === 204) return undefined as T;
  const text = await resp.text();
  return (text ? JSON.parse(text) : undefined) as T;
}

/** Bytes from the API's base64 fields, as text for a terminal or a log view. */
export function decodeB64(s: string | null | undefined): string {
  if (!s) return "";
  const bin = atob(s);
  const bytes = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i);
  return new TextDecoder().decode(bytes);
}

/**
 * Follows a process's output from the start: one callback per line of the
 * stream, the last carrying exit_code. Ends when the process exits, the
 * signal aborts, or the connection drops.
 */
export async function followOutput(
  sandbox: string,
  pid: number,
  onEvent: (ev: OutputEvent) => void,
  signal: AbortSignal,
): Promise<void> {
  const resp = await fetch(
    `/api/v1/sandboxes/${encodeURIComponent(sandbox)}/processes/${pid}/output`,
    { headers: authHeaders(), signal },
  );
  if (!resp.ok || !resp.body) throw new ApiError(resp.status, resp.statusText);
  const reader = resp.body.getReader();
  const dec = new TextDecoder();
  let buf = "";
  for (;;) {
    const { value, done } = await reader.read();
    if (value) buf += dec.decode(value, { stream: true });
    let nl: number;
    while ((nl = buf.indexOf("\n")) >= 0) {
      const line = buf.slice(0, nl);
      buf = buf.slice(nl + 1);
      if (!line) continue;
      const ev = JSON.parse(line);
      onEvent("exit_code" in ev ? { exit_code: ev.exit_code } : { stream: ev.stream, data: decodeB64(ev.data) });
    }
    if (done) return;
  }
}

/** The WebSocket address of a process's terminal bridge. */
export function attachURL(sandbox: string, pid?: number): string {
  const proto = window.location.protocol === "https:" ? "wss:" : "ws:";
  const q = new URLSearchParams({ sandbox, token: getToken() });
  if (pid) q.set("pid", String(pid));
  // A browser cannot give a WebSocket headers, so the organisation rides in
  // the query, as the token does; Studio's server passes it on as a header.
  const org = currentOrg();
  if (org) q.set("org", org);
  return `${proto}//${window.location.host}/api/ws/attach?${q.toString()}`;
}
