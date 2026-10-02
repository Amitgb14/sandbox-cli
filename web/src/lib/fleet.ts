/**
 * The multi-agent story, as data.
 *
 * Mirrors the repository — `internal/fleet` (spec.go, runner.go, land.go),
 * `internal/routing`, `internal/agents` — and the commands as they print. If
 * the CLI's behaviour changes, edit this file and the page follows.
 *
 * The one rule worth restating here, because every claim below depends on it:
 * a fleet owns no isolation policy. Every task is the same create request a
 * `sandbox-cli agent <name>` run makes — a fresh VM, a clone of your
 * repository — so nothing on this page is a different boundary from the one
 * the landing page describes.
 */

/** The rungs of the same ladder, weakest commitment first. */
export type Rung = {
  id: string;
  label: string;
  flag: string;
  adds: string;
  /** Why you would stop here rather than climb further. */
  enough: string;
};

export const RUNGS: Rung[] = [
  {
    id: "agent",
    label: "One agent, watched",
    flag: "sandbox-cli agent claude",
    adds: "A VM of its own on a clone of the repository; its commits come back to refs/sandbox/<id> for you to merge.",
    enough: "You are running one agent and watching it.",
  },
  {
    id: "detach",
    label: "In the background",
    flag: "--detach",
    adds: "The sandbox outlives the terminal, so one window can start several; list, logs and attach find them again.",
    enough: "You want two or three going and will check on them by hand.",
  },
  {
    id: "fallback",
    label: "With somewhere to fall through to",
    flag: "--fallback codex",
    adds: "If the provider is down, or the run fails having changed nothing, the next agent gets the task in a fresh sandbox with a briefing of the first one's conversation.",
    enough: "One task, and an outage should cost minutes rather than the afternoon.",
  },
  {
    id: "fleet",
    label: "A fleet",
    flag: "agent fleet run",
    adds: "Many tasks from one file, in parallel, plus the answer the rungs above cannot give: which of these actually worked?",
    enough: "You want the work checked, not just started.",
  },
];

/**
 * Agents eligible for a fleet or a fallback, and the argv each one is actually
 * started with.
 *
 * The bar is a **verified** headless mode, not a documented-looking flag: a
 * fleet has no terminal, so an agent that stops for approval does not fail — it
 * hangs, holding a slot. `internal/agents` pins each argv with a test, so this
 * list cannot grow by guesswork.
 */
export type FleetAgent = {
  name: string;
  argv: string;
  delivery: "baked" | "first-run";
  /** The thing worth knowing before naming it in a file. */
  note?: string;
};

export const FLEET_AGENTS: FleetAgent[] = [
  {
    name: "claude",
    argv: "claude -p PROMPT --dangerously-skip-permissions",
    delivery: "baked",
  },
  {
    name: "codex",
    argv: "codex exec PROMPT",
    delivery: "baked",
    note: "Codex applies its own approval policy on top; relax it through the task's args:.",
  },
  {
    name: "gemini",
    argv: "gemini --yolo -p PROMPT",
    delivery: "baked",
    note: "-p alone runs to completion and then stops at a tool it wants confirmed, so --yolo is not optional here.",
  },
  {
    name: "opencode",
    argv: "opencode run PROMPT",
    delivery: "baked",
  },
  {
    name: "droid",
    argv: "droid exec PROMPT",
    delivery: "first-run",
    note: "Not in the image: installed in each task's sandbox when it starts (~148 MB).",
  },
];

/** Everything else is refused when the file is parsed, before a sandbox starts. */
export const UNSUPPORTED_AGENT_COUNT = 10;

/** The commented file the page leads with. */
export const FLEET_YAML = `agent: claude          # the default for tasks that name no agent
max_parallel: 2        # sandboxes at once; the rest wait their turn
defaults:
  memory: 4g           # per sandbox (the default); "0" for the server's own
  cpus: "2"
  git: true            # so the agents' commits carry your name and email

tasks:
  - branch: feature-login
    prompt: Implement the login form in src/auth/. Add tests. Commit when they pass.
    verify: go build ./... && go test ./...

  - branch: feature-ratelimit
    agent: codex       # a different agent for this branch
    memory: 8g         # and its own limits
    allow: [proxy.golang.org]   # added to the fleet's allowlist, never subtracted
    prompt: Add per-IP rate limiting to src/server/. Add tests. Commit when they pass.
    verify: go test ./src/server/...`;

/** The mixed-agent excerpt, on its own, for the section that is only about that. */
export const MIXED_YAML = `agent: claude
tasks:
  - branch: feature-login
    prompt: Implement the login form.

  - branch: feature-ratelimit
    agent: codex
    prompt: Add per-IP rate limiting.`;

