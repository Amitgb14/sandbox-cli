import { existsSync, realpathSync } from "node:fs";
import { dirname, isAbsolute, resolve } from "node:path";

/**
 * Answering "which repository am I in?" from the machine running the script.
 *
 * Every other path in this package belongs to the daemon. These two functions
 * are the exception, and they are kept in their own file so that stays visible:
 * what they produce is a fact about *this* machine, offered to the daemon as a
 * guess, never trusted as an answer. A daemon on another host resolves the same
 * string against its own disk and refuses — which is the correct outcome, and
 * the reason nothing here decides anything on its own.
 */

/**
 * The git repository containing `from`, or null.
 *
 * Walks up because `process.cwd()` is usually a subdirectory — `src/`, `web/`,
 * wherever the script happens to live — and Studio addresses work by branch,
 * which belongs to a repository rather than to whichever directory somebody was
 * standing in. The daemon performs the same resolution on the path it is given;
 * this one exists so that the *lookup* against the registry can compare roots.
 *
 * `.git` is tested with existsSync rather than statted as a directory: a linked
 * worktree's `.git` is a file, and a script run inside one is in a repository by
 * any definition that matters here.
 */
export function gitRootOf(from: string): string | null {
  let dir = realpathish(resolve(from));
  for (;;) {
    if (existsSync(resolve(dir, ".git"))) return dir;
    const up = dirname(dir);
    if (up === dir) return null;
    dir = up;
  }
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
