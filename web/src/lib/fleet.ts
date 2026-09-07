/**
 * The multi-agent story, as data.
 *
 * Mirrors the repository — `internal/fleet`, `internal/agents`,
 * `docs/GUIDE.md` ("Running agents in parallel"), `docs/AGENTS.md` ("Agents a
 * fleet can run") and `docs/examples/fleet.yaml`. If the CLI's behaviour
 * changes, edit this file and the page follows.
 *
 * The one rule worth restating here, because every claim below depends on it:
 * a fleet owns no isolation policy. Every task becomes the same options a
 * `--worktree` run produces, with detach set, so nothing on this page is a
 * different boundary from the one the landing page describes.
 */

/** The four rungs of the same ladder, weakest commitment first. */
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
    id: "worktree",
    label: "One agent per branch",
    flag: "--worktree feature-a",
    adds: "A git worktree of its own, so two agents never edit the same files or fight over the same branch.",
    enough: "You are running one agent and watching it.",
  },
  {
    id: "detach",
    label: "In the background",
    flag: "--detach",
    adds: "The container outlives the terminal, so one window can start several.",
    enough: "You want two or three going and will check on them by hand.",
  },
  {
    id: "fleet",
    label: "A fleet",
    flag: "fleet run",
    adds: "All of them from one file, plus the answer the rung above cannot give: which of these actually worked?",
    enough: "You want the work checked, not just started.",
  },
  {
    id: "share",
    label: "Handing files over",
    flag: "--share",
    adds: "One directory two sandboxes can both see, for an artifact that crosses between them.",
    enough: "One agent produces something another needs.",
  },
];

/**
 * Agents eligible for a fleet, and the argv each one is actually started with.
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
    name: "cline",
    argv: "cline PROMPT --auto-approve true",
    delivery: "first-run",
    note:
      "The prompt is a bare positional and the TUI is the opt-in (-i), which is the inverse of the others. --auto-approve is passed explicitly rather than relying on its default, because an unattended run that starts asking does not fail — it hangs.",
  },
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
];

/** Everything else is refused when the file is parsed, before a container starts. */
export const UNSUPPORTED_AGENT_COUNT = 9;

