import { spawn, spawnSync, type ChildProcess } from "node:child_process";
import { randomBytes } from "node:crypto";
import { existsSync, mkdtempSync, readFileSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { GATEWAY_STUDIOS, HOSTED_PORT, HOSTED_USERS, type E2EState } from "./state";

const bin = (name: string) => resolve(__dirname, "../../bin", name);

async function waitFor(fn: () => Promise<boolean>, what: string) {
  const deadline = Date.now() + 20_000;
  while (Date.now() < deadline) {
    if (await fn().catch(() => false)) return;
    await new Promise((r) => setTimeout(r, 200));
  }
  throw new Error(`timed out waiting for ${what}`);
}

/** Starts `sandbox-cli studio` for one context and returns its token. */
async function startStudio(env: NodeJS.ProcessEnv, dir: string, port: number, pids: number[]): Promise<string> {
  const studio = spawn(bin("sandbox-cli"), ["studio", "--port", String(port), "--ui-dir", resolve(__dirname, "../out")], { env, cwd: dir });
  pids.push(studio.pid!);
  let out = "";
  studio.stdout.on("data", (b) => (out += b.toString()));
  await waitFor(async () => /token=[0-9a-f]+/.test(out), `studio on ${port}`);
  return out.match(/token=([0-9a-f]+)/)![1];
}

function cli(env: NodeJS.ProcessEnv, ...args: string[]) {
  const r = spawnSync(bin("sandbox-cli"), args, { env, encoding: "utf8" });
  if (r.status !== 0) throw new Error(`sandbox-cli ${args.join(" ")}: ${r.stderr}`);
}

/**
 * Two things for Studio to talk to:
 *
 * - a plain sandboxd on its in-memory backend, behind the Studio on 7181 —
 *   what every test but the gateway ones uses;
 * - a real sandbox-gateway in front of one more such sandboxd, with SSH and a
 *   secrets key, and one Studio per API key on 7182… (e2e/state.ts says which
 *   key holds which scopes). The keys are made with `sandbox-gateway keys
 *   create` before it serves, as an operator makes the first one.
 */
export default async function globalSetup() {
  const dir = mkdtempSync(join(tmpdir(), "studio-e2e-"));
  const pids: number[] = [];
  const track = (c: ChildProcess) => (pids.push(c.pid!), c);
  const env = { ...process.env, XDG_CONFIG_HOME: join(dir, "cfg"), SANDBOX_CONTEXT: "e2e" };
  // A unix socket, as a local sandboxd serves by default: only its owner can
  // connect, so it needs no token. A TCP port, even on loopback, needs one.
  const sock = join(dir, "d.sock");
  track(spawn(bin("sandboxd"), ["--backend", "fake", "--state-dir", join(dir, "state"), "--listen", `unix://${sock}`], { env, stdio: "ignore" }));
  await waitFor(async () => existsSync(sock), "sandboxd");
  cli(env, "context", "add", "e2e", `unix://${sock}`);
  const token = await startStudio(env, dir, 7181, pids);

  // --- the gateway --------------------------------------------------------------
  const node = join(dir, "n1.sock");
  track(spawn(bin("sandboxd"), ["--backend", "fake", "--node-id", "n1", "--state-dir", join(dir, "n1"), "--listen", `unix://${node}`], { env, stdio: "ignore" }));
  await waitFor(async () => existsSync(node), "the gateway's node");
  const state = join(dir, "gw", "state.json");
  const gw = (...args: string[]) => {
    const r = spawnSync(bin("sandbox-gateway"), ["--state", state, ...args], { encoding: "utf8" });
    if (r.status !== 0) throw new Error(`sandbox-gateway ${args.join(" ")}: ${r.stderr}`);
    return r.stdout;
  };
  gw("nodes", "add", "n1", `unix://${node}`);
  const secretsKey = join(dir, "secrets.key");
  writeFileSync(secretsKey, randomBytes(32), { mode: 0o600 });

  // The hosted Studio's users, with invite links, before the gateway serves.
  const hostedUI = resolve(__dirname, "../out-hosted");
  const hostedBase = `http://127.0.0.1:${HOSTED_PORT}`;
  let hosted: E2EState["hosted"];
  if (existsSync(join(hostedUI, "index.html"))) {
    hosted = {} as NonNullable<E2EState["hosted"]>;
    for (const user of HOSTED_USERS) {
      const out = gw("keys", "create", "--user", user, "--tenant", user, "--scope", "sandbox:read", "--scope", "sandbox:create", "--scope", "sandbox:delete", "--invite-url", hostedBase);
      hosted[user] = { invite: out.match(/^invite: (\S+)$/m)![1], key: out.match(/^secret: (\S+)$/m)![1], id: out.match(/^id: +(\S+)$/m)![1] };
    }
  }

  const keys: Record<string, string> = {};
  for (const s of GATEWAY_STUDIOS) {
    const out = gw("keys", "create", "--user", s.user, ...(s.tenant ? ["--tenant", s.tenant] : []), ...s.scopes.flatMap((x) => ["--scope", x]));
    const keyFile = join(dir, `${s.name}.key`);
    writeFileSync(keyFile, out.match(/^secret: (\S+)$/m)![1] + "\n", { mode: 0o600 });
    keys[s.name] = keyFile;
  }
  const api = "127.0.0.1:7190";
  track(spawn(bin("sandbox-gateway"), ["--state", state, "serve", "--listen", api, "--ssh-listen", "127.0.0.1:7191", "--secrets-key-file", secretsKey], { env, stdio: "ignore" }));
  await waitFor(async () => (await fetch(`http://${api}/v1/health`)).ok, "sandbox-gateway");

  const gateway: E2EState["gateway"] = {};
  for (const s of GATEWAY_STUDIOS) {
    cli(env, "context", "add", s.name, `http://${api}`, "--token-file", keys[s.name]);
    gateway[s.name] = await startStudio({ ...env, SANDBOX_CONTEXT: s.name }, dir, s.port, pids);
  }

  if (hosted) {
    const h = spawn(bin("sandbox-cli"), ["studio", "host", "--gateway", `http://${api}`, "--public-url", hostedBase, "--listen", `127.0.0.1:${HOSTED_PORT}`, "--ui-dir", hostedUI], { env, stdio: "ignore" });
    pids.push(h.pid!);
    await waitFor(async () => (await fetch(`${hostedBase}/`)).ok, "the hosted Studio");
  }

  const adminKey = readFileSync(keys["gw-admin"], "utf8").trim();
  const st: E2EState = { token, gateway, pids, dir, hosted, gatewayURL: `http://${api}`, adminKey };
  writeFileSync(join(__dirname, ".state.json"), JSON.stringify(st));
  process.env.STUDIO_TOKEN = token;
}
