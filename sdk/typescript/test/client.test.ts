import { test } from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, mkdirSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { FakeDaemon } from "./daemon.js";
import { Studio, ApiError, ConnectionError } from "../src/index.js";

async function connected(opts: ConstructorParameters<typeof FakeDaemon>[0] = {}) {
  const daemon = new FakeDaemon(opts);
  await daemon.start();
  const studio = await Studio.connect({ url: daemon.url, token: opts.token ?? "" });
  return { daemon, studio };
}

test("a run posts the repository and worktree it belongs to, and waits for the exit", async () => {
  const { daemon, studio } = await connected();
  try {
    const project = await studio.project("app");
    const ws = await project.workspace("feature");
    const out = await ws.run(["npm", "test"], { env: { CI: "true" } });

    const launch = daemon.requests.find((r) => r.method === "POST" && r.path === "/v1/runs");
    assert.ok(launch, "no launch reached the daemon");
    assert.deepEqual(launch.body, {
      command: ["npm", "test"],
      repo: "repo-1",
      worktree: "feature",
      env: { CI: "true" },
    });
    // A body must be labelled as JSON; the daemon refuses one that is not.
    assert.equal(launch.contentType, "application/json");

    assert.equal(out.exitCode, 0);
    assert.equal(out.stdout, "installing\ndone");
    assert.equal(out.stderr, "a warning");
    assert.equal(out.stopped, false);
  } finally {
    await daemon.stop();
  }
});

test("an outcome reports the agent that actually ran, not the one asked for", async () => {
  const { daemon, studio } = await connected();
  try {
    const ws = await (await studio.project("app")).workspace("feature");
    const out = await ws.agent("claude", "fix the parser", { fallback: ["codex"] });

    const launch = daemon.requests.find((r) => r.method === "POST" && r.path === "/v1/runs");
    assert.equal((launch!.body as Record<string, unknown>).fallback !== undefined, true);
    // A script that could not see the failover would attribute codex's work to
    // claude — under the wrong login and the wrong bill.
    assert.equal(out.agent, "codex");
    assert.equal(out.routedFrom, "claude");
    assert.equal(out.routeReason, "provider answered 503");
  } finally {
    await daemon.stop();
  }
});

test("a run that outlives its deadline is stopped, and says so", async () => {
  // Never exits on its own: the wait has to end it.
  const { daemon, studio } = await connected({ runStates: ["running"] });
  try {
    const ws = await (await studio.project("app")).workspace("feature");
    const out = await ws.run(["sleep", "600"], { timeoutMs: 300 });

    assert.deepEqual(daemon.stopped, ["run-1"], "the deadline passed without stopping the run");
    assert.equal(out.stopped, true, "a stopped run must not be reported as one that ended on its own");
  } finally {
    await daemon.stop();
  }
});

test("nothing is removed on the way out", async () => {
  const { daemon, studio } = await connected();
  try {
    const ws = await (await studio.project("app")).workspace("feature");
    await ws.run(["true"]);
    // A finished run's logs are the evidence for what it did. Tidying up here
    // would throw that away on every happy path.
    assert.deepEqual(daemon.pathsHit("DELETE"), []);
  } finally {
    await daemon.stop();
  }
});

test("the daemon's refusal is what the caller sees", async () => {
  const { daemon, studio } = await connected();
  try {
    const ws = await (await studio.project("app")).workspace("feature");
    await assert.rejects(
      () => ws.logs("no-such-run"),
      (err: unknown) => {
        assert.ok(err instanceof ApiError);
        assert.equal(err.status, 404);
        assert.match(err.message, /no route GET \/v1\/runs\/no-such-run\/logs/);
        return true;
      },
    );
  } finally {
    await daemon.stop();
  }
});

test("a token is sent when there is one, and its absence is explained", async () => {
  const daemon = new FakeDaemon({ authRequired: true, token: "s3cret" });
  await daemon.start();
  try {
    await assert.rejects(
      () => Studio.connect({ url: daemon.url, token: "" }),
      (err: unknown) => {
        assert.ok(err instanceof ApiError);
        assert.match(err.message, /requires a token/);
        return true;
      },
    );

    const studio = await Studio.connect({ url: daemon.url, token: "s3cret" });
    await studio.projects();
    const listing = daemon.requests.find((r) => r.path === "/v1/projects");
    assert.equal(listing?.auth, "Bearer s3cret");

    const health = daemon.requests.find((r) => r.path === "/v1/health");
    assert.equal(health?.contentType, undefined, "a bodiless request must not claim to carry JSON");
  } finally {
    await daemon.stop();
  }
});

