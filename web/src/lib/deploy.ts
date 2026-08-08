/**
 * The two ways sandbox-cli is actually deployed: a developer's own machine, and
 * an unattended run nobody is watching.
 *
 * The split is deliberately NOT "a lax mode and a strict mode" — local
 * development is where a prompt-injected agent has the most valuable thing in
 * reach, so no profile relaxes the host boundary. What differs is what each
 * optimises within a secure baseline, and one thing of kind rather than degree:
 * a control this host cannot provide is a warning under dev and a refusal under
 * prod.
 *
 * Everything here mirrors internal/config/profile.go — profileBase sets it,
 * ValidateProfile asserts it against the configuration that will actually run.
 * When that file changes, this one changes.
 */

export type DeployStep = {
  title: string;
  /** Shell to run, when the step is a command. */
  code?: string;
  body: string;
  /** A refusal or sharp edge the step has to lead with. */
  warn?: string;
};

export type DeployMode = {
  id: string;
  label: string;
  /** The flag that selects it, shown under the tab label. */
  flag: string;
  tagline: string;
  summary: string;
  steps: DeployStep[];
};

/** What the profile changes, side by side. Rows follow profileBase(). */
export const PROFILE_MATRIX: {
  setting: string;
  dev: string;
  prod: string;
  note?: string;
}[] = [
  {
    setting: "Egress",
    dev: "allowlist + 9 baseline domains",
    prod: "allowlist, baseline off — you name the list",
    note: "The baseline contains github.com, a write endpoint and so an exfiltration channel for any token the agent holds. Prod drops it; an allowlist that resolves to nothing refuses the run rather than starting unfiltered.",
  },
  {
    setting: "Persisted agent login",
    dev: "on — log in once",
    prod: "off — nothing to steal",
    note: "The default auth path is an OAuth refresh token in the persisted HOME, which the agent can read. Prod does not mount it and authenticates through the secrets broker instead, which is the whole answer to the credential problem — no TLS-terminating proxy required.",
  },
  {
    setting: "Host conversation history",
    dev: "this project's bucket, mounted rw",
    prod: "not mounted",
  },
  {
    setting: "A kernel of its own",
    dev: "reported, never required",
    prod: "required where the engine proves it can",
    note: "A container shares the host kernel, and prod may carry untrusted agents. If the engine reports a microVM or gVisor runtime and nothing selected it, prod refuses — the boundary was there and unused. If it reports none, prod runs and warns on every run, because an engine's silence is not evidence: podman answers with its active runtime rather than its registered set, and nothing distinguishes a Linux host that could install Kata from a VM image its user does not compose. Naming the runtime is how you turn the warning into a guarantee; prod will not choose one for you, since which of Kata or gVisor a machine has is the machine's business.",
  },
  {
    setting: "Seccomp missing on the daemon",
    dev: "warns",
    prod: "refuses, non-zero exit",
    note: "The one difference of kind. You are there to read a warning; a scheduler is not, and a production run that quietly degraded is the failure the profile exists to prevent.",
  },
  {
    setting: "memory · cpus · pids",
    dev: "unlimited · unlimited · 1024",
    prod: "2g · 2 · 512",
  },
  {
    setting: "Ports published",
    dev: "whatever you ask for",
    prod: "none in config",
    note: "An inbound port is the one thing that opens the boundary the other way, and prod is likelier to be multi-tenant.",
  },
  {
    setting: "Container user",
    dev: "sandbox (non-root)",
    prod: "sandbox (non-root), asserted",
  },
];

/**
 * What ValidateProfile checks on the *resolved* config, after every layer has
 * been merged. This is what keeps the profile honest: prod's settings are a base
 * layer a later layer could in principle undo, and a named profile that quietly
 * stopped delivering would be worse than no profile at all.
 */
export const PROD_ASSERTIONS = [
  'network.mode is "allowlist"',
  "network.baseline is false",
  "persist_auth is false",
  "sync is false",
  'security.seccomp is "required"',
  "user is not root",
  "security.cap_add is empty",
  "security.memory, cpus and pids_limit are all bounded",
  "ports is empty",
];

