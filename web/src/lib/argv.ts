/**
 * A faithful model of `runtime.BuildArgs` — the pure function that turns a
 * resolved spec into the `docker` argv — so the builder on the page produces
 * the same command `--dry-run` would print, in the same order.
 *
 * Order matters and is fixed upstream for deterministic output:
 *   run --init [--runtime] --rm -it --hostname --user [--network] [--add-host]
 *   [hardening] [limits] [--mount …] -w -e HOME [-e K=V …] [-e K …] IMAGE CMD
 */

export type OptionId =
  | "worktree"
  | "allow"
  | "cache"
  | "share"
  | "paste"
  | "git"
  | "secret"
  | "prodProfile"
  | "openNetwork"
  | "noPersistAuth"
  | "noHardening"
  | "root"
  | "limits"
  | "microvm";

export type Option = {
  id: OptionId;
  flag: string;
  label: string;
  /** What it does to the boundary. */
  effect: string;
  /** Does this widen the host-connected surface, tighten it, or neither? */
  direction: "widens" | "tightens" | "neutral";
};

export const OPTIONS: Option[] = [
  {
    id: "prodProfile",
    flag: "--profile prod",
    label: "Production profile",
    effect:
      "Baseline-off egress, no persisted login (so no long-lived credential is in the container), no host history mount, seccomp required rather than hoped for, and bounded memory/CPU/pids. A control the host cannot provide refuses the run instead of warning.",
    direction: "tightens",
  },
  {
    id: "openNetwork",
    flag: "--network default",
    label: "Decline the allowlist",
    effect:
      "Egress is default-deny out of the box; this turns that off for one run. The container reaches anything the host can, which is what every run did before the allowlist became the default.",
    direction: "widens",
  },
  {
    id: "allow",
    flag: "--allow",
    label: "Egress allowlist",
    effect:
      "Adds NET_ADMIN just long enough to program an in-container firewall, then drops back to the sandbox user. Outbound traffic is default-deny after that.",
    direction: "tightens",
  },
  {
    id: "noHardening",
    flag: "--no-hardening",
    label: "Disable hardening",
    effect:
      "Drops the cap-drop, no-new-privileges and pids-limit defaults. For debugging only — it is the one toggle here that makes the container weaker.",
    direction: "widens",
  },
  {
    id: "root",
    flag: "--user root",
    label: "Run as root",
    effect:
      "Root inside the container. Agents refuse --dangerously-skip-permissions as root, which is exactly why the default is the non-root sandbox user.",
    direction: "widens",
  },
  {
    id: "microvm",
    flag: "--runtime kata-fc",
    label: "microVM runtime",
    effect:
      "Hands the container to a different OCI runtime — Kata gives it its own kernel. kata-fc names the hypervisor (Firecracker), which is the part that decides how large the escape surface is. Everything else is built identically on top.",
    direction: "tightens",
  },
  {
    id: "limits",
    flag: "--memory 4g --cpus 2",
    label: "Resource limits",
    effect: "Caps memory and CPU. Off by default: sandbox-cli measures, it does not throttle.",
    direction: "neutral",
  },
  {
    id: "worktree",
    flag: "--worktree feature-a",
    label: "Git worktree",
    effect:
      "Runs in a sandbox-owned worktree for the branch instead of your checkout. The parent repo's .git has to come along read-write, or git in the container cannot work at all.",
    direction: "widens",
  },
  {
    id: "git",
    flag: "--git",
    label: "Git identity",
    effect:
      "Forwards your name and email by name and marks the workspace trusted, so the agent's commits are attributed to you.",
    direction: "neutral",
  },
  {
    id: "cache",
    flag: "--cache",
    label: "Package caches",
    effect:
      "Named Docker volumes for npm, pip, cargo and go. Volumes, not host directories — nothing on your filesystem is exposed.",
    direction: "neutral",
  },
  {
    id: "share",
    flag: "--share",
    label: "Shared directory",
    effect:
      "Mounts one sandbox-owned host directory at /shared so two sandboxes can hand files to each other. Opt-in, because a channel between projects is exactly the reach the sandbox refuses by default.",
    direction: "widens",
  },
  {
    id: "paste",
    flag: "--paste",
    label: "Pasted images",
    effect:
      "Mounts ~/Desktop, ~/Downloads and ~/Pictures read-only at their own host paths so a pasted image path resolves. The agent can then read everything in those folders, not just the file you pasted.",
    direction: "widens",
  },
  {
    id: "secret",
    flag: "--secret",
    label: "Brokered secret",
    effect:
      "Resolved at run time from a host command and forwarded by name. Notice the value never appears below — that is the whole point.",
    direction: "tightens",
  },
  {
    id: "noPersistAuth",
    flag: "--no-persist-auth",
    label: "Throwaway login",
    effect:
      "Drops the persisted agent home. Nothing is kept and you log in again next run — the right choice for a one-off session.",
    direction: "tightens",
  },
];

export type ArgLine = {
  /** The rendered argv fragment. */
  text: string;
  /** Which option put it there — undefined means it is always present. */
  from?: OptionId;
  /** Highlight class hint. */
  kind: "base" | "mount" | "env" | "harden" | "image" | "cmd";
  /** Shown on hover. */
  why?: string;
};

const IMAGE = "sandbox-base:0.0.1-9f95ae16";

