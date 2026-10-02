/**
 * What an agent can touch, with and without the sandbox. The point of the
 * blast-radius map: on a bare host every one of these is reachable, and inside
 * the sandbox — a VM with its own kernel and its own disk — all but one of them
 * simply does not exist, and that one is a clone rather than your checkout.
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
    stake: "The work itself. Inside, it is a clone sent in as a git bundle; what comes back is commits you choose to merge, and your checkout is never written.",
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
    stake: "rm -rf ~ is one hallucinated path away. Inside, HOME is the guest's own, on a disk discarded with the VM.",
    inside: "ephemeral",
    weight: 10,
  },
  {
    path: "/etc, /usr, /",
    what: "the system",
    stake: "Everything the user can write. Inside, these are the guest's own, under a kernel that is not yours.",
    inside: "ephemeral",
    weight: 7,
  },
  {
    path: "~/data",
    what: "a directory you choose to share",
    stake: "Reachable only if you pass --bind on a local Mac, which can be read-only. Never your home or an ancestor of it, whatever you ask.",
    inside: "opt-in",
    weight: 3,
  },
];

export const INSIDE_LABEL: Record<HostPath["inside"], string> = {
  workspace: "cloned into /workspace",
  absent: "not there — nothing to read",
  ephemeral: "the guest's own, discarded",
  "opt-in": "only with --bind, on a local Mac",
};
