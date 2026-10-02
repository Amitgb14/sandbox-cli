import { test } from "node:test";
import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { existsSync, mkdtempSync, mkdirSync, realpathSync, writeFileSync } from "node:fs";
import { homedir, tmpdir } from "node:os";
import { basename, join } from "node:path";
import { FakeDaemon } from "./daemon.js";
import { Studio, ApiError, ConnectionError, NothingToSnapshotError, TimeoutError, WaitError } from "../src/index.js";
import { gitRootOf, localRepo, wirePath } from "../src/local.js";


/** A real repository: init plus one commit.
 *
 * The commit is not ceremony. A repository with files and no commits makes empty
 * worktrees, which addProject refuses — so a fixture without one is testing a
 * state no working setup is in. */
function initRepoWithCommit(dir: string): void {
  const git = (...args: string[]) =>
    execFileSync("git", ["-c", "init.templateDir=", "-c", "user.email=a@b", "-c", "user.name=a",
      "-c", "commit.gpgsign=false", ...args], { cwd: dir, stdio: ["ignore", "ignore", "ignore"] });
  git("init", "-q", "-b", "main");
  writeFileSync(join(dir, "README.md"), "x\n");
  git("add", "README.md");
  git("commit", "-qm", "init");
}

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

test("addProject registers a directory on the daemon's machine", async () => {
  const { daemon, studio } = await connected();
  try {
    const p = await studio.addProject("/repo/new");
    assert.equal(p.name, "new");
    assert.equal(p.root, "/repo/new");
    // The path crosses in the body, never in the path or a query string: it is
    // the daemon's job to decide what it will touch, and a POST is where it does.
    const sent = daemon.requests.find((r) => r.method === "POST" && r.path === "/v1/projects");
    assert.deepEqual(sent?.body, { path: "/repo/new" });
  } finally {
    await daemon.stop();
  }
});

test("addProject surfaces the daemon's refusal rather than a generic failure", async () => {
  const { daemon, studio } = await connected();
  try {
    // The daemon decides what it will touch. This client expands a path; it
    // never vouches for one.
    await assert.rejects(() => studio.addProject("/repo/not-a-repo"), /not a git repository/);
    await assert.rejects(() => studio.addProject("  "), /needs a path/);
  } finally {
    await daemon.stop();
  }
});

test("a path is resolved here and looked up, never invented", async () => {
  const { daemon, studio } = await connected();
  const cwd = process.cwd();
  try {
    // The failure this replaces: `.project(".")` reported as a missing repo.
    // The daemon lists /repo/app, and this test process is not in it, so the
    // answer is about *paths* rather than about a name nobody registered.
    await assert.rejects(() => studio.project("."), (e: Error) => {
      assert.match(e.message, /lists no repository at/);
      assert.match(e.message, /It knows: \/repo\/app/);
      assert.match(e.message, /addProject/);
      return true;
    });
    // A lookup and nothing more: no repository was added on the way past.
    assert.equal(daemon.requests.some((r) => r.method === "POST" && r.path === "/v1/projects"), false);
    // A name still reads as a name, and now says what to compare against.
    await assert.rejects(() => studio.project("ap"), /Registered: app, twin, twin/);
  } finally {
    process.chdir(cwd);
    await daemon.stop();
  }
});

test("the current repository is found by root, with no argument", async () => {
  const dir = mkdtempSync(join(tmpdir(), "sdk-cwd-"));
  const repo = realpathSync(dir);
  mkdirSync(join(repo, ".git"));
  mkdirSync(join(repo, "scripts"));
  const { daemon, studio } = await connected({ projectRoot: repo });
  const cwd = process.cwd();
  try {
    // Standing in a subdirectory, as a script normally is: the walk up to the
    // git root is what makes "no argument" mean the repository rather than
    // whichever directory the file happens to live in.
    process.chdir(join(repo, "scripts"));
    const p = await studio.project();
    assert.equal(p.root, repo);
    // And the same path spelled by hand resolves to the same repository.
    assert.equal((await studio.project("..")).id, p.id);
  } finally {
    process.chdir(cwd);
    await daemon.stop();
  }
});

