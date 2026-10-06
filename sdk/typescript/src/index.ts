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
  /** The snapshot schedule, when it has one. */
  snapshot_every_secs?: number;
  snapshot_keep?: number;
  labels?: Record<string, string>;
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
  /** Snapshot the sandbox this often while it runs, keeping the newest `snapshot_keep` (default 1); within the server's limits. */
  snapshot_every_secs?: number;
  snapshot_keep?: number;
  /** Your own metadata: returned with the sandbox, filters `sandboxes()`, recorded in its audit events. */
  labels?: Record<string, string>;
  /** Named volumes to mount (capability `volumes`); one live sandbox at a time. */
  volumes?: VolumeMount[];
}

export interface VolumeMount {
  name: string;
  path: string;
  read_only?: boolean;
}

export interface Volume {
  name: string;
  size_mb: number;
  created_at: string;
  attached_to?: string;
}

/** One audit event. Environment variables appear by name only. */
export interface AuditEvent {
  time: string;
  type: string;
  sandbox: string;
  name?: string;
  image?: string;
  labels?: Record<string, string>;
  network?: NetworkPolicy;
  env_names?: string[];
  snapshot?: string;
  pid?: number;
  /** process.started: the program, how many arguments, and SHA-256 over them (each followed by NUL). Never their text. */
  program?: string;
  arg_count?: number;
  args_sha256?: string;
  cwd?: string;
  exit_code?: number;
  duration_ms?: number;
  path?: string;
  bytes?: number;
  port?: number;
  reason?: string;
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
  /** "memory": memory, processes and disk; "disk": the files only, a fork boots afresh. */
  kind: "memory" | "disk";
  /** Taken by the sandbox's schedule, which may remove it. */
  scheduled?: boolean;
  bytes: number;
  created_at: string;
}

/** GET /v1/whoami on a gateway: who the API key belongs to. */
export interface Whoami {
  user: string;
  /** The key's own tenant; "" is the default one. */
  tenant: string;
  key_id: string;
  scopes: string[];
  /** The organization this request acted in ("default" for the default tenant). */
  org?: string;
}

/** One organization the caller may act in (GET /v1/orgs). */
export interface Org {
  name: string;
  role: "owner" | "member";
  created?: string;
  current: boolean;
}

/** A member of an organization: a user, named with the tenant of their own keys. */
export interface OrgMember {
  user: string;
  /** Absent for the default tenant. */
  tenant?: string;
  role: "owner" | "member";
  added?: string;
}

/** An organization as an admin lists it (GET /v1/admin/orgs). */
export interface AdminOrg {
  name: string;
  created: string;
  created_by: string;
  created_by_tenant?: string;
  members: number;
  owners: number;
}

/** GET /v1/ssh on a gateway. */
export interface SSHInfo {
  host: string;
  port: number;
  /** Public host keys as known_hosts lines without the host ("ssh-ed25519 AAAA…"). */
  host_keys: string[];
  /** SHA256:… of the first host key. */
  fingerprint: string;
}

/** A registered SSH public key. */
export interface SSHKeyInfo {
  id: string;
  fingerprint: string;
  key: string;
  /** Set when the key is limited to one sandbox. */
  sandbox?: string;
  created: string;
}

/** A short-lived SSH login: `user` is the token, and the whole credential until it expires. */
export interface SSHAccess {
  user: string;
  host: string;
  port: number;
  expires_at: string;
  command: string;
}

/** A job: one sandbox per run, made, watched and taken down by a gateway. */
export interface JobSpec {
  name?: string;
  image?: string;
  /** The argv each run starts; or `agent` and `prompt` (or `prompts`, one run each). */
  command?: string[];
  agent?: string;
  prompt?: string;
  prompts?: string[];
  parallelism?: number;
  completions?: number;
  retries?: number;
  timeout_secs?: number;
  env?: Record<string, string>;
  /** Names of the tenant's secrets, each set in the run's environment under its name. */
  secrets?: string[];
  network?: NetworkPolicy;
  resources?: { cpus?: number; memory_mb?: number; disk_mb?: number };
  from_snapshot?: string;
  keep?: { output?: boolean; files?: string[] };
  /** POSTed the run's and the job's state when each ends: https, or http to loopback. */
  notify?: string;
}

/** POST /v1/agent-runs: a job of one agent run. */
export type AgentRunOptions = Omit<JobSpec, "agent" | "prompt" | "command" | "prompts" | "parallelism" | "completions">;

