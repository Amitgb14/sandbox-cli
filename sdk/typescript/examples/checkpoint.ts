/**
 * A checkpoint around a risky step, rolled back when the step fails.
 *
 * Compiled by `npm test`, so an example that stopped matching the API fails the
 * build rather than misleading somebody who typed it.
 *
 * The shape here is the one worth copying: snapshot, act, and restore only on a
 * failure — because /workspace is a bind mount, so an agent's work is already on
 * disk and a snapshot is the belt rather than the braces.
 */
import { Studio, NothingToSnapshotError, type SnapshotInfo } from "@sandbox-cli/sdk";

const studio = await Studio.connect();
const repo = await studio.project("my-app");
const ws = await repo.workspace("agent-42");

// An unchanged tree is not a failure, and it is not a snapshot either: there is
// nothing to put back, so there is nothing to hold. Catching it here is what
// keeps a clean checkout from stopping the script on its first line.
let before: SnapshotInfo | undefined;
try {
  before = await ws.snapshot({ label: "before the migration", retention: "72h" });
  console.log(`checkpoint ${before.id} — ${before.commit}, kept ${before.retentionEffective}`);
} catch (err) {
  if (!(err instanceof NothingToSnapshotError)) throw err;
  console.log("nothing to check point: the workspace is exactly what is committed");
}

const out = await ws.agent("claude", "migrate the schema to the new column layout", {
  timeoutMs: 20 * 60_000,
});

if (out.exitCode === 0) {
  console.log("migration succeeded; the checkpoint ages out on its own");
} else if (before) {
  // Branch mode, the default: it points a new branch at the snapshot and leaves
  // the working tree alone, so the agent's attempt is still there to read. The
  // destructive option — mode: "worktree" — is a decision to make after looking,
  // not part of a failure path that runs unattended.
  const restored = await ws.restore(before.id);
  console.log(`agent failed (${out.exitCode}); its starting point is on ${restored.branch}`);
  if (restored.matchesWorkingTree) {
    console.log("...and the working tree already matched it: nothing was actually lost");
  }
}
