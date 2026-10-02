import { execFile } from "node:child_process";
import { existsSync, readdirSync, realpathSync } from "node:fs";
import { homedir } from "node:os";
import { basename, dirname, isAbsolute, resolve, sep } from "node:path";
import { promisify } from "node:util";

const run = promisify(execFile);

/**
 * Answering "which repository am I in?" from the machine running the script.
 *
 * Every other path in this package belongs to the daemon. This file is the
 * exception, and it is kept separate so that stays visible: what it produces is
 * a fact about *this* machine, offered to the daemon as a guess and never
 * trusted as an answer. A daemon on another host resolves the same string
 * against its own disk and refuses, which is the correct outcome.
 */

/** Where a script is standing, in the two forms that answer different questions. */
export interface LocalRepo {
  /** The repository, as the daemon records it: the main checkout for a linked
   *  worktree, the submodule's own tree for a submodule, the repository itself
   *  for a bare one. */
  root: string;
  /** The working tree the script is actually in, which is the worktree rather
   *  than the main checkout when those differ. Empty for a bare repository.
   *  Kept because a daemon started *inside* a worktree registers its default
   *  project as that worktree, so a lookup that only knew the main root would
   *  miss the one repository that cannot be removed. */
  tree: string;
}

/**
 * The repository containing `from`, asked of git — and asked the way the daemon
 * asks it.
 *
 * The first version walked up looking for a `.git`, which is right often enough
 * to be misleading. Inside a **linked worktree** it answers with the worktree,
 * while the daemon resolves through `--git-common-dir` to the main repository —
 * so a lookup missed a registry entry that was there. Worktrees are where agents
 * work, so the two answers have to be one answer.
 *
 * `--git-common-dir` alone is not that answer either, and the second version
 * learned it the expensive way: for a **submodule** it names
 * `<super>/.git/modules/<name>`, and for `--separate-git-dir` it names the
 * detached git directory. Registering either hands `/workspace` an object store
 * instead of the source. So the rule covers all four layouts git can produce:
 * `<x>/.git` means a checkout or a worktree of `<x>`; anything else means the
 * git directory is not beside its tree, and git is asked to name the tree; only
 * when there is no tree — a bare repository — is the git directory the answer.
 *
 * The `-c` overrides mirror `internal/githard`. `rev-parse` runs no filter, hook
 * or textconv, so this is not the vector that package was written for — but this
 * command's working directory can be a tree an agent has been writing to, and
 * the cost of saying so in advance is nine arguments.
 *
 * Falls back to a `.git` walk when git is not installed or refuses: an answer
 * git cannot give is not a reason for the SDK to have none, and the daemon
 * checks whatever it is handed anyway.
 */
export async function localRepo(from: string): Promise<LocalRepo | null> {
  const dir = realpathish(resolve(from));
  if (!existsSync(dir)) return null;

  const common = await git(dir, "--git-common-dir");
  if (common) {
    const tree = (await git(dir, "--show-toplevel")) ?? "";
    // <main>/.git -> <main>: the checkout, or any linked worktree of it.
    if (basename(common) === ".git") return { root: realpathish(dirname(common)), tree: realpathish(tree) };
    // A git directory that is not beside its tree. The tree is the repository.
    if (tree) return { root: realpathish(tree), tree: realpathish(tree) };
    // Nothing checked out: a bare repository genuinely is its own root.
    return { root: realpathish(common), tree: "" };
  }

  const walked = walkUp(dir);
  return walked ? { root: walked, tree: walked } : null;
}

/**
 * `git init` in a directory that is not in a repository yet.
 *
 * On **this** machine, which is the whole reason the caller has to ask for it:
 * a path names a directory on the daemon's host, and against a remote daemon
 * initialising here creates a repository nobody will ever see while the
 * registration still fails. There is no way to tell the two apart — an SSH
 * tunnel makes a remote daemon answer on loopback — so this stays something you
 * type rather than something that happens to you.
 *
 * `init.templateDir=` for the same reason the read path pins its config: a
 * template directory can carry hooks, and this is the one git command here that
 * *writes*. No initial commit is made and none is needed — git creates an orphan
 * worktree from a commitless repository, and a run in one works (measured).
 */
export async function initRepo(dir: string): Promise<void> {
  if (!existsSync(dir)) throw new Error(`${dir} does not exist, so there is nothing to initialise`);
  await run("git", ["-c", "init.templateDir=", "init", "-q"], { cwd: dir, timeout: 30_000 });
}

