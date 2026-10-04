/**
 * Every capability the rewrite actually ships, grouped the way a developer
 * evaluating it would ask about them. Mirrors README.md, docs/api/v1.md and
 * docs/self-hosting.md; the measured numbers are the ones in
 * docs/rewrite/PLAN.md and docs/testing/end-to-end.md.
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
  workflow: "Workflow",
  network: "Network",
  credentials: "Credentials",
  observability: "Observability",
};

export const FEATURES: Feature[] = [
  // --- the boundary -----------------------------------------------------------
  {
    title: "A VM per sandbox, with its own kernel",
    group: "boundary",
    body: "Firecracker on Linux, the native container runtime on Apple-silicon Macs: either way the guest runs its own kernel behind a hypervisor. Escalating to root inside is root of a machine with nothing of yours in it.",
    state: "default",
  },
  {
    title: "Nothing of yours is mounted",
    group: "boundary",
    body: "No host directory is shared, on any backend. Every process starts in /sandbox/home, the sandbox user's home; code gets in the way it gets onto any machine — the agent or the command runs git clone, or the files API writes it. Nothing comes back to your machine but the agent's saved login.",
    code: "sandbox-cli agent claude -p \"clone github.com/you/app and fix its failing test\"",
    state: "default",
  },
  {
    title: "The guest is treated as hostile",
    group: "boundary",
    body: "The host talks to one agent inside the VM over a bounded, framed protocol and never acts on what the guest volunteers. Files copied out — an agent's saved login — are written without following links.",
    state: "default",
  },
  {
    title: "Tighten, never loosen",
    group: "boundary",
    flag: "--profile",
    body: "The server's policy is the ceiling; a request may only ask for less. A project's .sandbox.yaml is untrusted and may tighten what your own config says, never widen it. dev warns when a control cannot be delivered; prod refuses.",
    code: "sandbox-cli run --profile prod -- make release",
    state: "default",
  },
  {
    title: "Fail closed",
    group: "boundary",
    body: "A control that was asked for and cannot be delivered refuses the run. A backend that cannot enforce an egress allowlist says so in its capabilities, and the request is refused — never served open, never quietly offline.",
    state: "default",
  },

  // --- workflow ---------------------------------------------------------------
  {
    title: "Which agent is waiting for you",
    group: "workflow",
    flag: "agent state",
    body: "Working, blocked, idle, done or failed, decided from the agent's process and its conversation: who spoke last, how long ago, and whether it has a terminal somebody can answer at. Never from the agent's wording, so a reworded prompt cannot make it lie. agent wait blocks until an agent is in a state you name; Studio's dashboard counts the ones waiting.",
    code: "sandbox-cli agent wait fix-auth --state blocked --timeout 30m",
    state: "default",
  },
  {
    title: "Pools: a create is a claim",
    group: "workflow",
    flag: "pools:",
    body: "The server keeps sandboxes of one image booted ahead of requests. A create of that shape takes under a millisecond; environment, name and labels are still the request's own, because the server applies them.",
    code: "pools: [{size: 2}]   # in sandboxd's policy",
    state: "opt-in",
  },
  {
    title: "Suspend, resume, fork",
    group: "workflow",
    body: "On Firecracker, suspend a sandbox with its memory and processes and pay nothing while it waits; snapshot a running one and start forks of it in about 20 ms each.",
    code: "sandbox-cli snapshot sbx_… && sandbox-cli run --from-snapshot snp_… -- bash",
    state: "default",
  },
  {
    title: "Volumes that outlive the sandbox",
    group: "workflow",
    flag: "--volume",
    body: "A named filesystem one sandbox writes and the next one reads: a package cache, a dataset. One live sandbox at a time, read-only enforced by the drive itself, and never mounted on the host.",
    code: "sandbox-cli run --volume cache:/sandbox/home/.cache -- npm ci",
    state: "opt-in",
  },
  {
    title: "Tunnels to a port inside",
    group: "workflow",
    body: "Forward a local port to a server listening on the guest's loopback, through the API — a dev server, a debugger — without opening anything on the guest's network.",
    code: "sandbox-cli tunnel sbx_… 3000",
    state: "default",
  },
  {
    title: "One API, three places",
    group: "workflow",
    body: "The CLI, the Python and TypeScript SDKs and Studio are clients of the same API, served by sandboxd on your Mac, on a Linux machine you control, or in the cloud. A conformance suite run against an endpoint is what “the same” means.",
    code: "sandbox-cli context use box",
    state: "default",
  },

  // --- network ----------------------------------------------------------------
  {
    title: "Egress is an allowlist of names",
    group: "network",
    flag: "--allow / --deny",
    body: "Open by default; under an allowlist only the agent's API, package registries and the names you add get through. The check is by name — TLS SNI, HTTP Host — so a host sharing an allowed address does not ride in on it; deny wins over allow, wildcards included.",
    code: "sandbox-cli run --allow internal.registry.example.com -- npm ci",
    state: "default",
  },
  {
    title: "Enforced outside the guest",
    group: "network",
    body: "On Linux the firewall and the name-checking proxy run on the host, in a table the guest cannot reach. DNS inside answers only allowlisted names and forwards nothing.",
    state: "default",
  },
  {
    title: "Change it while it runs",
    group: "network",
    body: "Narrow or widen a running sandbox's egress, under the same rules as create. No restart, no lost state.",
    state: "default",
  },

  // --- credentials ------------------------------------------------------------
  {
    title: "Environment by name, never by value",
    group: "credentials",
    flag: "--env",
    body: "Values reach the guest and nowhere else: the API returns names only, the audit log records names only. Some names are refused outright, because they are instructions to the loader or the shell rather than settings.",
    code: "sandbox-cli run -e NPM_TOKEN -- npm publish",
    state: "default",
  },
  {
    title: "Secrets resolved on your machine",
    group: "credentials",
    flag: "secrets:",
    body: "A secret is a reference — a file, a command, a host variable — resolved on the client and handed to the sandbox, never written into an argv or a config file. Long-lived tokens are named when they are recognised.",
    code: 'secrets: {GITHUB_TOKEN: {command: "gh auth token"}}',
    state: "opt-in",
  },
  {
    title: "Agent logins, kept and contained",
    group: "credentials",
    flag: "--no-persist-auth",
    body: "An agent's login files are copied into each sandbox and back out when it ends, never mounted. prod turns this off entirely, so a refresh token is never in reach of an unattended agent.",
    state: "opt-out",
  },

  // --- observability ----------------------------------------------------------
  {
    title: "An audit log on the server",
    group: "observability",
    body: "Every create, every process with its argv and exit code, every file read and written, every network change and how each sandbox ended — whichever client asked. Environment variables by name only.",
    code: "sandbox-cli events sbx_…",
    state: "default",
  },
  {
    title: "Labels",
    group: "observability",
    flag: "--label",
    body: "Your own metadata on a sandbox: shown by list, filterable, recorded in its audit events. The agent layer labels its runs itself, so a run that fell back to another agent says which one it skipped and why.",
    code: "sandbox-cli list --label team=infra",
    state: "opt-in",
  },
  {
    title: "Sessions outlive the terminal",
    group: "observability",
    body: "Detach, close the laptop, come back: list what is running, follow its output from the start, attach a real terminal, or stop it. A reference is matched against the server's own sandboxes, never resolved by a backend.",
    code: "sandbox-cli logs sbx_… · attach · kill",
    state: "default",
  },
];
