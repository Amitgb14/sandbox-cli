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
export const SDK_PREREQS = [
  {
    label: "From the repository you want to work in, start the control plane",
    code: "cd ~/code/my-app\ncurl -fsSL https://raw.githubusercontent.com/Amitgb14/sandbox-cli/main/studio.sh | sh",
    note: "It installs sandbox-cli and the daemon if they are missing, starts both halves, and registers the repository it was run in — which is what gives studio.project(\"my-app\") something to find. It also writes the API port and a generated token into ~/.config/sandbox/studio, which is what lets Studio.connect() take no arguments. Docker is the one thing it will not install for you: the daemon holds its socket, and every run is a container.",
  },
  {
    label: "Then add the client to your own project",
    code: "npm install @sandbox-cli/sdk",
    note: "Node 20 or newer, and this half needs nothing else — no docker socket, no binaries, nothing to configure. Save studio.sh beside your repository if you want `sh studio.sh status` and `sh studio.sh down` later; the one-liner above starts it, and the file is how you manage it.",
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
      "const repo = await studio.project(\"my-app\");",
      "const ws = await repo.workspace(\"agent-42\");",
    ].join("\n"),
    body:
      "A Project is a repository the daemon has been told about — named by id, or by name when only one repository has it, because two clones share a name and never an id. A Workspace is that branch's git worktree, created if it is not there, and it is the isolation unit: two agents in one tree is a data race with a filesystem in the middle.",
  },
  {
    title: "Run something, and get back what happened",
    code: [
      "await ws.run([\"pnpm\", \"install\"]);",
      "",
      "const tests = await ws.run([\"pnpm\", \"test\"], {",
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
export const SDK_EXAMPLE = "import { Studio, WaitError, type Outcome } from \"@sandbox-cli/sdk\";\n\nconst studio = await Studio.connect(); // port and token from ~/.config/sandbox/studio\nconst repo = await studio.project(\"my-app\");\nconst ws = await repo.workspace(\"agent-42\"); // a git worktree on that branch\n\ntry {\n  const install = await ws.run([\"pnpm\", \"install\", \"--frozen-lockfile\"], {\n    timeoutMs: 10 * 60_000,\n  });\n  if (install.exitCode !== 0) {\n    console.error(install.stderr);\n    process.exit(install.exitCode);\n  }\n\n  const fix: Outcome = await ws.agent(\"claude\", \"make the failing test pass\", {\n    fallback: [\"codex\"],\n    timeoutMs: 20 * 60_000,\n  });\n\n  // Reported on every outcome, not on request: a script that cannot see the\n  // failover credits the wrong agent \u2014 and bills the wrong account.\n  if (fix.routedFrom) {\n    console.warn(`${fix.routedFrom} was unavailable \u2014 ${fix.agent} did the work`);\n  }\n  // A stopped run is not a failed one. The exit code of a container somebody\n  // interrupted is not a verdict on the work.\n  if (fix.stopped) {\n    console.error(`${fix.agent} outlived its deadline and was stopped`);\n    process.exit(1);\n  }\n\n  // node_modules survived the first container because it was written to the\n  // worktree, not because anything stayed alive.\n  const tests = await ws.run([\"pnpm\", \"test\"], { env: { CI: \"true\" } });\n  console.log(tests.stdout);\n  process.exit(tests.exitCode);\n} catch (err) {\n  // The launch succeeded and the wait did not, so the container is still out\n  // there holding this branch's name \u2014 which docker will not let anything else\n  // take until it is gone.\n  if (err instanceof WaitError) await ws.stop(err.run.id);\n  throw err;\n}";

/** The claims a reader should be able to check, rather than take on trust. */
export const SDK_RULES = [
  {
    title: "It is a client, and only a client",
    body:
      "No docker socket, no shelling out to sandbox-cli, no argv assembled here. Every gate that makes a sandbox a sandbox — the workspace refusals, the fake HOME, default-deny environment, the egress allowlist — is applied where the container is built, on the machine running the daemon. When this package wants a capability the daemon does not expose, the daemon grows an endpoint and the gate is written once, in Go, with a test.",
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