export interface JobRun {
  n: number;
  state: "queued" | "running" | "succeeded" | "failed" | "timed_out" | "cancelled";
  sandbox?: string;
  pid?: number;
  attempts: number;
  exit_code?: number;
  started_at?: string;
  finished_at?: string;
  error?: string;
  output_truncated?: boolean;
  files?: { path: string; size: number; truncated?: boolean; error?: string }[];
}

export interface Job {
  id: string;
  name?: string;
  state: "running" | "succeeded" | "failed" | "cancelled";
  spec: JobSpec;
  env_names?: string[];
  created_at: string;
  finished_at?: string;
  expires_at?: string;
  queued: number;
  running: number;
  succeeded: number;
  failed: number;
  /** Absent in a listing. */
  runs?: JobRun[];
  error?: string;
}

export interface JobOutput {
  stdout: Uint8Array;
  stderr: Uint8Array;
  truncated: boolean;
}

/** A secret as listed: its name, never its value. */
export interface SecretInfo {
  name: string;
  updated_at: string;
}

/** What POST /v1/services and PUT /v1/services/{name} send (docs/services.md). */
export interface ServiceSpec {
  /** A DNS label with no "--"; unique within the tenant. */
  name: string;
  image?: string;
  /** Started in each replica as a detached process; a replica whose command exits is replaced. */
  command?: string[];
  replicas: number;
  resources?: { cpus?: number; memory_mb?: number; disk_mb?: number };
  /** The port on each replica's loopback: where an HTTP health check and the router go. */
  port?: number;
  /** Exactly one of `http` (a path; 2xx or 3xx is healthy) or `command` (exit 0 is healthy). */
  health?: { http?: string; command?: string[]; every_secs?: number; timeout_secs?: number; failures?: number };
  env?: Record<string, string>;
  network?: NetworkPolicy;
  placement?: { spread?: "node" };
  /** Routed through the gateway's HTTP router. */
  public?: boolean;
  /** Names of the tenant's secrets, each set in every replica's environment. */
  secrets?: string[];
}

export interface ServiceReplica {
  sandbox: string;
  node: string;
  revision: number;
  state: "starting" | "healthy" | "unhealthy" | "lost";
  healthy: boolean;
  last_check?: string;
  last_error?: string;
  restarts: number;
  created_at: string;
}

export interface Service {
  /** The spec, without env values: their names are in env_names. */
  spec: ServiceSpec;
  env_names?: string[];
  owner: string;
  tenant?: string;
  revision: number;
  serving: number;
  desired: number;
  ready: number;
  restarts: number;
  rollout?: { state: "in_progress" | "done" | "failed"; from: number; to: number; reason?: string };
  error?: string;
  url?: string;
  replicas: ServiceReplica[];
  created_at: string;
  updated_at: string;
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

/**
 * A request body from bytes. Since TypeScript 5.7 a Uint8Array may view a
 * SharedArrayBuffer, which fetch does not accept as a body, so a plain
 * Uint8Array no longer type-checks as one. slice() copies into an
 * ArrayBuffer-backed array, which every TypeScript version accepts; the copy
 * is bounded by the API's own limits on what one request carries.
 */
function asBody(b: Uint8Array): BodyInit {
  return b.slice();
}

export interface ClientOptions {
  token?: string;
  /**
   * On a gateway, the organization every request acts in (the X-Sandbox-Org
   * header); absent is the key's own tenant. One the key's user is not a
   * member of is refused with ApiError code "not_found".
   */
  org?: string;
  /** A fetch implementation; default globalThis.fetch. */
  fetch?: typeof fetch;
}

export class Client {
  private readonly base: string;
  private readonly token: string;
  private readonly org: string;
  private readonly fetchImpl: typeof fetch;

  constructor(endpoint: string, opts: ClientOptions = {}) {
    const u = new URL(endpoint);
    if (u.protocol !== "https:" && u.protocol !== "http:") {
      throw new Error("endpoint: want http(s)://host[:port]");
    }
    this.base = endpoint.replace(/\/+$/, "");
    this.token = opts.token ?? "";
    this.org = opts.org ?? "";
    this.fetchImpl = opts.fetch ?? globalThis.fetch.bind(globalThis);
  }

