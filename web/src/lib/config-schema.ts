/**
 * The .sandbox.yaml schema, as the CLI actually reads it. Mirrors
 * internal/config (config.go for the keys and their defaults, load.go for the
 * precedence and merge rules) and the scaffold `sandbox-cli init` writes, so
 * the page has one place to update when the schema changes.
 */

export const CONFIG_FILE = ".sandbox.yaml";

/** Lowest to highest. Later layers win, key by key. */
export const PRECEDENCE = [
  {
    label: "Built-in defaults",
    hint: "config.Default()",
    body: "Non-root sandbox user, /workspace, a fake HOME, all capabilities dropped.",
  },
  {
    label: "~/.config/sandbox/config.yaml",
    hint: "you, everywhere",
    body: "Preferences that follow you across every project on this machine.",
  },
  {
    label: `Nearest ${CONFIG_FILE}`,
    hint: "the project",
    body: "Found by walking up from the current directory. Commit it; everyone on the repo gets the same boundary.",
  },
  {
    label: "Command-line flags",
    hint: "this one run",
    body: "Scalar flags override the file for one run; the list-shaped ones — --mount, --env-allow, --allow, --publish — add to what it declared rather than replacing it.",
  },
];

export type ConfigSample = {
  id: string;
  label: string;
  hint: string;
  yaml: string;
  note: string;
};

export const CONFIG_SAMPLES: ConfigSample[] = [
  {
    id: "starter",
    label: "Project file",
    hint: ".sandbox.yaml",
    yaml: `# .sandbox.yaml — commit this with the project.
# It travels with the repo, so it is treated as UNTRUSTED: it may describe
# the project, and tighten the sandbox, but never loosen it.

# hostname: sandbox     # cosmetic

# cache:
#   enabled: true       # npm/pip/cargo/go caches survive the --rm container

# network:
#   mode: none          # tighter than the default allowlist. "default" is
#                       # REFUSED here — a project may not widen egress.

# profile: prod         # a repo that handles untrusted input may demand the
#                       # stricter profile. It may never ask for the weaker one.
`,
    note: "Almost nothing belongs here, and that is the design. A .sandbox.yaml travels with the repository and the agent can rewrite it mid-run, so the keys that choose an image, mount a host path, forward a credential or relax confinement are refused outright — and any network.mode or profile that weakens what is already in force is refused too. Setting one makes every sandbox-cli command in that directory fail, on purpose.",
  },
  {
    id: "user",
    label: "Your own config",
    hint: "~/.config/sandbox/config.yaml",
    yaml: `# ~/.config/sandbox/config.yaml — yours, trusted, never in a repo.

profile: dev            # dev warns when a control is unavailable; prod refuses

env_allow:              # host vars forwarded ONLY if they are set
  - ANTHROPIC_API_KEY
  - OPENAI_API_KEY

network:
  mode: allowlist       # the default. "default" = unrestricted, "none" = nothing
  allow:
    - internal.registry.example.com
  # baseline: false     # drop the built-in domains so "allow" is the WHOLE list

ports:
  - 3000:3000           # no address given => binds 127.0.0.1

mounts:
  - { host: ~/datasets, container: /data, mode: rw }

secrets:                # resolved at run time, forwarded by name only
  GITHUB_TOKEN:
    command: gh auth token
`,
    note: "Everything the project file may not say lives here, because typing a path into your own home directory is a deliberate act that cloning a repository is not. An explicit --config <path> is the third layer, for a checked-in file you have actually read.",
  },
  {
    id: "locked",
    label: "Locked down",
    hint: "prod, or close to it",
    yaml: `profile: prod           # allowlist + baseline off, no persisted login,
                        # no host history, seccomp required, bounded resources

network:
  allow:                # prod starts with an EMPTY list: name what you need,
    - api.anthropic.com # and an allowlist resolving to nothing is refused

security:
  seccomp: required     # refuse to run unless the daemon applies a filter
  memory: 2g
  cpus: "2"
  pids_limit: 512

runtime: runsc          # gVisor — a shared kernel is not a boundary for
                        # untrusted agents. sandbox-cli doctor --profile prod
                        # tells you whether this host has one registered.
`,
    note: "Run sandbox-cli doctor --profile prod on a machine before scheduling anything on it: it checks whether the host can actually deliver this — seccomp applied, a container able to program the egress firewall — and exits non-zero if not, rather than letting an unattended run proceed in a weaker configuration than it asked for.",
  },
  {
    id: "full",
    label: "Everything",
    hint: "config show",
    yaml: `image: ""                # "" = the built-in content-addressed sandbox-base
workdir: /workspace
user: sandbox
home: /sandbox/home
hostname: ""
profile: dev
persist_auth: true       # agent login survives --rm (agent wrappers only)
sync: true               # mount this project's host agent history

mounts: []
env: {}
env_allow: []

network:
  mode: allowlist        # allowlist | default | none
  allow: []
  baseline: null         # null/true = keep the built-in domains

ports: []

security:
  no_new_privileges: true
  cap_drop: [ALL]
  cap_add: []
  pids_limit: 1024
  memory: ""             # "" = unlimited
  cpus: ""
  seccomp: ""            # "" = docker's default; "required" = refuse without one

cache:
  enabled: false
  paths: []

snapshot:
  interval: 2m
  retention: 336h

secrets: {}
runtime: ""              # "" = docker's default (runc)
`,
    note: "Everything sandbox-cli reads, with its default value. sandbox-cli config show prints the merged result for the current directory, and sandbox-cli config validate checks that it is internally consistent without starting a container.",
  },
];