test("outside a repository, no argument says so rather than guessing", async () => {
  const outside = realpathSync(mkdtempSync(join(tmpdir(), "sdk-bare-")));
  const { daemon, studio } = await connected();
  const cwd = process.cwd();
  try {
    process.chdir(outside);
    await assert.rejects(() => studio.project(), /is not inside a git repository/);
    await assert.rejects(() => studio.addProject(), /is not inside a git repository/);
  } finally {
    process.chdir(cwd);
    await daemon.stop();
  }
});

test("addProject expands a relative path against the current directory", async () => {
  const repo = realpathSync(mkdtempSync(join(tmpdir(), "sdk-rel-")));
  initRepoWithCommit(repo);
  mkdirSync(join(repo, "sub"));
  const { daemon, studio } = await connected();
  const cwd = process.cwd();
  try {
    process.chdir(repo);
    await studio.addProject("sub");
    const sent = daemon.requests.find((r) => r.method === "POST" && r.path === "/v1/projects");
    // Absolute on the wire: the daemon resolves against its own disk, so a
    // relative path would name whatever its working directory happened to be.
    assert.deepEqual(sent?.body, { path: join(repo, "sub") });

    // No argument sends the repository root, not the directory we stand in.
    process.chdir(join(repo, "sub"));
    await studio.addProject();
    const bare = daemon.requests.filter((r) => r.method === "POST" && r.path === "/v1/projects").pop();
    assert.deepEqual(bare?.body, { path: repo });
  } finally {
    process.chdir(cwd);
    await daemon.stop();
  }
});

test("inside a linked worktree, the repository is the main one — as the daemon resolves it", async () => {
  // The bug this pins: a `.git` walk answers with the worktree, while the daemon
  // resolves the same path through --git-common-dir to the main repository. A
  // lookup would then miss a registry entry that is there, and addProject would
  // register something other than what was asked for. Worktrees are where agents
  // work, so the two have to agree.
  const main = realpathSync(mkdtempSync(join(tmpdir(), "sdk-wt-")));
  const git = (args: string[], cwd: string) =>
    execFileSync("git", args, { cwd, encoding: "utf8", stdio: ["ignore", "pipe", "ignore"] });
  // -c init.templateDir=: a templated hooks directory on the developer's
  // machine would run during init. -c commit.gpgsign=false below: global signing
  // with no key available to a non-tty process fails the commit, and the test
  // would report a bug in code it never reached.
  git(["-c", "init.templateDir=", "init", "-q", "-b", "main"], main);
  writeFileSync(join(main, "README.md"), "x\n");
  git(["add", "README.md"], main);
  git(["-c", "user.email=a@b", "-c", "user.name=a", "-c", "commit.gpgsign=false", "commit", "-qm", "init"], main);
  const linked = join(main, "..", `${basename(main)}-wt`);
  git(["worktree", "add", "-q", "-b", "feature", linked], main);

  assert.equal(await gitRootOf(linked), main, "a linked worktree resolves to the main repository");
  assert.equal(await gitRootOf(join(main, ".git")), main, "the git directory resolves to its repository");

  // And end to end: standing in the worktree finds the registered main repo.
  const { daemon, studio } = await connected({ projectRoot: main });
  const cwd = process.cwd();
  try {
    process.chdir(linked);
    assert.equal((await studio.project()).root, main);
  } finally {
    process.chdir(cwd);
    git(["worktree", "remove", "--force", linked], main);
    await daemon.stop();
  }
});

test("a directory that is not a repository is not one, whatever is lying around", async () => {
  // The walk's failure mode, and why git answers instead: a stray `.git` makes
  // any directory look like a repository, and the daemon would refuse the path
  // this client had just called a root.
  const bare = realpathSync(mkdtempSync(join(tmpdir(), "sdk-stray-")));
  writeFileSync(join(bare, ".git"), "gitdir: /nowhere\n");
  assert.equal(await gitRootOf(bare), bare, "the fallback still answers when git cannot");
  const empty = realpathSync(mkdtempSync(join(tmpdir(), "sdk-empty-")));
  assert.equal(await gitRootOf(empty), null);
});

