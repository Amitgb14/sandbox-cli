import { test } from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, mkdirSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { FakeDaemon } from "./daemon.js";
import { Studio, ApiError, ConnectionError, TimeoutError, WaitError } from "../src/index.js";

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
      // "fetch failed" is true and useless. Somebody who never started a daemon
      // has no reason to connect a network error to studio.sh, and connect() is
      // the one place that cause is overwhelmingly likely.
      assert.match(err.message, /no daemon is running there/);
      assert.match(err.message, /studio\.sh/);
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

test("a stop the daemon refused is not reported as a stop", async () => {
  // The container is still running and still holding its branch's name. Saying
  // `stopped: true` here would announce the very outcome the deadline exists to
  // prevent as though it had been prevented.
  const { daemon, studio } = await connected({ runStates: ["running"], stopFails: true });
  try {
    const ws = await (await studio.project("app")).workspace("feature");
    await assert.rejects(
      () => ws.run(["sleep", "600"], { timeoutMs: 200 }),
      (err: unknown) => {
        assert.ok(err instanceof WaitError, `wanted WaitError, got ${String(err)}`);
        assert.equal(err.run.id, "run-1");
        assert.match(String(err.cause), /docker is unreachable/);
        return true;
      },
    );
    assert.deepEqual(daemon.stopped, [], "the stub refused the stop, so nothing was stopped");
  } finally {
    await daemon.stop();
  }
});

test("a run that was launched is never lost, even when the wait fails", async () => {
  // The launch succeeded, so the container exists whatever happened next — and
  // a detached run holds sandbox-<repo>-<branch>, which docker will not
  // duplicate. Without the id, the branch is blocked by something nobody can
  // name.
  const { daemon, studio } = await connected({ runStates: ["running"] });
  try {
    const ws = await (await studio.project("app")).workspace("feature");
    const ac = new AbortController();
    setTimeout(() => ac.abort(), 120);
    await assert.rejects(
      () => ws.run(["sleep", "600"], { signal: ac.signal, timeoutMs: 60_000 }),
      (err: unknown) => {
        assert.ok(err instanceof WaitError);
        assert.equal(err.run.id, "run-1");
        assert.equal(err.run.name, "sandbox-app-feature");
        return true;
      },
    );
  } finally {
    await daemon.stop();
  }
});

test("an injected fetch is used by every call, following included", async () => {
  const { daemon } = await connected();
  try {
    const seen: string[] = [];
    const spy: typeof globalThis.fetch = (input, init) => {
      seen.push(String(input));
      return globalThis.fetch(input, init);
    };
    const studio = await Studio.connect({ url: daemon.url, token: "", fetch: spy });
    const ws = await (await studio.project("app")).workspace("feature");
    for await (const _ of ws.follow("run-1")) break;
    assert.ok(
      seen.some((u) => u.includes("/logs?follow=1")),
      `the stream bypassed the injected fetch: ${seen.join(", ")}`,
    );
  } finally {
    await daemon.stop();
  }
});

test("slow is not unreachable, and a cancel is not a network failure", async () => {
  const { daemon, studio } = await connected({ runStates: ["running"] });
  try {
    const ws = await (await studio.project("app")).workspace("feature");
    // A caller's cancel arrives as an AbortError, which is the check callers
    // already write — `err.name === "AbortError"` — rather than a bare Error.
    const ac = new AbortController();
    setTimeout(() => ac.abort(), 50);
    await assert.rejects(
      () => ws.run(["sleep", "600"], { signal: ac.signal, timeoutMs: 60_000 }),
      (err: unknown) => {
        const cause = (err as WaitError).cause;
        assert.equal((cause as Error).name, "AbortError", `cause was ${String(cause)}`);
        return true;
      },
    );
    assert.equal(typeof TimeoutError, "function"); // exported for the slow-daemon case
  } finally {
    await daemon.stop();
  }
});

test("waiting does not accumulate listeners on the caller's signal", async () => {
  const { daemon, studio } = await connected({ runStates: ["running", "running", "running", "exited"] });
  try {
    const ws = await (await studio.project("app")).workspace("feature");
    const ac = new AbortController();
    await ws.run(["true"], { signal: ac.signal, timeoutMs: 30_000 });
    // Node exposes the count; a loop that registered one per sleep and never
    // removed it left nineteen after thirty seconds.
    const listeners = (ac.signal as unknown as { listenerCount?: (t: string) => number })
      .listenerCount?.("abort");
    if (typeof listeners === "number") {
      assert.equal(listeners, 0, `${listeners} abort listeners survived the wait`);
    }
  } finally {
    await daemon.stop();
  }
});

