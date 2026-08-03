import { test } from "@playwright/test";
test("a wrong token: does Studio offer any way out?", async ({ page }) => {
  const failed: string[] = [];
  page.on("response", (r) => { if (r.status() === 401) failed.push(new URL(r.url()).pathname); });
  await page.addInitScript(() => localStorage.setItem("sandbox-studio-token", "wrong-value"));
  await page.goto("/runs");
  await page.waitForLoadState("networkidle");
  const body = await page.locator("body").innerText();
  console.log("TOKEN BAR SHOWN:", body.includes("requires a token") || body.includes("refused this token"));
  const k = body.indexOf("token");
  console.log("BAR TEXT:", body.slice(Math.max(0,k-40), k+140).replace(/\n+/g, " | "));
  console.log("401s:", failed.length ? failed.slice(0, 3) : "none");
  const i = body.indexOf("Could not");
  console.log("WHAT THE USER SEES:", i < 0 ? body.slice(0, 120).replace(/\n+/g, " | ") : body.slice(i, i + 120).replace(/\n+/g, " | "));
});
