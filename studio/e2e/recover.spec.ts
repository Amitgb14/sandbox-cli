import { test, expect } from "@playwright/test";
/**
 * A token that is *wrong* used to be unrecoverable: the bar asking for one only
 * appeared when none was stored, so a stale value hid it, every request 401'd,
 * and the only way out was devtools. This walks that path.
 */
const GOOD = process.env.SANDBOX_STUDIO_TOKEN ?? "";
test("a wrong token can be corrected from the UI", async ({ page }) => {
  test.skip(!GOOD, "needs the daemon real token to prove recovery");
  // Seeded once, not via addInitScript — that re-runs on every reload and would
  // put the wrong value back the moment the fix takes effect.
  await page.goto("/");
  await page.evaluate(() => localStorage.setItem("sandbox-studio-token", "wrong-value"));
  await page.goto("/runs");
  await page.waitForLoadState("networkidle");
  await expect(page.locator("body")).toContainText("refused this token");

  await page.getByPlaceholder("bearer token").fill(GOOD);
  await page.getByRole("button", { name: "Save" }).click();
  await page.waitForLoadState("networkidle");
  await page.waitForTimeout(2500);

  const body = await page.locator("body").innerText();
  console.log("BAR GONE:", !/refused this token|requires a token/.test(body));
  console.log("STORED IS GOOD:", await page.evaluate(() => localStorage.getItem("sandbox-studio-token")) === GOOD);

  const failed: string[] = [];
  page.on("response", (r) => { if (r.status() === 401) failed.push(new URL(r.url()).pathname); });
  await page.goto("/runs");
  await page.waitForLoadState("networkidle");
  console.log("401s AFTER:", failed.length ? failed : "none");
  expect(failed).toEqual([]);
});
