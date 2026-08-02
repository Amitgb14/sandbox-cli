import { test, expect } from "@playwright/test";

/**
 * Clicking into the attached terminal must not stop it accepting input.
 *
 * Claude Code turns on any-event mouse tracking, so a terminal that forwards
 * mouse reports sends an escape sequence for every click *and every pointer
 * movement*. That was faithful and unusable: one click to focus the panel left
 * the agent unable to accept the Enter that followed, so a typed question sat
 * in its input box forever — while the same question submitted fine when
 * nothing had been clicked.
 *
 * So this does what a person does — click, move the mouse, type — and checks
 * that only the typing reaches the daemon.
 */
const API = process.env.NEXT_PUBLIC_SANDBOX_API ?? "http://localhost:8787";
const TOK = process.env.SANDBOX_STUDIO_TOKEN;

test("clicking the terminal does not break typing", async ({ page, request }) => {
  test.setTimeout(120000);
  test.skip(!TOK, "typing at a running agent needs the daemon's token");

  let id: string | undefined;
  const res = await request.get(`${API}/v1/runs`, {
    headers: { authorization: `Bearer ${TOK}` },
  });
  if (res.ok()) {
    const runs = (await res.json())?.runs ?? [];
    id = runs.find(
      (r: { tty: boolean; state: string; openStdin: boolean }) =>
        r.tty && r.openStdin && r.state === "running",
    )?.id;
  }
  test.skip(!id, "no console run to attach to");

  await page.goto("/");
  await page.evaluate(
    (t) => localStorage.setItem("sandbox-studio-token", t),
    TOK!,
  );

  const sent: string[] = [];
  page.on("request", (r) => {
    if (r.method() === "POST" && r.url().includes("/console/input")) {
      sent.push(r.postData() ?? "");
    }
  });

  await page.goto(`/runs/${id}`);
  await page.waitForLoadState("networkidle");
  await page.getByRole("tab", { name: "Terminal" }).click();
  await page.getByRole("button", { name: "Attach" }).first().click();
  await expect(page.locator(".xterm-screen")).toContainText("/workspace", {
    timeout: 30000,
  });

  await page.locator(".xterm-screen").click();
  await page.mouse.move(300, 300);
  await page.mouse.move(320, 310);
  // A sum unique to this run, asserted on the *answer* rather than the echo.
  // Two earlier versions passed in five seconds and proved nothing: a fixed
  // question matched an answer already on the container's screen, and a marker
  // typed into the question matched its own echo in the input box.
  const a = 100 + (Date.now() % 800);
  const b = 100 + ((Date.now() >> 3) % 800);
  const answer = String(a + b);
  const question = `what is ${a} plus ${b}? reply with just the number.`;
  await page.keyboard.type(question);
  await page.keyboard.press("Enter");

  await expect(
    page.locator(".xterm-screen"),
    "the agent did not answer after a click",
  ).toContainText(answer, { timeout: 90000 });

  // Nothing resembling a mouse report may have been sent. The escape byte is
  // built by code so this file stays plain text.
  const esc = String.fromCharCode(27);
  const mouse = sent.filter(
    (b) => b.includes(`${esc}[<`) || b.includes("u001b[<"),
  );
  expect(mouse, "mouse reports must not reach the container").toEqual([]);
});
