import { readFileSync, rmSync } from "node:fs";
import { join } from "node:path";

export default async function globalTeardown() {
  const file = join(__dirname, ".state.json");
  const { pids } = JSON.parse(readFileSync(file, "utf8")) as { pids: number[] };
  for (const pid of pids) {
    try {
      process.kill(pid);
    } catch {
      // already gone
    }
  }
  rmSync(file, { force: true });
}
