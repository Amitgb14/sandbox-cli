/**
 * The SDK page's content, kept out of the component for the same reason the
 * Studio walkthrough is: prose and code that someone will correct later should
 * be editable without reading JSX.
 *
 * Everything here is checked against sdk/typescript — the snippets are the ones
 * in its README and its tests, not invented for the page. A marketing example
 * that does not compile is worse than none, because somebody types it.
 */

export interface SdkStep {
  title: string;
  body: string;
  code?: string;
  /** What you should see, so a reader can tell working from nearly-working. */
  expect?: string;
}

/**
 * What has to be true before the first line of the example runs.
 *
 * The package talks to a daemon; it does not start one, and it cannot. Leaving
 * this out left `Studio.connect()` looking like the first step when it is the
 * last — and its failure ("cannot reach the sandbox daemon") reads as a bug in
 * the library rather than as a daemon nobody started.
 *
 * Two steps, not three: `studio.sh` installs the binaries it needs, so telling
 * people to install sandbox-cli first is a step that does nothing. And it is
 * fetched rather than run from the project directory — `sh studio.sh up` in
 * ~/code/my-app assumes a file nothing put there.
 */
export const SDK_PREREQS: {
  label: string;
  where: string;
  /** What the block is, which decides whether a `$` is drawn in front of it. */
  lang: "sh" | "ts";
  code: string;
  note: string;
}[] = [
  {
    label: "From the repository you want to work in, start the control plane",
    where: "Terminal — the machine that will run the containers",
    lang: "sh",
    code: "cd ~/code/my-app\ncurl -fsSL https://raw.githubusercontent.com/Amitgb14/sandbox-cli/main/studio.sh | sh",
    note: "It installs sandbox-cli and the daemon if they are missing, starts both halves, and registers the repository it was run in — which is what gives studio.project(\"my-app\") something to find. It also writes the API port and a generated token into ~/.config/sandbox/studio, which is what lets Studio.connect() take no arguments. Docker is the one thing it will not install for you: the daemon holds its socket, and every run is a container.",
  },
  {
    label: "Then add the client to your own project",
    where: "Terminal — wherever your code lives",
    lang: "sh",
    code: "npm install @sandbox-cli/sdk",
    note: "Node 20 or newer, and this half needs nothing else: no docker socket, no binaries, nothing to configure. The package is the client only \u2014 the daemon from step one is what holds the socket and starts the containers.",
  },
  {
    label: "Write your script as an ES module",
    where: "agent.mts",
    lang: "ts",
    code: "import { Studio } from \"@sandbox-cli/sdk\";\n\nconst studio = await Studio.connect();\nfor (const p of await studio.projects()) console.log(p.id, p.name);",
    note: "Everything here uses top-level await, which needs an ES module: either \"type\": \"module\" in your package.json, or the .mts extension as above. Without one, tsx compiles the file as CommonJS and stops at \"Top-level await is currently not supported\" — a fact about your project rather than about the client.",
  },
  {
    label: "Run it",
    where: "Terminal — wherever your code lives",
    lang: "sh",
    code: "npx tsx agent.mts",
    note: "tsx runs TypeScript directly, so there is no build step for a script. Node 20 or newer runs the same file as JavaScript if you would rather: rename it .mjs and drop the types. What you should see is the daemon answering — the repositories it knows about, or a typed error naming which half is wrong.",
  },
];

