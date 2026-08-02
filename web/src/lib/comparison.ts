/**
 * The landscape table from README.md ("Alternatives & prior art"), typed so the
 * page can render it and score it consistently.
 *
 * This is the project's own read of the landscape and the ratings for other
 * tools are a snapshot that will age — the page says so, and so does the README.
 */

export type Tone = "strong" | "ok" | "weak" | "none" | "neutral";

export type Cell = { text: string; tone: Tone };

export type Column = {
  id: string;
  name: string;
  sub: string;
  /** The one column that sits inside the boundary this page is about. */
  highlight?: boolean;
};

export const COLUMNS: Column[] = [
  { id: "sandbox", name: "sandbox-cli", sub: "this project", highlight: true },
  { id: "builtin", name: "Built-in agent sandboxes", sub: "Claude / Codex" },
  { id: "sbx", name: "Docker Sandboxes", sub: "sbx" },
  { id: "os", name: "Native OS tools", sub: "Seatbelt / Landlock" },
  { id: "cloud", name: "Cloud microVMs", sub: "E2B, Daytona, …" },
];

export type Row = {
  label: string;
  /** Short plain-language gloss shown under the label. */
  note?: string;
  cells: Record<string, Cell>;
};

const s = (text: string): Cell => ({ text, tone: "strong" });
const o = (text: string): Cell => ({ text, tone: "ok" });
const w = (text: string): Cell => ({ text, tone: "weak" });
const n = (text: string): Cell => ({ text, tone: "none" });
const x = (text: string): Cell => ({ text, tone: "neutral" });

export const ROWS: Row[] = [
  {
    label: "Isolation strength",
    note: "How hard the wall actually is",
    cells: {
      sandbox: o("Good — Docker + hardening, optional gVisor/Kata"),
      builtin: x("Medium — OS-level, shared kernel"),
      sbx: s("Excellent — microVM / Firecracker"),
      os: o("Good — kernel primitives"),
      cloud: s("Excellent — microVMs"),
    },
  },
  {
    label: "Local, no cloud",
    note: "Your code never leaves the machine",
    cells: {
      sandbox: s("Yes"),
      builtin: s("Yes"),
      sbx: s("Yes"),
      os: s("Yes"),
      cloud: n("No"),
    },
  },
  {
    label: "Persistent agent auth",
    note: "Log in once, not every run",
    cells: {
      sandbox: s("Excellent — dedicated persistent home"),
      builtin: x("Varies"),
      sbx: o("Good"),
      os: x("Varies"),
      cloud: x("Varies"),
    },
  },
  {
    label: "Package cache persistence",
    note: "No cold npm install every run",
    cells: {
      sandbox: s("Yes — --cache volumes"),
      builtin: w("Limited"),
      sbx: o("Good"),
      os: w("Manual"),
      cloud: o("Often built-in"),
    },
  },
  {
    label: "Parallel agents (worktrees)",
    note: "Several branches at once, no collisions",
    cells: {
      sandbox: s("Excellent — built-in --worktree"),
      builtin: w("Poor"),
      sbx: o("Good"),
      os: w("Poor"),
      cloud: x("Varies"),
    },
  },
  {
    label: "Credential broker",
    note: "Secrets off the argv and out of history",
    // This row used to read "Excellent — file / cmd / env sources", and that was
    // an overclaim. internal/creds resolves secret *references* so values stay
    // off the argv and out of config files — real, but the value still lands in
    // the container's environment where the agent can read it with printenv. A
    // broker that terminates TLS and injects the credential is open security
    // item 2 and is not built. Prod's blunter answer is the honest one to sell.
    cells: {
      sandbox: w("Basic — references resolved; prod mounts no token at all"),
      builtin: w("Basic"),
      sbx: o("Good — proxy"),
      os: x("Varies"),
      cloud: o("Good"),
    },
  },
  {
    label: "Egress / network control",
    note: "Stop exfiltration, keep installs working",
    cells: {
      sandbox: s("Strong — allowlist with baselines"),
      builtin: w("Basic"),
      sbx: s("Strong"),
      os: x("Varies"),
      cloud: s("Strong"),
    },
  },
  {
    label: "Observability / metrics",
    note: "What is this thing actually doing",
    // Downgraded from "Excellent" for the same reason as the row above: a live
    // gauge, stats and one line per run are real, but there is no per-command
    // trace and no replay. That is roadmap task 4, not a shipped capability.
    cells: {
      sandbox: o("Good — live gauge, stats, per-run log; no per-command trace"),
      builtin: w("Limited"),
      sbx: o("Good"),
      os: w("Poor"),
      cloud: x("Varies"),
    },
  },
  {
    label: "Project config",
    note: "Per-repo policy, checked in",
    cells: {
      sandbox: s("Excellent — .sandbox.yaml"),
      builtin: w("Limited"),
      sbx: o("Good"),
      os: w("Poor"),
      cloud: x("API / config"),
    },
  },
  {
    label: "Dry-run / preview",
    note: "Read the boundary before trusting it",
    cells: {
      sandbox: s("Yes"),
      builtin: n("No"),
      sbx: x("Varies"),
      os: n("No"),
      cloud: x("Varies"),
    },
  },
  {
    label: "Ease of use",
    cells: {
      sandbox: s("High — CLI-focused, thorough docs"),
      builtin: s("High"),
      sbx: s("High"),
      os: x("Medium"),
      cloud: x("Medium — setup"),
    },
  },
  {
    label: "Cross-platform",
    cells: {
      sandbox: o("Good — macOS / Linux / Windows"),
      builtin: o("Good"),
      sbx: s("Excellent"),
      os: w("Platform-specific"),
      cloud: x("N/A"),
    },
  },
  {
    label: "Docker dependency",
    cells: {
      sandbox: x("Yes"),
      builtin: x("No"),
      sbx: x("Yes"),
      os: x("No"),
      cloud: x("No"),
    },
  },
  {
    label: "Best for",
    cells: {
      sandbox: x("Local multi-agent workflows, ergonomics"),
      builtin: x("Quick minimal protection"),
      sbx: x("Strongest local isolation"),
      os: x("Lightweight, zero deps"),
      cloud: x("Scale & long-running tasks"),
    },
  },
];