  /** A client like this one acting in organization `org` ("" for the key's own tenant). */
  withOrg(org: string): Client {
    return new Client(this.base, { token: this.token, org, fetch: this.fetchImpl });
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
    if (this.org) headers["X-Sandbox-Org"] = this.org;
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

  /** Newest first; with `labels`, only sandboxes carrying every one. */
  async sandboxes(labels?: Record<string, string>): Promise<Sandbox[]> {
    let path = "/v1/sandboxes";
    if (labels && Object.keys(labels).length > 0) {
      const q = new URLSearchParams();
      for (const [k, v] of Object.entries(labels)) q.append("label", `${k}=${v}`);
      path += "?" + q.toString();
    }
    return (await this.json<{ sandboxes: Sandbox[] }>("GET", path)).sandboxes;
  }

  /** A named filesystem that outlives the sandboxes it is mounted in. */
  createVolume(name: string, sizeMB?: number): Promise<Volume> {
    return this.json("POST", "/v1/volumes", sizeMB ? { name, size_mb: sizeMB } : { name });
  }

  async volumes(): Promise<Volume[]> {
    return (await this.json<{ volumes: Volume[] }>("GET", "/v1/volumes")).volumes;
  }

  /** Deletes the volume and everything on it; refused while it is mounted. */
  async deleteVolume(name: string): Promise<void> {
    await this.json("DELETE", "/v1/volumes/" + encodeURIComponent(name));
  }

  /** The sandbox's audit events, oldest first (capability `audit`). */
  events(ref: string): Promise<{ events: AuditEvent[]; truncated?: boolean }> {
    return this.json("GET", this.sbx(ref) + "/events");
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

  /** Snapshot a running sandbox every `everySecs`, keeping the newest `keep`; 0 stops it. */
  setSnapshotSchedule(ref: string, everySecs: number, keep = 0): Promise<Sandbox> {
    return this.json("PUT", this.sbx(ref) + "/snapshot-schedule", { every_secs: everySecs, keep });
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
      body: asBody(data),
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
    await this.request("PUT", this.sbx(ref) + "/files", { query: { path }, body: asBody(data), contentType: "application/octet-stream" });
  }

  async removeFile(ref: string, path: string): Promise<void> {
    await this.request("DELETE", this.sbx(ref) + "/files", { query: { path } });
  }

  async listDir(ref: string, path: string): Promise<DirEntry[]> {
    const resp = await this.request("GET", this.sbx(ref) + "/dirs", { query: { path } });
    return ((await resp.json()) as { entries: DirEntry[] }).entries;
  }

  // Gateway only. A gateway in front of many sandboxd nodes adds these; a
  // plain sandboxd answers each with ApiError code "not_found".

  /** The caller as the gateway sees its API key. */
  whoami(): Promise<Whoami> {
    return this.json("GET", "/v1/whoami");
  }

  /** The organizations the key may act in: its own tenant first, then each it is a member of. */
  async orgs(): Promise<Org[]> {
    return (await this.json<{ orgs: Org[] }>("GET", "/v1/orgs")).orgs;
  }

  /** Create an organization with the caller as its owner (scope org:create). */
  createOrg(name: string): Promise<Org> {
    return this.json("POST", "/v1/orgs", { name });
  }

  async orgMembers(org: string): Promise<OrgMember[]> {
    return (await this.json<{ members: OrgMember[] }>("GET", "/v1/orgs/" + encodeURIComponent(org) + "/members")).members;
  }

  /** Add a member or change one's role; an owner may. `tenant` defaults to the caller's. */
  setOrgMember(org: string, user: string, opts: { role?: "owner" | "member"; tenant?: string } = {}): Promise<OrgMember> {
    return this.json("POST", "/v1/orgs/" + encodeURIComponent(org) + "/members", { user, ...opts });
  }

  async removeOrgMember(org: string, user: string, tenant?: string): Promise<void> {
    const resp = await this.request(
      "DELETE",
      "/v1/orgs/" + encodeURIComponent(org) + "/members/" + encodeURIComponent(user),
      tenant ? { query: { tenant } } : {},
    );
    await resp.arrayBuffer();
  }

  async adminOrgs(): Promise<AdminOrg[]> {
    return (await this.json<{ orgs: AdminOrg[] }>("GET", "/v1/admin/orgs")).orgs;
  }

  /** Where the gateway's SSH server listens, and the host keys to pin. */
  sshInfo(): Promise<SSHInfo> {
    return this.json("GET", "/v1/ssh");
  }

  /**
   * Register a public key (one authorized_keys line, no options) for SSH
   * logins: `ssh SANDBOX@host -p port`. `sandbox` limits it to one sandbox.
   */
  addSSHKey(key: string, sandbox?: string): Promise<SSHKeyInfo> {
    return this.json("POST", "/v1/ssh-keys", sandbox ? { key, sandbox } : { key });
  }

  async sshKeys(): Promise<SSHKeyInfo[]> {
    return (await this.json<{ keys: SSHKeyInfo[] }>("GET", "/v1/ssh-keys")).keys;
  }

  async removeSSHKey(id: string): Promise<void> {
    await this.json("DELETE", "/v1/ssh-keys/" + encodeURIComponent(id));
  }

  /**
   * A short-lived SSH login to one sandbox. `user` is the token and the whole
   * credential until `expires_at`. `ttlSecs` 0 or absent takes the gateway's
   * default.
   */
  sshAccess(ref: string, ttlSecs?: number): Promise<SSHAccess> {
    return this.json("POST", this.sbx(ref) + "/ssh-access", ttlSecs ? { ttl_secs: ttlSecs } : {});
  }

  // Jobs, agent runs and secrets (gateway only): work the gateway runs after
  // the request that started it has gone.

  private job_(id: string): string {
    return "/v1/jobs/" + encodeURIComponent(id);
  }

  createJob(spec: JobSpec): Promise<Job> {
    return this.json("POST", "/v1/jobs", spec);
  }

  /** A one-run job of `agent` on `prompt`. */
  agentRun(agent: string, prompt: string, opts: AgentRunOptions = {}): Promise<Job> {
    return this.json("POST", "/v1/agent-runs", { ...opts, agent, prompt });
  }

  /** The caller's jobs, newest first, without their runs. */
  async jobs(): Promise<Job[]> {
    return (await this.json<{ jobs: Job[] }>("GET", "/v1/jobs")).jobs;
  }

  job(id: string): Promise<Job> {
    return this.json("GET", this.job_(id));
  }

  /** Runs not started never are; running ones' sandboxes are terminated. */
  cancelJob(id: string): Promise<Job> {
    return this.json("DELETE", this.job_(id));
  }

  async jobOutput(id: string, run: number): Promise<JobOutput> {
    const o = await this.json<{ stdout?: string; stderr?: string; truncated: boolean }>(
      "GET", `${this.job_(id)}/runs/${run}/output`);
    return { stdout: fromB64(o.stdout), stderr: fromB64(o.stderr), truncated: o.truncated };
  }

  async jobFile(id: string, run: number, path: string): Promise<Uint8Array> {
    const resp = await this.request("GET", `${this.job_(id)}/runs/${run}/files`, { query: { path } });
    return new Uint8Array(await resp.arrayBuffer());
  }

  /** Set one of the tenant's secrets. The value is never returned. */
  async setSecret(name: string, value: string): Promise<void> {
    await this.json("PUT", "/v1/secrets/" + encodeURIComponent(name), { value });
  }

  async secrets(): Promise<SecretInfo[]> {
    return (await this.json<{ secrets: SecretInfo[] }>("GET", "/v1/secrets")).secrets;
  }

  async deleteSecret(name: string): Promise<void> {
    await this.json("DELETE", "/v1/secrets/" + encodeURIComponent(name));
  }

  // Services, on a gateway: a sandbox spec and a count it keeps true.

  private svc(name: string): string {
    return "/v1/services/" + encodeURIComponent(name);
  }

  /** Create a service. ApiError "conflict" if the tenant has one by that name: use updateService. */
  deployService(spec: ServiceSpec): Promise<Service> {
    return this.json("POST", "/v1/services", spec);
  }

  /** Replace a service's spec; a change to what a replica is rolls out one replica at a time. */
  updateService(spec: ServiceSpec): Promise<Service> {
    return this.json("PUT", this.svc(spec.name), spec);
  }

  async services(): Promise<Service[]> {
    return (await this.json<{ services: Service[] }>("GET", "/v1/services")).services;
  }

  service(name: string): Promise<Service> {
    return this.json("GET", this.svc(name));
  }

  scaleService(name: string, replicas: number): Promise<Service> {
    return this.json("POST", this.svc(name) + "/scale", { replicas });
  }

  /** Delete a service and terminate its replicas. */
  async deleteService(name: string): Promise<void> {
    await this.json("DELETE", this.svc(name));
  }
}
