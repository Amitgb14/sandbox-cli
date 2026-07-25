/**
 * What an agent can touch, with and without the sandbox. The point of the
 * blast-radius map: on a bare host every one of these is reachable, and inside
 * the sandbox all but one of them simply does not exist as a path.
 */

export type HostPath = {
  path: string;
  what: string;
  /** Why it matters if an agent reads or writes it. */
  stake: string;
  /** Inside the sandbox: is it there at all? */
  inside: "workspace" | "absent" | "ephemeral" | "opt-in";
  /** Ordering weight in the map — higher is scarier. */
  weight: number;
};

export const HOST_PATHS: HostPath[] = [
  {
    path: "~/projects/app",
    what: "the repo you asked it to work on",
    stake: "The work itself. This is the blast radius you accepted when you started the agent.",
    inside: "workspace",
    weight: 0,
  },
  {
    path: "~/.ssh",
    what: "private keys, known_hosts",
    stake: "Push access to every repo and shell access to every box those keys open.",
    inside: "absent",
    weight: 10,
  },
  {
    path: "~/.aws",
    what: "credentials, config",
    stake: "Long-lived access keys to production infrastructure.",
    inside: "absent",
    weight: 10,
  },
  {
    path: "~/.kube",
    what: "cluster contexts",
    stake: "kubectl against every cluster you can reach, from a process that improvises.",
    inside: "absent",
    weight: 9,
  },
  {
    path: "~/.gnupg",
    what: "signing and encryption keys",
    stake: "Your identity, forgeable.",
    inside: "absent",
    weight: 9,
  },
  {
    path: "~/.config/gh",
    what: "GitHub CLI token",
    stake: "A PAT reaches every repository you can, far beyond the one being edited.",
    inside: "absent",
    weight: 8,
  },
  {
    path: "~/Library/Cookies",
    what: "browser session cookies",
    stake: "Logged-in sessions for everything you use, no password required.",
    inside: "absent",
    weight: 8,
  },
  {
    path: "~/Documents",
    what: "everything else you own",
    stake: "Contracts, exports, tax returns. None of it is any of the agent's business.",
    inside: "absent",
    weight: 6,
  },
  {
    path: "~/other-projects",
    what: "your other repos",
    stake: "An unrelated client's source, one wrong glob away.",
    inside: "absent",
    weight: 6,
  },
  {
    path: "$HOME",
    what: "the home directory itself",
    stake: "rm -rf ~ is one hallucinated path away. Inside, HOME is a fake ephemeral directory.",
    inside: "ephemeral",
    weight: 10,
  },
  {
    path: "/etc, /usr, /",
    what: "the system",
    stake: "Everything the user can write. Inside, these are the container's own, thrown away on exit.",
    inside: "ephemeral",
    weight: 7,
  },
  {
    path: "~/Desktop, ~/Downloads",
    what: "pasted screenshots",
    stake: "Reachable only if you pass --paste, and then read-only. Opt-in, because it widens what the agent sees.",
    inside: "opt-in",
    weight: 3,
  },
];

export const INSIDE_LABEL: Record<HostPath["inside"], string> = {
  workspace: "mounted at /workspace",
  absent: "not mounted — nothing to read",
  ephemeral: "ephemeral, destroyed on exit",
  "opt-in": "only with --paste, read-only",
};
