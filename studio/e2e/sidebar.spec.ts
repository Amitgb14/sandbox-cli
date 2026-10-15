import { readFileSync } from "node:fs";
import { join } from "node:path";
import { expect, test, type Page } from "@playwright/test";

const { token } = JSON.parse(readFileSync(join(__dirname, ".state.json"), "utf8")) as { token: string };

/** How far the sidebar's scrolling part is wider than itself: 0 when nothing overhangs. */
async function overhang(page: Page): Promise<number> {
  return page.locator('[data-sidebar="content"]').evaluate((el) => el.scrollWidth - el.clientWidth);
}

async function sidebarWidth(page: Page): Promise<number> {
  return page.locator('[data-slot="sidebar-container"]').evaluate((el) => el.getBoundingClientRect().width);
}

// The sidebar never scrolls sideways (its group rules overhung it by 8px),
// and collapses to its icons: each screen named in a tooltip, still a link,
// the choice kept across a reload.
test("the sidebar collapses to icons, keeps that, and never scrolls sideways", async ({ page }) => {
  await page.setViewportSize({ width: 1280, height: 900 });
  await page.goto(`/#token=${token}`);
  await expect(page.getByText("e2e · fake")).toBeVisible();
  expect(await overhang(page)).toBeLessThanOrEqual(0);
  expect(await sidebarWidth(page)).toBeGreaterThan(200);

  await page.getByRole("button", { name: /Collapse the sidebar/ }).click();
  await expect.poll(() => sidebarWidth(page)).toBeLessThan(64);
  expect(await overhang(page)).toBeLessThanOrEqual(0);
  const nav = page.locator('[data-sidebar="content"]');
  await expect(nav.getByText("Templates", { exact: true })).toBeHidden();
  const templates = nav.getByRole("link", { name: "Templates" });
  await templates.hover();
  await expect(page.getByRole("tooltip").filter({ hasText: "Templates" })).toBeVisible();
  await templates.click();
  await expect(page).toHaveURL(/\/templates\/$/);
  await expect(page.getByRole("button", { name: /Search/ })).toBeVisible();
  await expect(page.getByRole("button", { name: /Theme: / })).toBeVisible();

  await page.reload();
  await expect(page.getByRole("button", { name: /Expand the sidebar/ })).toBeVisible();
  // Restored without the slide: already narrow, not on its way there.
  expect(await sidebarWidth(page)).toBeLessThan(64);

  await page.getByRole("button", { name: /Expand the sidebar/ }).click();
  await expect.poll(() => sidebarWidth(page)).toBeGreaterThan(200);
  await expect(nav.getByText("Templates", { exact: true })).toBeVisible();
  await page.reload();
  await expect(page.getByRole("button", { name: /Collapse the sidebar/ })).toBeVisible();
});
