/**
 * The fifteen agent wrappers, mirroring `agentCmds()` in internal/cli and the
 * per-agent sections of docs/AGENTS.md. Sizes are the on-disk installed sizes
 * measured for arm64 in July 2026 (see docs/AGENTS.md for the caveats).
 */

export type Agent = {
  /** The subcommand: `sandbox-cli <id>`. */
  id: string;
  name: string;
  vendor: string;
  /** Baked into the base image, or installed into the persisted home on first use. */
  delivery: "baked" | "first-run";
  /** Approximate installed size for first-run agents. */
  size?: string;
  /** How you authenticate from a container with no browser. */
  login: string;
  /** Host variables forwarded only if they are set. */
  env: string[];
  /** Extra domains needed when the egress allowlist is on. */
  allow?: string[];
  /** The one thing that will bite you if nobody tells you. */
  gotcha?: string;
  /** A representative invocation. */
  example: string;
};

export const AGENTS: Agent[] = [
  {
    id: "claude",
    name: "Claude Code",
    vendor: "Anthropic",
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
      "The only wrapper with a live memory/CPU status line in the agent's own UI, and the only one that shares your host conversation history for this project so --resume works on both sides.",
    example: "sandbox-cli claude --dangerously-skip-permissions",
  },
  {
    id: "codex",
    name: "Codex CLI",
    vendor: "OpenAI",
    delivery: "baked",
    login: "A ChatGPT account, or export OPENAI_API_KEY on the host.",
    env: ["OPENAI_API_KEY", "OPENAI_BASE_URL", "CODEX_HOME"],
    example: "sandbox-cli codex exec 'run the tests'",
  },
  {
    id: "gemini",
    name: "Gemini CLI",
    vendor: "Google",
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
      "GOOGLE_APPLICATION_CREDENTIALS is deliberately not forwarded — it names a host file that isn't mounted. Mount the file and repoint the variable.",
    example: "sandbox-cli gemini --yolo",
  },
  {
    id: "opencode",
    name: "OpenCode",
    vendor: "SST",
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
    example: "sandbox-cli opencode run 'run the tests'",
  },
  {
    id: "copilot",
    name: "Copilot CLI",
    vendor: "GitHub",
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
      "Think before forwarding a GitHub PAT: it reaches every repository you can, far beyond the workspace. Leave it unset and use the device flow. Also the largest first-run download — it looks like a hang, it isn't.",
    example: "sandbox-cli copilot -p 'run the tests'",
  },
  {
    id: "goose",
    name: "Goose",
    vendor: "Block",
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
      "The sandbox sets GOOSE_DISABLE_KEYRING=1 for you — a container has no OS keyring, so without it the login would not survive. Don't override it.",
    example: "sandbox-cli goose run -t 'run the tests'",
  },
  {
    id: "cursor",
    name: "Cursor CLI",
    vendor: "Anysphere",
    delivery: "first-run",
    size: "219 MB",
    login: "`cursor-agent login` prints a URL to open on your host; it polls for the result.",
    env: ["CURSOR_API_KEY", "CURSOR_API_ENDPOINT"],
    allow: ["cursor.com", "downloads.cursor.com"],
    gotcha:
      "The sandbox sets NO_OPEN_BROWSER=1. If it complains about its own sandboxing, pass --sandbox disabled — this container already provides what that feature exists for.",
    example: "sandbox-cli cursor -- --sandbox disabled",
  },
  {
    id: "droid",
    name: "Droid",
    vendor: "Factory",
    delivery: "first-run",
    size: "148 MB",
    login: "Device-code flow — code and URL printed, opened on your host.",
    env: [
      "FACTORY_API_KEY",
      "FACTORY_API_BASE_URL",
      "FACTORY_APP_BASE_URL",
      "FACTORY_AIRGAP_ENABLED",
      "FACTORY_ENV",
    ],
    gotcha:
      "The sandbox sets FACTORY_DISABLE_KEYRING=1 so credentials stay in a file in the persisted home even if the upstream default changes.",
    example: "sandbox-cli droid exec 'run the tests'",
  },
  {
    id: "cline",
    name: "Cline",
    vendor: "Cline",
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
    example: "sandbox-cli cline task 'run the tests'",
  },
  {
    id: "amp",
    name: "Amp",
    vendor: "Sourcegraph",
    delivery: "first-run",
    size: "107 MB",
    login: "`amp login` prints a URL for your host and takes the code back in the terminal.",
    env: ["AMP_API_KEY", "AMP_URL", "AMP_LOG_LEVEL", "AMP_SKIP_UPDATE_CHECK"],
    gotcha:
      "Leave the native-keyring setting off. Turning it on migrates the token into a keyring and deletes the file — in a container that trades a working login for none.",
    example: "sandbox-cli amp -x 'run the tests'",
  },
  {
    id: "qwen",
    name: "Qwen Code",
    vendor: "Alibaba",
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
      "The sandbox sets SANDBOX=1 and NO_BROWSER=1. As a Gemini CLI fork it would otherwise try to re-run itself inside a container it starts via docker, and fail well after startup.",
    example: "DASHSCOPE_API_KEY=… sandbox-cli qwen",
  },
  {
    id: "openhands",
    name: "OpenHands CLI",
    vendor: "All Hands AI",
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
    example: "sandbox-cli openhands -- --override-with-envs",
  },
  {
    id: "crush",
    name: "Crush",
    vendor: "Charm",
    delivery: "first-run",
    size: "81 MB",
    login: "`crush login` shows a short code — open the page on your host and paste it.",
    env: [
      "ANTHROPIC_API_KEY",
      "OPENAI_API_KEY",
      "GEMINI_API_KEY",
      "OPENROUTER_API_KEY",
      "GROQ_API_KEY",
      "HYPER_API_KEY",
    ],
    gotcha:
      "Crush speaks to roughly 25 providers; add any other key with --env-allow NAME rather than editing the wrapper.",
    example: "sandbox-cli crush --env-allow CEREBRAS_API_KEY",
  },
  {
    id: "continue",
    name: "Continue CLI",
    vendor: "Continue",
    delivery: "first-run",
    size: "65 MB",
    login: "None. Hub auth was removed upstream — the key is written into the agent home.",
    env: ["ANTHROPIC_API_KEY", "CONTINUE_API_BASE", "GOOGLE_CLOUD_PROJECT"],
    allow: ["api.continue.dev"],
    gotcha:
      "`cn login` and CONTINUE_API_KEY in the published docs are stale and do nothing. With --allow, permit api.continue.dev or it has no config to start from.",
    example: "sandbox-cli continue --allow api.continue.dev",
  },
  {
    id: "aider",
    name: "Aider",
    vendor: "Aider AI",
    delivery: "first-run",
    size: "~300 MB",
    login: "None at all — export a provider key on your host.",
    env: [
      "OPENAI_API_KEY",
      "ANTHROPIC_API_KEY",
      "GEMINI_API_KEY",
      "DEEPSEEK_API_KEY",
      "OPENROUTER_API_KEY",
      "OPENAI_API_BASE",
      "ANTHROPIC_API_BASE",
    ],
    allow: ["astral.sh"],
    gotcha:
      "The workspace must be a git repo, and Aider writes into your project: a chat history file, a tags cache, and an appended `.aider*` line in .gitignore. Pass --no-gitignore to stop the last one.",
    example: "OPENAI_API_KEY=… sandbox-cli aider",
  },
];

export const BAKED_COUNT = AGENTS.filter((a) => a.delivery === "baked").length;

/** Always permitted when the egress allowlist is on (docs/AGENTS.md). */
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