const DEV_STEPS: DeployStep[] = [
  {
    title: "Ask the host what it can deliver",
    code: "sandbox-cli doctor",
    body: "Reports whether the daemon applies a syscall filter, whether a container here can program the egress firewall — tried, not queried — and which OCI runtimes are registered. Under dev a control the host cannot provide is a warning, so you can read it and decide; the run still starts.",
  },
  {
    title: "Run the agent where you already are",
    code: "cd ~/your-project\nsandbox-cli claude",
    body: "No profile flag needed — dev is the default. The first run builds the base image, which takes a few minutes once. Only this directory is mounted, at /workspace; HOME inside the container is a fake path that dies with it.",
  },
  {
    title: "Log in once, not every run",
    code: "# nothing to do — this is the default\nsandbox-cli claude --no-persist-auth   # opt out for a throwaway session",
    body: "The agent's whole HOME is a sandbox-owned host directory (~/.config/sandbox/agents/claude), separate from your real ~/.claude, so the login survives the disposable container. Your host conversation history for this one project is mounted too, so a session started on the host resumes inside the sandbox and vice versa — --no-sync opts out.",
  },
  {
    title: "Let it commit as you",
    code: "sandbox-cli claude --git",
    body: "Forwards your git identity and marks /workspace as a trusted directory, so commits carry your name and email instead of failing on dubious ownership. Without it the agent can still edit files; it just cannot commit cleanly.",
  },
  {
    title: "Widen egress only when a build needs it",
    code: "sandbox-cli claude --allow deb.debian.org --allow proxy.golang.org",
    body: "Dev already runs default-deny with a baseline covering the agent APIs, npm, PyPI and GitHub, so most projects need nothing. --allow adds to that list for one run. A proxy inside the container decides on the hostname it reads from the TLS SNI or the HTTP Host header, resolving fresh per connection, so an allowlisted domain does not let every host sharing its IP in with it.",
  },
  {
    title: "Commit the project's own boundary",
    code: "sandbox-cli init      # scaffolds .sandbox.yaml",
    warn: "A .sandbox.yaml is untrusted input: it travels with the repo and the agent can rewrite it between runs.",
    body: "So it may describe the project — ports, hostname, caches, snapshots — and never the security boundary. image, user, mounts, env, secrets, security.* and runtime are refused from it with an error naming the key, and network settings may only ever tighten what you already have in force. Put those in ~/.config/sandbox/config.yaml, or load a project file you have read deliberately with --config ./.sandbox.yaml.",
  },
  {
    title: "Fan out across branches",
    code: 'sandbox-cli claude --worktree feat/api --detach -- -p "add pagination"\nsandbox-cli ps\nsandbox-cli worktree commit feat/api -m "wip"',
    body: "--worktree runs the agent in a real git worktree for its own branch, in its own container, so three agents on three branches never collide. --detach puts it in the background and keeps the container after it exits, because the exit code and the logs are the whole supervision story.",
  },
  {
    title: "Watch it, and pick up after it",
    code: "sandbox-cli stats           # live memory/CPU of running sandboxes\nsandbox-cli context list    # conversations agents have had here\nsandbox-cli recover         # what a crashed run left behind",
    body: "Claude Code gets the gauge inside its own UI through its statusLine hook; every other agent gets stats in a second terminal, because faking a status line by wrapping their TUIs in tmux was tried and reverted. After a crash the files are usually already on disk — recover says so rather than pretending it saved you — and correlates the run with its transcript to print the resume command.",
  },
];

