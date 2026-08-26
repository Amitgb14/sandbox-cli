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
    title: "Two profiles, neither of them lax",
    group: "boundary",
    flag: "--profile",
    body: "dev and prod are both secure — they differ in what they optimise, never in whether the boundary holds. The one difference of kind: a control the host cannot provide is a warning under dev, because a developer is watching, and a refusal under prod, because nobody is. A committed .sandbox.yaml may demand the stricter profile and may never ask for the weaker one.",
    code: "sandbox-cli claude --profile prod",
    state: "default",
  },
  {
    title: "Check the host before you trust it",
    group: "observability",
    flag: "doctor",
    body: "Asks whether this machine can actually deliver what the profile promises: docker reachable, a syscall filter really applied, a container able to program the egress firewall — tried, not queried, because rootless and userns-remapped daemons cannot — and which OCI runtimes are registered. Non-zero exit under prod, so a scheduler notices before an unattended run does.",
    code: "sandbox-cli doctor --profile prod",
    state: "default",
  },
  {
    title: "The agent never holds a long-lived credential in prod",
    group: "boundary",
    body: "Persisted agent login is off under prod, and that is the whole answer rather than a mitigation: with it on, the agent's HOME holds an OAuth refresh token it can read. prod does not mount it, so there is nothing to steal and no TLS-intercepting proxy is needed to hide it. Prod authenticates with scoped, revocable tokens through the secrets broker.",
    state: "default",
  },
  {
    title: "A fallback when a provider is down",
    group: "workflow",
    flag: "--fallback",
    body: "Claude's API having an outage should not mean the afternoon stops. A chain runs the next agent instead — the provider is probed before launching, so an outage skips that agent before a container exists, and a run that failed having changed no files is retried with the next one. A run that changed files is never retried: that is a failed attempt, not an outage, and handing the next agent half-finished edits is the thing this must never do. When it fires, the previous agent's briefing is carried across — what was asked, what it said it was doing, and a ledger of the files it touched, derived from git rather than from anything the agent claimed. It is a briefing, not a resume, and says so: session ids do not cross between vendors and neither do transcripts. Both halves work in Studio too: the daemon outlives the launch, so it watches the run and hands the work over when one fails quietly — a daemon restarted mid-run leaves that run alone rather than guessing. The same handover is something you can ask for: every conversation in Studio offers Continue with, where picking the agent that held it reopens it and picking another starts that one with the briefing instead. It is recorded as a handoff rather than as routing, because a provider going down and a person choosing are different answers to why this agent is doing that work.",
    code: "sandbox-cli claude --fallback codex 'fix the flaky test'",
    state: "opt-in",
  },
  {
    title: "Sessions you can list, follow, attach to and stop",
    group: "workflow",
    flag: "list / logs / attach / kill",
    body: "A kill -9 on sandbox-cli leaves the container running — the daemon owns it, not the client — with an agent still writing to your project, and --detach means to. Four commands address one by id, container name or branch, and a reference is matched against sandbox-cli's own containers rather than handed to the engine, so kill postgres finds nothing instead of your database. attach cannot kill: Ctrl-C detaches and the agent keeps working.",
    code: "sandbox-cli list --all",
    state: "default",
  },
  {
    title: "Carry a conversation on, or hand it to another agent",
    group: "workflow",
    flag: "Studio → Agents → Conversations",
    body: "Every conversation Studio lists offers Continue with: pick the agent that held it and it reopens, pick another and that one starts with a briefing about it. The second is deliberately not a resume — a session id is a primary key into one vendor's private store, so what crosses is HANDOFF.md, a vendor-neutral transcript and a file ledger derived from git, mounted read-only, with a prompt that tells the target it is reading a briefing rather than its own history. Two transcript formats are parsed against a confirmed shape, claude's and codex's; a conversation in any other is listed with its id and dates and marked unknown rather than guessed at, and cannot be handed over, because a briefing carrying nothing would claim a conversation crossed when it did not.",
    state: "default",
  },
  {
    title: "Containers you left behind, found and reaped",
    group: "workflow",
    flag: "clean",
    body: "Detached and fleet containers are kept after they exit, unlike every other run: their exit code and their logs are the only record the work happened, so --rm would delete exactly what you came back for. clean reaps them once you have read what you needed, and stopping a still-running one takes --force.",
    code: "sandbox-cli clean",
    state: "default",
  },
  {
    title: "A run log that says what ran, under what policy",
    group: "observability",
    body: "One line per run in ~/.config/sandbox/audit/sessions.jsonl: image, workspace, branch, agent, command, network posture, the resolved egress allowlist, exit code and duration. Environment variables are recorded by name only — never a value, because a log is a file and the broker exists to keep secrets out of those.",
    code: "~/.config/sandbox/audit/sessions.jsonl",
    state: "default",
  },
  {
    title: "And what it was refused",
    group: "observability",
    body: "A non-interactive run under an allowlist — CI, a redirected shell, --no-tty — records how many egress refusals it reported and a sample of the names, so you can answer \"did this go looking for something it was not allowed to?\" without the scrollback. Read the coverage before relying on it: an interactive session records nothing, because with a pty docker returns one merged stream and reading it would cost the container its terminal size; and --detach and fleet tasks record nothing yet either, since nothing reads their output back from docker logs. The field is called egress_denied_reported rather than egress_denied on purpose: the proxy prints those lines on the container's stderr, which the agent can write to as well, so this is the container's report and not an attested fact.",
    code: '"egress_denied_reported": 2, "egress_denied_hosts_reported": ["gist.github.com"]',
    state: "default",
  },
  {
    title: "Agents install a version somebody chose",
    group: "boundary",
    body: "Nine of the thirteen wrappers download their agent from a vendor host the first time you run it. Each one installs a version recorded in the tool, announced as it installs, rather than whatever the vendor published that morning — so a hijacked or typosquatted release does not reach a sandbox until someone bumps that line. It does not defend against a compromised registry serving different bytes for a version it already published; that needs integrity hashes a global install has no lockfile for. Self-updating agents are unaffected after the first run.",
    code: "sandbox-cli: installing qwen 0.21.3 into the sandbox agent home (first run only)...",
    state: "default",
  },
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
    body: "Point a run at any OCI runtime the daemon knows: kata-fc or kata-clh for a microVM with its own kernel, runsc for gVisor's userspace kernel. Mounts, hardening, caches, secrets and the egress allowlist all work unchanged on top. gVisor takes two adjustments — it has no connection tracking, so the allowlist is built by uid and destination instead and inbound filtering is skipped (nothing inside can answer an unsolicited connection anyway), and it cannot reach docker's embedded resolver, so the host's own nameservers are supplied.",
    code: "sandbox-cli claude --runtime kata-fc",
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
    title: "Egress allowlist, on by default",
    group: "network",
    flag: "--allow",
    body: "Outbound traffic is default-deny, enforced by an in-container firewall and decided by hostname rather than by resolved address. DNS, established flows and a baseline of agent APIs and package registries stay open, so npm install and git keep working. Worth knowing the limit: the baseline includes github.com, a write endpoint — so this bounds and logs exfiltration rather than ending it. network.baseline: false with an explicit allow is the stronger setting, and is what prod uses.",
    code: "sandbox-cli claude --allow internal.registry.example.com",
    state: "default",
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
    body: "sandbox-cli usage prints the same two subscription windows from anywhere — a second terminal, or a run that already finished. Several sandboxes on several branches are separate containers but one account quota. The reading comes from the cache Claude Code keeps for its own /usage, so the command always prints how old it is, and a window that has since reset shows no percentage rather than a figure about the period before it. --refresh spends one throwaway turn to make it current; --json for scripts. And when the file is being written while the reading in it is not — the agent running, but no longer recording usage there — it says so instead of offering a refresh that cannot help, because an old reading and a dead one are fixed by opposite things.",
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