test("a submodule is its own tree, never the superproject's git directory", async () => {
  // The bug this pins: --git-common-dir names <super>/.git/modules/<name> for a
  // submodule, and that answer becomes a project root — which is bind-mounted at
  // /workspace. An agent would be handed an object store instead of the source.
  const base = realpathSync(mkdtempSync(join(tmpdir(), "sdk-sub-")));
  const git = (args: string[], cwd: string) =>
    execFileSync("git", ["-c", "init.templateDir=", "-c", "commit.gpgsign=false",
      "-c", "user.email=a@b", "-c", "user.name=a", ...args],
      { cwd, encoding: "utf8", stdio: ["ignore", "pipe", "ignore"] });
  const mod = join(base, "mod");
  const sup = join(base, "super");
  for (const d of [mod, sup]) {
    mkdirSync(d);
    git(["init", "-q", "-b", "main"], d);
    writeFileSync(join(d, "f"), "x\n");
    git(["add", "f"], d);
    git(["commit", "-qm", "init"], d);
  }
  try {
    // protocol.file.allow: git refuses local-path submodules by default since
    // CVE-2022-39253. This is a fixture on disk, not a clone of anything named
    // by a repository.
    git(["-c", "protocol.file.allow=always", "submodule", "add", "-q", mod, "mod"], sup);
  } catch {
    return; // this git refuses local submodules outright; nothing to assert
  }
  const inside = join(sup, "mod");
  assert.equal(await gitRootOf(inside), realpathSync(inside), "the submodule tree, not .git/modules");
  const info = await localRepo(inside);
  assert.equal(info?.tree, realpathSync(inside));
});

test("a bare repository is its own root, and the second question's failure is not an error", async () => {
  const base = realpathSync(mkdtempSync(join(tmpdir(), "sdk-bare2-")));
  execFileSync("git", ["-c", "init.templateDir=", "init", "-q", "--bare", "b.git"],
    { cwd: base, stdio: ["ignore", "ignore", "ignore"] });
  const bare = join(base, "b.git");
  const info = await localRepo(bare);
  assert.equal(info?.root, realpathSync(bare));
  // No working tree to name. Reporting one would be worse than reporting none.
  assert.equal(info?.tree, "");
});

test("what crosses the wire is expanded, never symlink-resolved", async () => {
  const { daemon, studio } = await connected();
  const cwd = process.cwd();
  try {
    // /tmp is a symlink to /private/tmp on macOS. Resolving it here would post a
    // path the user never typed — and against a Linux daemon, one that does not
    // exist. The daemon resolves against its own disk; that is its job.
    await studio.addProject("/tmp/some-api").catch(() => {});
    const sent = daemon.requests.filter((r) => r.method === "POST" && r.path === "/v1/projects").pop();
    assert.deepEqual(sent?.body, { path: "/tmp/some-api" });

    // `~` is expanded, because a shell would have done it before argv and a
    // string literal is the one place it survives into a path.
    await studio.addProject("~/code/api").catch(() => {});
    const tilde = daemon.requests.filter((r) => r.method === "POST" && r.path === "/v1/projects").pop();
    assert.equal((tilde?.body as { path: string }).path, join(homedir(), "code", "api"));
    assert.equal(wirePath("~", cwd), homedir());
  } finally {
    process.chdir(cwd);
    await daemon.stop();
  }
});

test("a forgotten argument is not a request for the current repository", async () => {
  const { daemon, studio } = await connected();
  try {
    // `studio.project(process.argv[2])` with nothing passed used to fail loudly.
    // Silently resolving it to wherever the script sits would launch agents in a
    // repository nobody named.
    const missing = undefined as unknown as string;
    await assert.rejects(() => studio.project(missing), /missing argument rather than a request/);
    await assert.rejects(() => studio.project(""), /missing argument rather than a request/);
  } finally {
    await daemon.stop();
  }
});

test("a second step on one workspace clears only the run this object delivered", async () => {
  // The real daemon keeps a finished run's container name, so `ws.run()` twice —
  // or a run then an agent — was refused with a 409. That rule protects *another*
  // script's evidence; applied to the next line of your own it makes sequential
  // work impossible, and both published examples had the shape that hit it.
  const { daemon, studio } = await connected({ holdsNameAfterRun: true });
  try {
    const ws = await (await studio.project("app")).workspace("feature");
    const first = await ws.run(["echo", "one"]);
    const second = await ws.run(["echo", "two"]);
    assert.equal(first.exitCode, 0);
    assert.equal(second.exitCode, 0);
    // Cleared by id — the run whose outcome was already handed back — rather
    // than by sweeping whatever holds the name.
    assert.deepEqual(daemon.removed, ["run-1"]);
  } finally {
    await daemon.stop();
  }
});

