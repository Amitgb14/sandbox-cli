/**
 * Every capability the CLI actually ships, grouped the way a developer
 * evaluating it would ask about them. Mirrors README.md's flag table and the
 * security model section.
 */

export type FeatureGroup =
  | "boundary"
  | "workflow"
  | "credentials"
  | "network"
  | "observability";

export type Feature = {
  title: string;
  group: FeatureGroup;
  /** The flag or config key that turns it on, if there is one. */
  flag?: string;
  body: string;
  /** Optional terminal-voice detail rendered under the body. */
  code?: string;
  /** Default-on, opt-in, or opt-out. */
  state: "default" | "opt-in" | "opt-out";
};

export const GROUP_LABEL: Record<FeatureGroup, string> = {
  boundary: "The boundary",
  credentials: "Credentials",
  network: "Network",
  workflow: "Workflow",
  observability: "Observability",
};

export const FEATURES: Feature[] = [
  {
    title: "One host path, mounted on purpose",
    group: "boundary",
    body: "The project you chose is bind-mounted at /workspace and nothing else is host-connected. HOME, /etc and / inside the container are ephemeral and destroyed on exit.",
    code: "~/projects/app  ->  /workspace     (the only host-connected path)",
    state: "default",
  },
  {
    title: "Refusals you cannot configure away",
    group: "boundary",
    body: "sandbox-cli refuses to mount /, your home directory, or any ancestor of it as the workspace. That check lives in ResolveWorkspace and no flag, config file or env var overrides it.",
    state: "default",
  },
  {
    title: "One pure function builds the argv",
    group: "boundary",
    flag: "--dry-run",
    body: "Every isolation decision funnels through runtime.BuildArgs — deterministic, no I/O, exhaustively unit-tested against a golden output. --dry-run prints the exact docker command and exits, so you can read the boundary before you trust it.",
    code: "sandbox-cli run --dry-run -- npm test",
    state: "default",
  },
  {
    title: "Hardened container by default",
    group: "boundary",
    flag: "--no-hardening",
    body: "Every run drops all Linux capabilities, forbids privilege escalation, and caps the process count to blunt fork bombs. Non-root by default, which is also why agents accept --dangerously-skip-permissions in here.",
    code: "--cap-drop ALL  --security-opt no-new-privileges  --pids-limit 1024",
    state: "default",
  },
  {
    title: "Stronger isolation on request",
    group: "boundary",
    flag: "--runtime",
    body: "Point a run at any OCI runtime the daemon knows: kata-runtime for a microVM with its own kernel, runsc for gVisor's userspace syscall filter. Mounts, hardening, allowlist, caches and secrets all work unchanged on top.",
    code: "sandbox-cli claude --runtime kata-runtime",
    state: "opt-in",
  },
  {
    title: "Default-deny environment",
    group: "credentials",
    flag: "--env-allow",
    body: "Nothing from your host environment crosses the boundary unless you name it. Each agent wrapper ships a small suggested allowlist — ANTHROPIC_API_KEY and friends — applied only if the value is actually set.",
    state: "default",
  },
  {
    title: "Credential broker",
    group: "credentials",
    flag: "--secret",
    body: "Resolve a secret at run time from a file, a host command, or a host env var, and forward it by name. The raw value never appears on the docker argv, in --dry-run output, in config, or in your shell history — and cmd: sources can be short-lived tokens fetched fresh each run.",
    code: "--secret GITHUB_TOKEN=cmd:'gh auth token'",
    state: "opt-in",
  },
  {
    title: "Logins that survive --rm",
    group: "credentials",
    flag: "--no-persist-auth",
    body: "Each agent gets its own sandbox-owned directory bind-mounted as the container's whole HOME, so you authenticate once. It is separate from your real ~/.claude — the sandbox never reads or writes your host agent config.",
    code: "~/.config/sandbox/agents/claude  ->  /sandbox/home",
    state: "opt-out",
  },
  {
    title: "Egress allowlist",
    group: "network",
    flag: "--allow",
    body: "Switch outbound traffic to default-deny with an in-container firewall. DNS, established flows and a baseline of agent APIs and package registries stay open, so npm install and git keep working while arbitrary exfiltration from a prompt-injected agent does not.",
    code: "sandbox-cli claude --allow internal.registry.example.com",
    state: "opt-in",
  },
  {
    title: "Fails closed",
    group: "network",
    body: "The firewall is programmed at startup with NET_ADMIN — added only in this mode — and the run then drops back to the non-root sandbox user. If setup errors, the run fails rather than silently continuing wide open.",
    state: "default",
  },
  {
    title: "Publish a port when you want to look",
    group: "network",
    flag: "--publish",
    body: "No container port is reachable from the host until you ask. When you do, a spec that names no address binds to 127.0.0.1 rather than every interface \u2014 the one place sandbox-cli deliberately differs from docker -p. Put your dev-server ports in .sandbox.yaml and stop typing them.",
    code: "sandbox-cli run -P 3000 -- npm run dev",
    state: "opt-in",
  },
  {
    title: "Reach host services deliberately",
    group: "network",
    flag: "--host-gateway",
    body: "An agent can talk to an MCP server on your machine through host.docker.internal. That resolves automatically on Docker Desktop; on Linux this flag maps it, and --add-host handles anything else.",
    state: "opt-in",
  },
  {
    title: "Parallel agents on real git worktrees",
    group: "workflow",
    flag: "--worktree",
    body: "Run several agents at once, each on its own branch, in its own container, with no collisions. The worktree lives in a sandbox-owned directory so your checkout stays clean, and the branch shows up in your repo immediately.",
    code: "sandbox-cli claude --worktree feature-a -- -p 'implement A'",
    state: "opt-in",
  },
  {
    title: "Addressable by branch, never by cd",
    group: "workflow",
    body: "worktree list, path, git, commit and rm all take the branch name. worktree git forwards everything after it straight to git — output and exit code included — so your config, hooks and commit signing still apply.",
    code: "sandbox-cli worktree commit feature-a -m 'implement A'",
    state: "opt-in",
  },
  {
    title: "Commits attributed to you",
    group: "workflow",
    flag: "--git",
    body: "Forwards your host user.name and user.email and marks the workspace trusted, so git in the container stops complaining about dubious ownership and the agent's commits carry your name.",
    state: "opt-in",
  },
  {
    title: "A channel between sandboxes",
    group: "workflow",
    flag: "--share",
    body: "Two sandboxes cannot see each other — that is the point, but it leaves no way to hand over an API contract. --share mounts one host directory at /shared in every sandbox that asks for it. Then just say so in the prompt.",
    state: "opt-in",
  },
  {
    title: "Pasted image paths that resolve",
    group: "workflow",
    flag: "--paste",
    body: "Your terminal pastes an image as an absolute host path, which names nothing inside a container. This mounts ~/Desktop, ~/Downloads and ~/Pictures read-only at their own host paths so the path resolves. Opt-in, because it widens what the agent can read.",
    state: "opt-in",
  },
  {
    title: "Package caches that persist",
    group: "workflow",
    flag: "--cache",
    body: "Containers are --rm, so a cold npm install every run gets old fast. --cache keeps npm, pip, cargo and go caches in named Docker volumes across runs — no host directory involved.",
    state: "opt-in",
  },
  {
    title: "Layered project config",
    group: "workflow",
    body: "Built-in defaults, then ~/.config/sandbox/config.yaml, then the nearest .sandbox.yaml walking up from cwd, then flags. sandbox-cli config show prints what actually won.",
    code: "sandbox-cli init      # scaffold .sandbox.yaml",
    state: "default",
  },
  {
    title: "Live resource gauge",
    group: "observability",
    flag: "--no-metrics",
    body: "Non-interactive runs pin a memory/CPU/elapsed gauge to the bottom of the terminal with the workspace's git branch at the right — the thing that tells parallel worktree sandboxes apart at a glance. Measurement only; no limits are imposed.",
    code: "sandbox-cli │ mem 512MiB/7.6GiB [▓░░░░░░] cpu 82% · 0m47s   git:feature/login",
    state: "opt-out",
  },
  {
    title: "A status line inside Claude",
    group: "observability",
    flag: "--no-statusline",
    body: "Claude Code renders the container's live memory and CPU in its own UI, plus the model answering and how much of your 5-hour and weekly windows is left — both from the JSON Claude already pipes to the hook. Injected through a managed-settings file that never touches your own Claude settings. Deliberately limited to claude: no other agent has a status-line hook, and running them under tmux to fake one made their TUIs render badly.",
    code: "⬢ sandbox · opus 5 · mem 412MiB · cpu 82% · 5h 23% (2h14m) · wk 49%   git:feature/login",
    state: "opt-out",
  },
  {
    title: "How much of the window is left",
    group: "observability",
    body: "sandbox-cli usage prints the same two subscription windows from anywhere — a second terminal, or a run that already finished. Several sandboxes on several branches are separate containers but one account quota. The reading comes from the cache Claude Code keeps for its own /usage, so the command always prints how old it is, and a window that has since reset shows no percentage rather than a figure about the period before it. --refresh spends one throwaway turn to make it current; --json for scripts.",
    code: "5h   23%  resets in 2h14m   ·   week   49%  resets in 4d",
    state: "opt-in",
  },
  {
    title: "Peak summary on every run",
    group: "observability",
    body: "Interactive sessions own the screen, so instead of drawing over them sandbox-cli samples in the background and prints one line when the run exits. You still get the numbers for a twelve-minute Claude session.",
    code: "sandbox-cli: peak mem 412MiB · cpu peak 138% · 12m04s · git:feature/login",
    state: "default",
  },
  {
    title: "Watch every sandbox at once",
    group: "observability",
    body: "sandbox-cli stats is a refreshing table of all running sandbox containers in a second terminal — the answer for the agents that have no status line of their own. --once for a scriptable snapshot.",
    code: "sandbox-cli stats --interval 1s",
    state: "opt-in",
  },
];

/** Shared conversation history is a default worth calling out on its own. */
export const HISTORY_NOTE = {
  mount: "~/.claude/projects/<project>  ->  /sandbox/home/.claude/projects/-workspace",
  body: "sandbox-cli claude mounts your host Claude history for the current project so --resume inside the sandbox lists the sessions you started on the host, and sessions you run inside show up outside afterwards. Only that one project's directory is mounted, it is read-write, and --no-sync opts out.",
};
