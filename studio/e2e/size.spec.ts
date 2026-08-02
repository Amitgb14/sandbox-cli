import { test } from "@playwright/test";
const TOK = process.env.SANDBOX_STUDIO_TOKEN!;
test("what size does the terminal compute", async ({ page }) => {
  test.setTimeout(90000);
  await page.goto("/");
  await page.evaluate((t) => localStorage.setItem("sandbox-studio-token", t), TOK);
  await page.goto("/runs/81a222772d4e");
  await page.waitForLoadState("networkidle");
  await page.getByRole("tab", { name: "Terminal" }).click();
  const body = await page.locator("body").innerText();
  const i = body.indexOf("Console");
  console.log("TABS AREA:", JSON.stringify(body.slice(i, i + 400).replace(/\n+/g, " | ")));
  console.log("HAS 'console disabled':", body.includes("console disabled"));
  console.log("DAEMON:", JSON.stringify(await page.evaluate(async () => {
    const r = await fetch("http://localhost:8787/v1/health"); return (await r.json()).authRequired;
  })));
  console.log("ATTACH COUNT:", await page.getByRole("button", { name: "Attach" }).count());
  await page.getByRole("button", { name: "Attach" }).click();
  await page.waitForTimeout(4000);
  const dims = await page.evaluate(() => {
    const el = document.querySelector(".xterm") as HTMLElement | null;
    const screen = document.querySelector(".xterm-screen") as HTMLElement | null;
    const rows = document.querySelectorAll(".xterm-rows > div").length;
    return {
      hostW: el?.clientWidth, hostH: el?.clientHeight,
      screenW: screen?.clientWidth, screenH: screen?.clientHeight,
      renderedRows: rows,
    };
  });
  console.log("DIMS:", JSON.stringify(dims));
  const txt = (await page.locator(".xterm-screen").innerText()).slice(0, 120);
  console.log("SCREEN:", JSON.stringify(txt));
});