export const SDK_STEPS: SdkStep[] = [
  {
    title: "Connect to the daemon you already have",
    code: [
      "import { Studio } from \"@sandbox-cli/sdk\";",
      "",
      "const studio = await Studio.connect();   // no arguments",
    ].join("\n"),
    body:
      "studio.sh writes the API port and a generated token into ~/.config/sandbox/studio, so a script on that machine has no reason to ask you for either. Explicit arguments win, then SANDBOX_API_URL and SANDBOX_STUDIO_TOKEN, then those files. Connecting makes one round trip to /v1/health, which is the only route that answers without a token — so a missing credential is reported as a missing credential rather than as a failure of whatever you ran first.",
    expect:
      "A Studio bound to http://127.0.0.1:<the port studio.sh is using>, or a typed error saying which half is wrong.",
  },
  {
    title: "Pick a repository, and a branch to work in",
    code: [
      "const repo = await studio.project();          // or (\"my-app\"), or a path",
      "const ws = await repo.workspace(\"agent-42\");",
    ].join("\n"),
    body:
      "A Project is a repository the daemon has been told about — by id, by name when only one repository has it, by path, or with no argument at all, which asks git which repository the current directory belongs to — the same question the daemon asks, so a linked worktree resolves to its main repository rather than to itself. That last form is a lookup rather than a shortcut: the root is matched against what the daemon already knows, so a directory nobody registered is refused and told which roots exist. Run the script from anywhere, including a machine that is not the daemon's — in which case a local path correctly finds nothing there. A Workspace is that branch's git worktree, created if it is not there, and it is the isolation unit: two agents in one tree is a data race with a filesystem in the middle.",
  },
  {
    title: "Run something, and get back what happened",
    code: [
      "await ws.run([\"npm\", \"ci\"]);",
      "",
      "const tests = await ws.run([\"npm\", \"test\"], {",
      "  env: { CI: \"true\" },",
      "  timeoutMs: 10 * 60_000,",
      "});",
      "",
      "console.log(tests.exitCode, tests.stdout, tests.stderr);",
    ].join("\n"),
    body:
      "Each run is its own container over that worktree, and the worktree is what persists: the second command finds node_modules because the first wrote it to disk, not because anything stayed alive. stdout and stderr come back separated, and the exit code is the container's.",
    expect: "The command's real exit code — 0, or whatever it actually returned.",
  },
  {
    title: "Or hand the work to an agent",
    code: [
      "const done = await ws.agent(\"claude\", \"make the failing test pass\", {",
      "  fallback: [\"codex\"],",
      "});",
      "",
      "if (done.routedFrom) {",
      "  console.warn(`${done.routedFrom} was down; ${done.agent} did the work`);",
      "}",
    ].join("\n"),
    body:
      "Every outcome carries agent, routedFrom and routeReason whether or not you ask for them. A script that cannot see a failover attributes one agent's work to another — under the wrong login, and the wrong bill.",
  },
  {
    title: "Follow a run while it happens",
    code: [
      "for await (const event of ws.follow(run.id)) {",
      "  if (event.type === \"log\") process.stdout.write(event.data + \"\\n\");",
      "}",
    ].join("\n"),
    body:
      "Server-sent events, because the daemon offers SSE and WebSocket carrying the identical payload and SSE needs nothing a Node runtime does not already have. The loop ends on the daemon's own end event; leaving early closes the connection, which is what stops the docker logs --follow behind it.",
  },
];

/**
 * The whole thing, as one script.
 *
 * Taken verbatim from sdk/typescript/examples/agent-run.ts, which `npm test`
 * compiles — and compiles the way a reader would, importing by package name
 * rather than by a path inside the repository. An example checked in a shape
 * nobody types is not checked.
 */
