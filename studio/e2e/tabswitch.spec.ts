import { test, expect } from "@playwright/test";
const API = process.env.NEXT_PUBLIC_SANDBOX_API ?? "http://localhost:8787";
const TOK = process.env.SANDBOX_STUDIO_TOKEN!;
/**
 * Glancing at another tab must not detach a live terminal.
 *
 * Radix unmounts inactive tab content, so switching to Console closed the
 * stream, disposed the emulator, lost its scrollback and offered an Attach
 * button again as though nothing had been running. The agent was never
 * affected — detaching is only ever a reader leaving — but losing your place
 * for looking at the next tab is not a trade anyone would make.
 */
test("switching tabs does not detach a live terminal", async ({ page, request }) => {
  test.setTimeout(120000);
  const res = await request.get(`${API}/v1/runs`, { headers: { authorization: `Bearer ${TOK}` } });
  const id = (await res.json()).runs.find((r: {tty: boolean; openStdin: boolean}) => r.tty && r.openStdin)?.id;
  test.skip(!id, "no console run to attach to");
  await page.goto("/");
  await page.evaluate((t) => localStorage.setItem("sandbox-studio-token", t), TOK);
  await page.goto(`/runs/${id}`);
  await page.waitForLoadState("networkidle");
  await page.getByRole("tab", { name: "Terminal" }).click();
  await page.getByRole("button", { name: "Attach" }).first().click();
  await page.waitForTimeout(4000);
  console.log("ATTACHED, terminals:", await page.locator(".xterm-screen").count());

  await page.getByRole("tab", { name: "Console" }).click();
  await page.waitForTimeout(1200);
  console.log("AFTER SWITCH AWAY, terminals:", await page.locator(".xterm-screen").count());

  await page.getByRole("tab", { name: "Terminal" }).click();
  await page.waitForTimeout(1500);
  console.log("BACK ON TERMINAL, terminals:", await page.locator(".xterm-screen").count());
  console.log("STILL ATTACHED:", await page.locator(".xterm-screen").isVisible());
  const screen = (await page.locator(".xterm-screen").innerText()).replace(/\s+/g, " ");
  console.log("SCREEN KEPT:", screen.includes("/workspace"));
  // And it still takes input after the round trip.
  const a = 100 + (Date.now() % 800), b = 100 + ((Date.now() >> 3) % 800);
  await page.keyboard.type(`what is ${a} plus ${b}? reply with just the number.`);
  await page.keyboard.press("Enter");
  await expect(page.locator(".xterm-screen")).toContainText(String(a + b), { timeout: 90000 });
  console.log("STILL TYPEABLE: true");
});
