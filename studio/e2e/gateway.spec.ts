import { generateKeyPairSync } from "node:crypto";
import { expect, test, type Page } from "@playwright/test";
import { e2eState, gatewayStudio, HOSTED_PORT, type GatewayStudio } from "./state";

/**
 * Studio in front of a real sandbox-gateway (e2e/global-setup.ts), one Studio
 * per API key: what each key sees, what it is offered, and that a screen it
 * may not have makes no request for it. Hiding is cosmetic — the gateway's
 * 403 is the control, and internal/gateway's tests pin that — so these pin
 * that Studio does not offer what will be refused, and asks for nothing the
 * caller may not see.
 */

const TENANT = ["Jobs", "Services", "Secrets", "SSH", "Account"];
const ADMIN = ["Nodes", "Lost sandboxes", "Users & keys", "Allowlist groups", "Audit", "All organizations"];
const ADMIN_ROUTES = ["/admin/nodes/", "/admin/lost/", "/admin/keys/", "/admin/egress-groups/", "/admin/audit/", "/admin/orgs/"];

/** Opens a gateway Studio with its token, once whoami has answered. */
async function open(page: Page, name: GatewayStudio, user: string) {
  const { base, token } = gatewayStudio(name);
  await page.goto(`${base}/#token=${token}`);
  await expect(page.locator("[data-sidebar=sidebar]").getByText(`${name} · ${user}`)).toBeVisible();
  return base;
}

/** Every /api/v1 path the page asks for from now on. */
function record(page: Page): string[] {
  const seen: string[] = [];
  page.on("request", (r) => {
    const u = new URL(r.url());
    if (u.pathname.startsWith("/api/v1/")) seen.push(u.pathname);
  });
  return seen;
}

function nav(page: Page) {
  return page.locator("[data-sidebar=sidebar]");
}

/** An OpenSSH public key line, made here so the test needs no ssh-keygen. */
function sshPublicKey(): string {
  const der = generateKeyPairSync("ed25519").publicKey.export({ format: "der", type: "spki" });
  const raw = der.subarray(der.length - 32);
  const str = (b: Buffer) => Buffer.concat([Buffer.from([0, 0, 0, b.length]), b]);
  return `ssh-ed25519 ${Buffer.concat([str(Buffer.from("ssh-ed25519")), str(raw)]).toString("base64")} e2e@studio`;
}

test("a plain sandboxd shows no gateway screens and asks for none", async ({ page }) => {
  const { token } = e2eState();
  const seen = record(page);
  await page.goto(`/#token=${token}`);
  await expect(page.getByText("e2e · fake")).toBeVisible();
  for (const name of [...TENANT, ...ADMIN, "Members"]) {
    await expect(nav(page).getByRole("link", { name, exact: true })).toHaveCount(0);
  }
  // No organisations on a plain sandboxd: no switcher, and nothing asked.
  await expect(page.getByTestId("org-switcher")).toHaveCount(0);
  // The sandboxd's own screens are all there, with their actions.
  await expect(page.getByRole("link", { name: "Create sandbox" }).first()).toBeVisible();
  for (const path of ["/jobs/", "/secrets/", "/members/", ...ADMIN_ROUTES]) {
    await page.goto(path);
    await expect(page.getByRole("heading", { name: "Not available" })).toBeVisible();
  }
  // whoami, which answered 404, is the only gateway endpoint it asked for.
  expect(seen.filter((p) => /^\/api\/v1\/(jobs|services|secrets|ssh|admin|orgs)/.test(p))).toEqual([]);
});

