import { createServer, type IncomingMessage, type Server, type ServerResponse } from "node:http";
import type { AddressInfo } from "node:net";

/**
 * A stand-in for the daemon, answering the shapes the real one answers with.
 *
 * Not a mock of the client's own calls: it is a server, so a test can assert
 * what actually crossed the wire — the method, the path, the headers, the body —
 * which is the only part of a client worth testing. The alternative, stubbing
 * `fetch` and checking it was called, tests that the code calls the code.
 */
export interface Recorded {
  method: string;
  path: string;
  auth?: string;
  contentType?: string;
  body?: unknown;
}

export interface FakeDaemonOptions {
  authRequired?: boolean;
  token?: string;
  /** States returned by GET /runs/:id, in order; the last repeats. */
  runStates?: string[];
  exitCode?: number;
  /** Refuse POST /runs/:id/stop, the way a daemon that cannot reach docker does. */
  stopFails?: boolean;
  /** Refuse the first launch with 409, the way a finished run holding the
   *  branch's container name does. Cleared by DELETE, as on the real daemon. */
  nameHeldBy?: string;
}

export class FakeDaemon {
  readonly requests: Recorded[] = [];
  private server!: Server;
  private port = 0;
  private stateIndex = 0;
  private stoppedRuns: string[] = [];

  constructor(private opts: FakeDaemonOptions = {}) {}

  get url(): string {
    return `http://127.0.0.1:${this.port}`;
  }

  get stopped(): string[] {
    return this.stoppedRuns;
  }

  pathsHit(method: string): string[] {
    return this.requests.filter((r) => r.method === method).map((r) => r.path);
  }

  async start(): Promise<void> {
    this.server = createServer((req, res) => void this.handle(req, res));
    await new Promise<void>((resolve) => this.server.listen(0, "127.0.0.1", resolve));
    this.port = (this.server.address() as AddressInfo).port;
  }

  async stop(): Promise<void> {
    // close() alone waits for live connections, so a client that leaked one
    // would hang the suite rather than fail a test. The leak is worth catching
    // on its own terms — `follow` asserts the stream is closed — and a teardown
    // that hangs tells you nothing about which test did it.
    const closed = new Promise<void>((resolve) => this.server.close(() => resolve()));
    this.server.closeAllConnections();
    await closed;
  }