/** Build the argv for the given agent + selected options. */
export function buildArgv(agent: string, on: Set<OptionId>): ArgLine[] {
  const lines: ArgLine[] = [];
  const push = (text: string, kind: ArgLine["kind"], from?: OptionId, why?: string) =>
    lines.push({ text, kind, from, why });

  // Egress is default-deny now, so the firewall appears unless the run declines
  // it. It used to be gated on --allow, which read as "the allowlist is what you
  // switch on" — the opposite of what happens today.
  const egress = !on.has("openNetwork");
  const prod = on.has("prodProfile");

  push("docker run --init --rm -it", "base", undefined, "Every container is --rm: nothing survives the run.");
  if (on.has("microvm"))
    push("--runtime kata-fc", "base", "microvm", "A different OCI runtime — its own kernel.");
  push("--hostname sandbox", "base");
  push(on.has("root") ? "--user root" : "--user sandbox", "base", on.has("root") ? "root" : undefined,
    on.has("root") ? "Root in the container." : "Non-root by default.");

  if (!on.has("noHardening")) {
    push("--security-opt no-new-privileges", "harden", undefined, "Blocks setuid privilege escalation.");
    push("--cap-drop ALL", "harden", undefined, "Every Linux capability dropped.");
    if (egress)
      push("--cap-add NET_ADMIN", "harden", on.has("allow") ? "allow" : undefined,
        "Needed to program the firewall at startup, then the run drops back to the sandbox user.");
    push(prod ? "--pids-limit 512" : "--pids-limit 1024", "harden", prod ? "prodProfile" : undefined, "Fork-bomb guard.");
  } else if (egress) {
    push("--cap-add NET_ADMIN", "harden", undefined, "Needed to program the firewall at startup.");
  }

  if (prod)
    push("--security-opt seccomp=…", "harden", "prodProfile",
      "prod refuses to start unless the daemon actually applies a syscall filter. sandbox-cli doctor --profile prod tells you whether yours does.");

  if (prod) {
    push("--memory 2g", "base", "prodProfile");
    push("--cpus 2", "base", "prodProfile");
  } else if (on.has("limits")) {
    push("--memory 4g", "base", "limits");
    push("--cpus 2", "base", "limits");
  }

  // Mounts — the part that decides what the container can actually reach.
  const ws = on.has("worktree")
    ? "~/.config/sandbox/worktrees/app-9f95/feature-a"
    : "~/projects/app";
  push(`--mount type=bind,source=${ws},target=/workspace`, "mount", on.has("worktree") ? "worktree" : undefined,
    "The one path that is always host-connected.");

  if (on.has("worktree"))
    push("--mount type=bind,source=~/projects/app/.git,target=~/projects/app/.git", "mount", "worktree",
      "A worktree's .git is a pointer into the parent repo. Without this every git command fails — and it is read-write.");

  if (!on.has("noPersistAuth") && !prod)
    push(`--mount type=bind,source=~/.config/sandbox/agents/${agent},target=/sandbox/home`, "mount", undefined,
      "The sandbox-owned agent home. Separate from your real ~/.claude — and it holds a long-lived OAuth refresh token the agent can read, which is why prod does not mount it at all.");

  if (agent === "claude" && !prod)
    push("--mount type=bind,source=~/.claude/projects/-Users-dev-app,target=/sandbox/home/.claude/projects/-workspace", "mount", undefined,
      "Your host Claude history for this one project, so --resume works on both sides. --no-sync opts out.");

  if (on.has("cache"))
    for (const c of ["npm", "pip", "cargo", "go"])
      push(`--mount type=volume,source=sandbox-cache-${c},target=/sandbox/home/.cache/${c}`, "mount", "cache",
        "A named Docker volume — not a host directory.");

  if (on.has("share"))
    push("--mount type=bind,source=~/.config/sandbox/shared,target=/shared", "mount", "share",
      "The one channel between two sandboxes.");

  if (on.has("paste"))
    for (const d of ["Desktop", "Downloads", "Pictures"])
      push(`--mount type=bind,source=~/${d},target=~/${d},readonly`, "mount", "paste",
        "Read-only: attaching an image never writes.");

  push("-w /workspace", "base");
  push("-e HOME=/sandbox/home", "env", undefined, "HOME is always the fake path — never your host home.");

  if (egress)
    push(
      prod
        ? "-e SANDBOX_EGRESS_ALLOW=api.anthropic.com"
        : "-e SANDBOX_EGRESS_ALLOW=api.anthropic.com,registry.npmjs.org,…",
      "env",
      prod ? "prodProfile" : on.has("allow") ? "allow" : undefined,
      prod
        ? "prod drops the baseline, so this list is the whole allowlist — github.com is a write endpoint and stays off it."
        : "Baseline registries and agent APIs, plus whatever you added.");
  if (on.has("git")) {
    push("-e GIT_AUTHOR_NAME -e GIT_AUTHOR_EMAIL", "env", "git", "Forwarded by name — docker reads the host value at exec time.");
    push("-e GIT_COMMITTER_NAME -e GIT_COMMITTER_EMAIL", "env", "git");
  }
  push("-e ANTHROPIC_API_KEY", "env", undefined, "Forwarded by name, and only if it is actually set on your host.");
  if (on.has("secret"))
    push("-e GITHUB_TOKEN", "env", "secret",
      "Resolved from `gh auth token` at run time. The value is nowhere on this line — not in --dry-run, not in your shell history.");

  push(IMAGE, "image");
  push(agent === "run" ? "bash" : agent, "cmd");

  return lines;
}

/** Count of host paths the container can reach, for the live read-out. */
export function hostReach(agent: string, on: Set<OptionId>): number {
  let n = 1; // /workspace
  if (on.has("worktree")) n += 1;
  if (!on.has("noPersistAuth")) n += 1;
  if (agent === "claude") n += 1;
  if (on.has("share")) n += 1;
  if (on.has("paste")) n += 3;
  return n;
}
