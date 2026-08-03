import { test, expect } from "@playwright/test";
const TOK = process.env.SANDBOX_STUDIO_TOKEN;
test("the worktrees table says how each branch's last run ended", async ({ page }) => {
  if (TOK) {
    await page.goto("/");
    await page.evaluate((t) => localStorage.setItem("sandbox-studio-token", t), TOK);
  }
  const errs: string[] = [];
  page.on("pageerror", (e) => errs.push(`${e.name}: ${e.message}`));
  await page.goto("/worktrees");
  await page.waitForLoadState("networkidle");
  await page.waitForTimeout(1500);

  await expect(page.getByRole("columnheader", { name: "Last run" })).toBeVisible();
  const row = page.getByRole("row").filter({ hasText: "feat/studio-reviewer" }).first();
  const text = (await row.innerText()).replace(/\s+/g, " ");
  console.log("ROW:", JSON.stringify(text));
  expect(errs).toEqual([]);
});
