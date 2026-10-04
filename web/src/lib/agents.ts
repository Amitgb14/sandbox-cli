/**
 * The twelve agents under `sandbox-cli agent`, mirroring internal/agents.
 * Sizes are the on-disk installed sizes measured for arm64 in July 2026.
 *
 * No vendor names: the page describes what each agent needs inside a sandbox,
 * and a company name adds nothing to that.
 *
 * Deliberately absent: the *version* each first-run agent installs. Those are
 * pinned, and the pins live in exactly one place (internal/agents/pins.go) —
 * copying them here would create a second list to keep in step, and a stale
 * version number on a marketing page is worse than no version number. The
 * policy is described in FIRST_RUN_NOTE below and the numbers are not.
 */

export type Agent = {
  /** The subcommand: `sandbox-cli <id>`. */
  id: string;
  name: string;
  /** Baked into the base image, or installed into the sandbox at the start of each run. */
  delivery: "baked" | "first-run";
  /** Approximate installed size for first-run agents. */
  size?: string;
  /** How you authenticate from a sandbox with no browser. */
  login: string;
  /** Host variables forwarded only if they are set. */
  env: string[];
  /** Extra domains needed when the egress allowlist is on. */
  allow?: string[];
  /**
   * A login method this sandbox cannot support, and the one to use instead.
   *
   * Only for flows that complete through a **loopback callback**: the agent
   * starts a server on the guest's own 127.0.0.1 and hands the provider that
   * address as the redirect URI, so your host browser follows the redirect to its
   * *own* loopback and finds nothing. It is stated as unsupported rather than
   * offered a workaround nobody has verified. Every agent listed here offers a device-code or paste flow that
   * needs no callback at all; that is the `instead`.
   */
  unsupportedLogin?: {
    /** The menu entry that cannot work here. */
    method: string;
    /** The menu entry to pick instead. */
    instead: string;
    /** Why the first one cannot work, in one sentence. */
    why: string;
  };
  /** The one thing that will bite you if nobody tells you. */
  gotcha?: string;
  /** A representative invocation. */
  example: string;
};

