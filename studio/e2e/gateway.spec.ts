import { generateKeyPairSync } from "node:crypto";
import { expect, test, type Page } from "@playwright/test";
import { e2eState, gatewayStudio, type GatewayStudio } from "./state";

/**
 * Studio in front of a real sandbox-gateway (e2e/global-setup.ts), one Studio
 * per API key: what each key sees, what it is offered, and that a screen it
 * may not have makes no request for it. Hiding is cosmetic — the gateway's
 * 403 is the control, and internal/gateway's tests pin that — so these pin
 * that Studio does not offer what will be refused, and asks for nothing the
 * caller may not see.
 */

const TENANT = ["Jobs", "Services", "Secrets", "SSH", "Account"];
const ADMIN = ["Nodes", "Lost sandboxes", "Users & keys", "Audit", "All organizations"];
const ADMIN_ROUTES = ["/admin/nodes/", "/admin/lost/", "/admin/keys/", "/admin/audit/", "/admin/orgs/"];

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
