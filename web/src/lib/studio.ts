/**
 * Sandbox Studio: the browser control plane, as a sequence you can follow.
 *
 * This is deliberately its own dataset rather than more entries in
 * TUTORIAL_STEPS, because Studio is a different *shape* of thing to set up. The
 * CLI tutorial is one process and one terminal: every step is `sandbox-cli
 * something`, and the only way it goes wrong is that the command refuses. Studio
 * is **two processes that have to agree** — a daemon holding the docker socket
 * and a Next app in your browser — and almost everything that goes wrong is a
 * disagreement between them about a port, an origin, or a token. A linear list
 * of shell commands hides exactly the thing a reader needs to keep straight, so
 * each step here says which side it belongs to.
 *
 * The three refusals in `internal/studioapi/guard.go` are the reason the setup
 * has as many steps as it does, and they are not incidental: a control plane on
 * loopback is reachable by any page you happen to be visiting, so it has to
 * answer "who may ask this process to start containers" before it answers
 * anything else. Each guard gets its own step rather than a footnote, because
 * every one of them presents as "Studio does not work" the first time you hit it.
 *
 * Mirrors cmd/sandbox-studio-api/main.go (the flags), internal/studioapi/guard.go
 * (the refusals), internal/studioapi/server.go (the routes) and studio/README.md.
 */

/** Which of the two processes — or the browser between them — a step belongs to. */
export type StudioSide = "daemon" | "ui" | "browser";

export type StudioStep = {
  title: string;
  side: StudioSide;
  /** Shell to run. Absent when the step is something to understand, not type. */
  code?: string;
  body: string;
  /** How you know it worked — the line that makes the step checkable. */
  expect?: string;
  /** A sharp edge the step has to lead with rather than mention afterwards. */
  warn?: string;
};

export const STUDIO_SIDES: Record<StudioSide, { label: string; hint: string }> = {
  daemon: {
    label: "daemon",
    hint: "sandbox-studio-api — holds the docker socket, answers HTTP on loopback",
  },
  ui: {
    label: "studio app",
    hint: "the Next app in studio/, served on :3100",
  },
  browser: {
    label: "browser",
    hint: "what you do once both halves are up",
  },
};

