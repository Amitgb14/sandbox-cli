import { Studio, WaitError, type Outcome } from "@sandbox-cli/sdk";

/**
 * A workflow, without writing an agent.
 *
 * Three tasks, three branches, three containers, in parallel — then one gate
 * that decides which of them a human should look at. The orchestration is
 * ordinary TypeScript: `Promise.all`, an array, an `if`. Nothing here needs a
 * model to decide what happens next, which is the point — a workflow whose
 * control flow is code fails the same way twice, and one whose control flow is a
 * prompt does not.
 */

/** The agent asked for first. `fallback` names who covers an outage. */
const PRIMARY = "claude";

const TASKS = [
  { branch: "wf-tests", prompt: "make the failing unit tests pass" },
  { branch: "wf-types", prompt: "remove every `any` in src/, keeping behaviour identical" },
  { branch: "wf-docs", prompt: "update README.md so the examples match the current API" },
];

const studio = await Studio.connect();
const repo = await studio.project(); // the repository this script is standing in

/** What the gate needs to know about one task, and nothing else. */
type Result = {
  branch: string;
  agent: string;
  changed: boolean;
  verified: boolean;
  note: string;
};

async function attempt(task: (typeof TASKS)[number]): Promise<Result> {
  const ws = await repo.workspace(task.branch);
  // A finished run holds the branch's container name — docker refuses a
  // duplicate, which is exactly what stops two agents sharing one checkout. This
  // clears yesterday's corpse and nothing that is running.
  await ws.clearFinished();

  try {
    const fix: Outcome = await ws.agent(PRIMARY, task.prompt, {
      fallback: ["codex"],
      timeoutMs: 20 * 60_000,
    });
    // What actually ran, which is not always what was asked for. The field is
    // absent only when the daemon did not say — and it says whenever anything
    // other than the primary did the work, so falling back to PRIMARY here never
    // credits the wrong agent.
    const agent = fix.agent ?? PRIMARY;
    if (fix.stopped) {
      // Not a verdict: a container somebody interrupted has no opinion about the
      // work. Reporting it as a failure is how a deadline becomes a bug report.
      return { branch: task.branch, agent, changed: false, verified: false, note: "outlived its deadline" };
    }
    if (fix.exitCode !== 0) {
      return { branch: task.branch, agent, changed: false, verified: false, note: fix.stderr.trim().split("\n").pop() ?? `exit ${fix.exitCode}` };
    }

    // Did it actually change anything? Asked of git rather than of the agent:
    // an agent that reports success having written nothing is the commonest
    // failure this gate exists to catch, and the one it cannot be told about.
    const diff = await ws.run(["git", "status", "--porcelain"]);
    const changed = diff.stdout.trim() !== "";

    // The verification runs in the sandbox too. On the host it would be host
    // code selected by files the agent just wrote.
    const tests = await ws.run(["npm", "test"], { env: { CI: "true" }, timeoutMs: 10 * 60_000 });

    return {
      branch: task.branch,
      agent,
      changed,
      verified: tests.exitCode === 0,
      note: tests.exitCode === 0 ? "tests pass" : `tests exit ${tests.exitCode}`,
    };
  } catch (err) {
    // The launch succeeded and the wait did not, so a container is still out
    // there holding this branch's name. Nothing else can take it until it is
    // gone — including the next run of this script.
    if (err instanceof WaitError) await ws.stop(err.run.id);
    throw err;
  }
}

// In parallel, because the isolation unit is the branch: one worktree, one
// container, one agent. Two agents in one tree would be a data race with a
// filesystem in the middle; three agents in three trees are simply three runs.
const settled = await Promise.allSettled(TASKS.map(attempt));

const results = settled.map((s, i) =>
  s.status === "fulfilled"
    ? s.value
    : { branch: TASKS[i].branch, agent: "?", changed: false, verified: false, note: String(s.reason) },
);

for (const r of results) {
  const mark = r.verified && r.changed ? "READY" : "SKIP ";
  console.log(`${mark} ${r.branch.padEnd(10)} ${r.agent.padEnd(7)} ${r.note}`);
}

// The gate. A branch is worth a human's attention when the agent changed
// something *and* the tests agree — the two halves catch different lies, and
// either one alone has been enough to waste a review.
const ready = results.filter((r) => r.changed && r.verified);
console.log(`\n${ready.length}/${results.length} ready to review:`);
for (const r of ready) console.log(`  sandbox-cli worktree git ${r.branch} -- diff`);

// Non-zero when nothing came out of it, so this can be the last line of a CI job
// without a wrapper deciding what "worked" meant.
process.exitCode = ready.length > 0 ? 0 : 1;