export const SDK_EXAMPLE = "import { Studio, WaitError, type Outcome } from \"@sandbox-cli/sdk\";\n\nconst studio = await Studio.connect(); // port and token from ~/.config/sandbox/studio\nconst repo = await studio.project(\"my-app\");\nconst ws = await repo.workspace(\"agent-42\"); // a git worktree on that branch\n\ntry {\n  // npm rather than pnpm: the base image is node:22-bookworm-slim and carries\n  // npm only, so an example reaching for pnpm would exit 127 on its first line \u2014\n  // in a script whose whole claim is that it was checked.\n  const install = await ws.run([\"npm\", \"ci\"], { timeoutMs: 10 * 60_000 });\n  if (install.exitCode !== 0) {\n    console.error(install.stderr);\n    // `process.exitCode` rather than `process.exit()`: writes to a pipe \u2014 CI\n    // logs, `| tee`, a parent capturing output \u2014 are asynchronous, and exiting\n    // discards whatever is still buffered. That truncates hardest on the runs\n    // with the most to say.\n    process.exitCode = install.exitCode;\n    throw new Error(\"install failed\");\n  }\n\n  const fix: Outcome = await ws.agent(\"claude\", \"make the failing test pass\", {\n    fallback: [\"codex\"],\n    timeoutMs: 20 * 60_000,\n  });\n\n  // Reported on every outcome, not on request: a script that cannot see the\n  // failover credits the wrong agent \u2014 and bills the wrong account.\n  if (fix.routedFrom) {\n    console.warn(`${fix.routedFrom} was unavailable \u2014 ${fix.agent} did the work`);\n  }\n  // A stopped run is not a failed one. The exit code of a container somebody\n  // interrupted is not a verdict on the work.\n  if (fix.stopped) {\n    console.error(`${fix.agent} outlived its deadline and was stopped`);\n    process.exitCode = 1;\n    throw new Error(\"the agent was stopped\");\n  }\n  // The verdict itself, which `stopped` is not: an agent that exited non-zero\n  // has finished and failed. Falling through to the tests would blame them for\n  // work the agent never completed.\n  if (fix.exitCode !== 0) {\n    console.error(fix.stderr);\n    process.exitCode = fix.exitCode;\n    throw new Error(`${fix.agent} exited ${fix.exitCode}`);\n  }\n\n  // node_modules survived the first container because it was written to the\n  // worktree, not because anything stayed alive.\n  const tests = await ws.run([\"npm\", \"test\"], { env: { CI: \"true\" } });\n  console.log(tests.stdout);\n  process.exitCode = tests.exitCode;\n} catch (err) {\n  // The launch succeeded and the wait did not, so the container is still out\n  // there holding this branch's name \u2014 which docker will not let anything else\n  // take until it is gone.\n  if (err instanceof WaitError) await ws.stop(err.run.id);\n  throw err;\n}";

/**
 * A workflow rather than an agent: fan out, verify, gate.
 *
 * The question this answers is one people ask before writing anything — whether
 * orchestrating agents means building one. It does not: the control flow is
 * Promise.all and an if, and the only model involved is the one doing the work
 * inside each container.
 *
 * Taken verbatim from sdk/typescript/examples/workflow.ts, compiled by the same
 * `npm test` as the example above.
 */
