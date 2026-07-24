/**
 * The fifteen shipped agent adapters.
 * Data mirrors docs/proposals/agent-adapters.md and docs/AGENTS.md.
 */

export interface Agent {
  /** Subcommand: `sandbox-cli <key>` */
  key: string;
  name: string;
  /** Baked into the base image, or installed lazily on first run. */
  baked: boolean;
  install: string;
  /** Host env vars forwarded only when they are actually set. */
  env: readonly string[];
  /** Variables the sandbox sets on the agent's behalf. */
  sets?: readonly string[];
  note: string;
}

export const AGENTS: readonly Agent[] = [
  {
    key: "claude",
    name: "Claude Code",
    baked: true,
    install: "Native installer into the persisted HOME; npm copy baked as an offline fallback.",
    env: ["ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL", "CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX"],
    note: "The only agent with an on-screen CPU/memory gauge, injected through read-only managed settings. Also mounts your Claude history for this one project so host and sandbox sessions resolve together.",
  },
  {
    key: "codex",
    name: "Codex CLI",
    baked: true,
    install: "@openai/codex — baked into the base image.",
    env: ["OPENAI_API_KEY", "OPENAI_BASE_URL", "CODEX_HOME"],
    note: "No status-line hook upstream, so use `sandbox-cli stats` in a second terminal for resource monitoring.",
  },
  {
    key: "gemini",
    name: "Gemini CLI",
    baked: true,
    install: "@google/gemini-cli — baked, with a HOME fallback.",
    env: ["GEMINI_API_KEY", "GOOGLE_API_KEY", "GOOGLE_GENAI_USE_VERTEXAI", "GOOGLE_CLOUD_PROJECT", "GOOGLE_CLOUD_LOCATION"],
    note: "Reads a system settings file that outranks user and project settings — the right place for a sandbox-imposed default, and the Gemini equivalent of Claude's managed settings.",
  },
  {
    key: "opencode",
    name: "OpenCode",
    baked: true,
    install: "opencode-ai — baked, with a HOME fallback.",
    env: ["ANTHROPIC_API_KEY", "OPENAI_API_KEY", "GEMINI_API_KEY", "GROQ_API_KEY", "OPENROUTER_API_KEY", "OPENCODE_CONFIG"],
    note: "Has no status-line hook of any kind upstream, so nothing renders on screen.",
  },
  {
    key: "cline",
    name: "Cline",
    baked: false,
    install: "cline (npm) — installed into the persisted HOME on first run.",
    env: ["ANTHROPIC_API_KEY", "CLINE_API_KEY", "OPENAI_API_KEY", "OPENROUTER_API_KEY", "AI_GATEWAY_API_KEY", "V0_API_KEY"],
    note: "Lazily installed, so it costs the base image nothing until you actually run it.",
  },
  {
    key: "goose",
    name: "Goose",
    baked: false,
    install: "Official installer on first run (needs bzip2, now in the image).",
    env: ["ANTHROPIC_API_KEY", "OPENAI_API_KEY", "GOOGLE_API_KEY", "GROQ_API_KEY", "OPENROUTER_API_KEY", "GOOSE_PROVIDER", "GOOSE_MODEL"],
    sets: ["GOOSE_DISABLE_KEYRING=1"],
    note: "The first adapter that had to set a variable rather than forward one: a container has no Secret Service over DBus, so secrets go to the persisted home instead of the OS keyring.",
  },
  {
    key: "crush",
    name: "Crush",
    baked: false,
    install: "@charmland/crush (npm) — installed on first run.",
    env: ["ANTHROPIC_API_KEY", "OPENAI_API_KEY", "GEMINI_API_KEY", "OPENROUTER_API_KEY", "GROQ_API_KEY", "HYPER_API_KEY"],
    note: "Also forwards AWS and Azure keys when they happen to be set on the host.",
  },
  {
    key: "aider",
    name: "Aider",
    baked: false,
    install: "aider-chat (PyPI) via uv — installed on first run.",
    env: ["OPENAI_API_KEY", "ANTHROPIC_API_KEY", "GEMINI_API_KEY", "DEEPSEEK_API_KEY", "OPENROUTER_API_KEY"],
    note: "The first non-npm adapter. Note it writes into the workspace: a chat history file, a tags cache, and an appended line in the repo's .gitignore.",
  },
  {
    key: "copilot",
    name: "GitHub Copilot CLI",
    baked: false,
    install: "@github/copilot (npm) — installed on first run.",
    env: ["COPILOT_GITHUB_TOKEN", "GH_TOKEN", "GITHUB_TOKEN", "GH_HOST", "COPILOT_MODEL"],
    note: "Forwards your GitHub token only when it is already set in the environment — nothing is read from the host's gh config.",
  },
  {
    key: "cursor",
    name: "Cursor CLI",
    baked: false,
    install: "Vendor installer — runs on first use.",
    env: ["CURSOR_API_KEY", "CURSOR_API_ENDPOINT"],
    sets: ["NO_OPEN_BROWSER=1"],
    note: "Login has to work without a browser, so the sandbox forces the headless path.",
  },
  {
    key: "qwen",
    name: "Qwen Code",
    baked: false,
    install: "@qwen-code/qwen-code (npm) — installed on first run.",
    env: ["OPENAI_API_KEY", "DASHSCOPE_API_KEY", "GEMINI_API_KEY", "OPENROUTER_API_KEY", "BAILIAN_CODING_PLAN_API_KEY"],
    sets: ["SANDBOX=1", "NO_BROWSER=1"],
    note: "Told explicitly that it is already sandboxed, so it does not try to nest another one.",
  },
  {
    key: "amp",
    name: "Amp",
    baked: false,
    install: "@ampcode/cli (npm) — installed on first run.",
    env: ["AMP_API_KEY", "AMP_URL", "AMP_LOG_LEVEL", "AMP_SKIP_UPDATE_CHECK"],
    note: "A straightforward adapter — nothing unusual in its auth flow.",
  },
  {
    key: "continue",
    name: "Continue CLI",
    baked: false,
    install: "@continuedev/cli (npm) — the binary is `cn`.",
    env: ["ANTHROPIC_API_KEY", "CONTINUE_API_BASE", "GOOGLE_CLOUD_PROJECT"],
    note: "You type `sandbox-cli continue`; the wrapper knows the executable is actually called cn.",
  },
  {
    key: "openhands",
    name: "OpenHands CLI",
    baked: false,
    install: "Standalone binary from GitHub releases — fetched on first run.",
    env: ["LLM_API_KEY", "LLM_MODEL", "LLM_BASE_URL", "ANTHROPIC_API_KEY", "OPENAI_API_KEY"],
    note: "The LLM_* variables need --override-with-envs before OpenHands will honour them.",
  },
  {
    key: "droid",
    name: "Droid",
    baked: false,
    install: "droid (npm) — installed on first run.",
    env: ["FACTORY_API_KEY", "FACTORY_API_BASE_URL", "FACTORY_APP_BASE_URL", "FACTORY_ENV"],
    sets: ["FACTORY_DISABLE_KEYRING=1"],
    note: "Disables the keyring for the same reason Goose does — no Secret Service inside a container.",
  },
];

export const AGENT_BY_KEY: Record<string, Agent> = Object.fromEntries(
  AGENTS.map((a) => [a.key, a]),
);