export type ConfigKey = {
  key: string;
  type: string;
  /** What you get when the key is absent. */
  fallback: string;
  body: string;
  /**
   * Where this key may be set. "project" means a committed .sandbox.yaml may
   * carry it; "user" means only your own config or an explicit --config, because
   * a repository setting it could reach the host or widen what the container
   * reaches. "tighten" means a project may set it, but only in the stricter
   * direction. Absent means project — the harmless majority.
   */
  where?: "user" | "tighten";
};

export type ConfigGroup = {
  id: string;
  label: string;
  blurb: string;
  keys: ConfigKey[];
};

export const CONFIG_GROUPS: ConfigGroup[] = [
  {
    id: "container",
    label: "The container",
    blurb: "What the run is made of.",
    keys: [
      {
        key: "image",
        where: "user",
        type: "string",
        fallback: "built-in sandbox-base",
        body: "The base image tag is content-addressed (sandbox-base:<gen>-<hash>) so it rebuilds itself whenever the image definition changes. Pinning your own tag opts out of that.",
      },
      {
        key: "workdir",
        where: "user",
        type: "path",
        fallback: "/workspace",
        body: "Where the project is mounted and where the guest command starts.",
      },
      {
        key: "user",
        where: "user",
        type: "sandbox | root",
        fallback: "sandbox",
        body: "Non-root by default — which is also why agents accept --dangerously-skip-permissions in here; they refuse it as root. On macOS, bind-mount ownership is virtualized, so files are still written as you.",
      },
      {
        key: "home",
        where: "user",
        type: "path",
        fallback: "/sandbox/home",
        body: "The fake HOME. Nothing under it is host-connected unless an agent wrapper persists its login there.",
      },
      {
        key: "profile",
        where: "tighten",
        type: "dev | prod",
        fallback: "dev",
        body: "dev warns when a control the host cannot provide is missing, because a developer is watching. prod refuses, because nobody is. prod also means allowlist egress with the baseline off, no persisted login, no host history mount, seccomp required and bounded resources. Both are secure — they differ in what they optimise, not in whether the boundary holds. A project may demand prod and may never ask for dev.",
      },
      {
        key: "persist_auth",
        where: "tighten",
        type: "bool",
        fallback: "true for agent wrappers",
        body: "Keeps the agent login across runs by mounting a sandbox-owned host directory as the agent's whole HOME. Worth knowing what that directory holds: a long-lived OAuth refresh token the agent can read. prod turns this off, which is why prod needs no TLS-intercepting proxy to protect a credential — it simply never carries one.",
      },
      {
        key: "sync",
        where: "tighten",
        type: "bool",
        fallback: "true",
        body: "Mounts this project's host agent history so sessions resolve on both sides of the sandbox. The one default that reaches a host path outside the workspace, scoped to the single project bucket.",
      },
      {
        key: "routing",
        where: "user",
        type: "string[]",
        fallback: "no routing — the agent you asked for is the agent that runs",
        body: "Agents to fall through when the one you asked for is unavailable, primary first. sandbox-cli probes the provider before launching and skips an agent that is not answering; on the command line it also retries a run that failed having changed no files, which is what a provider dying mid-run looks like. A run that changed files is never retried — that is a failed attempt, not an outage. User-config only: choosing the agent chooses which persisted login and which forwarded variables are in reach.",
      },
      {
        key: "providers",
        where: "user",
        type: "map[agent]host",
        fallback: "each agent's own provider, and no probe for the ones that have none",
        body: "Which host routing probes for an agent, e.g. opencode: api.groq.com. It is what makes a provider-agnostic agent probeable at all, and what points the check at your own endpoint when an agent runs behind a proxy. Blank means do not probe. User-config only: a probe decides which agent a chain skips, so a host that always answers keeps a dead agent in play and one that never answers forces a fall through to another agent's login.",
      },
      {
        key: "hostname",
        type: "string",
        fallback: "sandbox",
        body: "The container hostname, which is what an agent's prompt shows.",
      },
      {
        key: "runtime",
        where: "user",
        type: "string",
        fallback: "docker default (runc)",
        body: "Any OCI runtime the daemon has registered: kata-fc or kata-clh for a microVM with its own kernel, runsc for gVisor. Mounts, hardening, allowlist and caches all work unchanged on top. Only names that say which hypervisor is underneath are reported as a kernel of their own — a bare kata resolves to whatever configuration.toml picks.",
      },
    ],
  },
  {
    id: "reach",
    label: "Mounts & environment",
    blurb: "Everything that crosses the boundary is named here.",
    keys: [
      {
        key: "mounts",
        where: "user",
        type: "list of { host, container, mode }",
        fallback: "just /workspace",
        body: "host may start with ~ and may be relative to the config file that declared it. mode is ro unless you say rw. Refusals no key overrides: never /, never your home directory, never an ancestor of it.",
      },
      {
        key: "env",
        where: "user",
        type: "map",
        fallback: "empty",
        body: "Literal values injected into the container. Merged key by key, so a project file can add one without wiping your user-level set.",
      },
      {
        key: "env_allow",
        where: "user",
        type: "list",
        fallback: "the agent's own suggestion",
        body: "A default-deny allowlist of host variables, forwarded only when actually set. The one list that appends across layers instead of replacing.",
      },
      {
        key: "secrets",
        where: "user",
        type: "map of { file | command | env }",
        fallback: "none",
        body: "Brokered credentials, resolved at run time and forwarded by name. Exactly one source per secret; a command: source can fetch a short-lived token fresh each run.",
      },
    ],
  },
  {
    id: "network",
    label: "Network",
    blurb: "The half of the problem filesystem isolation does nothing about.",
    keys: [
      {
        key: "network.mode",
        where: "tighten",
        type: "allowlist | default | none",
        fallback: "allowlist",
        body: "allowlist is the default: a default-deny egress firewall is programmed inside the container at startup, then privileges drop back to the non-root user. If it cannot be programmed, the run fails instead of running open. Pass --network default to decline it for one run, or none to reach nothing at all. A project file may tighten this and never loosen it.",
      },
      {
        key: "network.baseline",
        where: "tighten",
        type: "bool",
        fallback: "true — the built-in domains are permitted",
        body: "false drops the built-in domain set so allow is the WHOLE list. It exists because allow could only ever add, leaving no way to decline github.com — which is a write endpoint, and so a channel for any token the agent holds. Turning it off is deliberately awkward: npm, pip and git stop working unless you list their hosts.",
      },
      {
        key: "network.allow",
        where: "user",
        type: "list",
        fallback: "baseline only",
        body: "Extra domains on top of the built-in baseline — agent APIs plus the common package registries — so npm install and git keep working. Replaces rather than appends, so a project can fully redefine it.",
      },
      {
        key: "ports",
        where: "user",
        type: "list",
        fallback: "nothing published",
        body: "A spec with no address of its own binds to 127.0.0.1, not every interface — the one place sandbox-cli deliberately differs from docker -p. Write 0.0.0.0:3000:3000 to expose it on purpose.",
      },
    ],
  },
  {
    id: "hardening",
    label: "Hardening",
    blurb: "Secure by default; every knob is here to be loosened knowingly.",
    keys: [
      {
        key: "security.no_new_privileges",
        where: "user",
        type: "bool",
        fallback: "true",
        body: "Blocks setuid privilege escalation inside the container.",
      },
      {
        key: "security.cap_drop / cap_add",
        where: "user",
        type: "list",
        fallback: "[ALL] / none",
        body: "All Linux capabilities are dropped, which is essentially free for the non-root sandbox user. Add one back only when a tool genuinely needs it.",
      },
      {
        key: "security.pids_limit",
        where: "user",
        type: "int",
        fallback: "1024",
        body: "A fork-bomb guard set well above real build and agent process counts. 0 disables it.",
      },
      {
        key: "security.memory / cpus",
        where: "user",
        type: "string",
        fallback: "unlimited",
        body: "Opt-in resource caps, e.g. 2g and 1.5. Empty leaves the container unbounded — sandbox-cli measures usage rather than throttling it.",
      },
      {
        key: "security.seccomp",
        where: "user",
        type: "string",
        fallback: "docker's default profile",
        body: "Point at your own seccomp profile when the default one blocks something you need — or set \"required\" to refuse the run unless the daemon actually applies one. Some daemons apply none and say nothing; sandbox-cli doctor tells you which yours is.",
      },
    ],
  },
  {
    id: "persistence",
    label: "What survives the run",
    blurb: "Containers are --rm; these two keys decide what outlives them.",
    keys: [
      {
        key: "cache.enabled / cache.paths",
        type: "bool / list",
        fallback: "false / built-in dirs",
        body: "Keeps npm, pip, cargo and go caches in named docker volumes so a cold install every run stops hurting. No host directory is involved. Extra paths are added to the built-in set.",
      },
      {
        key: "snapshot.enabled / interval / retention",
        type: "bool / duration / duration",
        fallback: "true / 2m / 336h",
        body: "The crash safety net: the workspace is committed under refs/sandbox/snapshots/ while a run is in flight, never touching your index, HEAD, branches or working tree. sandbox-cli recover reads it back.",
      },
    ],
  },
];

/** The merge behaviours that surprise people, stated once. */
export const MERGE_RULES = [
  {
    title: "Some lists append, the rest replace",
    body: "mounts and env_allow accumulate across layers — a project adds a variable without restating yours — and env and secrets overlay key by key. network.allow, ports, cache.paths and the security lists replace instead, so a project can fully redefine a policy, and say “none” with an empty list.",
  },
  {
    title: "Relative paths follow their file",
    body: "A mount written as ./data resolves against the directory of the config file that declared it, not your current directory — so the same .sandbox.yaml means the same thing no matter where in the repo you run from.",
  },
  {
    title: "Omitted is not false",
    body: "enabled, no_new_privileges and pids_limit are tri-state: leaving a key out keeps the inherited value, while writing it out explicitly overrides it — which is how a project turns a default-on setting off.",
  },
];