test("a tenant key sees the tenant screens and not the admin ones, by sidebar or by URL", async ({ page }) => {
  const seen = record(page);
  const base = await open(page, "gw-tenant", "alice@team-a");
  for (const name of TENANT) await expect(nav(page).getByRole("link", { name, exact: true })).toBeVisible();
  for (const name of ADMIN) await expect(nav(page).getByRole("link", { name, exact: true })).toHaveCount(0);
  for (const path of ADMIN_ROUTES) {
    await page.goto(base + path);
    await expect(page.getByRole("heading", { name: "Not available" })).toBeVisible();
  }
  expect(seen.filter((p) => p.startsWith("/api/v1/admin"))).toEqual([]);
  // A node's images are its operator's: no gateway key is offered them yet.
  await expect(nav(page).getByRole("link", { name: "Images", exact: true })).toHaveCount(0);
  await page.goto(`${base}/images/`);
  await expect(page.getByText("a gateway does not offer it")).toBeVisible();
  expect(seen.filter((p) => p.startsWith("/api/v1/images"))).toEqual([]);

  await page.goto(`${base}/account/`);
  await expect(page.locator("main").getByText("alice", { exact: true })).toBeVisible();
  await expect(page.locator("main").getByText("team-a", { exact: true }).first()).toBeVisible();
  // With nothing selected, the organization is the key's own tenant.
  await expect(page.getByTestId("account-org")).toHaveText("team-a");
});

test("the sandbox list on a gateway sums what was given, and never asks for a node's status", async ({ page }) => {
  const base = await open(page, "gw-tenant", "alice@team-a");
  const seen = record(page);
  await page.goto(`${base}/sandboxes/`);
  await expect(page.locator("main h1")).toHaveText("Sandboxes");
  await page.waitForTimeout(1500);
  // Totals from the list, with no capacity to measure them against.
  if (await page.getByText("vCPU given").count()) {
    await expect(page.getByRole("meter")).toHaveCount(0);
  }
  expect(seen.filter((p) => p === "/api/v1/node")).toEqual([]);
});

test("a tenant submits a job and reads its output", async ({ page }) => {
  const base = await open(page, "gw-tenant", "alice@team-a");
  await page.goto(`${base}/jobs/`);
  await page.getByRole("button", { name: "Submit job" }).click();
  await page.getByLabel("Name").fill("e2e-job");
  await page.getByLabel("Command").fill("echo hello-from-a-job");
  await page.getByRole("button", { name: "Submit", exact: true }).click();
  await expect(page).toHaveURL(/\/job\/?\?id=job_/);
  await expect(page.getByRole("heading", { name: "e2e-job" })).toBeVisible();
  // Exact: the page also shows the command, "echo hello-from-a-job". A
  // substring match found that alone before the output arrived, passing
  // without it, and both after, failing strict mode.
  await expect(page.getByText("hello-from-a-job", { exact: true })).toBeVisible();
  await expect(page.locator("main").getByText("Succeeded").first()).toBeVisible();
  await page.goto(`${base}/jobs/`);
  await expect(page.getByRole("link", { name: /e2e-job/ })).toBeVisible();
});

test("a job is submitted with labels, and the list filters by them", async ({ page }) => {
  const base = await open(page, "gw-tenant", "alice@team-a");
  await page.goto(`${base}/jobs/`);
  await page.getByRole("button", { name: "Submit job" }).click();
  await page.getByLabel("Name").fill("e2e-labelled");
  await page.getByLabel("Command").fill("echo labelled");
  const labels = page.getByLabel("Labels");
  await labels.fill("team=e2e");
  await labels.press("Enter");
  await labels.fill("nightly");
  await labels.press("Enter");
  // One the gateway would refuse is marked, and stops the submit.
  await labels.fill("Bad Key");
  await labels.press("Enter");
  await page.getByRole("button", { name: "Submit", exact: true }).click();
  await expect(page).toHaveURL(/\/jobs\/?$/);
  await page.getByRole("button", { name: "Remove Key" }).click();
  await page.getByRole("button", { name: "Remove Bad" }).click();
  await page.getByRole("button", { name: "Submit", exact: true }).click();
  await expect(page).toHaveURL(/\/job\/?\?id=job_/);
  await expect(page.locator("main").getByText("team=e2e")).toBeVisible();

  await page.goto(`${base}/jobs/`);
  await expect(page.getByRole("link", { name: /e2e-labelled/ })).toBeVisible();
  await page.getByRole("button", { name: "Labels" }).click();
  await page.getByRole("option", { name: /team=e2e/ }).click();
  await page.keyboard.press("Escape");
  await expect(page.getByRole("link", { name: /e2e-labelled/ })).toBeVisible();
  await expect(page.locator("tbody tr")).toHaveCount(1);
  await page.getByRole("button", { name: "Reset" }).click();
  await page.getByLabel("Search jobs").fill("no-such-job-anywhere");
  await expect(page.getByText("No job matches.")).toBeVisible();
});