/** The commented file the page leads with. Kept in sync with docs/examples/fleet.yaml. */
export const FLEET_YAML = `agent: claude          # the default for tasks that name no agent
max_parallel: 2
defaults:
  memory: 4g
  cpus: "2"
  git: true            # so the agents' commits carry your name and email

tasks:
  - branch: feature-login
    prompt: Implement the login form in src/auth/. Add tests. Commit when they pass.
    verify: go build ./... && go test ./...

  - branch: feature-ratelimit
    agent: codex       # a different agent for this branch
    memory: 8g         # and its own limits
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

/**
 * The file, as a thing you pass rather than as a schema.
 *
 * Separate from the YAML sample because the sample answers "what goes in it"
 * and these answer "how does it get read", which is where the friction actually
 * is — a file named fleet.yml is not the default and will not be found.
 */
export const FILE_RULES = [
  {
    title: "Named, never discovered",
    body: "fleet run reads exactly the path you give it, and defaults to ./fleet.yaml. It never walks up the directory tree looking for one, unlike .sandbox.yaml. That is what lets the file carry command-line trust: naming it is an act you performed, so a fleet.yaml in a parent directory or a repository you cloned can never be picked up by accident.",
    code: "sandbox-cli fleet run -f fleet.yml",
  },
  {
    title: "A typo is an error, not a default",
    body: "Unknown keys are rejected rather than ignored. max_parallell: 4 fails the load, because the alternative is a fleet running with limits its author believed they had set — including the memory caps.",
    code: "line 1: field max_parallell not found in type fleet.Spec",
  },
  {
    title: "-f and -c are different files",
    body: "-f is the task list. -c is the sandbox config, and it is where image:, mounts: and env_allow: live — a task does not choose what it runs in, so a fleet file has no key for any of them.",
    code: "sandbox-cli fleet run -f ci.yml -c ~/.config/sandbox/prod.yaml",
  },
];

/**
 * What is actually inside a fleet container, for the questions that only come up
 * once something is missing. Every one of these was somebody's confusing hour.
 */
export const REACH = [
  {
    title: "The worktree, the login, and the repository's .git",
    body: "/workspace is the worktree and /sandbox/home is the agent's persisted login. A worktree task also gets the parent repository's .git — read-write, mounted at its own host path, because a linked worktree cannot commit without it — with .git/hooks read-only over the top. Nothing else on your machine is there: not /tmp, not your other repositories, not the skills your own Claude Code has. An agent told to write outside those paths writes into a container that is about to be thrown away.",
  },
  {
    title: "Skills travel with the repository, not with you",
    body: "A skill committed at .claude/skills/ arrives inside /workspace and every task sees it. One installed at ~/.claude/skills/ on the host does not — the container's HOME is a different directory entirely. It has to be committed, too: a worktree is a checkout of the branch, so an untracked skill in your main checkout is invisible to it.",
  },
  {
    title: "The handover directory is opt-in",
    body: "/shared exists only when you pass --share, and a fleet file cannot turn it on. Without the flag an agent asked to read /shared/x is being asked for a path that is not there, which it will report as missing rather than invent.",
  },
  {
    title: "The image is not the fleet's to choose",
    body: "The base image carries node and python3 and no other toolchain — no pip, no go, no compiler beyond build-essential. A verify: of go test or pip install exits 127 in it. The fix is a config layer rather than a fleet one: image: in your own config, or -c naming one.",
  },
];

/**
 * Reading a running agent. The distinction that catches people is that a fleet
 * container has no keyboard — which is deliberate, and is not the same as being
 * unreachable.
 */
export const WATCHING = [
  {
    title: "You read it, you do not type at it",
    body: "A fleet container is created with no tty and no stdin, so there is nothing to answer a question with. That is the point: a fleet is unattended, and an agent that stopped to ask would not fail but hang, holding a max_parallel slot. To watch one live, sandbox-cli attach <branch> — the top-level command, since there is no fleet attach — streams its output and says in as many words that it has no keyboard.",
    code: "sandbox-cli fleet logs feature-login -f",
  },
  {
    title: "Silence is usually not a hang",
    body: "An agent in headless mode buffers and prints its answer at the end rather than narrating as it goes, so an empty log a minute in is normal. fleet status is the better progress signal: it reports what each branch has produced — commits, uncommitted files, how far ahead it is — rather than waiting on stdout.",
    code: "sandbox-cli fleet status --watch",
  },
  {
    title: "To talk to one, run it interactively instead",
    body: "Same worktree, same branch, attached. Stop the fleet task first: nothing refuses this for you. The duplicate-name rule that enforces one agent per branch only applies to detached containers, and a foreground run is named for the moment it started — so two agents will happily edit one worktree, which is the silent loss that rule exists to prevent everywhere else.",
    code: "sandbox-cli fleet stop feature-login\nsandbox-cli claude --worktree feature-login",
  },
];

export type LoopStep = {
  cmd: string;
  what: string;
  /** Shown as a secondary line when there is a variant worth knowing. */
  also?: string;
};

/** The whole cycle, run from your normal checkout. */
export const LOOP: LoopStep[] = [
  {
    cmd: "sandbox-cli claude",
    what: "Log in once per agent. A detached container cannot answer a login prompt, so every agent the file names needs this first.",
    also: "…and again for each other agent: sandbox-cli codex",
  },
  {
    cmd: "sandbox-cli fleet run --dry-run",
    what: "See what each task would do — the agent argv, the verify, the limits, the mounts — without launching anything.",
  },
  {
    cmd: "sandbox-cli fleet run",
    what: "Fan out. One branch, one worktree and one container per task.",
  },
  {
    cmd: "sandbox-cli fleet status",
    what: "One line per branch: which agent, whether it is running, how long, what it left uncommitted, how far ahead it is.",
    also: "--watch redraws until you stop it",
  },
  {
    cmd: "sandbox-cli fleet logs feature-login",
    what: "What one agent actually said. Works after it exits, because fleet containers are kept.",
    also: "-f to follow it live",
  },
  {
    cmd: "sandbox-cli fleet land --all",
    what: "Commit whatever each agent left, then merge every branch that can be merged, oldest first.",
    also: "or one at a time: sandbox-cli fleet land feature-login",
  },
  {
    cmd: "sandbox-cli fleet clean --worktrees",
    what: "Reap the finished containers, and the checkouts too — skipping any with uncommitted work rather than discarding it.",
  },
];

/** What to run when part of a fleet goes wrong, instead of re-running the file. */
export const RECOVERY = [
  {
    cmd: "sandbox-cli fleet run --only feature-login",
    what: "Retry the one task that failed. A branch the file does not contain is an error listing the ones it does — launching nothing looks exactly like success.",
  },
  {
    cmd: "sandbox-cli fleet run --resume",
    what: "Pick up an interrupted run: skip branches whose agent is still working and branches that already exited 0, start the rest.",
  },
];

/**
 * `land` is the only operation that writes to your base branch, so it refuses
 * on every ambiguity. `--all` splits those refusals in two, and that split is
 * the design rather than a convenience.
 */
export type Refusal = {
  when: string;
  /** Under --all: does the fleet carry on, or stop here? */
  scope: "skips this branch" | "stops the run";
  why: string;
};

export const LAND_REFUSALS: Refusal[] = [
  {
    when: "The agent is still running",
    scope: "skips this branch",
    why: "Its next action could change what you just merged.",
  },
  {
    when: "The work failed its verify",
    scope: "skips this branch",
    why: "Nothing has said this work is right. --force lands it anyway, and says so.",
  },
  {
    when: "There is nothing to merge",
    scope: "skips this branch",
    why: "No commits beyond the base, so a merge would be of zero commits.",
  },
  {
    when: "Your checkout moved since launch",
    scope: "stops the run",
    why: "Each container records the branch its work was meant for. Landing onto a branch nobody chose needs a rewrite to undo. --onto says you mean it.",
  },
  {
    when: "The base checkout is dirty",
    scope: "stops the run",
    why: "The merge commit would sweep up your unrelated in-progress work.",
  },
  {
    when: "An agent is working in the base checkout",
    scope: "stops the run",
    why: "The merge rewrites files under it, mid-edit.",
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
    title: "One agent per branch",
    body: "Enforced by construction, not by a check: a detached container is named sandbox-<repo>-<branch>, and docker refuses a duplicate name. Two agents in one checkout lose work silently.",
  },
  {
    title: "The fleet has to fit in the machine",
    body: "Before anything starts, sandbox-cli multiplies how many agents run at once by the widest per-task memory cap and compares it with what the host has. Too big and it refuses, naming the arithmetic. It is the concurrent count, not the task count — twenty tasks at max_parallel: 2 is two agents' worth.",
  },
  {
    title: "A fleet never touches your own session",
    body: "fleet stop --all does not reach an interactive --detach session in the same repository, fleet clean does not reap one, and max_parallel does not count one. sandbox-cli list marks which is which, because that is where you decide what to kill.",
  },
  {
    title: "A fleet agent is a session",
    body: "fleet status prints the same id sandbox-cli list does, and logs, attach and kill all take a branch name — so there is one way to reach a running agent whatever started it.",
  },
  {
    title: "Run it under --profile prod",
    body: "dev warns when a control cannot be satisfied; prod refuses. Nobody is watching a fleet, so a warning goes into a log no one reads. prod also declines to mount the persisted login, so each agent needs its key in the environment instead.",
  },
];

/** The share convention: a pattern, deliberately not a protocol. */
export const SHARE_YAML = `tasks:
  - branch: api-contract
    prompt: |
      Design the API for the new billing flow and write it to
      /shared/billing/openapi.yaml. Do not implement anything.
    verify: test -s /shared/billing/openapi.yaml

  - branch: api-client
    prompt: |
      Read /shared/billing/openapi.yaml and implement a typed client for it
      in src/api/. If the file is not there, stop and say so.
    verify: go build ./...`;

export const SHARE_RULES = [
  {
    title: "Turn it on from the command line",
    body: "sandbox-cli fleet run --share. A cross-project directory is exactly the reach the sandbox otherwise refuses, so it is a flag and not a fleet.yaml key — switching it on stays something you can see in your shell history.",
  },
  {
    title: "Order with max_parallel: 1",
    body: "Tasks start in file order, so one slot means the producer finishes before the consumer starts. There is no depends_on: and there will not be one — a dependency graph is the beginning of a workflow engine, and this is a CLI.",
  },
  {
    title: "Say what to do when the file is missing",
    body: "An agent that invents the API rather than stopping is the failure mode here, and the consumer's verify is what catches it.",
  },
];
