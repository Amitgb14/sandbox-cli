// TypeScript client for Sandbox API v1 (docs/api/v1.md).
//
// fetch-based, so it runs on Node 18+, Deno, Bun and in browsers, against an
// https:// or http:// endpoint. (A unix-socket endpoint needs a runtime-specific
// fetch dispatcher; pass your own `fetch`.) Byte fields are Uint8Array; the
// wire format's base64 is handled here.
//
//   const c = new Client("https://sandbox.example.internal:7443", { token });
//   const sb = await c.createSandbox({ network: { mode: "none" } });
//   const res = await c.run(sb.id, ["echo", "hello"]);
//   new TextDecoder().decode(res.stdout);  // "hello\n"
//   await c.terminateSandbox(sb.id);

export interface NetworkPolicy {
  mode: "none" | "allowlist" | "open";
  /** Absent or null: the server's default list. [] means nothing, and is refused. */
  allow?: string[] | null;
  deny?: string[];
}

export interface Sandbox {
  id: string;
  name?: string;
  state: "pending" | "running" | "suspended" | "terminated";
  image: string;
  cpus: number;
  memory_mb: number;
  disk_mb: number;
  /** Names only: values are never returned. */
  env_names?: string[];
  network: NetworkPolicy;
  created_at: string;
  idle_timeout_secs: number;
}

export interface CreateSandboxRequest {
  name?: string;
  image?: string;
  cpus?: number;
  memory_mb?: number;
  disk_mb?: number;
  env?: Record<string, string>;
  network?: NetworkPolicy;
  idle_timeout_secs?: number;
  snapshot_id?: string;
  bind?: { host_path: string; read_only?: boolean };
}

export interface RunOptions {
  env?: Record<string, string>;
  cwd?: string;
  stdin?: Uint8Array;
  timeoutSecs?: number;
}

export interface RunResult {
  exit_code: number;
  stdout: Uint8Array;
  stderr: Uint8Array;
  truncated: boolean;
  timed_out: boolean;
}

export interface Process {
  pid: number;
  tty?: boolean;
  argv: string[];
  state: "running" | "exited";
  exit_code: number | null;
  started_at: string;
}

export type OutputEvent =
  | { stream: "stdout" | "stderr"; data: Uint8Array }
  | { exit_code: number };

export interface DirEntry {
  name: string;
  type: "file" | "dir";
  size: number;
}

export interface Snapshot {
  id: string;
  sandbox: string;
  image: string;
  bytes: number;
  created_at: string;
}

/** A non-2xx response; `code` is the API's error code (refused, unsupported, not_found, ...). */
export class ApiError extends Error {
  constructor(
    public readonly status: number,
    public readonly code: string,
    message: string,
  ) {
    super(`${code} (${status}): ${message}`);
    this.name = "ApiError";
  }
}

function toB64(b: Uint8Array): string {
  let s = "";
  for (const c of b) s += String.fromCharCode(c);
  return btoa(s);
}

function fromB64(s: string | null | undefined): Uint8Array {
  if (!s) return new Uint8Array();
  const bin = atob(s);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}

export interface ClientOptions {
  token?: string;
  /** A fetch implementation; default globalThis.fetch. */
  fetch?: typeof fetch;
}

export class Client {
  private readonly base: string;
  private readonly token: string;
  private readonly fetchImpl: typeof fetch;

  constructor(endpoint: string, opts: ClientOptions = {}) {
    const u = new URL(endpoint);
    if (u.protocol !== "https:" && u.protocol !== "http:") {
      throw new Error("endpoint: want http(s)://host[:port]");
    }
    this.base = endpoint.replace(/\/+$/, "");
    this.token = opts.token ?? "";
    this.fetchImpl = opts.fetch ?? globalThis.fetch.bind(globalThis);
  }

  private async request(
    method: string,
    path: string,
    init: { query?: Record<string, string>; body?: BodyInit; contentType?: string } = {},
  ): Promise<Response> {
    let url = this.base + path;
    if (init.query) url += "?" + new URLSearchParams(init.query).toString();
    const headers: Record<string, string> = {};
    if (init.contentType) headers["Content-Type"] = init.contentType;
    if (this.token) headers["Authorization"] = "Bearer " + this.token;
    const resp = await this.fetchImpl(url, { method, headers, body: init.body });
    if (!resp.ok) {
      const text = await resp.text();
      let code = "internal";
      let message = text.trim();
      try {
        const e = JSON.parse(text).error;
        code = e.code ?? code;
        message = e.message ?? message;
      } catch {
        // not a JSON error body
      }
      throw new ApiError(resp.status, code, message);
    }
    return resp;
  }

  private async json<T>(method: string, path: string, payload?: unknown): Promise<T> {
    const resp = await this.request(method, path, payload === undefined ? {} : {
      body: JSON.stringify(payload),
      contentType: "application/json",
    });
    const text = await resp.text();
    return (text ? JSON.parse(text) : undefined) as T;
  }

  private sbx(ref: string): string {
    return "/v1/sandboxes/" + encodeURIComponent(ref);
  }

  capabilities(): Promise<Record<string, unknown>> {
    return this.json("GET", "/v1/capabilities");
  }