/**
 * A command line using the shell runs in one, and a run that never started
 * says why in plain sight. The fake backend has no sh, so the run fails, as
 * a missing program does on a real one — which is what the box is for.
 */
test("a shell command runs as sh -c, and a failed run says why", async ({ page }) => {
  // The default image is built on the node once anything has started from
  // it, and the gateway lists it after its next poll of the node. The page
  // asks once, so wait for the list here rather than count on an earlier
  // test having run long enough before this one.
  const { gatewayURL, adminKey } = e2eState();
  const admin = (method: string, path: string, body?: object) =>
    fetch(`${gatewayURL}${path}`, { method, headers: { Authorization: `Bearer ${adminKey}`, "Content-Type": "application/json" }, body: body && JSON.stringify(body) });
  const sb = (await (await admin("POST", "/v1/sandboxes", {})).json()) as { id: string };
  await admin("DELETE", `/v1/sandboxes/${sb.id}`);
  await expect
    .poll(async () => ((await (await admin("GET", "/v1/fleet/images")).json()) as { images: unknown[] }).images.length, { timeout: 20_000 })
    .toBeGreaterThan(0);
  const base = await open(page, "gw-tenant", "alice@team-a");
  await page.goto(`${base}/jobs/`);
  await page.getByRole("button", { name: "Submit job" }).click();
  // The image picker offers what the gateway's nodes have built.
  await page.getByRole("combobox", { name: "Image" }).click();
  await expect(page.getByRole("group", { name: "Built on the gateway's nodes" })).toBeVisible({ timeout: 15_000 });
  await page.keyboard.press("Escape");
  await page.getByLabel("Name").fill("e2e-loop");
  await page.getByLabel("Command").fill('for i in 1 2 3; do echo "n $i"; done');
  await expect(page.getByTestId("runs-in-shell")).toBeVisible();
  await page.getByRole("button", { name: "Submit", exact: true }).click();
  await expect(page).toHaveURL(/\/job\/?\?id=job_/);
  await expect(page.locator("main").getByText(/^sh -c 'for i in 1 2 3; do echo "n \$i"; done'$/)).toBeVisible();
  const why = page.getByRole("alert").filter({ hasText: "Why it failed" });
  await expect(why).toBeVisible({ timeout: 15_000 });
  await expect(why).toContainText("no such command");
  await expect(page.getByText("The command never started, so there is no output.")).toBeVisible();
});

test("a tenant sets a secret it can never read back", async ({ page }) => {
  const base = await open(page, "gw-tenant", "alice@team-a");
  await page.goto(`${base}/secrets/`);
  await page.getByLabel("Secret name").fill("E2E_TOKEN");
  await page.getByLabel("Secret value").fill("value-that-stays-put");
  await page.getByRole("button", { name: "Set secret" }).click();
  await expect(page.getByRole("cell", { name: "E2E_TOKEN" })).toBeVisible();
  await expect(page.getByLabel("Secret value")).toHaveValue("");
  await page.reload();
  await expect(page.getByRole("cell", { name: "E2E_TOKEN" })).toBeVisible();
  expect(await page.content()).not.toContain("value-that-stays-put");
});

