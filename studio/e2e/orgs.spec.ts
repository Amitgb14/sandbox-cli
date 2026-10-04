import { expect, test, type APIRequestContext, type Page } from "@playwright/test";
import { gatewayStudio, type GatewayStudio } from "./state";

/**
 * Organisations in Studio, against the real sandbox-gateway global-setup.ts
 * starts. Whether a selection is allowed is the gateway's to decide — its
 * tests pin that — so these pin what Studio does with one: lists the key's,
 * sends the selection on every call, shows nothing of one organisation under
 * another's name, and offers creating and managing only to whom may.
 */

const ORG = "e2e-one";

async function open(page: Page, name: GatewayStudio, user: string) {
  const { base, token } = gatewayStudio(name);
  await page.goto(`${base}/#token=${token}`);
  await expect(page.locator("[data-sidebar=sidebar]").getByText(`${name} · ${user}`)).toBeVisible();
  return base;
}

function switcher(page: Page) {
  return page.getByTestId("org-switcher");
}

/** Makes a sandbox through Studio's proxy, in org when given. */
async function sandbox(request: APIRequestContext, studio: GatewayStudio, name: string, org?: string) {
  const { base, token } = gatewayStudio(studio);
  const headers: Record<string, string> = { Authorization: `Bearer ${token}` };
  if (org) headers["X-Sandbox-Org"] = org;
  const resp = await request.fetch(`${base}/api/v1/sandboxes`, { method: "POST", data: { name }, headers });
  expect(resp.status(), await resp.text()).toBe(201);
}

async function choose(page: Page, org: string) {
  await switcher(page).click();
  await page.getByRole("menuitem", { name: new RegExp(`^${org}\\b`) }).click();
  await expect(switcher(page)).toHaveAccessibleName(`Organization: ${org}`);
}

test("a tenant creates an organization and sees only the selected one's sandboxes", async ({ page, request }) => {
  await open(page, "gw-tenant", "alice@team-a");
  await expect(switcher(page)).toHaveAccessibleName("Organization: team-a");
  await switcher(page).click();
  await expect(page.getByRole("menuitem", { name: /^team-a/ })).toBeVisible();
  await page.getByRole("menuitem", { name: "Create organization" }).click();
  await page.getByLabel("Organization name").fill("Bad--name");
  await expect(page.getByRole("button", { name: "Create", exact: true })).toBeDisabled();
  await page.getByLabel("Organization name").fill(ORG);
  await page.getByRole("button", { name: "Create", exact: true }).click();
  // Created, and switched to.
  await expect(switcher(page)).toHaveAccessibleName(`Organization: ${ORG}`);

  await sandbox(request, "gw-tenant", "in-team-a");
  await sandbox(request, "gw-tenant", "in-e2e-one", ORG);

  // Every call carries the selection.
  const sent: (string | null)[] = [];
  page.on("request", (r) => {
    if (new URL(r.url()).pathname === "/api/v1/sandboxes") sent.push(r.headers()["x-sandbox-org"] ?? null);
  });
  await page.reload();
  await expect(switcher(page)).toHaveAccessibleName(`Organization: ${ORG}`);
  const main = page.locator("main");
  await expect(main.getByText("in-e2e-one").first()).toBeVisible();
  await expect(main.getByText("in-team-a")).toHaveCount(0);
  expect(sent.length).toBeGreaterThan(0);
  expect(sent.every((h) => h === ORG)).toBe(true);

  await choose(page, "team-a");
  await expect(main.getByText("in-team-a").first()).toBeVisible();
  await expect(main.getByText("in-e2e-one")).toHaveCount(0);

  await choose(page, ORG);
  await expect(main.getByText("in-e2e-one").first()).toBeVisible();
  await expect(main.getByText("in-team-a")).toHaveCount(0);

  const { base } = gatewayStudio("gw-tenant");
  await page.goto(`${base}/account/`);
  await expect(page.getByTestId("account-org")).toHaveText(ORG);
});

test("an owner adds and removes a member, who sees it read-only and loses it", async ({ page, browser }) => {
  const base = await open(page, "gw-tenant", "alice@team-a");
  await choose(page, ORG);
  await page.goto(`${base}/members/`);
  await expect(page.locator("main h1")).toHaveText("Members");
  await expect(page.getByRole("row").filter({ hasText: "alice" })).toBeVisible();
  await page.getByLabel("Member user").fill("dana");
  await page.getByRole("button", { name: "Add member" }).click();
  const danaRow = page.getByRole("row").filter({ hasText: "dana" });
  await expect(danaRow).toBeVisible();
  await expect(danaRow.getByText("member", { exact: true })).toBeVisible();

  // dana, in her own Studio: the organisation is listed; its members are hers to read only.
  const other = await browser.newPage();
  const cbase = await open(other, "gw-member", "dana@team-a");
  await choose(other, ORG);
  await other.goto(`${cbase}/members/`);
  await expect(other.getByRole("row").filter({ hasText: "alice" })).toBeVisible();
  await expect(other.getByRole("button", { name: "Add member" })).toHaveCount(0);
  await expect(other.getByRole("button", { name: "Remove" })).toHaveCount(0);
  // dana may not create organizations.
  await switcher(other).click();
  await expect(other.getByRole("menuitem", { name: "Create organization" })).toHaveCount(0);
  await other.keyboard.press("Escape");

  // alice removes her.
  page.once("dialog", (d) => d.accept());
  await danaRow.getByRole("button", { name: "Remove" }).click();
  await expect(page.getByRole("row").filter({ hasText: "dana" })).toHaveCount(0);

  // dana's stored selection is refused now: Studio falls back to her own tenant and says so.
  await other.reload();
  await expect(other.getByText(/is not available to this key/)).toBeVisible();
  await expect(switcher(other)).toHaveAccessibleName("Organization: team-a");
  await other.close();
});

test("a key without org:create is offered no create, and its own tenant has no member list", async ({ page }) => {
  const base = await open(page, "gw-readonly", "bob@team-a");
  await expect(switcher(page)).toHaveAccessibleName("Organization: team-a");
  await switcher(page).click();
  await expect(page.getByRole("menuitem", { name: "Create organization" })).toHaveCount(0);
  await expect(page.getByRole("menuitem", { name: new RegExp(ORG) })).toHaveCount(0);
  await page.keyboard.press("Escape");
  await page.goto(`${base}/members/`);
  await expect(page.getByText("team-a has no member list")).toBeVisible();
});

test("an admin lists every organization", async ({ page }) => {
  const base = await open(page, "gw-admin", "ops");
  await page.goto(`${base}/admin/orgs/`);
  await expect(page.locator("main h1")).toHaveText("All organizations");
  const row = page.getByRole("row").filter({ hasText: ORG });
  await expect(row).toBeVisible();
  await expect(row.getByText("alice")).toBeVisible();
});
