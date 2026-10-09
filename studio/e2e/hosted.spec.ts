import { expect, test, type Page } from "@playwright/test";
import { e2eState, HOSTED_PORT, type HostedUser } from "./state";

/**
 * The hosted Studio (`sandbox-cli studio host`, e2e/global-setup.ts) in front
 * of the real gateway: users sign in by invite link, each sees only their own
 * tenant, the key leaves the address bar and no request carries a token, and
 * a session ends on sign-out and when the key is revoked.
 */

const base = `http://127.0.0.1:${HOSTED_PORT}`;

function hosted() {
  const h = e2eState().hosted;
  test.skip(!h, "no out-hosted/: run npm run build:hosted first (npm run check does)");
  return h!;
}

/** Signs a user in with their invite link, once the sidebar says who they are. */
async function signIn(page: Page, user: HostedUser) {
  await page.goto(hosted()[user].invite);
  await expect(page.locator("[data-sidebar=sidebar]").getByText(`hosted · ${user}@${user}`)).toBeVisible();
}

/** Creates a sandbox at the gateway with a user's own key, as their CLI would. */
async function createAs(user: HostedUser, name: string): Promise<string> {
  const { gatewayURL } = e2eState();
  const resp = await fetch(`${gatewayURL}/v1/sandboxes`, {
    method: "POST",
    headers: { Authorization: `Bearer ${hosted()[user].key}`, "Content-Type": "application/json" },
    body: JSON.stringify({ name }),
  });
  expect(resp.ok).toBe(true);
  return ((await resp.json()) as { id: string }).id;
}

test("an invite link signs in, leaves the address bar, and no request carries a credential", async ({ page }) => {
  const h = hosted();
  const sent: string[] = [];
  page.on("request", (r) => {
    if (r.headers()["authorization"]) sent.push(`${r.url()} Authorization`);
    if (/[?&](token|key)=/.test(r.url())) sent.push(r.url());
  });
  await signIn(page, "hana");
  expect(page.url()).not.toContain("key=");
  expect(page.url()).not.toContain(h.hana.key);
  // The cookie, not the link, keeps the session: a reload stays signed in.
  await page.reload();
  await expect(page.locator("[data-sidebar=sidebar]").getByText("hosted · hana@hana")).toBeVisible();
  // No admin screens and no token paste box in a hosted build.
  await expect(page.locator("[data-sidebar=sidebar]").getByRole("link", { name: "Users & keys" })).toHaveCount(0);
  await expect(page.getByText("Studio needs its token")).toHaveCount(0);
  expect(sent).toEqual([]);
});

test("each user sees only their own sandboxes", async ({ page }) => {
  const ivos = await createAs("ivo", "ivo-box");
  await createAs("hana", "hana-box");
  await signIn(page, "hana");
  await page.goto(`${base}/sandboxes/`);
  await expect(page.getByText("hana-box").first()).toBeVisible();
  await expect(page.getByText("ivo-box")).toHaveCount(0);
  // Not hidden, absent: asked for directly, through hana's session, it does not exist.
  const r = await page.request.get(`${base}/api/v1/sandboxes/${ivos}`);
  expect(r.status()).toBe(404);
});

test("the Playground runs a command, and sends unattended agents to Jobs", async ({ page }) => {
  await signIn(page, "hana");
  await page.goto(`${base}/launch/`);
  await expect(page.getByText("Agent, unattended")).toHaveCount(0);
  await expect(page.getByRole("link", { name: "Jobs", exact: true }).last()).toBeVisible();
  await page.locator("label[for=kind-command]").click();
  await page.getByRole("textbox", { name: "Command" }).fill("echo hosted-hello");
  await page.getByRole("button", { name: "Launch", exact: true }).click();
  await expect(page).toHaveURL(/\/sandbox\/\?id=sbx_/);
});

test("signing out ends the session", async ({ page }) => {
  await signIn(page, "ivo");
  await page.locator("[data-sidebar=sidebar]").getByRole("button", { name: "Sign out" }).click();
  await expect(page.getByText("Your session has ended")).toBeVisible();
  await page.reload();
  await expect(page.getByText("Your session has ended")).toBeVisible();
});

test("revoking a user's key ends their session", async ({ page }) => {
  const { gatewayURL, adminKey } = e2eState();
  await signIn(page, "gil");
  const r = await fetch(`${gatewayURL}/v1/admin/keys/${hosted().gil.id}`, { method: "DELETE", headers: { Authorization: `Bearer ${adminKey}` } });
  expect(r.ok).toBe(true);
  await page.reload();
  await expect(page.getByText("Your session has ended")).toBeVisible();
  // And the old link does not sign in again.
  await page.goto(hosted().gil.invite);
  await expect(page.getByText("This invite link did not sign you in")).toBeVisible();
});

test("a link without a valid key, or with an admin key, does not sign in", async ({ page }) => {
  hosted();
  await page.goto(`${base}/#key=not-a-key`);
  await expect(page.getByText("This invite link did not sign you in")).toBeVisible();
  await page.goto(`${base}/#key=sgk_${"z".repeat(52)}`);
  await expect(page.getByText("not accepted")).toBeVisible();
  await page.goto(`${base}/#key=${e2eState().adminKey}`);
  await expect(page.getByText("an admin key cannot sign in")).toBeVisible();
  expect(page.url()).not.toContain("key=");
});
