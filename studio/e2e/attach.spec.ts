import { test, expect } from "@playwright/test";

/**
 * The attached terminal, driven the way a person drives it: click Attach, read
 * what the agent painted, type a question, read the answer.
 *
 * Needs a *console* run to be alive, so it skips rather than fails when there
 * is none — an empty machine is not a broken one, the same bargain the other
 * id-based tests make. Start one with:
 *
 *   curl -H "authorization: Bearer $SANDBOX_STUDIO_TOKEN" -XPOST \
 *     localhost:8787/v1/runs -H 'content-type: application/json' \
 *     -d '{"agent":"claude","branch":"probe","console":true,"prompt":"Say READY and wait."}'
 */
const API = process.env.NEXT_PUBLIC_SANDBOX_API ?? "http://localhost:8787";
const TOKEN = process.env.SANDBOX_STUDIO_TOKEN;

test("attach opens a live terminal you can type at", async ({ page, request }) => {
  test.setTimeout(180000);
  test.skip(!TOKEN, "typing at a running agent needs the daemon's token");

  // A run that is running *and* was created with stdin. Both halves matter:
  // a headless run has a terminal to read and nothing listening.
  let id: string | undefined;
  try {
    const res = await request.get(`${API}/v1/runs`, {
      headers: { authorization: `Bearer ${TOKEN}` },
    });
    if (res.ok()) {
      const runs = (await res.json())?.runs ?? [];
      id = runs.find(
        (r: { state: string; openStdin: boolean }) =>
          r.state === "running" && r.openStdin,
      )?.id;
    }
  } catch {
    // No daemon; the skip below covers it.
  }
  test.skip(!id, "no console run to attach to");

  const errs: string[] = [];
  page.on("pageerror", (e) => errs.push(`${e.name}: ${e.message}`));
  await page.addInitScript((t) => {
    localStorage.setItem("sandbox-studio-token", t);
  }, TOKEN!);

  await page.goto(`/runs/${id}`);
  await page.waitForLoadState("networkidle");
  await page.getByRole("tab", { name: "Terminal" }).click();
  await page.getByRole("button", { name: "Attach" }).click();

  // The agent repaints when it is told the terminal size, which is what the
  // attach does on connect. Without that this stays empty over a healthy run.
  const screen = page.locator(".xterm-screen");
  await expect(screen, "the agent never painted").toContainText("/workspace", {
    timeout: 30000,
  });

  await screen.click();
  const question = "What is 12 times 12? Reply with just the number.";
  await page.keyboard.type(question);
  await page.keyboard.press("Enter");

  // Order is the whole contract of a byte stream, and it was broken at first:
  // one POST per keystroke meant concurrent requests raced, and this arrived
  // as "rtWha is21 t ime1 2s?".
  await expect(screen, "keystrokes arrived out of order").toContainText(
    "What is 12 times 12?",
    { timeout: 15000 },
  );
  await expect(screen, "the agent did not answer").toContainText("144", {
    timeout: 120000,
  });
  expect(errs).toEqual([]);
});