test("an unreachable daemon is a different failure from a refused request", async () => {
  await assert.rejects(
    () => Studio.connect({ url: "http://127.0.0.1:1", token: "" }),
    (err: unknown) => {
      assert.ok(err instanceof ConnectionError, `wanted ConnectionError, got ${String(err)}`);
      return true;
    },
  );
});

test("following a run stops at the end event rather than at the connection", async () => {
  const { daemon, studio } = await connected();
  try {
    const ws = await (await studio.project("app")).workspace("feature");
    const seen: string[] = [];
    // The stub deliberately holds the connection open after `end`. Without the
    // event, this loop would never finish and the test would time out.
    for await (const ev of ws.follow("run-1")) seen.push(ev.type);
    assert.deepEqual(seen, ["log", "log", "end"]);
  } finally {
    await daemon.stop();
  }
});

test("an ambiguous repository name is refused rather than picked", async () => {
  const { daemon, studio } = await connected();
  try {
    await assert.rejects(() => studio.project("twin"), /use an id/);
    await assert.rejects(() => studio.project("nope"), /no repository nope is registered/);
  } finally {
    await daemon.stop();
  }
});

test("an agent run and a handoff both refuse an empty prompt", async () => {
  const { daemon, studio } = await connected();
  try {
    const ws = await (await studio.project("app")).workspace("feature");
    await assert.rejects(() => ws.agent("claude", "   "), /needs a prompt/);
    await assert.rejects(
      () => ws.handoff("codex", { agent: "claude", sessionId: "abc" }, ""),
      /the prompt says what to do now/,
    );
    // Neither reached the daemon: a request that cannot succeed should not be
    // sent for the daemon to refuse.
    assert.deepEqual(daemon.pathsHit("POST").filter((p) => p === "/v1/runs"), []);
  } finally {
    await daemon.stop();
  }
});

test("a handoff names the conversation by id, and carries the prompt", async () => {
  const { daemon, studio } = await connected();
  try {
    const ws = await (await studio.project("app")).workspace("feature");
    await ws.handoff("codex", { agent: "claude", sessionId: "sess-1" }, "finish it");
    const launch = daemon.requests.find((r) => r.method === "POST" && r.path === "/v1/runs");
    assert.deepEqual((launch!.body as Record<string, unknown>).handoffFrom, {
      agent: "claude",
      sessionId: "sess-1",
    });
  } finally {
    await daemon.stop();
  }
});

test("the daemon's location and token are discovered from what studio.sh writes", async () => {
  const home = mkdtempSync(join(tmpdir(), "sdk-discover-"));
  mkdirSync(join(home, "sandbox", "studio"), { recursive: true });
  writeFileSync(join(home, "sandbox", "studio", "ports"), "3199\n8799\n");
  writeFileSync(join(home, "sandbox", "studio", "token"), "from-the-file\n");

  const prevXdg = process.env.XDG_CONFIG_HOME;
  const prevUrl = process.env.SANDBOX_API_URL;
  const prevToken = process.env.SANDBOX_STUDIO_TOKEN;
  process.env.XDG_CONFIG_HOME = home;
  delete process.env.SANDBOX_API_URL;
  delete process.env.SANDBOX_STUDIO_TOKEN;
  try {
    const { discoverToken, discoverUrl } = await import("../src/discover.js");
    assert.equal(discoverUrl(), "http://127.0.0.1:8799");
    assert.equal(discoverToken(), "from-the-file");
    // Anything given explicitly outranks it, and so does the environment.
    assert.equal(discoverUrl("http://box:1234/"), "http://box:1234");
    process.env.SANDBOX_STUDIO_TOKEN = "from-the-env";
    assert.equal(discoverToken(), "from-the-env");
  } finally {
    if (prevXdg === undefined) delete process.env.XDG_CONFIG_HOME;
    else process.env.XDG_CONFIG_HOME = prevXdg;
    if (prevUrl !== undefined) process.env.SANDBOX_API_URL = prevUrl;
    if (prevToken === undefined) delete process.env.SANDBOX_STUDIO_TOKEN;
    else process.env.SANDBOX_STUDIO_TOKEN = prevToken;
  }
});
