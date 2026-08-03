import { test } from "@playwright/test";
test("debug the bar's inputs", async ({ page }) => {
  await page.addInitScript(() => localStorage.setItem("sandbox-studio-token", "wrong-value"));
  const statuses: string[] = [];
  page.on("response", (r) => { if (r.url().includes("8787")) statuses.push(`${r.status()} ${new URL(r.url()).pathname}`); });
  await page.goto("/runs");
  await page.waitForLoadState("networkidle");
  console.log("RESPONSES:", statuses.slice(0, 6).join(", "));
  const health = await page.evaluate(async () => {
    const r = await fetch("http://localhost:8787/v1/health");
    return await r.json();
  });
  console.log("HEALTH authRequired:", health.authRequired);
  console.log("localStorage:", await page.evaluate(() => localStorage.getItem("sandbox-studio-token")));
});