const PROD_STEPS: DeployStep[] = [
  {
    title: "Ask the machine before you schedule anything on it",
    code: "sandbox-cli doctor --profile prod",
    warn: "Non-zero exit means do not schedule here yet.",
    body: "Same checks as dev, opposite disposition: every warning becomes a failure. A question that could not be asked counts as a failure too — it does not get to assume the answer it would prefer. On Docker Desktop the usual blocker is \"seccomp-profile\": \"unconfined\" in Settings → Docker Engine; remove that line and apply.",
  },
  {
    title: "Name every domain the run may reach",
    code: "sandbox-cli claude --profile prod \\\n  --allow api.anthropic.com \\\n  --allow registry.npmjs.org \\\n  -- -p \"run the migration\"",
    warn: "Prod turns the baseline off, so --profile prod on its own refuses to start.",
    body: 'The refusal reads: network.mode is "allowlist" with baseline: false and no domains in network.allow — that permits nothing, and would run with no egress firewall at all. That is deliberate: the strictest-sounding request must not produce the weakest result. List what the job actually needs, or say network.mode: none to reach nothing.',
  },
  {
    title: "Hand it a scoped credential, never a login",
    code: "sandbox-cli claude --profile prod \\\n  --allow api.anthropic.com \\\n  --secret ANTHROPIC_API_KEY=cmd:'vault read -field=key secret/agent' \\\n  -- -p \"...\"",
    body: "Prod mounts no persisted HOME, so there is no long-lived refresh token in the container to steal. The broker resolves file:, cmd: or env: references on the host at run time and passes the value to the docker child's environment — it never reaches the argv, so it is absent from ps output, and the run log records forwarded variables by name only because a log is a file.",
  },
  {
    title: "Give it something that exits",
    code: "name=$(sandbox-cli claude --profile prod --detach \\\n  --allow api.anthropic.com -- -p \"...\")\ndocker logs -f \"$name\"\nsandbox-cli ps          # state and exit code\nsandbox-cli clean       # reap the exited ones",
    body: "A detached container gets no terminal, so the guest must be a command — or an agent in its non-interactive mode — that ends on its own. --detach also keeps the container instead of removing it, since the exit code and the logs become interesting exactly when the run is over. It is named sandbox-<repo>-<branch>, so the engine's own duplicate-name refusal enforces one agent per branch.",
  },
  {
    title: "Pin the profile where a repository cannot reach it",
    code: "# ~/.config/sandbox/config.yaml\nprofile: prod",
    body: "Your own config is trusted; a project file is not. A .sandbox.yaml may raise the profile to prod and may never lower it — if a repository could select the weaker one it would drop the run out of prod and leave every other refusal decorative. A typo in this key is an error rather than a silent fall back to dev, because those two are indistinguishable to the operator and one of them is not what they asked for.",
  },
  {
    title: "Know what prod will refuse to run without",
    code: "sandbox-cli run --profile prod --allow api.anthropic.com --dry-run -- true",
    body: "The profile is applied as the base layer under your own config, so you can still tune the setup — a profile that cannot be adjusted gets abandoned. What stops that from hollowing prod out is a validation pass over the fully-resolved configuration: the settings that define prod are asserted against what will actually run, and a run that no longer satisfies them stops with the list.",
  },
  {
    title: "Keep the receipts",
    code: "tail -n 1 ~/.config/sandbox/audit/sessions.jsonl",
    body: "One JSON line per run, written after it ends because how it ended is the point: image, workspace, branch, agent, command, network posture, the resolved egress allowlist, exit code and duration. Environment variables appear by name only — there is nowhere in the record to put a value, deliberately.",
  },
  {
    title: "Give the run a kernel of its own",
    code: "# ~/.config/sandbox/config.yaml\nruntime: runsc        # gVisor; or kata-runtime for a microVM",
    body: "A container namespace is a boundary against a mistaken agent, not against a determined one with a kernel exploit. Under prod this is the difference between a warning on every run and a guarantee: if your engine reports such a runtime and you have not selected it, prod refuses outright, and if it reports none, prod says on every run that the container shares the host kernel. Naming one settles it. What prod will not do is choose for you — which of Kata or gVisor a machine has is the machine's business. `sandbox-cli doctor --profile prod --runtime runsc` asks the preflight about the exact run you are about to make. This is a user-config key, never a project one: a repository choosing which binary runs on your host would be choosing what executes.",
  },
];

export const DEPLOY_MODES: DeployMode[] = [
  {
    id: "dev",
    label: "Local development",
    flag: "--profile dev · default",
    tagline: "You are watching, so it warns",
    summary:
      "Optimised for the loop you are actually in: log in once, resume yesterday's conversation, commit as yourself, fan out across branches. The host boundary is identical to prod — this is where your credentials and your other repositories live, so nothing about it is relaxed.",
    steps: DEV_STEPS,
  },
  {
    id: "prod",
    label: "Production",
    flag: "--profile prod",
    tagline: "Nobody is watching, so it refuses",
    summary:
      "Optimised for a run that has to be trustworthy without a person in front of it: no long-lived credential in the container, an allowlist you wrote rather than inherited, bounded resources, and a refusal instead of a warning whenever the host cannot deliver a control.",
    steps: PROD_STEPS,
  },
];
