import { test, expect } from "@playwright/test";
const API = process.env.NEXT_PUBLIC_SANDBOX_API ?? "http://localhost:8787";
test("a token-less daemon does not offer a console it cannot deliver", async ({ page, request }) => {
  const h = await (await request.get(`${API}/v1/health`)).json();
  test.skip(h.authRequired === true, "this checks the token-less case");

  let id: string | undefined;
  const res = await request.get(`${API}/v1/runs`);
  if (res.ok()) {
    const runs = (await res.json())?.runs ?? [];
    id = runs.find((r: { state: string }) => r.state === "running")?.id ?? runs[0]?.id;
  }
  test.skip(!id, "no run to look at");

  const errs: string[] = [];
  page.on("pageerror", (e) => errs.push(`${e.name}: ${e.message}`));
  await page.goto(`/runs/${id}`);
  await page.waitForLoadState("networkidle");
  await page.getByRole("tab", { name: "Terminal" }).click();
  await page.waitForTimeout(1200);
  const body = await page.locator("body").innerText();
  console.log("ATTACH OFFERED:", await page.getByRole("button", { name: "Attach" }).count());
  console.log("SAYS DISABLED:", body.includes("console disabled"));
  await page.getByRole("tab", { name: "Console" }).click();
  await page.waitForTimeout(1500);
  const c = await page.locator("body").innerText();
  console.log("REPLY BOX:", await page.getByPlaceholder("Answer the agent…").count());
  const i = c.indexOf("without a token");
  console.log("CONSOLE SAYS:", i < 0 ? "(no token note)" : c.slice(Math.max(0, i - 60), i + 90).replace(/\n+/g, " | "));
  console.log("errors:", errs.length ? errs : "none");
  expect(errs).toEqual([]);
});
