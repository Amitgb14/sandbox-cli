import { execFileSync } from "node:child_process";
import { existsSync, realpathSync } from "node:fs";
import { basename, dirname, isAbsolute, resolve } from "node:path";

/**
 * Answering "which repository am I in?" from the machine running the script.
 *
 * Every other path in this package belongs to the daemon. This file is the
 * exception, and it is kept separate so that stays visible: what it produces is
 * a fact about *this* machine, offered to the daemon as a guess and never
 * trusted as an answer. A daemon on another host resolves the same string
 * against its own disk and refuses, which is the correct outcome.
 */

/**
 * The repository containing `from`, asked of git — and asked the way the daemon
 * asks it.
 *
 * The first version walked up looking for a `.git`, which is right often enough
 * to be misleading. Inside a **linked worktree** it answers with the worktree,
 * while the daemon resolves the same path through `--git-common-dir` to the
 * *main* repository — so a lookup would miss a registry entry that is there, and
 * `addProject` would report registering something other than what was asked for.
 * Worktrees are where agents work, so the two answers have to be one answer.
 *
 * `--git-common-dir` also settles the cases the walk gets wrong on its own: a
 * `.git` that is a file, `GIT_DIR` set in the environment, a bare repository,
 * and a stray `.git` in a directory that is not a repository at all.
 *
 * The `-c` overrides mirror `internal/githard`. `rev-parse` runs no filter, hook
 * or textconv, so this is not the vector that motivated that package — but this
 * command's working directory can be a tree an agent has been writing to, and
 * the cost of saying so in advance is nine arguments.
 *
 * Falls back to the walk when git is not installed or refuses: an answer git
 * cannot give is not a reason for the SDK to have none, and the daemon checks
 * whatever it is handed anyway.
 */
export function gitRootOf(from: string): string | null {
  const dir = realpathish(resolve(from));
  if (!existsSync(dir)) return null;
  try {
    const out = execFileSync(
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
        "rev-parse", "--path-format=absolute", "--git-common-dir",
      ],
      { cwd: dir, encoding: "utf8", stdio: ["ignore", "pipe", "ignore"], timeout: 10_000 },
    ).trim();
    // <main>/.git -> <main>; a bare repository answers with itself.
    if (out) return realpathish(basename(out) === ".git" ? dirname(out) : out);
  } catch {
    // git missing, or this is not a repository. The walk decides which.
  }
  return walkUp(dir);
}

/**
 * A path as the daemon should receive it: absolute, and symlink-resolved.
 *
 * Relative input is resolved against the current directory — the one place this
 * package reads `process.cwd()`, and only ever to expand what somebody typed
 * rather than to supply a value they did not.
 *
 * The realpath matters for the comparison rather than for the daemon: on macOS
 * the same repository is `/tmp/x` and `/private/tmp/x`, and a registry lookup
 * comparing the two forms concludes the repository is not registered.
 */
export function absolutePath(path: string, cwd: string): string {
  return realpathish(isAbsolute(path) ? path : resolve(cwd, path));
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