/**
 * Whether a repository has files but no commits — the state in which every
 * worktree Studio makes is **empty**.
 *
 * This is the trap `git init` leaves behind, and it is silent: git creates an
 * orphan worktree from a commitless repository, the daemon registers it happily,
 * the run starts, and the agent finds nothing in /workspace. Measured — a
 * directory with `main.py` in it, `git init`, one run: `ls` printed nothing.
 * Nobody would connect the missing code to a missing commit.
 *
 * Reported rather than fixed. `git add -A && git commit` is one line to type and
 * a bad thing to do *for* somebody: in a directory that has never been a
 * repository there is usually no .gitignore yet, so it is exactly where
 * node_modules, a .env and a stray key get committed.
 */
export async function unbornWithFiles(dir: string): Promise<boolean> {
  try {
    await run("git", ["rev-parse", "--verify", "--quiet", "HEAD"], { cwd: dir, timeout: 10_000 });
    return false; // there is a commit, so a worktree has something in it
  } catch {
    // Unborn HEAD. Only a problem if there is something here that would be lost.
  }
  try {
    return readdirSync(dir).some((entry) => entry !== ".git");
  } catch {
    return false;
  }
}

/** Just the repository, for callers that do not care where inside it they are. */
export async function gitRootOf(from: string): Promise<string | null> {
  return (await localRepo(from))?.root ?? null;
}

/**
 * A path as the daemon should receive it: `~` expanded, and absolute.
 *
 * Deliberately **not** symlink-resolved. That resolution is a fact about this
 * machine, and applying it to a value bound for another one rewrites what the
 * user typed into something they never wrote: on macOS `addProject("/tmp/api")`
 * would post `/private/tmp/api`, and a Linux daemon would refuse a path nobody
 * named. Whether it happened at all would depend on whether *this* machine
 * has a same-named directory, which is the coupling this file exists to avoid.
 * The realpath belongs to the comparison instead, where both sides are local.
 *
 * Relative input is resolved against the current directory — the one place this
 * package reads `process.cwd()` for a value somebody typed. `~` is expanded
 * because a shell would have done it before argv, and a TypeScript string
 * literal is the one place it survives to become part of a path.
 */
export function wirePath(path: string, cwd: string): string {
  const expanded =
    path === "~" || path.startsWith(`~${sep}`) || path.startsWith("~/")
      ? resolve(homedir(), path.slice(2))
      : path;
  return isAbsolute(expanded) ? expanded : resolve(cwd, expanded);
}

/** Whether two local paths name the same directory, symlinks included. This is
 *  both sides of a comparison on one machine, which is where a realpath means
 *  something: `/tmp/x` and `/private/tmp/x` are one repository. */
export function samePath(a: string, b: string): boolean {
  return realpathish(a) === realpathish(b);
}

/** The fallback: a `.git` anywhere above. Tested with existsSync rather than as
 *  a directory, because a linked worktree's `.git` is a file. */
function walkUp(from: string): string | null {
  let dir = from;
  for (;;) {
    if (existsSync(resolve(dir, ".git"))) return dir;
    const up = dirname(dir);
    if (up === dir) return null;
    dir = up;
  }
}

/** One `rev-parse` question, or null when git cannot answer it. */
async function git(cwd: string, question: string): Promise<string | null> {
  try {
    const { stdout } = await run(
      "git",
      [
        "-c", "core.hooksPath=/dev/null",
        "-c", "core.attributesFile=/dev/null",
        "-c", "core.fsmonitor=",
        "-c", "core.sshCommand=",
        "-c", "core.askPass=",
        "-c", "credential.helper=",
        "-c", "core.pager=cat",
        "-c", "core.editor=false",
        "-c", "core.alternateRefsCommand=",
        "rev-parse", "--path-format=absolute", question,
      ],
      { cwd, encoding: "utf8", timeout: 10_000 },
    );
    return stdout.trim() || null;
  } catch {
    // git missing, not a repository, or no working tree to name.
    return null;
  }
}

/** realpath, falling back to the input: a path that does not exist here is not
 *  an error — it may exist on the daemon's machine, which is the one that
 *  decides. */
function realpathish(path: string): string {
  try {
    return realpathSync(path);
  } catch {
    return path;
  }
}
