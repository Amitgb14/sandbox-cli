/**
 * One branch, one agent, one bounded run — and every claim it makes checked.
 *
 * Runnable: `npx tsx examples/agent-run.ts` against a daemon started by
 * `studio.sh`, with a repository called "my-app" registered. It is also compiled
 * by `npm test`, so an example that stopped matching the API fails the build
 * rather than misleading somebody who typed it.
 */
import { Studio, WaitError, type Outcome } from "../src/index.js";

const studio = await Studio.connect(); // port and token from ~/.config/sandbox/studio
const repo = await studio.project("my-app");
const ws = await repo.workspace("agent-42"); // a git worktree on that branch

try {
  const install = await ws.run(["pnpm", "install", "--frozen-lockfile"], {
    timeoutMs: 10 * 60_000,
  });
  if (install.exitCode !== 0) {
    console.error(install.stderr);
    process.exit(install.exitCode);
  }

  const fix: Outcome = await ws.agent("claude", "make the failing test pass", {
    fallback: ["codex"],
    timeoutMs: 20 * 60_000,
  });

  // Reported on every outcome, not on request: a script that cannot see the
  // failover credits the wrong agent — and bills the wrong account.
  if (fix.routedFrom) {
    console.warn(`${fix.routedFrom} was unavailable — ${fix.agent} did the work`);
  }
  // A stopped run is not a failed one. The exit code of a container somebody
  // interrupted is not a verdict on the work.
  if (fix.stopped) {
    console.error(`${fix.agent} outlived its deadline and was stopped`);
    process.exit(1);
  }

  // node_modules survived the first container because it was written to the
  // worktree, not because anything stayed alive.
  const tests = await ws.run(["pnpm", "test"], { env: { CI: "true" } });
  console.log(tests.stdout);
  process.exit(tests.exitCode);
} catch (err) {
  // The launch succeeded and the wait did not, so the container is still out
  // there holding this branch's name — which docker will not let anything else
  // take until it is gone.
  if (err instanceof WaitError) await ws.stop(err.run.id);
  throw err;
}
