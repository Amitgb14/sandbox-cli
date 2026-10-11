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
  await expect(page.locator("[data-sidebar=sidebar]").getByRole("link", { name: "Users", exact: true })).toHaveCount(0);
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

test("signing out ends the session, and the sign-in page is all there is", async ({ page }) => {
  await signIn(page, "ivo");
  await page.locator("[data-sidebar=sidebar]").getByRole("button", { name: "Sign out" }).click();
  await expect(page.getByRole("heading", { name: "Sign in" })).toBeVisible();
  await expect(page.locator("[data-sidebar=sidebar]")).toHaveCount(0);
  await page.reload();
  await expect(page.getByRole("heading", { name: "Sign in" })).toBeVisible();
  await expect(page.locator("[data-sidebar=sidebar]")).toHaveCount(0);
  // A key signs in from the page too.
  await page.getByRole("button", { name: "Sign in with an API key instead" }).click();
  await page.getByLabel("API key").fill(hosted().ivo.key);
  await page.getByRole("form", { name: "Sign in" }).getByRole("button", { name: "Sign in" }).click();
  await expect(page.locator("[data-sidebar=sidebar]").getByText("hosted · ivo@ivo")).toBeVisible();
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

test("a link without a valid key does not sign in", async ({ page }) => {
  hosted();
  await page.goto(`${base}/#key=not-a-key`);
  await expect(page.getByText("This invite link did not sign you in")).toBeVisible();
  await page.goto(`${base}/#key=sgk_${"z".repeat(52)}`);
  await expect(page.getByText("not accepted")).toBeVisible();
  expect(page.url()).not.toContain("key=");
});

/**
 * This hosted Studio runs with --allow-admin (global-setup.ts): the admin
 * signs in by link as anyone does, adds a demo user, hands over the invite
 * link, and ends the user's access by expiring their key. Without the flag
 * an admin key is refused (internal/studio's TestHostedAdminSignIn).
 */
test("an admin adds a demo user by invite link, and expiring their key ends their session", async ({ page, browser }) => {
  const { gatewayURL, adminKey } = e2eState();
  hosted();
  await page.goto(`${base}/#key=${adminKey}`);
  await expect(page.locator("[data-sidebar=sidebar]").getByRole("link", { name: "Users", exact: true })).toBeVisible();
  expect(page.url()).not.toContain("key=");

  await page.goto(`${base}/admin/keys/`);
  await page.getByLabel("Policy name").fill("trial");
  await page.getByLabel("Max lifetime in minutes").fill("30");
  await page.getByRole("button", { name: "Save policy" }).click();
  await expect(page.getByRole("cell", { name: "trial", exact: true })).toBeVisible();

  // One kind of account: what differs is the policy and the expiry picked.
  await page.getByLabel("User", { exact: true }).fill("jun");
  await page.getByLabel("Tenant", { exact: true }).fill("jun");
  await page.getByRole("combobox", { name: "Policy", exact: true }).click();
  await page.getByRole("option", { name: "trial" }).click();
  await page.getByRole("combobox", { name: "Key expires" }).click();
  await page.getByRole("option", { name: "expires in 7 days" }).click();
  await page.getByRole("button", { name: "Issue key" }).click();
  const key = (await page.getByTestId("new-key-secret").textContent())!;
  const invite = (await page.getByTestId("new-key-invite").textContent())!;
  expect(invite).toBe(`${base}/#key=${key}`);
  await page.getByRole("button", { name: "Done" }).click();
  const row = page.getByRole("row").filter({ hasText: "jun" });
  await expect(row.getByText(/^in [67]d$/)).toBeVisible();

  // jun opens the link in a browser of their own, and holds a policy, which
  // the create form and the Account page say.
  const jun = await (await browser.newContext()).newPage();
  await jun.goto(invite);
  await expect(jun.locator("[data-sidebar=sidebar]").getByText("hosted · jun@jun")).toBeVisible();
  await jun.goto(`${base}/launch/`);
  await expect(jun.getByTestId("policy-lifetime")).toHaveText(/policy, trial, a sandbox ends 30 minutes after it starts/);
  await jun.goto(`${base}/account/`);
  await expect(jun.getByTestId("account-policy")).toContainText("ends 30 min after it starts");
  await expect(jun.getByTestId("account-role")).toHaveText("admin of jun");
  await expect(jun.locator("[data-sidebar=sidebar]").getByRole("link", { name: "Users", exact: true })).toHaveCount(0);
  const made = await fetch(`${gatewayURL}/v1/sandboxes`, {
    method: "POST",
    headers: { Authorization: `Bearer ${key}`, "Content-Type": "application/json" },
    body: JSON.stringify({ name: "jun-box" }),
  });
  const sb = (await made.json()) as { id: string; lifetime_secs: number };
  expect(sb.lifetime_secs).toBe(1800);
  await fetch(`${gatewayURL}/v1/sandboxes/${sb.id}`, { method: "DELETE", headers: { Authorization: `Bearer ${adminKey}` } });

  // The admin expires the key now; jun's next action ends the session.
  page.once("dialog", (d) => d.accept());
  await row.getByRole("button", { name: /^Renew/ }).click();
  await page.getByRole("menuitem", { name: "Expire now" }).click();
  await expect(row.getByText("expired")).toBeVisible();
  await jun.reload();
  await expect(jun.getByText("Your session has ended")).toBeVisible();
});

/**
 * The invite by email: the admin invites an address, the person opens the
 * link, chooses a password, and from then on signs in with it; the key the
 * sign-in uses is the gateway's, minted for it, and never in the page.
 */
test("an admin invites a person by email, who chooses a password and signs in with it", async ({ page, browser }) => {
  const { adminKey } = e2eState();
  hosted();
  await page.goto(`${base}/#key=${adminKey}`);
  await expect(page.locator("[data-sidebar=sidebar]").getByRole("link", { name: "Users", exact: true })).toBeVisible();
  await page.goto(`${base}/admin/keys/`);
  await page.getByLabel("Email", { exact: true }).fill("kim@example.com");
  await expect(page.getByLabel("User", { exact: true })).toHaveValue("kim");
  await page.getByLabel("Tenant", { exact: true }).fill("kim");
  await page.getByRole("button", { name: "Create invite" }).click();
  const first = (await page.getByTestId("new-invite-link").textContent())!;
  expect(first).toMatch(new RegExp(`^${base}/#invite=sgi_[a-z0-9]+$`));
  await page.getByRole("button", { name: "Done" }).click();
  // Sent again from Pending invites: a new link, and the first stops working.
  await page.getByRole("button", { name: "Resend the invite for kim@example.com" }).click();
  await expect(page.getByTestId("new-invite-link")).not.toHaveText(first);
  const link = (await page.getByTestId("new-invite-link").textContent())!;
  expect(link).toMatch(new RegExp(`^${base}/#invite=sgi_[a-z0-9]+$`));
  const mailto = (await page.getByTestId("email-invite").getAttribute("href"))!;
  expect(mailto.startsWith("mailto:kim%40example.com?subject=")).toBe(true);
  expect(decodeURIComponent(mailto)).toContain(link);
  await page.getByRole("button", { name: "Done" }).click();
  await expect(page.getByRole("row").filter({ hasText: "kim@example.com" }).getByRole("button", { name: "Withdraw" })).toBeVisible();

  const kim = await (await browser.newContext()).newPage();
  const sent: string[] = [];
  kim.on("request", (r) => {
    if (r.headers()["authorization"] || /[?&](token|key|invite)=/.test(r.url())) sent.push(r.url());
  });
  await kim.goto(link);
  await expect(kim.getByText("Create your account")).toBeVisible();
  await expect(kim.getByText("kim@example.com")).toBeVisible();
  expect(kim.url()).not.toContain("invite=");
  await kim.getByLabel("Password", { exact: true }).fill("kim's long password");
  await kim.getByLabel("Password again").fill("kim's long password");
  await kim.getByRole("button", { name: "Create account" }).click();
  await expect(kim.locator("[data-sidebar=sidebar]").getByText("hosted · kim@kim")).toBeVisible();
  await kim.goto(`${base}/account/`);
  await expect(kim.getByTestId("account-email")).toHaveText("kim@example.com");

  // The link worked once.
  const again = await (await browser.newContext()).newPage();
  await again.goto(link);
  await expect(again.getByText("This invite link did not sign you in")).toBeVisible();

  // Signed out, kim signs in with the password; a wrong one is refused.
  await kim.locator("[data-sidebar=sidebar]").getByRole("button", { name: "Sign out" }).click();
  await expect(kim.getByRole("heading", { name: "Sign in" })).toBeVisible();
  const form = kim.getByRole("form", { name: "Sign in" });
  await form.getByLabel("Email").fill("kim@example.com");
  await form.getByLabel("Password").fill("not kim's password");
  await form.getByRole("button", { name: "Sign in" }).click();
  await expect(form.getByText("do not match")).toBeVisible();
  await form.getByLabel("Password").fill("kim's long password");
  await form.getByRole("button", { name: "Sign in" }).click();
  await expect(kim.locator("[data-sidebar=sidebar]").getByText("hosted · kim@kim")).toBeVisible();
  expect(sent).toEqual([]);

  // The admin sees the account, and its last sign-in.
  await page.reload();
  await expect(page.getByTestId("account-kim")).toContainText("kim@example.com · signed in");
});

/**
 * A user who has made no sandbox yet still sees what they can start from:
 * the gateway lists what its nodes have built (GET /v1/fleet/images), where
 * before the list was empty until their first sandbox.
 */
test("a new user's create form lists the images the nodes have built", async ({ page }) => {
  const { gatewayURL, adminKey } = e2eState();
  hosted();
  const admin = (method: string, path: string, body?: object) =>
    fetch(`${gatewayURL}${path}`, { method, headers: { Authorization: `Bearer ${adminKey}`, "Content-Type": "application/json" }, body: body && JSON.stringify(body) });
  // The default image is built on a node once anything has started from it.
  const sb = (await (await admin("POST", "/v1/sandboxes", {})).json()) as { id: string; image: string };
  await admin("DELETE", `/v1/sandboxes/${sb.id}`);
  const key = (await (await admin("POST", "/v1/admin/keys", { user: "newbie", tenant: "newbie", scopes: ["sandbox:read", "sandbox:create"] })).json()) as { secret: string };
  await page.goto(`${base}/#key=${key.secret}`);
  await expect(page.locator("[data-sidebar=sidebar]").getByText("hosted · newbie@newbie")).toBeVisible();
  await page.goto(`${base}/launch/`);
  await page.getByRole("combobox", { name: "Image" }).click();
  const group = page.getByRole("group", { name: "Built on the gateway's nodes" });
  await expect(group).toBeVisible({ timeout: 15_000 });
  await expect(group.getByRole("option").filter({ hasText: sb.image.replace(/^.*\//, "") })).toBeVisible();
});

test("a user may bring their sandbox's end nearer, and is offered no way to push it out", async ({ page }) => {
  const { gatewayURL } = e2eState();
  const resp = await fetch(`${gatewayURL}/v1/sandboxes`, {
    method: "POST",
    headers: { Authorization: `Bearer ${hosted().hana.key}`, "Content-Type": "application/json" },
    body: JSON.stringify({ name: "hana-timed", lifetime_secs: 3600 }),
  });
  const { id } = (await resp.json()) as { id: string };
  await signIn(page, "hana");
  await page.goto(`${base}/sandbox/?id=${id}`);
  await expect(page.getByText("Ends")).toBeVisible();
  await expect(page.getByRole("button", { name: "Extend lifetime by 15 minutes" })).toHaveCount(0);
  await page.getByRole("button", { name: "Shorten lifetime by 15 minutes" }).click();
  await expect(page.getByText("Lifetime shortened")).toBeVisible();
  // The one node is shared with every other test: give its room back.
  await fetch(`${gatewayURL}/v1/sandboxes/${id}`, { method: "DELETE", headers: { Authorization: `Bearer ${hosted().hana.key}` } });
});
