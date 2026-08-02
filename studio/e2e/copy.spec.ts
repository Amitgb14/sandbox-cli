import { test, expect } from "@playwright/test";
const API = process.env.NEXT_PUBLIC_SANDBOX_API ?? "http://localhost:8787";
const TOK = process.env.SANDBOX_STUDIO_TOKEN!;
test("text can be selected and copied in the attached terminal", async ({ page, request, context }) => {
  test.setTimeout(120000);
  await context.grantPermissions(["clipboard-read", "clipboard-write"]);
  const res = await request.get(`${API}/v1/runs`, { headers: { authorization: `Bearer ${TOK}` } });
  const id = (await res.json()).runs.find((r: {tty: boolean; openStdin: boolean}) => r.tty && r.openStdin)?.id;
  test.skip(!id, "no console run");
  await page.goto("/");
  await page.evaluate((t) => localStorage.setItem("sandbox-studio-token", t), TOK);
  await page.goto(`/runs/${id}`);
  await page.waitForLoadState("networkidle");
  await page.getByRole("tab", { name: "Terminal" }).click();
  await page.getByRole("button", { name: "Attach" }).first().click();
  await expect(page.locator(".xterm-screen")).toContainText("/workspace", { timeout: 30000 });

  // Drag across the banner line, the way a person selects text.
  const box = await page.locator(".xterm-screen").boundingBox();
  if (!box) throw new Error("no terminal box");
  await page.mouse.move(box.x + 20, box.y + 30);
  await page.mouse.down();
  await page.mouse.move(box.x + 400, box.y + 30, { steps: 12 });
  await page.mouse.up();

  const info = await page.evaluate(() => ({
    domSelection: window.getSelection()?.toString() ?? "",
    overlayDivs: document.querySelectorAll(".xterm-selection div").length,
    helper: (document.querySelector(".xterm-helper-textarea") as HTMLTextAreaElement | null)?.value ?? "",
  }));
  console.log("SELECTION OVERLAY DIVS:", info.overlayDivs);

  // Cmd-C on macOS, the browser's own copy. Ctrl-C is deliberately not used:
  // in a terminal that interrupts the agent.
  // The terminal convention, which has to work where Cmd-C does not exist.
  // Ctrl-C is deliberately left alone: in a terminal it interrupts the agent.
  await page.keyboard.press("Control+Shift+C");
  await page.waitForTimeout(500);
  const clip = await page.evaluate(() => navigator.clipboard.readText());
  console.log("CLIPBOARD:", JSON.stringify(clip.slice(0, 60)));
  expect(clip.trim().length, "Ctrl-Shift-C copied nothing").toBeGreaterThan(0);
  expect(info.overlayDivs, "drag produced no selection").toBeGreaterThan(0);
});