export const SDK_WORKFLOW = "import { Studio, WaitError, type Outcome } from \"@sandbox-cli/sdk\";\n\n/**\n * A workflow, without writing an agent.\n *\n * Three tasks, three branches, three containers, in parallel \u2014 then one gate\n * that decides which of them a human should look at. The orchestration is\n * ordinary TypeScript: `Promise.all`, an array, an `if`. Nothing here needs a\n * model to decide what happens next, which is the point \u2014 a workflow whose\n * control flow is code fails the same way twice, and one whose control flow is a\n * prompt does not.\n */\n\n/** The agent asked for first. `fallback` names who covers an outage. */\nconst PRIMARY = \"claude\";\n\nconst TASKS = [\n  { branch: \"wf-tests\", prompt: \"make the failing unit tests pass\" },\n  { branch: \"wf-types\", prompt: \"remove every `any` in src/, keeping behaviour identical\" },\n  { branch: \"wf-docs\", prompt: \"update README.md so the examples match the current API\" },\n];\n\nconst studio = await Studio.connect();\nconst repo = await studio.project(); // the repository this script is standing in\n\n/** What the gate needs to know about one task, and nothing else. */\ntype Result = {\n  branch: string;\n  agent: string;\n  changed: boolean;\n  verified: boolean;\n  note: string;\n};\n\nasync function attempt(task: (typeof TASKS)[number]): Promise<Result> {\n  const ws = await repo.workspace(task.branch);\n  // A finished run holds the branch's container name \u2014 docker refuses a\n  // duplicate, which is exactly what stops two agents sharing one checkout. This\n  // clears yesterday's corpse and nothing that is running.\n  await ws.clearFinished();\n\n  try {\n    const fix: Outcome = await ws.agent(PRIMARY, task.prompt, {\n      fallback: [\"codex\"],\n      timeoutMs: 20 * 60_000,\n    });\n    // What actually ran, which is not always what was asked for. The field is\n    // absent only when the daemon did not say \u2014 and it says whenever anything\n    // other than the primary did the work, so falling back to PRIMARY here never\n    // credits the wrong agent.\n    const agent = fix.agent ?? PRIMARY;\n    if (fix.stopped) {\n      // Not a verdict: a container somebody interrupted has no opinion about the\n      // work. Reporting it as a failure is how a deadline becomes a bug report.\n      return { branch: task.branch, agent, changed: false, verified: false, note: \"outlived its deadline\" };\n    }\n    if (fix.exitCode !== 0) {\n      return { branch: task.branch, agent, changed: false, verified: false, note: fix.stderr.trim().split(\"\\n\").pop() ?? `exit ${fix.exitCode}` };\n    }\n\n    // Did it actually change anything? Asked of git rather than of the agent:\n    // an agent that reports success having written nothing is the commonest\n    // failure this gate exists to catch, and the one it cannot be told about.\n    const diff = await ws.run([\"git\", \"status\", \"--porcelain\"]);\n    const changed = diff.stdout.trim() !== \"\";\n\n    // The verification runs in the sandbox too. On the host it would be host\n    // code selected by files the agent just wrote.\n    const tests = await ws.run([\"npm\", \"test\"], { env: { CI: \"true\" }, timeoutMs: 10 * 60_000 });\n\n    return {\n      branch: task.branch,\n      agent,\n      changed,\n      verified: tests.exitCode === 0,\n      note: tests.exitCode === 0 ? \"tests pass\" : `tests exit ${tests.exitCode}`,\n    };\n  } catch (err) {\n    // The launch succeeded and the wait did not, so a container is still out\n    // there holding this branch's name. Nothing else can take it until it is\n    // gone \u2014 including the next run of this script.\n    if (err instanceof WaitError) await ws.stop(err.run.id);\n    throw err;\n  }\n}\n\n// In parallel, because the isolation unit is the branch: one worktree, one\n// container, one agent. Two agents in one tree would be a data race with a\n// filesystem in the middle; three agents in three trees are simply three runs.\nconst settled = await Promise.allSettled(TASKS.map(attempt));\n\nconst results = settled.map((s, i) =>\n  s.status === \"fulfilled\"\n    ? s.value\n    : { branch: TASKS[i].branch, agent: \"?\", changed: false, verified: false, note: String(s.reason) },\n);\n\nfor (const r of results) {\n  const mark = r.verified && r.changed ? \"READY\" : \"SKIP \";\n  console.log(`${mark} ${r.branch.padEnd(10)} ${r.agent.padEnd(7)} ${r.note}`);\n}\n\n// The gate. A branch is worth a human's attention when the agent changed\n// something *and* the tests agree \u2014 the two halves catch different lies, and\n// either one alone has been enough to waste a review.\nconst ready = results.filter((r) => r.changed && r.verified);\nconsole.log(`\\n${ready.length}/${results.length} ready to review:`);\nfor (const r of ready) console.log(`  sandbox-cli worktree git ${r.branch} -- diff`);\n\n// Non-zero when nothing came out of it, so this can be the last line of a CI job\n// without a wrapper deciding what \"worked\" meant.\nprocess.exitCode = ready.length > 0 ? 0 : 1;\n";

/**
 * Talking to a daemon on another machine.
 *
 * The browser docs make a lot of CORS and Host headers, and a script needs
 * neither: those checks fire on an `Origin`, which a browser sends and Node does
 * not. What a script needs is the URL and that machine's token — two values the
 * box prints when you start the daemon there.
 */