// Running the same script twice is the commonest thing anybody does with this,
// and it was refused: docker will not duplicate a container name, and a finished
// run keeps its branch's name until somebody reaps it. That refusal is right —
// the logs are the evidence for what the run did — so the way through is a
// caller saying the evidence is spent, not a helper deciding it for them.
test("a finished run holding the branch's name can be replaced on request", async () => {
  const { daemon, studio } = await connected({ nameHeldBy: "old-run" });
  try {
    const ws = await (await studio.project("app")).workspace("feature");

    // Without the flag: the daemon's refusal, verbatim, with its remedy in it.
    await assert.rejects(
      () => ws.run(["true"]),
      (err: unknown) => {
        assert.ok(err instanceof ApiError);
        assert.equal(err.status, 409);
        // The daemon's sentence, kept whole…
        assert.match(err.message, /still holds "feature"'s container name/);
        // …and the one it cannot know to write. Being told to reach for curl
        // from inside a typed client is how a library teaches people to work
        // around it.
        assert.match(err.message, /replaceFinished/);
        assert.match(err.message, /clearFinished/);
        return true;
      },
    );

    // With it: the holder is removed and the run goes ahead.
    const out = await ws.run(["true"], { replaceFinished: true });
    assert.equal(out.exitCode, 0);
    assert.ok(
      daemon.pathsHit("DELETE").includes("/v1/runs/old-run"),
      `the holder was never reaped: ${daemon.pathsHit("DELETE").join(", ")}`,
    );
  } finally {
    await daemon.stop();
  }
});

// "Finished" is the whole of the claim. Reaping a live run would stop somebody
// else's agent, which is not a side effect a convenience flag may have.
//
// Every state that is not exited or dead, because the first version of this test
// pointed at a branch the stub reported nothing for — so it asserted null and
// never reached the refusal it was named after. `paused` is the case that made
// the guard wrong as well as untested: it holds the name and holds unwritten
// work, and a list of "live" states nobody had thought to include it in reaped
// it.
test("clearFinished refuses a run that is not finished", async () => {
  for (const state of ["running", "created", "restarting", "paused", "unknown"]) {
    const daemon = new FakeDaemon({ listedBranch: "feature", listedState: state });
    await daemon.start();
    try {
      const studio = await Studio.connect({ url: daemon.url, token: "" });
      const ws = await (await studio.project("app")).workspace("feature");
      await assert.rejects(
        () => ws.clearFinished(),
        new RegExp(`is ${state}, not finished`),
        `a ${state} run was reaped`,
      );
      assert.deepEqual(daemon.pathsHit("DELETE"), [], `a ${state} run was removed`);
    } finally {
      await daemon.stop();
    }
  }
});

// Nothing of this branch's to clear is not an error; it is null.
test("clearFinished reports nothing when the name is free", async () => {
  const { daemon, studio } = await connected({ listedBranch: "someone-else" });
  try {
    const ws = await (await studio.project("app")).workspace("feature");
    assert.equal(await ws.clearFinished(), null);
  } finally {
    await daemon.stop();
  }
});

// A 409 the flag cannot clear must still arrive as the ApiError it was, or a
// caller's `e instanceof ApiError && e.status === 409` stops matching the moment
// they pass an option.
test("a live agent's 409 stays an ApiError even with replaceFinished", async () => {
  const { daemon, studio } = await connected({
    nameHeldBy: "old-run",
    listedBranch: "feature",
    listedState: "running",
  });
  try {
    const ws = await (await studio.project("app")).workspace("feature");
    await assert.rejects(
      () => ws.run(["true"], { replaceFinished: true }),
      (err: unknown) => {
        assert.ok(err instanceof ApiError, `wanted ApiError, got ${String(err)}`);
        assert.equal(err.status, 409);
        assert.match(err.message, /still holds "feature"'s container name/);
        assert.match(err.message, /is running, not finished/);
        return true;
      },
    );
  } finally {
    await daemon.stop();
  }
});
