import type { AgentDelivery, AgentName, Profile } from "@/lib/types";

/**
 * Where the daemon is, resolved per call rather than frozen into the bundle.
 *
 * `NEXT_PUBLIC_*` is inlined by Next at **build** time, which is fine for a
 * developer running `npm run dev` and wrong for a *published image*: the URL
 * would be decided when the image was built, on a machine that could not know
 * which port this user's daemon ended up on. So the published image is told at
 * `docker run` time — `SANDBOX_API_URL` — and the root layout emits that value
 * as `window.__SANDBOX_API__` before the bundle loads.
 *
 * Three sources, in this order, mirroring how `apiToken` resolves:
 *
 *   window.__SANDBOX_API__   injected per request from the server's environment.
 *                            Wins, because it is the one that can change without
 *                            a rebuild.
 *   NEXT_PUBLIC_SANDBOX_API  baked in at build time — `npm run dev`, and any
 *                            deployment that builds its own bundle.
 *   http://localhost:8787    the daemon's own default address.
 *
 * Server and browser agree on the answer because both read the same variable:
 * the server reads `SANDBOX_API_URL` directly, and what the browser reads is
 * that value, rendered into the document. A mismatch would surface as a
 * hydration error on the one screen that prints the endpoint.
 */
declare global {
  interface Window {
    __SANDBOX_API__?: string;
    /** `SANDBOX_STUDIO_TOKEN`, injected the same way. See `apiToken`. */
    __SANDBOX_TOKEN__?: string;
  }
}

export function apiBase(): string {
  if (typeof window !== "undefined" && window.__SANDBOX_API__) {
    return window.__SANDBOX_API__;
  }
  if (typeof process !== "undefined" && process.env.SANDBOX_API_URL) {
    return process.env.SANDBOX_API_URL;
  }
  return process.env.NEXT_PUBLIC_SANDBOX_API ?? "http://localhost:8787";
}

/**
 * `config.baselineEgress` — the always-permitted domain set in allowlist mode.
 * Kept deliberately small and auditable, and mirrored here in the same order so
 * the Launch screen can show which of the domains on screen the user chose and
 * which were already there.
 */
export const BASELINE_EGRESS = [
  "api.anthropic.com",
  "api.openai.com",
  "registry.npmjs.org",
  "pypi.org",
  "files.pythonhosted.org",
  "github.com",
  "codeload.github.com",
  "objects.githubusercontent.com",
  "raw.githubusercontent.com",
] as const;

/**
 * `config.reservedEnvNames`. These are instructions, not settings — they cannot
 * be set or forwarded from outside, so the Launch form refuses them in the
 * env-allow field rather than letting the daemon be the first to say no.
 *
 * An exact-name list, not a `SANDBOX_*` prefix: `SANDBOX_STATUSLINE_*` is a
 * documented user knob read *after* the privilege drop.
 */
export const RESERVED_ENV = new Set([
  "SANDBOX_RUN_AS",
  "SANDBOX_EGRESS_ALLOW",
  "SANDBOX_INGRESS_PORTS",
  "SANDBOX_PROXY_PORT",
  "BASH_ENV",
  "ENV",
  "LD_PRELOAD",
  "LD_AUDIT",
  "LD_LIBRARY_PATH",
  "SHELLOPTS",
  "BASHOPTS",
  "PS4",
  "IFS",
  "GLOBIGNORE",
  "DOCKER_HOST",
  "DOCKER_CONFIG",
  "DOCKER_CERT_PATH",
  "DOCKER_TLS_VERIFY",
  "DOCKER_CONTEXT",
]);

export const PROFILES: Record<
  Profile,
  { label: string; blurb: string; unsatisfied: "warns" | "refuses" }
> = {
  dev: {
    label: "dev",
    blurb:
      "Local development, where a prompt-injected agent has the most valuable thing in reach. Egress defaults to the baseline allowlist.",
    unsatisfied: "warns",
  },
  prod: {
    label: "prod",
    blurb:
      "Unattended runs. Persisted auth is not mounted, so there is no refresh token to steal, and the baseline is off — `allow` is the whole list.",
    unsatisfied: "refuses",
  },
};

interface AgentSeed {
  name: AgentName;
  label: string;
  delivery: AgentDelivery;
  headlessVerified: boolean;
  envAllow: string[];
  env: string[];
  statusLine?: boolean;
  historySync?: boolean;
  /**
   * The flag that turns this agent's approval prompts off, where it has one.
   * Mirrors `Descriptor.SkipPermissionArgs`; the daemon sends the real value on
   * `GET /v1/agents`, so this is only what the fixtures answer offline.
   */
  skipPermissionArgs?: string[];
  note: string;
}

/**
 * The fifteen adapters in `cli.agentCmds()`, in that order — newest-supported
 * last. `headlessVerified` is the gate that matters: only those five may be
 * named in a `fleet.yaml`, because a fleet is unattended.
 */