test("a secret named for its purpose is set in a job as the variable the job says", async ({ page }) => {
  const base = await open(page, "gw-tenant", "alice@team-a");
  await page.goto(`${base}/secrets/`);
  await page.getByLabel("Secret name").fill("e2e-openai-prod");
  await page.getByLabel("Secret value").fill("mapped-value-stays-put");
  await page.getByRole("button", { name: "Set secret" }).click();
  await expect(page.getByRole("cell", { name: "e2e-openai-prod" })).toBeVisible();

  await page.goto(`${base}/jobs/`);
  await page.getByRole("button", { name: "Submit job" }).click();
  await page.getByLabel("Name").fill("e2e-mapped");
  await page.getByLabel("Command").fill("printenv OPENAI_API_KEY");
  await page.getByLabel("Secrets").fill("OPENAI_API_KEY=e2e-openai-prod");
  await page.getByRole("button", { name: "Submit", exact: true }).click();
  await expect(page).toHaveURL(/\/job\/?\?id=job_/);
  await expect(page.locator("main").getByText("OPENAI_API_KEY=e2e-openai-prod")).toBeVisible();
  expect(await page.content()).not.toContain("mapped-value-stays-put");

  // A reserved variable is refused, and the gateway says why.
  await page.goto(`${base}/jobs/`);
  await page.getByRole("button", { name: "Submit job" }).click();
  await page.getByLabel("Command").fill("true");
  await page.getByLabel("Secrets").fill("LD_PRELOAD=e2e-openai-prod");
  await page.getByRole("button", { name: "Submit", exact: true }).click();
  await expect(page.getByText(/LD_PRELOAD is reserved/)).toBeVisible();
});

// The examples a deploy starts from are specs the gateway takes.
test("the service examples, a Git repository's among them, are specs the gateway accepts", async ({ page }) => {
  const base = await open(page, "gw-tenant", "alice@team-a");
  await page.goto(`${base}/services/`);
  await page.getByRole("button", { name: "Deploy service" }).click();
  await page.getByRole("button", { name: "From a Git repository" }).click();
  await expect(page.getByLabel("Service spec")).toHaveValue(/git clone --depth 1 https:\/\/github\.com\/OWNER\/REPO/);
  const spec = JSON.parse(await page.getByLabel("Service spec").inputValue());
  spec.name = "e2e-git";
  await page.getByLabel("Service spec").fill(JSON.stringify(spec));
  await page.getByRole("button", { name: "Deploy", exact: true }).click();
  await expect(page).toHaveURL(/\/service\/?\?name=e2e-git/);
  page.once("dialog", (d) => d.accept());
  await page.getByRole("button", { name: "Remove" }).click();
  await expect(page).toHaveURL(/\/services\/?$/);
});

test("a tenant deploys, scales and removes a service", async ({ page }) => {
  const base = await open(page, "gw-tenant", "alice@team-a");
  await page.goto(`${base}/services/`);
  await page.getByRole("button", { name: "Deploy service" }).click();
  await page.getByLabel("Service spec").fill(JSON.stringify({ name: "e2e-web", command: ["sleep", "1000"], replicas: 1 }));
  await page.getByRole("button", { name: "Deploy", exact: true }).click();
  await expect(page).toHaveURL(/\/service\/?\?name=e2e-web/);
  await expect(page.getByRole("heading", { name: "e2e-web" })).toBeVisible();
  await expect(page.getByText("1/1 ready")).toBeVisible({ timeout: 15_000 });
  await page.getByLabel("Replicas").fill("2");
  await page.getByRole("button", { name: "Scale" }).click();
  await expect(page.getByText("2/2 ready")).toBeVisible({ timeout: 15_000 });
  await expect(page.locator("main").getByText("Healthy")).toHaveCount(2);
  // The list finds it by search and by health, and says when nothing matches.
  await page.goto(`${base}/services/`);
  await page.getByLabel("Search services").fill("e2e-web");
  await expect(page.getByRole("link", { name: /e2e-web/ })).toBeVisible();
  await page.getByRole("button", { name: "Health" }).click();
  await page.getByRole("option", { name: /Unhealthy/ }).click();
  await page.keyboard.press("Escape");
  await expect(page.getByText("No service matches.")).toBeVisible();
  await page.getByRole("button", { name: "Reset" }).click();
  await page.getByRole("link", { name: /e2e-web/ }).click();
  await expect(page).toHaveURL(/\/service\/?\?name=e2e-web/);
  page.once("dialog", (d) => d.accept());
  await page.getByRole("button", { name: "Remove" }).click();
  await expect(page).toHaveURL(/\/services\/?$/);
});

