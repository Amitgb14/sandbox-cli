import { spawn } from "node:child_process";
import { mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";

const bin = (name: string) => resolve(__dirname, "../../bin", name);

async function waitFor(fn: () => Promise<boolean>, what: string) {
  const deadline = Date.now() + 20_000;
  while (Date.now() < deadline) {
    if (await fn().catch(() => false)) return;
    await new Promise((r) => setTimeout(r, 200));
  }
  throw new Error(`timed out waiting for ${what}`);
}

export default async function globalSetup() {
  const dir = mkdtempSync(join(tmpdir(), "studio-e2e-"));
  const env = { ...process.env, XDG_CONFIG_HOME: join(dir, "cfg"), SANDBOX_CONTEXT: "e2e" };
  const sandboxd = spawn(bin("sandboxd"), ["--backend", "fake", "--state-dir", join(dir, "state"), "--listen", "127.0.0.1:7180"], { env, stdio: "ignore" });
  await waitFor(async () => (await fetch("http://127.0.0.1:7180/v1/health")).ok, "sandboxd");
  const add = spawn(bin("sandbox-cli"), ["context", "add", "e2e", "http://127.0.0.1:7180"], { env, stdio: "inherit" });
  await new Promise((r) => add.on("exit", r));
  const studio = spawn(bin("sandbox-cli"), ["studio", "--port", "7181", "--ui-dir", resolve(__dirname, "../out")], { env, cwd: dir });
  let out = "";
  studio.stdout.on("data", (b) => (out += b.toString()));
  await waitFor(async () => /token=[0-9a-f]+/.test(out), "studio");
  const token = out.match(/token=([0-9a-f]+)/)![1];
  writeFileSync(join(__dirname, ".state.json"), JSON.stringify({ token, pids: [sandboxd.pid, studio.pid] }));
  process.env.STUDIO_TOKEN = token;
}
