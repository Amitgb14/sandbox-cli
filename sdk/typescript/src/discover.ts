import { readFileSync } from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";

/**
 * Where the daemon is, and what it will accept — without asking anybody to
 * paste anything.
 *
 * `studio.sh` already writes both into ~/.config/sandbox/studio: a generated
 * token, and a `ports` file whose second line is the API port. A script that
 * made the user copy those out of a terminal would be asking for a credential
 * the machine already holds, which is how tokens end up in shell history and
 * committed config.
 *
 * Explicit arguments win, then the environment, then the files. The last of
 * those is the only one that can be wrong about *which* daemon — a laptop with
 * two checkouts has one studio state directory — so it is last.
 */

function studioStateDir(): string {
  const xdg = process.env.XDG_CONFIG_HOME;
  const base = xdg && xdg.length > 0 ? xdg : join(homedir(), ".config");
  return join(base, "sandbox", "studio");
}

function readTrimmed(path: string): string | null {
  try {
    const text = readFileSync(path, "utf8").trim();
    return text.length > 0 ? text : null;
  } catch {
    return null;
  }
}

/** The API port studio.sh recorded, or null when it has never run here. */
export function discoveredPort(): number | null {
  const raw = readTrimmed(join(studioStateDir(), "ports"));
  if (!raw) return null;
  // Two lines: the UI's port, then the API's. Read by position because that is
  // how the file is written; a malformed one answers null rather than a guess.
  const lines = raw.split("\n");
  if (lines.length < 2) return null;
  const port = Number.parseInt(lines[1].trim(), 10);
  return Number.isFinite(port) && port > 0 ? port : null;
}

export function discoverUrl(explicit?: string): string {
  if (explicit) return explicit.replace(/\/+$/, "");
  const fromEnv = process.env.SANDBOX_API_URL;
  if (fromEnv) return fromEnv.replace(/\/+$/, "");
  const port = discoveredPort();
  return `http://127.0.0.1:${port ?? 8787}`;
}

export function discoverToken(explicit?: string): string {
  if (explicit !== undefined) return explicit;
  const fromEnv = process.env.SANDBOX_STUDIO_TOKEN;
  if (fromEnv) return fromEnv;
  return readTrimmed(join(studioStateDir(), "token")) ?? "";
}