  createSandbox(req: CreateSandboxRequest = {}): Promise<Sandbox> {
    const body: CreateSandboxRequest = { ...req };
    if (body.network && body.network.allow === undefined) body.network = { ...body.network, allow: null };
    return this.json("POST", "/v1/sandboxes", body);
  }

  sandbox(ref: string): Promise<Sandbox> {
    return this.json("GET", this.sbx(ref));
  }

  async sandboxes(): Promise<Sandbox[]> {
    return (await this.json<{ sandboxes: Sandbox[] }>("GET", "/v1/sandboxes")).sandboxes;
  }

  updateNetwork(ref: string, network: NetworkPolicy): Promise<Sandbox> {
    return this.json("PATCH", this.sbx(ref), { network });
  }

  async terminateSandbox(ref: string): Promise<void> {
    await this.json("DELETE", this.sbx(ref));
  }

  suspend(ref: string): Promise<Sandbox> {
    return this.json("POST", this.sbx(ref) + "/suspend");
  }

  resume(ref: string): Promise<Sandbox> {
    return this.json("POST", this.sbx(ref) + "/resume");
  }

  createSnapshot(ref: string): Promise<Snapshot> {
    return this.json("POST", this.sbx(ref) + "/snapshots");
  }

  async snapshots(): Promise<Snapshot[]> {
    return (await this.json<{ snapshots: Snapshot[] }>("GET", "/v1/snapshots")).snapshots;
  }

  async deleteSnapshot(id: string): Promise<void> {
    await this.json("DELETE", "/v1/snapshots/" + encodeURIComponent(id));
  }

  async run(ref: string, argv: string[], opts: RunOptions = {}): Promise<RunResult> {
    const req: Record<string, unknown> = { argv };
    if (opts.env) req.env = opts.env;
    if (opts.cwd) req.cwd = opts.cwd;
    if (opts.stdin) req.stdin = toB64(opts.stdin);
    if (opts.timeoutSecs) req.timeout_secs = opts.timeoutSecs;
    const r = await this.json<Record<string, unknown>>("POST", this.sbx(ref) + "/run", req);
    return {
      exit_code: r.exit_code as number,
      stdout: fromB64(r.stdout as string),
      stderr: fromB64(r.stderr as string),
      truncated: Boolean(r.truncated),
      timed_out: Boolean(r.timed_out),
    };
  }

  startProcess(ref: string, argv: string[], opts: Omit<RunOptions, "stdin" | "timeoutSecs"> = {}): Promise<Process> {
    return this.json("POST", this.sbx(ref) + "/processes", { argv, ...opts });
  }

  async processes(ref: string): Promise<Process[]> {
    return (await this.json<{ processes: Process[] }>("GET", this.sbx(ref) + "/processes")).processes;
  }

  /** Output events from the start; the last carries exit_code. */
  async *followOutput(ref: string, pid: number): AsyncGenerator<OutputEvent> {
    const resp = await this.request("GET", `${this.sbx(ref)}/processes/${pid}/output`);
    if (!resp.body) return;
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
        yield "exit_code" in ev ? { exit_code: ev.exit_code } : { stream: ev.stream, data: fromB64(ev.data) };
      }
      if (done) return;
    }
  }

  async writeStdin(ref: string, pid: number, data: Uint8Array, close = false): Promise<void> {
    await this.request("POST", `${this.sbx(ref)}/processes/${pid}/stdin`, {
      query: close ? { close: "1" } : undefined,
      body: data,
      contentType: "application/octet-stream",
    });
  }

  async signal(ref: string, pid: number, signal: "INT" | "TERM" | "KILL" | "HUP"): Promise<void> {
    await this.json("POST", `${this.sbx(ref)}/processes/${pid}/signal`, { signal });
  }

  async readFile(ref: string, path: string): Promise<Uint8Array> {
    const resp = await this.request("GET", this.sbx(ref) + "/files", { query: { path } });
    return new Uint8Array(await resp.arrayBuffer());
  }

  async writeFile(ref: string, path: string, data: Uint8Array): Promise<void> {
    await this.request("PUT", this.sbx(ref) + "/files", { query: { path }, body: data, contentType: "application/octet-stream" });
  }

  async removeFile(ref: string, path: string): Promise<void> {
    await this.request("DELETE", this.sbx(ref) + "/files", { query: { path } });
  }

  async listDir(ref: string, path: string): Promise<DirEntry[]> {
    const resp = await this.request("GET", this.sbx(ref) + "/dirs", { query: { path } });
    return ((await resp.json()) as { entries: DirEntry[] }).entries;
  }

  /** Clone a git bundle (which must carry HEAD) into /workspace as `branch`. */
  async putWorkspace(ref: string, branch: string, bundle: Uint8Array): Promise<void> {
    await this.request("POST", this.sbx(ref) + "/workspace", { query: { branch }, body: bundle, contentType: "application/octet-stream" });
  }

  /** base..branch as a git bundle. It comes from the guest: verify it before fetching from it. */
  async getWorkspaceBundle(ref: string, base: string, branch: string): Promise<Uint8Array> {
    const resp = await this.request("GET", this.sbx(ref) + "/workspace/bundle", { query: { base, branch } });
    return new Uint8Array(await resp.arrayBuffer());
  }
}