/**
 * The same judgement as numbers. Axes are all "higher is better", scored 0–5
 * straight from the table above, so the chart and the table cannot disagree.
 */
export const SCORE_AXES = [
  "Isolation",
  "Ergonomics",
  "Parallelism",
  "Credential hygiene",
  "Observability",
  "Stays local",
] as const;

export type ScoreSeries = {
  id: string;
  name: string;
  values: Record<(typeof SCORE_AXES)[number], number>;
};

export const SCORES: ScoreSeries[] = [
  {
    id: "sandbox",
    name: "sandbox-cli",
    values: {
      Isolation: 3.5,
      Ergonomics: 5,
      Parallelism: 5,
      // Both of these came down with their rows above, because the chart is not
      // allowed to say something the table does not. "Basic" scores 2 elsewhere
      // in this chart; credential hygiene sits half a point above that only
      // because prod does not mount the refresh token, which is a real
      // mitigation rather than a broker.
      "Credential hygiene": 2.5,
      Observability: 3.5,
      "Stays local": 5,
    },
  },
  {
    id: "builtin",
    name: "Built-in agent sandboxes",
    values: {
      Isolation: 2.5,
      Ergonomics: 5,
      Parallelism: 1.5,
      "Credential hygiene": 2,
      Observability: 1.5,
      "Stays local": 5,
    },
  },
  {
    id: "cloud",
    name: "Cloud microVMs",
    values: {
      Isolation: 5,
      Ergonomics: 3,
      Parallelism: 4,
      "Credential hygiene": 4,
      Observability: 3,
      "Stays local": 0,
    },
  },
];

/** README's platform matrix, trimmed to the rows that actually differ. */
export const PLATFORMS = [
  {
    capability: "run, agent wrappers, mounts, env, hardening, metrics",
    macos: "yes",
    linux: "yes",
    windows: "yes",
  },
  {
    capability: "--cache, --secret, --worktree, --git, --share",
    macos: "yes",
    linux: "yes",
    windows: "yes",
  },
  {
    capability: "Egress allowlist (--allow)",
    macos: "partial",
    linux: "yes",
    windows: "partial",
    footnote:
      "The firewall runs iptables inside the Linux container, so it works wherever the container kernel is Linux. Verified in CI on native Linux; not yet independently verified on Docker Desktop.",
  },
  {
    capability: "--host-gateway",
    macos: "auto",
    linux: "needed",
    windows: "auto",
    footnote: "host.docker.internal resolves automatically on Docker Desktop; native Linux needs the flag.",
  },
  {
    capability: "/workspace file ownership",
    macos: "virtualized to you",
    linux: "container uid",
    windows: "virtualized to you",
    footnote: "On native Linux, use --user \"$(id -u):$(id -g)\" if ownership matters.",
  },
  {
    capability: "--runtime kata-runtime / runsc",
    macos: "no",
    linux: "yes",
    windows: "no",
    footnote:
      "Docker Desktop runs containers in its own managed Linux VM and won't let you register custom OCI runtimes — you already get a VM boundary from Docker Desktop itself.",
  },
] as const;