test("somebody else's finished run is still refused", async () => {
  // Nothing was delivered by this object, so there is nothing it may clear: the
  // holder belongs to an earlier session and its logs are evidence this client
  // never received.
  const { daemon, studio } = await connected({ nameHeldBy: "abc123" });
  try {
    const ws = await (await studio.project("app")).workspace("feature");
    await assert.rejects(() => ws.run(["echo", "hi"]), (e: Error) => {
      assert.match(e.message, /still holds "feature"'s container name/);
      assert.match(e.message, /replaceFinished/);
      return true;
    });
    assert.deepEqual(daemon.removed, []);
  } finally {
    await daemon.stop();
  }
});

test("addProject can initialise a directory that is not a repository yet", async () => {
  const dir = realpathSync(mkdtempSync(join(tmpdir(), "sdk-init-")));
  const { daemon, studio } = await connected();
  const cwd = process.cwd();
  try {
    // Without the flag it refuses and says what to do — creating a repository is
    // a bigger side effect than registering one, and a mistyped path would
    // otherwise leave an empty repository somewhere nobody meant.
    process.chdir(dir);
    await assert.rejects(() => studio.addProject(), /Pass \{ init: true \}/);
    assert.equal(existsSync(join(dir, ".git")), false, "the refusal must not have created anything");

    await studio.addProject({ init: true } as never).catch(() => {}); // wrong shape: path is first
    assert.equal(existsSync(join(dir, ".git")), false, "an options object in the path slot is not a path");

    await studio.addProject(undefined, { init: true });
    assert.ok(existsSync(join(dir, ".git")), "git init should have run here");
    const sent = daemon.requests.filter((r) => r.method === "POST" && r.path === "/v1/projects").pop();
    assert.deepEqual(sent?.body, { path: dir });
  } finally {
    process.chdir(cwd);
    await daemon.stop();
  }
});

test("init is a no-op inside a repository that already exists", async () => {
  const dir = realpathSync(mkdtempSync(join(tmpdir(), "sdk-noinit-")));
  initRepoWithCommit(dir);
  const sub = join(dir, "scripts");
  mkdirSync(sub);
  const { daemon, studio } = await connected();
  const cwd = process.cwd();
  try {
    process.chdir(sub);
    await studio.addProject(undefined, { init: true });
    // The subdirectory must not become its own repository: the walk found one,
    // so there is nothing to create, and the root is what gets registered.
    assert.equal(existsSync(join(sub, ".git")), false);
    const sent = daemon.requests.filter((r) => r.method === "POST" && r.path === "/v1/projects").pop();
    assert.deepEqual(sent?.body, { path: dir });
  } finally {
    process.chdir(cwd);
    await daemon.stop();
  }
});

test("a repository with files and no commits is refused, with what to run", async () => {
  // The trap `git init` leaves: registerable, and every worktree Studio makes
  // from it is empty — the agent starts in a /workspace with none of this code
  // in it, and nothing says so. Measured before this check existed: a directory
  // with main.py in it, init, one run, and `ls` printed nothing.
  const dir = realpathSync(mkdtempSync(join(tmpdir(), "sdk-unborn-")));
  writeFileSync(join(dir, "main.py"), "print('hi')\n");
  const { daemon, studio } = await connected();
  const cwd = process.cwd();
  try {
    process.chdir(dir);
    await assert.rejects(() => studio.addProject(undefined, { init: true }), (e: Error) => {
      assert.match(e.message, /no commits yet/);
      assert.match(e.message, /every worktree it makes would be empty/);
      assert.match(e.message, /git add -A && git commit/);
      return true;
    });
    // Refused before the daemon was asked: registering it would have produced a
    // project whose every run starts empty.
    assert.equal(daemon.requests.some((r) => r.method === "POST" && r.path === "/v1/projects"), false);
    // And an *empty* new directory is fine — there is nothing to lose.
    const empty = realpathSync(mkdtempSync(join(tmpdir(), "sdk-fresh-")));
    process.chdir(empty);
    await studio.addProject(undefined, { init: true });
    const sent = daemon.requests.filter((r) => r.method === "POST" && r.path === "/v1/projects").pop();
    assert.deepEqual(sent?.body, { path: empty });
  } finally {
    process.chdir(cwd);
    await daemon.stop();
  }
});

test("a failover is followed, not reported as the failure", async () => {
  // The daemon renames the failed container and starts a replacement stamped
  // with routedFrom. Returning the first attempt credits the agent that failed
  // and leaves the retry running — and the next run on this branch then
  // conflicts with a live container this object cannot clear.
  const { daemon, studio } = await connected({ failover: true });
  try {
    const ws = await (await studio.project("app")).workspace("feature");
    const out = await ws.agent("claude", "do the thing", { fallback: ["codex"] });
    assert.equal(out.id, "run-2", "the outcome should be the retry's");
    assert.equal(out.exitCode, 0);
    assert.equal(out.agent, "codex");
  } finally {
    await daemon.stop();
  }
});

test("an unknown run option is a typo, not a preference", async () => {
  // TypeScript catches this in an object literal; JavaScript consumers — which
  // this package ships for — got a silent launch with the daemon's default
  // egress posture, from a misspelling of a security control.
  const { daemon, studio } = await connected();
  try {
    const ws = await (await studio.project("app")).workspace("feature");
    await assert.rejects(
      () => ws.run(["echo", "hi"], { alow: ["api.example.com"] } as never),
      /unknown run option\(s\): alow/,
    );
    assert.equal(daemon.requests.some((r) => r.method === "POST" && r.path === "/v1/runs"), false);
  } finally {
    await daemon.stop();
  }
});


test("a snapshot is scoped to the workspace that took it, without the caller repeating either", async () => {
  const { daemon, studio } = await connected();
  try {
    const ws = await (await studio.project("app")).workspace("feature");
    const snap = await ws.snapshot({ label: "before the refactor", retention: "72h" });

    const req = daemon.requests.find((r) => r.method === "POST" && r.path === "/v1/snapshots");
    assert.ok(req, "no capture reached the daemon");
    // A Workspace *is* a repository and a branch, so it supplies both. A caller
    // that had to repeat them could get them wrong, and the daemon would write
    // files under a repository this object never named.
    assert.deepEqual(req.body, {
      repo: "repo-1",
      branch: "feature",
      label: "before the refactor",
      retention: "72h",
    });
    assert.equal(snap.id, "snap-1");
    assert.equal(snap.retention, "72h");
  } finally {
    await daemon.stop();
  }
});

test("snapshotting a run sends neither repo nor branch: the run already answers both", async () => {
  const { daemon, studio } = await connected();
  try {
    const ws = await (await studio.project("app")).workspace("feature");
    await ws.snapshotRun("run-1", { label: "midway" });

    const req = daemon.requests.find((r) => r.path === "/v1/runs/run-1/snapshot");
    assert.ok(req, "no capture reached the daemon");
    // The daemon refuses these in this body rather than letting a second answer
    // decide where files are written, so sending them would fail every call.
    assert.deepEqual(req.body, { label: "midway" });
  } finally {
    await daemon.stop();
  }
});

test("an unchanged workspace is a typed error, not a failure to report", async () => {
  const { daemon, studio } = await connected({ nothingToSnapshot: true });
  try {
    const ws = await (await studio.project("app")).workspace("feature");
    // The shape this exists for: checkpoint before the risky step, unless there
    // is nothing new. Matching on an ApiError's message would work until the
    // daemon's wording improved.
    await assert.rejects(
      () => ws.snapshot(),
      (err: unknown) => {
        assert.ok(err instanceof NothingToSnapshotError, `got ${(err as Error).name}`);
        assert.equal((err as NothingToSnapshotError).branch, "feature");
        return true;
      },
    );
  } finally {
    await daemon.stop();
  }
});

test("listing is scoped to the branch unless the whole repository is asked for", async () => {
  const { daemon, studio } = await connected();
  try {
    const ws = await (await studio.project("app")).workspace("feature");
    const snaps = await ws.snapshots();
    assert.equal(snaps.length, 1);
    assert.equal(snaps[0].source, "sdk");

    await ws.snapshots({ allBranches: true });
    const paths = daemon.requests.filter((r) => r.path.startsWith("/v1/snapshots?")).map((r) => r.path);
    assert.ok(paths[0].includes("branch=feature"), `scoped listing was ${paths[0]}`);
    assert.ok(!paths[1].includes("branch="), `allBranches listing was ${paths[1]}`);
  } finally {
    await daemon.stop();
  }
});

test("restore defaults to the mode that cannot destroy anything", async () => {
  const { daemon, studio } = await connected();
  try {
    const ws = await (await studio.project("app")).workspace("feature");
    const res = await ws.restore("snap-1");
    assert.equal(res.mode, "branch");
    assert.equal(res.branch, "sandbox-recover/feature-snap-1");

    // The default is the daemon's, not a value this client sends: an SDK that
    // spelled "branch" itself would keep sending it after the daemon's default
    // changed, which is the one way a client can silently disagree with the
    // contract it generated its types from.
    const req = daemon.requests.find((r) => r.path === "/v1/snapshots/snap-1/restore");
    assert.deepEqual(req?.body, { repo: "repo-1" });

    await ws.restore("snap-1", { mode: "worktree" });
    const explicit = daemon.requests.filter((r) => r.path === "/v1/snapshots/snap-1/restore").at(-1);
    assert.deepEqual(explicit?.body, { repo: "repo-1", mode: "worktree" });
  } finally {
    await daemon.stop();
  }
});

test("retention can be set on one snapshot, and cleared back to the default", async () => {
  const { daemon, studio } = await connected();
  try {
    const ws = await (await studio.project("app")).workspace("feature");
    const set = await ws.setSnapshotRetention("snap-1", "72h");
    assert.equal(set.retention, "72h");

    const cleared = await ws.setSnapshotRetention("snap-1", "");
    assert.equal(cleared.retention, "");
    assert.equal(cleared.retentionEffective, "168h0m0s");

    const reqs = daemon.requests.filter((r) => r.path === "/v1/snapshots/snap-1/retention");
    assert.deepEqual(reqs.at(-1)?.body, { retention: "", repo: "repo-1" });
  } finally {
    await daemon.stop();
  }
});

test("a snapshot can be mirrored to object storage after the fact", async () => {
  const { daemon, studio } = await connected({ bucket: "my-snapshots" });
  try {
    const ws = await (await studio.project("app")).workspace("feature");
    const snap = await ws.uploadSnapshot("snap-1");
    assert.equal(snap.remote?.uploaded, true);
    assert.equal(snap.remote?.bucket, "my-snapshots");
    assert.equal(snap.remote?.key, "snapshots/repo-1/snap-1.bundle");

    // The repository travels with it; the bucket does not. A client able to name
    // one would be choosing where a repository's contents are sent.
    const req = daemon.requests.find((r) => r.path === "/v1/snapshots/snap-1/upload");
    assert.deepEqual(req?.body, { repo: "repo-1" });
  } finally {
    await daemon.stop();
  }
});

test("uploading with no bucket configured names the setting to set", async () => {
  const { daemon, studio } = await connected();
  try {
    const ws = await (await studio.project("app")).workspace("feature");
    await assert.rejects(
      () => ws.uploadSnapshot("snap-1"),
      (err: Error) => /snapshot\.s3\.bucket/.test(err.message),
    );
  } finally {
    await daemon.stop();
  }
});

test("a bucket that refuses is a result, not a thrown error", async () => {
  const { daemon, studio } = await connected({
    bucket: "my-snapshots",
    bucketError: "s3: GET s3.amazonaws.com: AccessDenied: no",
  });
  try {
    // The request succeeded; the storage did not. A client that had to catch an
    // exception to render "not connected" would treat a working daemon as a
    // broken one.
    const res = await studio.checkSnapshotStorage();
    assert.equal(res.ok, false);
    assert.match(res.error ?? "", /AccessDenied/);
  } finally {
    await daemon.stop();
  }
});

test("snapshot settings report the credential by name, never by value", async () => {
  const { daemon, studio } = await connected({ bucket: "my-snapshots" });
  try {
    const settings = await studio.snapshotSettings();
    assert.equal(settings.s3?.bucket, "my-snapshots");
    assert.equal(settings.s3?.upload, "manual");
    // Which variable is read, and whether it resolves — the only two things a
    // client is told about the credential, and the most it may have.
    assert.equal(settings.s3?.accessKeyEnv, "AWS_ACCESS_KEY_ID");
    assert.equal(settings.s3?.credentialsResolved, true);
    assert.equal(settings.manualRetention, "168h0m0s");
  } finally {
    await daemon.stop();
  }
});

test("no bucket configured is an absence, not a failure", async () => {
  const { daemon, studio } = await connected();
  try {
    const settings = await studio.snapshotSettings();
    assert.equal(settings.s3, undefined);
    assert.equal(settings.writable, true);
  } finally {
    await daemon.stop();
  }
});