export const SDK_REMOTE_STEPS: {
  label: string;
  where: string;
  /** What the block is, which decides whether a `$` is drawn in front of it. */
  lang: "sh" | "ts" | "text";
  code: string;
  note: string;
}[] = [
  {
    label: "On the Linux box: start the daemon, bound to an address your machine can reach",
    where: "Terminal — the Linux box",
    lang: "sh",
    code: "cd ~/code/your-repo\ncurl -fsSL https://raw.githubusercontent.com/Amitgb14/sandbox-cli/main/studio.sh -o studio.sh\nsh studio.sh up --api-only --bind 10.0.0.5",
    note: "--api-only starts the daemon without the browser half, which a script does not need. --bind is the address to dial; the daemon refuses a routable one without a token, so the script generates one and prints it. It also tells the daemon to answer to that name — it answers to loopback names by default and refuses everything else, which looks exactly like the daemon being down.",
  },
  {
    label: "It prints the two values your script needs",
    where: "What it prints",
    lang: "text",
    code: "On the machine with the browser, open Studio → Settings → Connection:\n  Daemon URL   http://10.0.0.5:8787\n  Token        3f9c1e7a…",
    note: "The token belongs to that machine, not to you: every daemon generates its own. Copy both. `sh studio.sh status` prints them again later.",
  },
  {
    label: "Open the port, and check it before you touch any code",
    where: "Terminal — the Linux box",
    lang: "sh",
    code: "sudo firewall-cmd --permanent --add-port=8787/tcp && sudo firewall-cmd --reload\n# Debian and Ubuntu: sudo ufw allow from 10.0.0.0/24 to any port 8787 proto tcp",
    note: "A server distribution denies inbound by default, and the failure is silent from the other side. Check it with curl from your own machine: /v1/health is the one route that answers without a token, so a JSON reply means the network, the bind and the firewall are all correct and anything left is authentication.",
  },
  {
    label: "In your script: the URL and the token, and nothing else",
    where: "agent.mts",
    lang: "ts",
    code: 'import { Studio } from "@sandbox-cli/sdk";\n\nconst studio = await Studio.connect({\n  url: "http://10.0.0.5:8787",\n  token: process.env.SANDBOX_STUDIO_TOKEN,\n});\n\nconsole.log(await studio.health());',
    note: "Keep the token in the environment rather than in the file — it is a credential for a machine that can start containers. No CORS origin and no Host flag are involved: those checks fire on an Origin header, which browsers send and scripts do not, so a script is governed by the token alone.",
  },
];

/**
 * The smallest useful things, for somebody who has just connected.
 *
 * Each is a whole script rather than a fragment: the first thing anybody does
 * with a new client is paste one and run it.
 */
export const SDK_SNIPPETS: { title: string; code: string; note: string }[] = [
  {
    title: "What repositories does this daemon know?",
    code: 'const studio = await Studio.connect();\nfor (const p of await studio.projects()) {\n  console.log(p.id, p.name, p.root);\n}',
    note: "Names come from the daemon's registry — what somebody added in Studio, plus the repository it was started in.",
  },
  {
    title: "Work on a repository the daemon has never heard of",
    code: 'const repo = await studio.addProject();   // this script\'s own repository\n// ...or a path on the daemon\'s machine:\n// const repo = await studio.addProject("/home/you/code/my-app");\n\nconst ws = await repo.workspace("agent-1");\nconsole.log(await ws.run(["git", "log", "--oneline", "-1"]));',
    note: "A path is resolved on the machine running the script, then sent absolute — so the no-argument form is for a daemon on this machine, and a remote one will say it has no such directory rather than guess. Adding a repository that is already registered returns the same row, so this is safe to run every time.",
  },
  {
    title: "Run one command and read its output",
    code: 'const repo = await studio.project("your-repo");\nconst ws = await repo.workspace("scratch");\n\nconst out = await ws.run(["sh", "-c", "ls -la; git status --short"]);\nconsole.log(out.exitCode, out.stdout, out.stderr);',
    note: "A workspace is a branch's worktree, created if it is not there. The container mounts that tree at /workspace and nothing else of yours.",
  },
  {
    title: "Run the same thing twice without them colliding",
    code: 'for (const name of ["one", "two"]) {\n  const ws = await repo.workspace(`try-${name}`);\n  console.log(await ws.run(["sh", "-c", `echo ${name}`]));\n}',
    note: "A branch per run. Docker refuses a duplicate container name, which is what stops two agents sharing one checkout — so two runs on one branch collide, and two branches do not. Both keep their logs.",
  },
  {
    title: "Hand a task to an agent",
    code: 'const done = await ws.agent("claude", "add a test for the parser", {\n  fallback: ["codex"],\n  timeoutMs: 15 * 60_000,\n});\n\nif (done.routedFrom) console.warn(`${done.routedFrom} was down; ${done.agent} did it`);\nconsole.log(done.exitCode, done.stopped);',
    note: "The agent needs a login inside the sandbox first — run it once interactively with `sandbox-cli claude` on that machine. routedFrom is how you notice a fallback fired; stopped is how you tell an interrupted run from a failed one.",
  },
  {
    title: "Watch a long run as it happens",
    code: 'const run = await ws.start({ agent: "claude", prompt: "explain this repository" });\n\nfor await (const event of ws.follow(run.id)) {\n  if (event.type === "log") console.log(event.data);\n}',
    note: "start() launches without waiting; follow() streams until the daemon says the output has ended. Leaving the loop early closes the stream, which is what stops the log tail behind it.",
  },
  {
    title: "Clean up when you are done with a run",
    code: 'await ws.stop(run.id);        // ask it to exit\nawait ws.remove(run.id);      // then discard the container and its logs',
    note: "Two calls, and neither happens for you: a finished run's logs are the evidence for what it did. remove() is also what frees the branch's name for the next run.",
  },
];