test("a tenant edits a service's spec and redeploys it, and its env is kept", async ({ page }) => {
  const base = await open(page, "gw-tenant", "alice@team-a");
  await page.goto(`${base}/services/`);
  await page.getByRole("button", { name: "Deploy service" }).click();
  await page.getByLabel("Service spec").fill(JSON.stringify({ name: "e2e-edit", command: ["sleep", "1000"], replicas: 1, env: { KEPT: "env-value-never-shown" } }));
  await page.getByRole("button", { name: "Deploy", exact: true }).click();
  await expect(page).toHaveURL(/\/service\/?\?name=e2e-edit/);
  await expect(page.getByText("1/1 ready")).toBeVisible({ timeout: 15_000 });
  await expect(page.getByText("· revision 1")).toBeVisible();

  await page.getByRole("button", { name: "Edit spec" }).click();
  const editor = page.getByLabel("Edit service spec");
  const spec = JSON.parse(await editor.inputValue());
  expect(spec.env).toBeUndefined();
  await expect(page.getByText(/The env \(KEPT\) is kept as it is/)).toBeVisible();
  spec.command = ["sleep", "2000"];
  await editor.fill(JSON.stringify(spec));
  await page.getByRole("button", { name: "Save and roll out" }).click();
  await expect(page.getByText("· revision 2")).toBeVisible();
  await expect(page.locator("main").getByText("sleep 2000", { exact: true })).toBeVisible();
  await expect(page.locator("main").getByText("KEPT")).toBeVisible();

  await page.getByRole("button", { name: "Redeploy" }).click();
  await expect(page.getByText("· revision 3")).toBeVisible();
  await expect(page.getByText("1/1 ready")).toBeVisible({ timeout: 15_000 });
  expect(await page.content()).not.toContain("env-value-never-shown");
  page.once("dialog", (d) => d.accept());
  await page.getByRole("button", { name: "Remove" }).click();
  await expect(page).toHaveURL(/\/services\/?$/);
});

test("a tenant adds an SSH key and issues an access token shown once", async ({ page, request }) => {
  const { base, token } = gatewayStudio("gw-tenant");
  const sb = await (
    await request.fetch(`${base}/api/v1/sandboxes`, { method: "POST", data: { name: "e2e-ssh" }, headers: { Authorization: `Bearer ${token}` } })
  ).json();
  await open(page, "gw-tenant", "alice@team-a");
  await page.goto(`${base}/ssh/`);
  await expect(page.locator("main").getByText("ssh -p 7191 SANDBOX@127.0.0.1")).toBeVisible();
  await page.getByLabel("Public key").fill(sshPublicKey());
  await page.getByRole("button", { name: "Add key" }).click();
  await expect(page.getByRole("cell", { name: /^SHA256:/ })).toBeVisible();

  await page.getByRole("combobox", { name: "Sandbox" }).click();
  await page.getByRole("option", { name: "e2e-ssh" }).click();
  await page.getByRole("button", { name: "Issue token" }).click();
  await expect(page.getByText(/Shown once/)).toBeVisible();
  await expect(page.locator("main code").filter({ hasText: /ssh -p 7191 sgt_/ })).toBeVisible();
  await page.getByRole("button", { name: "Done" }).click();
  await expect(page.locator("main").getByText(/sgt_/)).toHaveCount(0);
  await request.fetch(`${base}/api/v1/sandboxes/${sb.id}`, { method: "DELETE", headers: { Authorization: `Bearer ${token}` } });
});