export const AGENT_SEEDS: AgentSeed[] = [
  {
    name: "claude",
    label: "Claude Code",
    delivery: "installer",
    headlessVerified: true,
    envAllow: ["ANTHROPIC_API_KEY", "ANTHROPIC_BASE_URL", "ANTHROPIC_MODEL"],
    env: [],
    skipPermissionArgs: ["--dangerously-skip-permissions"],
    statusLine: true,
    historySync: true,
    note: "The only agent with a status line, and the only one whose host history bucket is mounted. Self-updating install in the persisted HOME; the baked npm copy is the offline fallback.",
  },
  {
    name: "codex",
    label: "OpenAI Codex",
    delivery: "baked",
    headlessVerified: true,
    envAllow: ["OPENAI_API_KEY", "OPENAI_BASE_URL"],
    env: [],
    note: "Baked into the base image. Headless via `exec --full-auto`.",
  },
  {
    name: "gemini",
    label: "Gemini CLI",
    delivery: "baked",
    headlessVerified: true,
    envAllow: ["GEMINI_API_KEY", "GOOGLE_API_KEY"],
    env: [],
    skipPermissionArgs: ["--yolo"],
    note: "No status-line hook upstream, so nothing is drawn on screen. Use Studio's metrics instead.",
  },
  {
    name: "opencode",
    label: "OpenCode",
    delivery: "baked",
    headlessVerified: true,
    envAllow: ["OPENAI_API_KEY", "ANTHROPIC_API_KEY", "OPENROUTER_API_KEY"],
    env: [],
    note: "Baked. No status-line hook upstream.",
  },
  {
    name: "cline",
    label: "Cline",
    delivery: "npm",
    headlessVerified: false,
    envAllow: ["ANTHROPIC_API_KEY", "OPENROUTER_API_KEY"],
    env: [],
    note: "Installed lazily into the persisted HOME on first run.",
  },
  {
    name: "goose",
    label: "Goose",
    delivery: "installer",
    headlessVerified: false,
    envAllow: ["ANTHROPIC_API_KEY", "OPENAI_API_KEY", "GOOSE_PROVIDER"],
    env: [],
    note: "Block's agent, installed by its own script on first run.",
  },
  {
    name: "crush",
    label: "Crush",
    delivery: "npm",
    headlessVerified: false,
    envAllow: ["ANTHROPIC_API_KEY", "OPENAI_API_KEY"],
    env: [],
    note: "Charm's agent. Lazily installed.",
  },
  {
    name: "aider",
    label: "Aider",
    delivery: "pip",
    headlessVerified: false,
    envAllow: ["OPENAI_API_KEY", "ANTHROPIC_API_KEY", "AIDER_MODEL"],
    env: [],
    note: "Python; needs pypi.org and files.pythonhosted.org, both in the baseline.",
  },
  {
    name: "copilot",
    label: "Copilot CLI",
    delivery: "npm",
    headlessVerified: false,
    envAllow: ["GITHUB_TOKEN", "GH_TOKEN"],
    env: [],
    note: "Authenticates against github.com, which is in the baseline — and is a write endpoint.",
  },
  {
    name: "cursor",
    label: "Cursor CLI",
    delivery: "installer",
    headlessVerified: false,
    envAllow: ["CURSOR_API_KEY"],
    env: [],
    note: "Installed by its own script on first run.",
  },
  {
    name: "qwen",
    label: "Qwen Code",
    delivery: "npm",
    headlessVerified: false,
    envAllow: ["DASHSCOPE_API_KEY", "OPENAI_API_KEY"],
    env: [],
    note: "A Gemini-CLI fork; same absence of a status-line hook.",
  },
  {
    name: "amp",
    label: "Amp",
    delivery: "npm",
    headlessVerified: false,
    envAllow: ["AMP_API_KEY"],
    env: [],
    note: "Sourcegraph's agent. Lazily installed.",
  },
  {
    name: "continue",
    label: "Continue CLI",
    delivery: "npm",
    headlessVerified: false,
    envAllow: ["CONTINUE_API_KEY", "ANTHROPIC_API_KEY"],
    env: [],
    note: "Lazily installed.",
  },
  {
    name: "openhands",
    label: "OpenHands",
    delivery: "pip",
    headlessVerified: false,
    envAllow: ["LLM_API_KEY", "LLM_MODEL"],
    env: [],
    note: "Python; the heaviest adapter, which is why nothing is baked.",
  },
  {
    name: "droid",
    label: "Droid",
    delivery: "npm",
    headlessVerified: true,
    envAllow: ["FACTORY_API_KEY"],
    env: ["FACTORY_DISABLE_KEYRING=1"],
    note: "The keyring opt-out lives in the descriptor, not the wrapper — a fleet gets no wrapper, and would otherwise look for a keyring the container does not have with nobody there to log in again.",
  },
];

/** Agents eligible for a `fleet.yaml`, in descriptor order. */
export const FLEET_AGENTS = AGENT_SEEDS.filter((a) => a.headlessVerified).map(
  (a) => a.name,
);

export const DELIVERY_LABEL: Record<AgentDelivery, string> = {
  baked: "Baked into image",
  npm: "npm, on first run",
  installer: "Vendor installer, on first run",
  pip: "pip, on first run",
};