/** The claims a reader should be able to check, rather than take on trust. */
export const SDK_RULES = [
  {
    title: "It is a client, and only a client",
    body:
      "No docker socket, no shelling out to sandbox-cli, no argv assembled here. Every gate that makes a sandbox a sandbox — the workspace refusals, the fake HOME, default-deny environment, the egress allowlist — is applied where the container is built, on the machine running the daemon. When this package wants a capability the daemon does not expose, the daemon grows an endpoint and the gate is written once, in Go, with a test.",
  },
  {
    title: "Finding a repository is a lookup, never a registration",
    body:
      "studio.project() with no argument walks up to the git root and matches it against the repositories the daemon has been told about. What it will not do is add the one it fails to find: the registry is the list of directories that daemon will touch, and a lookup that quietly grew it would turn a typo into a permanent entry. studio.addProject() is the sentence that asks — the only call that hands over a path, mirroring the one endpoint that accepts one, where every check on a directory is applied by the daemon. It is a no-op for a repository already registered, so it is safe on every start.",
  },
  {
    title: "There is no mock mode",
    body:
      "A fake run() returning exitCode 0 is the worst possible default for a library whose entire job is telling you what happened. A test double belongs in your test suite, where you can see it.",
  },
  {
    title: "A deadline stops the run, and says so",
    body:
      "The wait is bounded — thirty minutes by default. When it expires the run is stopped and the outcome reports stopped: true, rather than putting a verdict on a container that was interrupted. If the stop itself is refused, that surfaces: claiming a run was stopped while it is still running would announce the outcome the deadline exists to prevent as though it had been prevented.",
  },
  {
    title: "A launched run is never lost",
    body:
      "If anything goes wrong after the launch — a daemon restart mid-poll, a cancel — you get a WaitError carrying the run. The container exists whatever happened, and a detached run holds sandbox-<repo>-<branch>, which docker will not duplicate, so an error without the id would leave the branch blocked by something you cannot name.",
  },
  {
    title: "stop and remove are different, and neither is implicit",
    body:
      "A finished run's logs are the evidence for what it did. Tidying up on the way out would discard that on every happy path, so nothing is removed unless you ask.",
  },
  {
    title: "Running it twice needs you to say so",
    body:
      "Docker refuses a duplicate container name, and that refusal is what enforces one agent per branch — so a finished run keeps its branch's name until somebody reaps it, and a second launch is refused with the run id and how to read its logs. Removing it for you would discard the evidence for what the first run did, on every second run. `{ replaceFinished: true }` says the evidence is spent; `clearFinished()` reaps it and tells you what went. Both refuse a run that is still going.",
  },
  {
    title: "The types are generated, not written",
    body:
      "src/contract.ts comes from internal/studioapi/types.go, the same pass that writes the documentation mirror, and CI fails when the checked-in copy differs from what the generator produces. A published client describing an API the daemon does not have is the failure that would be hardest to notice.",
  },
];

/** Errors are typed and distinct on purpose; each sends you somewhere different. */
export const SDK_ERRORS = [
  { name: "ApiError", meaning: "the daemon refused, and its own message is carried verbatim" },
  { name: "ConnectionError", meaning: "nothing answered at that address" },
  { name: "TimeoutError", meaning: "it answered too slowly — reachable, not down" },
  { name: "WaitError", meaning: "the run started; waiting for it did not finish. Carries the run" },
  { name: "AbortError", meaning: "you cancelled it — err.name, the check callers already write" },
];