test("a read-only key is offered no create, secret-write or ssh actions", async ({ page }) => {
  const base = await open(page, "gw-readonly", "bob@team-a");
  for (const name of ["Jobs", "Services", "Secrets", "SSH"]) await expect(nav(page).getByRole("link", { name, exact: true })).toBeVisible();
  for (const name of ["Playground", ...ADMIN]) await expect(nav(page).getByRole("link", { name, exact: true })).toHaveCount(0);
  await expect(page.locator("main h1")).toHaveText("Sandboxes");
  await expect(page.getByRole("link", { name: "Create sandbox" })).toHaveCount(0);

  await page.goto(`${base}/launch/`);
  await expect(page.getByRole("heading", { name: "Not available" })).toBeVisible();
  await page.goto(`${base}/jobs/`);
  await expect(page.locator("main h1")).toHaveText("Jobs");
  await expect(page.getByRole("button", { name: "Submit job" })).toHaveCount(0);
  await page.goto(`${base}/services/`);
  await expect(page.locator("main h1")).toHaveText("Services");
  await expect(page.getByRole("button", { name: "Deploy service" })).toHaveCount(0);
  // The tenant's secrets are listed by name — alice set this one — with nothing to change them.
  await page.goto(`${base}/secrets/`);
  await expect(page.getByRole("cell", { name: "E2E_TOKEN" })).toBeVisible();
  await expect(page.getByLabel("Secret value")).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Remove" })).toHaveCount(0);
  await page.goto(`${base}/ssh/`);
  await expect(page.getByText(/no sandbox:ssh scope/)).toBeVisible();
  await expect(page.getByRole("button", { name: "Add key" })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Issue token" })).toHaveCount(0);
  await page.goto(`${base}/volumes/`);
  await expect(page.getByRole("button", { name: "Create" })).toHaveCount(0);
});

test("an admin key sees the admin screens, and no node endpoint", async ({ page }) => {
  const base = await open(page, "gw-admin", "ops");
  for (const name of [...TENANT, ...ADMIN]) await expect(nav(page).getByRole("link", { name, exact: true })).toBeVisible();

  await nav(page).getByRole("link", { name: "Nodes", exact: true }).click();
  await expect(page.locator("main h1")).toHaveText("Nodes");
  await expect(page.getByRole("cell", { name: "n1", exact: true })).toBeVisible();
  await expect(page.locator("main").getByText("Healthy")).toBeVisible();
  const html = await page.content();
  expect(html).not.toContain("unix://");
  expect(html).not.toContain(e2eState().dir);
  await page.getByRole("button", { name: "Actions for n1" }).click();
  await page.getByRole("menuitem", { name: "Cordon" }).click();
  // Exact: the page's description says "A cordoned node…", and a substring
  // match found it and the badge both, failing before the uncordon below —
  // which left the node cordoned for every test after this one.
  await expect(page.locator("main").getByText("Cordoned", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Actions for n1" }).click();
  await page.getByRole("menuitem", { name: "Uncordon" }).click();
  await expect(page.locator("main").getByText("Healthy")).toBeVisible();

  await page.goto(`${base}/admin/lost/`);
  await expect(page.getByText("Nothing lost")).toBeVisible();

  await page.goto(`${base}/admin/keys/`);
  await expect(page.getByRole("cell", { name: /alice/ }).first()).toBeVisible();
  await page.getByLabel("User", { exact: true }).fill("carol");
  await page.getByRole("button", { name: "Issue key" }).click();
  const secret = page.getByTestId("new-key-secret");
  await expect(secret).toHaveText(/^sgk_/);
  const value = (await secret.textContent())!;
  await page.getByRole("button", { name: "Done" }).click();
  await expect(page.getByTestId("new-key-secret")).toHaveCount(0);
  expect(await page.content()).not.toContain(value);
  const row = page.getByRole("row").filter({ hasText: "carol" });
  await expect(row).toBeVisible();
  page.once("dialog", (d) => d.accept());
  await row.getByRole("button", { name: "Revoke" }).click();
  await expect(row.getByText("revoked")).toBeVisible();

  await page.goto(`${base}/admin/audit/`);
  await expect(page.getByRole("cell", { name: "GET /v1/whoami" }).first()).toBeVisible();
});

test("an admin gives a user a policy, and moves a sandbox's end both ways", async ({ page }) => {
  const { gatewayURL } = e2eState();
  const base = await open(page, "gw-admin", "ops");
  await page.goto(`${base}/admin/keys/`);
  await page.getByLabel("Policy name").fill("demo");
  await page.getByLabel("Max lifetime in minutes").fill("30");
  await page.getByRole("button", { name: "Save policy" }).click();
  await expect(page.getByRole("cell", { name: "30 min" })).toBeVisible();

  await page.getByLabel("User", { exact: true }).fill("pia");
  await page.getByLabel("Tenant", { exact: true }).fill("pia");
  await page.getByRole("combobox", { name: "Policy", exact: true }).click();
  await page.getByRole("option", { name: "demo" }).click();
  await page.getByRole("button", { name: "Issue key" }).click();
  const key = (await page.getByTestId("new-key-secret").textContent())!;
  await page.getByRole("button", { name: "Done" }).click();
  await expect(page.getByRole("row").filter({ hasText: "pia" }).getByRole("combobox")).toHaveText("demo");

  // pia's sandbox gets the policy's lifetime, and she may not push it out.
  const as = (method: string, path: string, body?: object) =>
    fetch(`${gatewayURL}${path}`, { method, headers: { Authorization: `Bearer ${key}`, "Content-Type": "application/json" }, body: body && JSON.stringify(body) });
  const sb = (await (await as("POST", "/v1/sandboxes", { name: "pia-box" })).json()) as { id: string; lifetime_secs: number };
  expect(sb.lifetime_secs).toBe(1800);
  expect((await as("PATCH", `/v1/sandboxes/${sb.id}`, { lifetime_secs: 3600 })).status).toBe(403);

  // The admin's list says whose it is and when it ends.
  await page.goto(`${base}/sandboxes/`);
  const row = page.getByRole("row").filter({ hasText: "pia-box" });
  await expect(row.getByRole("cell", { name: "pia", exact: true })).toBeVisible();
  await expect(row.getByText(/^in (29|30)m/)).toBeVisible();

  // The admin may move it, from the sandbox's page.
  await page.goto(`${base}/sandbox/?id=${sb.id}`);
  await expect(page.getByText("Ends")).toBeVisible();
  await expect(page.getByText("Owner", { exact: true })).toBeVisible();
  await page.getByRole("button", { name: "Extend lifetime by 15 minutes" }).click();
  await expect.poll(async () => ((await (await as("GET", `/v1/sandboxes/${sb.id}`)).json()) as { lifetime_secs: number }).lifetime_secs).toBe(2700);
  await page.getByRole("button", { name: "Shorten lifetime by 15 minutes" }).click();
  await expect.poll(async () => ((await (await as("GET", `/v1/sandboxes/${sb.id}`)).json()) as { lifetime_secs: number }).lifetime_secs).toBe(1800);
  // The one node is shared with every other test: give its room back.
  await as("DELETE", `/v1/sandboxes/${sb.id}`);
});

/**
 * The gateway's allowlist groups: an admin makes one on its screen, and a
 * hosted user — whose Playground reads the gateway's groups, not a local
 * Studio's — picks it, and the sandbox gets its hosts. Only an admin is
 * offered the screen; a user is told an admin keeps the groups.
 */
test("an admin makes an allowlist group, and a hosted user picks it in the Playground", async ({ page, browser }) => {
  const base = await open(page, "gw-admin", "ops");
  await nav(page).getByRole("link", { name: "Allowlist groups", exact: true }).click();
  await expect(page.locator("main h1")).toHaveText("Allowlist groups");
  await page.getByRole("button", { name: "New group" }).click();
  await page.getByLabel("Group name").fill("e2e-go");
  await page.getByLabel("Group description").fill("Go modules");
  await page.getByLabel("Group hosts").fill("proxy.golang.org, sum.golang.org");
  await page.getByRole("button", { name: "Create", exact: true }).click();
  const card = page.getByRole("group", { name: "Group e2e-go" });
  await expect(card.getByRole("list", { name: "e2e-go hosts" })).toContainText("proxy.golang.org");
  // No default switch: a gateway group is only ever in a launch that names it.
  await expect(card.getByRole("switch")).toHaveCount(0);
  await card.getByLabel("Add a host to e2e-go").fill("golang.org");
  await card.getByRole("button", { name: "Add" }).click();
  await expect(card.getByText("3 hosts")).toBeVisible();
  await card.getByRole("button", { name: "Remove sum.golang.org from e2e-go" }).click();
  await expect(card.getByText("2 hosts")).toBeVisible();

  const { hosted: h, gatewayURL, adminKey } = e2eState();
  if (!h) {
    await fetch(`${gatewayURL}/v1/admin/egress-groups/e2e-go`, { method: "DELETE", headers: { Authorization: `Bearer ${adminKey}` } });
    test.skip(true, "no out-hosted/: run npm run build:hosted first (npm run check does)");
  }
  const hostedBase = `http://127.0.0.1:${HOSTED_PORT}`;
  const user = await browser.newPage();
  await user.goto(h!.hana.invite);
  await expect(user.locator("[data-sidebar=sidebar]").getByText("hosted · hana@hana")).toBeVisible();
  await user.goto(`${hostedBase}/launch/`);
  await user.locator("label[for=kind-command]").click();
  await user.getByRole("textbox", { name: "Command" }).fill("echo grouped");
  await user.locator("label[for=net-allowlist]").click();
  await expect(user.getByText("Kept by an admin")).toBeVisible();
  await expect(user.getByRole("link", { name: "Manage groups" })).toHaveCount(0);
  const pick = user.getByRole("checkbox", { name: "Group e2e-go" });
  await expect(pick).toHaveAttribute("aria-checked", "false");
  await pick.click();
  await expect(pick).toHaveAttribute("aria-checked", "true");
  await user.getByText(/^Reaches \d+ hosts?/).click();
  await expect(user.getByText(/proxy\.golang\.org/).first()).toBeVisible();
  // The launch names the group; the gateway puts its hosts in the allowlist.
  await user.getByRole("button", { name: "Launch", exact: true }).click();
  await expect(user).toHaveURL(/\/sandbox\/\?id=sbx_/);
  const id = new URL(user.url()).searchParams.get("id")!;
  const as = (method: string, path: string) => fetch(`${gatewayURL}${path}`, { method, headers: { Authorization: `Bearer ${h!.hana.key}` } });
  const got = (await (await as("GET", `/v1/sandboxes/${id}`)).json()) as { network: { mode: string; allow?: string[] } };
  expect(got.network.mode).toBe("allowlist");
  expect(got.network.allow).toEqual(expect.arrayContaining(["proxy.golang.org", "golang.org"]));
  await as("DELETE", `/v1/sandboxes/${id}`);
  await user.close();

  await page.goto(`${base}/admin/egress-groups/`);
  page.once("dialog", (d) => d.accept());
  await page.getByRole("button", { name: "Delete group e2e-go" }).click();
  await expect(page.getByRole("group", { name: "Group e2e-go" })).toHaveCount(0);
});
