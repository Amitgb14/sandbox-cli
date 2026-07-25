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
    label: "Starter",
    hint: "sandbox-cli init",
    yaml: `# .sandbox.yaml — commit this with the project
# Only /workspace is mounted; HOME is fake and dies with the container.

env_allow:              # host vars forwarded ONLY if they are set
  - ANTHROPIC_API_KEY
  - OPENAI_API_KEY

network:
  mode: default         # default | none | allowlist
`,
    note: "sandbox-cli init writes a fuller, fully-commented version of this file into the current directory. Zero config is required — every key below has a working default.",
  },
  {
    id: "dev",
    label: "Dev server",
    hint: "ports, mounts, caches",
    yaml: `ports:                  # published to the host
  - 3000:3000           # no address given => binds 127.0.0.1
  - 0.0.0.0:8080:8080   # reachable from your network, deliberately

mounts:                 # extra binds beyond the automatic /workspace one
  - { host: ./fixtures, container: /workspace/fixtures, mode: ro }
  - { host: ~/datasets, container: /data, mode: rw }

cache:
  enabled: true         # npm/pip/cargo/go caches survive the --rm container

env:
  NODE_ENV: development
`,
    note: "Declaring the dev-server port here is the point: sandbox-cli run -- npm run dev then just works. Flags add to the list rather than replacing it, so -P 9229 opens a debugger port for one run without disturbing the project's own.",
  },
  {
    id: "locked",
    label: "Locked down",
    hint: "egress + hardening",
    yaml: `network:
  mode: allowlist       # default-deny egress, enforced inside the container
  allow:                # on top of the baseline agent APIs + registries
    - internal.registry.example.com

security:               # secure-by-default; these are the defaults, tunable
  no_new_privileges: true
  cap_drop: [ALL]
  cap_add: []
  pids_limit: 1024
  memory: 4g            # opt-in — empty means unlimited
  cpus: "2"

runtime: runsc          # gVisor; kata-runtime for a microVM. Must be
                        # registered with the docker daemon.
`,
    note: "A run that asks for an allowlist and cannot program the firewall refuses to start rather than running open. Memory and CPU stay unlimited unless you set them, because an unexpected OOM-kill is worse than an unbounded but observed container.",
  },
  {
    id: "secrets",
    label: "Secrets & snapshots",
    hint: "credentials, crash safety",
    yaml: `secrets:                # resolved at run time, forwarded by name
  GITHUB_TOKEN:
    command: gh auth token        # stdout of a host command
  ANTHROPIC_API_KEY:
    file: ~/.secrets/anthropic    # contents of a host file
  NPM_TOKEN:
    env: NPM_TOKEN                # a host env var

snapshot:               # crash safety net — sandbox-cli recover
  enabled: true
  interval: 2m          # how often the workspace is snapshotted
  retention: 336h       # 14d, then old snapshots are pruned
`,
    note: "Exactly one of file, command or env per secret. The raw value never lands on the docker command line, in --dry-run output, or in this file — so the file stays safe to commit.",
  },
  {
    id: "every",
    label: "Every key",
    hint: "the whole schema",
    yaml: `image: my-org/dev:latest   # default: the built-in sandbox-base:<gen>-<hash>
workdir: /workspace
user: sandbox              # sandbox | root
home: /sandbox/home        # the fake, ephemeral HOME
hostname: sandbox
runtime: ""                # "" = docker's default (runc)

mounts:
  - { host: ./data, container: /workspace/data, mode: ro }

env:
  NODE_ENV: development
env_allow:
  - ANTHROPIC_API_KEY

network:
  mode: default
  allow: []

ports: []

security:
  no_new_privileges: true
  cap_drop: [ALL]
  cap_add: []
  pids_limit: 1024
  memory: ""
  cpus: ""
  seccomp: ""              # "" = docker's default profile

cache:
  enabled: false
  paths: []                # added to the built-in cache dirs

snapshot:
  enabled: true
  interval: 2m
  retention: 336h

secrets:
  GITHUB_TOKEN: { command: gh auth token }
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
        type: "string",
        fallback: "built-in sandbox-base",
        body: "The base image tag is content-addressed (sandbox-base:<gen>-<hash>) so it rebuilds itself whenever the image definition changes. Pinning your own tag opts out of that.",
      },
      {
        key: "workdir",
        type: "path",
        fallback: "/workspace",
        body: "Where the project is mounted and where the guest command starts.",
      },
      {
        key: "user",
        type: "sandbox | root",
        fallback: "sandbox",
        body: "Non-root by default — which is also why agents accept --dangerously-skip-permissions in here; they refuse it as root. On macOS, bind-mount ownership is virtualized, so files are still written as you.",
      },
      {
        key: "home",
        type: "path",
        fallback: "/sandbox/home",
        body: "The fake HOME. Nothing under it is host-connected unless an agent wrapper persists its login there.",
      },
      {
        key: "hostname",
        type: "string",
        fallback: "sandbox",
        body: "The container hostname, which is what an agent's prompt shows.",
      },
      {
        key: "runtime",
        type: "string",
        fallback: "docker default (runc)",
        body: "Any OCI runtime the daemon has registered: kata-runtime for a microVM with its own kernel, runsc for gVisor. Mounts, hardening, allowlist and caches all work unchanged on top.",
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
        type: "list of { host, container, mode }",
        fallback: "just /workspace",
        body: "host may start with ~ and may be relative to the config file that declared it. mode is ro unless you say rw. Refusals no key overrides: never /, never your home directory, never an ancestor of it.",
      },
      {
        key: "env",
        type: "map",
        fallback: "empty",
        body: "Literal values injected into the container. Merged key by key, so a project file can add one without wiping your user-level set.",
      },
      {
        key: "env_allow",
        type: "list",
        fallback: "the agent's own suggestion",
        body: "A default-deny allowlist of host variables, forwarded only when actually set. The one list that appends across layers instead of replacing.",
      },
      {
        key: "secrets",
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
        type: "default | none | allowlist",
        fallback: "default",
        body: "allowlist programs a default-deny egress firewall inside the container at startup, then drops back to the non-root user. If it cannot be programmed, the run fails instead of running open.",
      },
      {
        key: "network.allow",
        type: "list",
        fallback: "baseline only",
        body: "Extra domains on top of the built-in baseline — agent APIs plus the common package registries — so npm install and git keep working. Replaces rather than appends, so a project can fully redefine it.",
      },
      {
        key: "ports",
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
        type: "bool",
        fallback: "true",
        body: "Blocks setuid privilege escalation inside the container.",
      },
      {
        key: "security.cap_drop / cap_add",
        type: "list",
        fallback: "[ALL] / none",
        body: "All Linux capabilities are dropped, which is essentially free for the non-root sandbox user. Add one back only when a tool genuinely needs it.",
      },
      {
        key: "security.pids_limit",
        type: "int",
        fallback: "1024",
        body: "A fork-bomb guard set well above real build and agent process counts. 0 disables it.",
      },
      {
        key: "security.memory / cpus",
        type: "string",
        fallback: "unlimited",
        body: "Opt-in resource caps, e.g. 2g and 1.5. Empty leaves the container unbounded — sandbox-cli measures usage rather than throttling it.",
      },
      {
        key: "security.seccomp",
        type: "string",
        fallback: "docker's default profile",
        body: "Point at your own seccomp profile when the default one blocks something you need.",
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