export type LoopStep = {
  cmd: string;
  what: string;
  /** Shown as a secondary line when there is a variant worth knowing. */
  also?: string;
};

/** The whole cycle, run from your normal checkout. */
export const LOOP: LoopStep[] = [
  {
    cmd: "sandbox-cli agent claude",
    what: "Log in once per agent the file names. The login is saved and copied into every task's sandbox; a sandbox with nobody attached cannot answer a login prompt.",
    also: "…and again for each other agent: sandbox-cli agent codex",
  },
  {
    cmd: "sandbox-cli agent fleet run -f fleet.yaml",
    what: "Fan out: one sandbox per task, max_parallel at a time, each a fresh VM on a clone of HEAD. As each finishes its line says verified, rejected, failed or lost, and where its work went.",
    also: "--keep leaves each task's sandbox up afterwards, to look inside",
  },
  {
    cmd: "sandbox-cli agent fleet status",
    what: "One line per branch: agent, state, exit code, and the ref its work came back to. It reads the run's record, so it works after every sandbox is gone.",
  },
  {
    cmd: "sandbox-cli agent fleet land --all",
    what: "Merge every verified task's work into the branch you are on, each as its own --no-ff merge. Refusals about one branch skip it; a refusal about the base stops.",
    also: "or one at a time: sandbox-cli agent fleet land feature-login",
  },
];

/** The output of a run, as it prints. */
export const RUN_OUTPUT = `sandbox-cli agent fleet run -f fleet.yaml
# feat/two                 rejected  exit 90  refs/sandbox/fleet/feat/two
# feat/one                 verified  exit 0  refs/sandbox/fleet/feat/one
#
# logs: /home/you/.config/sandbox/fleet/902ba2c8ae7c053d/logs
# land: sandbox-cli agent fleet land --all
# sandbox-cli: 1 of 2 tasks did not verify`;

export const LAND_OUTPUT = `sandbox-cli agent fleet land --all
# landed  feat/one
# skipped feat/two: "feat/two" finished as rejected (exit 90); --unverified lands it anyway`;

/**
 * `land` is the only operation that writes to one of your branches, so it
 * refuses on every ambiguity. `--all` splits those refusals in two, and that
 * split is the design rather than a convenience.
 */
export type Refusal = {
  when: string;
  /** Under --all: does the fleet carry on, or stop here? */
  scope: "skips this branch" | "stops the run";
  why: string;
};

export const LAND_REFUSALS: Refusal[] = [
  {
    when: "The task is still running",
    scope: "skips this branch",
    why: "Its commits are not final.",
  },
  {
    when: "The work did not verify",
    scope: "skips this branch",
    why: "Nothing has said this work is right — it failed, or its verify rejected it. --unverified lands it anyway, and the merge commit says which.",
  },
  {
    when: "There is nothing to merge",
    scope: "skips this branch",
    why: "It brought back no commits, or none your branch does not already have.",
  },
  {
    when: "You are on a different branch than the fleet started from",
    scope: "stops the run",
    why: "The run recorded the branch its work was meant for. Merging into one nobody chose needs a rewrite to undo; --onto says you mean it.",
  },
  {
    when: "HEAD is detached",
    scope: "stops the run",
    why: "There is no branch to merge into.",
  },
  {
    when: "Your checkout has uncommitted changes",
    scope: "stops the run",
    why: "The merge commit would sweep up your unrelated work in progress.",
  },
  {
    when: "The merge conflicts",
    scope: "stops the run",
    why: "It stops with git's own message and leaves the merge in place. land never resolves anything itself.",
  },
];

/** The guardrails that are easy to miss until one of them fires. */
export const GUARDRAILS = [
  {
    title: "Work lands from refs, never from a sandbox",
    body: "Each task's work is brought back, verified against your repository, into refs/sandbox/fleet/<branch> before land sees it. land merges that ref — never a directory an agent could still be writing.",
  },
  {
    title: "One agent per branch",
    body: "Two tasks naming one branch are refused when the file is read, and each task's sandbox is named after its branch, which the server will not give to two live sandboxes at once.",
  },
  {
    title: "Verify runs in the sandbox",
    body: "The task's verify is wrapped around the agent's own argv and its exit code becomes the task's: 0, or 90 for rejected. In the sandbox because a verify run on your machine would be host code chosen by a file the agent can write.",
  },
  {
    title: "Labelled, so you can find them",
    body: "Every task's sandbox carries agent= and fleet.branch= labels: sandbox-cli list --label fleet.branch=feature-login, and the same labels are in its audit events.",
  },
  {
    title: "Run it under --profile prod",
    body: "dev warns when a control cannot be satisfied; prod refuses. Nobody is watching a fleet, so a warning goes into a log no one reads. prod also copies no agent login into a sandbox, so each agent needs an API key in the environment instead.",
  },
];