export const AGENTS: Agent[] = [
  {
    id: "claude",
    name: "Claude Code",
    delivery: "baked",
    login: "Run it and follow the prompt — a Claude account or ANTHROPIC_API_KEY.",
    env: [
      "ANTHROPIC_API_KEY",
      "ANTHROPIC_AUTH_TOKEN",
      "ANTHROPIC_BASE_URL",
      "CLAUDE_CODE_USE_BEDROCK",
      "CLAUDE_CODE_USE_VERTEX",
    ],
    gotcha:
      "Its login files (.claude/.credentials.json and .claude.json) are copied into each sandbox and back out when the run ends; your own ~/.claude is never read or written. --fallback codex starts codex instead when the provider is not answering, probed before a sandbox is made; codex runs with its own login, not a resumed conversation.",
    example: "sandbox-cli agent claude --dangerously-skip-permissions",
  },
  {
    id: "codex",
    name: "Codex CLI",
    delivery: "baked",
    login: "A ChatGPT account, or export OPENAI_API_KEY on the host.",
    env: ["OPENAI_API_KEY", "OPENAI_BASE_URL", "CODEX_HOME"],
    unsupportedLogin: {
      method: "Sign in with ChatGPT",
      instead: "Sign in with Device Code",
      why: "Codex starts a login server on the guest's own loopback and gives the provider that address as the redirect, so your host browser lands on its own 127.0.0.1 and finds nothing. The port is picked at runtime, so nothing can be forwarded to it ahead of time.",
    },
    example: "sandbox-cli agent codex exec 'clone github.com/you/app and run its tests'",
  },
  {
    id: "gemini",
    name: "Gemini CLI",
    delivery: "baked",
    login: "Prints a Google sign-in URL — open it on your host. GEMINI_API_KEY skips it.",
    env: [
      "GEMINI_API_KEY",
      "GOOGLE_API_KEY",
      "GOOGLE_GENAI_USE_VERTEXAI",
      "GOOGLE_CLOUD_PROJECT",
      "GOOGLE_CLOUD_LOCATION",
    ],
    allow: ["generativelanguage.googleapis.com"],
    gotcha:
      "GOOGLE_APPLICATION_CREDENTIALS is deliberately not forwarded — it names a file on your machine, which the sandbox cannot see. Use an API key, or the sign-in URL.",
    example: "sandbox-cli agent gemini --yolo",
  },
  {
    id: "opencode",
    name: "OpenCode",
    delivery: "baked",
    login: "`opencode auth login` inside the sandbox, or forward a provider key.",
    env: [
      "ANTHROPIC_API_KEY",
      "OPENAI_API_KEY",
      "GEMINI_API_KEY",
      "GROQ_API_KEY",
      "OPENROUTER_API_KEY",
      "OPENCODE_CONFIG",
      "OPENCODE_DISABLE_AUTOUPDATE",
    ],
    allow: ["auth.x.ai", "api.x.ai"],
    unsupportedLogin: {
      method: "xAI Grok OAuth (SuperGrok Subscription)",
      instead: "xAI Grok OAuth (Headless / Remote / VPS)",
      why: "The subscription method redirects to a fixed http://127.0.0.1:56121/callback served inside the guest — a URI registered with the provider, so it cannot be repointed — and your host browser follows it to its own loopback instead.",
    },
    example: "sandbox-cli agent opencode run 'clone github.com/you/app and run its tests'",
  },
  {
    id: "kilocode",
    name: "Kilo Code",
    delivery: "first-run",
    size: "372 MB",
    login: "`kilocode auth`, or forward a provider key.",
    env: ["KILOCODE_API_KEY", "ANTHROPIC_API_KEY", "OPENAI_API_KEY", "GEMINI_API_KEY", "GROQ_API_KEY", "OPENROUTER_API_KEY"],
    gotcha:
      "Its CLI is an opencode fork — the same command surface, and the same provider keys. `kilocode run <message>` is its non-interactive mode, unverified here, so Studio cannot launch it unattended and --fallback cannot route to it yet.",
    example: "sandbox-cli agent kilocode run 'clone github.com/you/app and explain it'",
  },
  {
    id: "copilot",
    name: "Copilot CLI",
    delivery: "first-run",
    size: "350 MB",
    login: "`copilot login` prints a device code for github.com — enter it on your host.",
    env: [
      "COPILOT_GITHUB_TOKEN",
      "GH_TOKEN",
      "GITHUB_TOKEN",
      "GH_HOST",
      "COPILOT_MODEL",
      "COPILOT_API_URL",
    ],
    gotcha:
      "Think before forwarding a GitHub PAT: it reaches every repository you can, far beyond what the sandbox needs. Leave it unset and use the device flow. Also the largest first-run download — it looks like a hang, it isn't.",
    example: "sandbox-cli agent copilot -p 'clone github.com/you/app and run its tests'",
  },
  {
    id: "goose",
    name: "Goose",
    delivery: "first-run",
    size: "273 MB",
    login: "`goose configure` — an interactive TUI, no browser involved.",
    env: [
      "ANTHROPIC_API_KEY",
      "OPENAI_API_KEY",
      "GOOGLE_API_KEY",
      "GROQ_API_KEY",
      "OPENROUTER_API_KEY",
      "GOOSE_PROVIDER",
      "GOOSE_MODEL",
      "GOOSE_FAST_MODEL",
      "GOOSE_MODE",
    ],
    gotcha:
      "The sandbox sets GOOSE_DISABLE_KEYRING=1 for you — a sandbox has no OS keyring, so without it the login would not survive. Don't override it.",
    example: "sandbox-cli agent goose run -t 'clone github.com/you/app and run its tests'",
  },
  {
    id: "cursor",
    name: "Cursor CLI",
    delivery: "first-run",
    size: "219 MB",
    login: "`cursor-agent login` prints a URL to open on your host; it polls for the result.",
    env: ["CURSOR_API_KEY", "CURSOR_API_ENDPOINT"],
    allow: ["cursor.com", "downloads.cursor.com"],
    gotcha:
      "The sandbox sets NO_OPEN_BROWSER=1. If it complains about its own sandboxing, pass --sandbox disabled — this VM already provides what that feature exists for.",
    example: "sandbox-cli agent cursor -- --sandbox disabled",
  },
  {
    id: "devin",
    name: "Devin CLI",
    delivery: "first-run",
    size: "158 MB",
    login: "`/login` inside a session, unverified here. It is a paid product; the CLI needs an account.",
    env: ["DEVIN_API_KEY", "DEVIN_API_BASE_URL"],
    allow: ["cli.devin.ai", "static.devin.ai"],
    gotcha:
      "Its headless mode (devin -p PROMPT) and auto-approval (--permission-mode bypass) are documented but unverified here, so Studio cannot launch it unattended and --fallback cannot route to it yet — a descriptor is earned by running the agent, not by reading its docs.",
    example: "sandbox-cli agent devin -p 'clone github.com/you/app and explain it'",
  },
  {
    id: "cline",
    name: "Cline",
    delivery: "first-run",
    size: "130 MB",
    login: "`cline auth --provider anthropic --apikey sk-…`, or forward a key.",
    env: [
      "ANTHROPIC_API_KEY",
      "CLINE_API_KEY",
      "OPENAI_API_KEY",
      "OPENROUTER_API_KEY",
      "AI_GATEWAY_API_KEY",
      "V0_API_KEY",
    ],
    gotcha:
      "With an OAuth provider and no stored credentials it fails with an auth message rather than opening a browser. That's intended, not a crash.",
    example: "sandbox-cli agent cline 'clone github.com/you/app and run its tests'",
  },
  {
    id: "qwen",
    name: "Qwen Code",
    delivery: "first-run",
    size: "88 MB",
    login: "Forward a key, or enter one with /auth inside the agent. Plan on a key.",
    env: [
      "DASHSCOPE_API_KEY",
      "OPENAI_API_KEY",
      "ANTHROPIC_API_KEY",
      "GEMINI_API_KEY",
      "GOOGLE_API_KEY",
      "OPENROUTER_API_KEY",
      "BAILIAN_CODING_PLAN_API_KEY",
      "OPENAI_BASE_URL",
      "ANTHROPIC_BASE_URL",
      "OPENAI_MODEL",
    ],
    allow: ["dashscope-intl.aliyuncs.com"],
    gotcha:
      "The sandbox sets SANDBOX=1 and NO_BROWSER=1. As a Gemini CLI fork it would otherwise try to re-run itself inside a container it starts via docker, which a sandbox does not have, and fail well after startup.",
    example: "DASHSCOPE_API_KEY=… sandbox-cli agent qwen",
  },
  {
    id: "openhands",
    name: "OpenHands CLI",
    delivery: "first-run",
    size: "82 MB",
    login: "`openhands login` is a device-code flow — open the URL on your host.",
    env: [
      "LLM_API_KEY",
      "LLM_MODEL",
      "LLM_BASE_URL",
      "ANTHROPIC_API_KEY",
      "OPENAI_API_KEY",
      "OPENHANDS_CLOUD_URL",
    ],
    allow: ["api.github.com"],
    gotcha:
      "LLM_* only take effect if you also pass --override-with-envs — that's OpenHands' rule, and it's why an exported key can look ignored.",
    example: "sandbox-cli agent openhands -- --override-with-envs",
  },
];