  private async handle(req: IncomingMessage, res: ServerResponse): Promise<void> {
    const chunks: Buffer[] = [];
    for await (const c of req) chunks.push(c as Buffer);
    const raw = Buffer.concat(chunks).toString();
    const url = new URL(req.url ?? "/", "http://127.0.0.1");
    this.requests.push({
      method: req.method ?? "",
      path: req.url ?? "",
      auth: req.headers.authorization,
      contentType: req.headers["content-type"] as string | undefined,
      body: raw ? JSON.parse(raw) : undefined,
    });

    const json = (status: number, body: unknown) => {
      res.writeHead(status, { "Content-Type": "application/json" });
      res.end(JSON.stringify(body));
    };

    // Health answers without a token, deliberately: it is how a client without
    // one is told that is what is missing.
    if (url.pathname === "/v1/health") {
      return json(200, {
        status: "ok",
        version: "test",
        engine: "docker",
        engineVersion: "27",
        dockerAvailable: true,
        project: "/repo",
        profile: "dev",
        authRequired: this.opts.authRequired ?? false,
        egress: { mode: "allowlist", baseline: true, allow: [] },
        host: {},
      });
    }
    if (this.opts.token && req.headers.authorization !== `Bearer ${this.opts.token}`) {
      return json(401, { error: "a bearer token is required" });
    }

    if (url.pathname === "/v1/projects" && req.method === "GET") {
      return json(200, {
        projects: [
          { id: "repo-1", name: "app", root: "/repo/app" },
          { id: "repo-2", name: "twin", root: "/a/twin" },
          { id: "repo-3", name: "twin", root: "/b/twin" },
        ],
      });
    }
    if (url.pathname === "/v1/worktrees" && req.method === "POST") {
      return json(201, { branch: "feature", path: "/repo/app-feature", repoId: "repo-1" });
    }
    if (url.pathname === "/v1/runs" && req.method === "POST" && this.opts.nameHeldBy) {
      return json(409, {
        error: `a finished run (${this.opts.nameHeldBy}, exit 0) still holds "feature"'s container name; ` +
          `read it with GET /v1/runs/${this.opts.nameHeldBy}/logs, then DELETE /v1/runs/${this.opts.nameHeldBy} to run again`,
      });
    }
    if (url.pathname === "/v1/runs" && req.method === "POST") {
      return json(201, { id: "run-1", containerId: "c1", name: "sandbox-app-feature", kind: "interactive", state: "running", detached: true, createdAt: new Date(0).toISOString() });
    }
    if (url.pathname === "/v1/runs/run-1" && req.method === "GET") {
      const states = this.opts.runStates ?? ["running", "exited"];
      const state = states[Math.min(this.stateIndex, states.length - 1)];
      this.stateIndex++;
      const run: Record<string, unknown> = {
        id: "run-1", containerId: "c1", name: "sandbox-app-feature",
        kind: "interactive", state, detached: true,
        createdAt: new Date(0).toISOString(), agent: "codex", routedFrom: "claude",
        routeReason: "provider answered 503",
      };
      if (state === "exited") run.exitCode = this.opts.exitCode ?? 0;
      return json(200, run);
    }
    if (url.pathname === "/v1/runs/run-1/stop" && req.method === "POST") {
      if (this.opts.stopFails) return json(502, { error: "docker is unreachable" });
      this.stoppedRuns.push("run-1");
      // A stopped container exits, which is what lets the wait finish.
      this.opts.runStates = ["exited"];
      this.stateIndex = 0;
      return json(200, { id: "run-1", state: "exited", exitCode: 137 });
    }
    if (url.pathname === "/v1/runs/run-1/logs" && !url.searchParams.has("follow")) {
      return json(200, [
        { seq: 0, ts: "", stream: "stdout", text: "installing" },
        { seq: 1, ts: "", stream: "stderr", text: "a warning" },
        { seq: 2, ts: "", stream: "stdout", text: "done" },
      ]);
    }
    if (url.pathname === "/v1/runs/run-1/logs" && url.searchParams.has("follow")) {
      res.writeHead(200, { "Content-Type": "text/event-stream" });
      res.write(`event: log\ndata: ${JSON.stringify({ type: "log", stream: "stdout", data: "one" })}\n\n`);
      res.write(`event: log\ndata: ${JSON.stringify({ type: "log", stream: "stdout", data: "two" })}\n\n`);
      res.write(`event: end\ndata: ${JSON.stringify({ type: "end", data: "" })}\n\n`);
      // Deliberately left open after `end`: a client that only stopped when the
      // connection closed would hang here, which is the bug this shape catches.
      return;
    }
    if (url.pathname === `/v1/runs/${this.opts.nameHeldBy}` && req.method === "DELETE") {
      // Reaping the holder frees the name, exactly as it does on the daemon.
      this.opts.nameHeldBy = undefined;
      res.writeHead(204);
      res.end();
      return;
    }
    if (url.pathname === "/v1/runs" && req.method === "GET") {
      return json(200, {
        runs: [
          { id: "old-run", containerId: "c0", name: "sandbox-app-feature", kind: "interactive",
            state: "exited", exitCode: 0, detached: true, branch: "feature",
            createdAt: new Date(0).toISOString() },
        ],
      });
    }
    if (url.pathname === "/v1/runs/run-1" && req.method === "DELETE") {
      res.writeHead(204);
      res.end();
      return;
    }
    return json(404, { error: `no route ${req.method} ${url.pathname}` });
  }
}