export const STUDIO_STEPS: StudioStep[] = [
  {
    title: "Build the daemon — it is a second binary, not a sandbox-cli subcommand",
    side: "daemon",
    code: ["make build-studio-api", "./bin/sandbox-studio-api -h"].join("\n"),
    body:
      "Studio is driven by its own process, sandbox-studio-api, built from cmd/sandbox-studio-api. It is separate from sandbox-cli on purpose: the CLI is a thing you run and watch exit, and this is a thing that stays up holding the docker socket while a browser talks to it. Nothing about the sandbox boundary changes — a run launched from Studio goes through the same BuildSpec as one launched from your shell.",
    expect:
      "A flag list including -addr, -project, -token, -cors-origin and -allow-host. If make is unavailable, `go build -o bin/sandbox-studio-api ./cmd/sandbox-studio-api` is the same thing.",
  },
  {
    title: "Start it against one project, with a token",
    side: "daemon",
    code: [
      "export SANDBOX_STUDIO_TOKEN=$(openssl rand -hex 16)",
      './bin/sandbox-studio-api -project "$PWD" -cors-origin http://localhost:3100',
    ].join("\n"),
    body:
      "-project is the checkout Studio manages; it becomes /workspace for runs launched without a worktree. The token is read from $SANDBOX_STUDIO_TOKEN when -token is not passed, which keeps it out of your shell history and out of the process list. -cors-origin is explained two steps down — pass it now so the browser step works first time.",
    expect:
      "A line naming the address it is listening on. With no token set it says so explicitly: every request but /v1/health would be unauthenticated.",
    warn:
      "Without a token the daemon still starts, and any process on your machine can then ask it to launch a container. It is one flag; set it before you leave this running.",
  },
  {
    title: "Check it is up — /v1/health is the one route that needs no token",
    side: "daemon",
    code: "curl -s http://127.0.0.1:8787/v1/health",
    body:
      "Health is exempt from the bearer token deliberately, so a client can find out whether the server is up before it has a credential to present. Everything else answers 401 without one. Note the /v1 prefix: it is on every route, and a UI pointed at an unprefixed path gets 404 rather than anything descriptive.",
    expect:
      "JSON naming the engine, the project and the profile this instance manages. A 401 here means you are hitting something other than /v1/health.",
  },
  {
    title: "Understand the three refusals before you meet them",
    side: "daemon",
    body:
      "A control plane on 127.0.0.1 is reachable by any web page you happen to have open, so the daemon answers three questions before it answers a request. The Host header must name a loopback address — that is what catches DNS rebinding, where a page on attacker.example resolves its own hostname to 127.0.0.1 and so satisfies the browser's same-origin policy. An unlisted Origin is refused outright rather than merely denied a CORS header, because a cross-origin POST can skip preflight entirely and would otherwise still start a container. And the bearer token governs everything that is not a browser.",
    expect:
      "Nothing yet — this is the step that makes the next two failures legible rather than mysterious.",
  },
  {
    title: "Install and start the UI",
    side: "ui",
    code: ["cd studio", "npm install", "npm run dev"].join("\n"),
    body:
      "Studio is a separate Next app from the landing page you are reading, and deliberately so: this site is a static export with no server, and Studio talks to a local daemon and is designed dark-first. It serves on :3100.",
    expect: "http://localhost:3100 with the runs table, and a badge in the header telling you whether it is reading the live daemon or its bundled fixtures.",
  },
  {
    title: "Let the browser origin through",
    side: "browser",
    code: './bin/sandbox-studio-api -project "$PWD" -cors-origin http://localhost:3100',
    body:
      "The UI is served from :3100 and the daemon listens on :8787, so every call the browser makes is cross-origin. The daemon refuses an Origin it was not told about — this is the check that actually stops a malicious page driving your containers, since refusing to reflect an origin only stops that page reading the reply. Non-browser clients like curl send no Origin at all and are unaffected.",
    expect:
      "The header badge flips from fixture to live. Until it does, you are looking at sample data — Studio never presents a fixture as a real reading.",
    warn:
      "A 403 naming your origin is this check, not a crash. The same applies to the Host header: reach the daemon by a name other than localhost or 127.0.0.1 and you need -allow-host for that name.",
  },
  {
    title: "Point the UI somewhere else, if you moved the daemon",
    side: "ui",
    code: "NEXT_PUBLIC_SANDBOX_API=http://127.0.0.1:9000 npm run dev",
    body:
      "The UI defaults to http://localhost:8787, which is where the daemon listens by default, so you only need this if you changed -addr. Keep the two in step — a UI pointed at a port nothing is serving looks identical to a daemon that failed to start, and the badge is the only thing that tells them apart.",
    expect: "The settings page names the endpoint it is actually calling, so you can confirm it rather than infer it.",
  },
  {
    title: "Launch a run, and read the boundary it got",
    side: "browser",
    body:
      "A run started from Studio takes the same path as one started from your shell: the same resolved config, the same profile, the same egress posture. The run detail shows what that resolved to rather than what was requested, which is the number worth reading — a launch that quietly could not apply a control is exactly what the dry run and the doctor exist to surface on the CLI side.",
    expect: "The new run in the table within a second or two, and its container visible to `sandbox-cli list` in a terminal — one control plane, two front ends.",
  },
  {
    title: "Answer an agent that stopped to ask",
    side: "browser",
    body:
      "A run launched with a console keeps a terminal open on the container, so an agent that asks a question can be answered from the browser. This is the one endpoint that refuses to work without a token even when the rest of the server is unauthenticated: everything else is read-only or launches a container you could have launched anyway, and a keyboard on a session that is already running — holding a workspace, and under dev's defaults an OAuth refresh token in the agent's HOME — is a different kind of reach.",
    expect: "Keystrokes landing in the agent's UI. A 403 mentioning -token means the daemon was started without one.",
    warn:
      "A console run cannot also carry a verify. Verify's exit code is the whole point of it — `fleet land` reads it — and an interactive session's exit code only says when somebody closed the window.",
  },
];