export const BAKED_COUNT = AGENTS.filter((a) => a.delivery === "baked").length;

/**
 * What "installs on first use" actually does, for the agents marked `first-run`.
 * See the note on AGENTS above for why no version numbers appear here.
 */
export const FIRST_RUN_NOTE = {
  line: "sandbox-cli: preparing volume agent-qwen-cfca24e1 for qwen, once: if the image does not carry it, it is installed there for every later run on this endpoint",
  body: "An agent the base image does not carry is installed once per endpoint, into a volume of its own, by a sandbox that does nothing else: no repository, no secrets, no agent running. Every later run mounts that volume read-only, so an agent cannot change what its next run executes. The first install needs network to the package registry, which the default allowlist includes; where an endpoint has no volumes, each run installs the agent itself. The version is pinned rather than resolved to whatever was published that morning, and it is printed as it installs: a pin's cost is going stale, and staleness nobody can see is the kind that lasts.",
  buys: "A hijacked or typosquatted release does not reach a sandbox until the pin is bumped.",
  doesNotBuy:
    "A compromised registry can still serve different bytes for a version it already published — that needs integrity hashes a global install has no lockfile for.",
};

/** The default policy's baseline: permitted with no --allow (internal/policy). */
export const BASELINE_DOMAINS = [
  "api.anthropic.com",
  "api.openai.com",
  "registry.npmjs.org",
  "pypi.org",
  "files.pythonhosted.org",
  "github.com",
  "codeload.github.com",
  "objects.githubusercontent.com",
  "raw.githubusercontent.com",
];
