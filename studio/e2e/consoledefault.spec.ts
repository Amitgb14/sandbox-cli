import { test, expect } from "@playwright/test";

/**
 * The console is no longer a question the launch form asks.
 *
 * An agent run keeps one by default, and the four exceptions are pairs the
 * daemon refuses rather than preferences — so the form states the mode and
 * names the field that changed it, instead of offering a toggle whose only
 * honest setting was "on" for everything anyone launches by hand.
 *
 * Written against the form alone: no daemon is needed to see what mode a
 * request would be launched in, and the assertions are what a person reads.
 */
test("an agent run keeps a console, and the toggle is gone", async ({
  page,
}) => {
  await page.goto("/launch");
  await page.waitForLoadState("networkidle");

  // The control this replaced. Its absence is half the change: a field nothing
  // may set is one the next reader wires a control back onto.
  await expect(page.locator("#console")).toHaveCount(0);
  await expect(page.getByText("Keep a console I can attach to")).toHaveCount(0);

  // claude is the default agent, and nothing else on the form is filled in.
  await expect(
    page.getByText("Keeps a console you can attach to"),
  ).toBeVisible();
});

test("a verify command is what asks for a headless run", async ({ page }) => {
  await page.goto("/launch");
  await page.waitForLoadState("networkidle");
  await expect(
    page.getByText("Keeps a console you can attach to"),
  ).toBeVisible();

  // Verify decides the run's exit code and an interactive session's exit code
  // is whenever somebody quit, so the daemon refuses the two together. The
  // field used to be disabled by the toggle; now it is the way to turn it off.
  const verify = page.locator("#verify");
  await expect(verify).toBeEnabled();
  await verify.fill("make test");

  await expect(page.getByText("because a verify command is set")).toBeVisible();
  await expect(page.getByText("Keeps a console you can attach to")).toHaveCount(
    0,
  );

  // And back: clearing it restores the default rather than leaving the run in
  // the mode a since-deleted field put it in.
  await verify.fill("");
  await expect(
    page.getByText("Keeps a console you can attach to"),
  ).toBeVisible();
});

/**
 * The review's first finding, and the one the default launch turned on.
 *
 * A headless run gets its skip-permissions flag from `Descriptor.Autonomous`
 * whatever the form says, so the old default — console off — made work by
 * clicking Launch. A console run takes `agent.Console(prompt, skipPermissions)`
 * instead, so making the console the default without this would have started
 * claude's interactive UI and stopped it at the first tool approval, detached,
 * with nobody attached. Same autonomy as before; unlocked rather than locked.
 */
test("a console run still works without asking, and says so", async ({
  page,
}) => {
  await page.goto("/launch");
  await page.waitForLoadState("networkidle");

  const skip = page.locator("#skip-permissions");
  await expect(skip).toBeChecked();
  // Unlike the headless case, which rendered it checked *and* disabled, this is
  // a real choice now: somebody who wants to be asked can have that.
  await expect(skip).toBeEnabled();
  await expect(page.getByText("works through without asking")).toBeVisible();

  // And unticking it says what the run then does, rather than leaving "seeds the
  // first turn" to be read as "gets on with it".
  await skip.click();
  await expect(
    page.getByText("stop at its first approval and wait"),
  ).toBeVisible();
});

/**
 * "Run detached" never travelled — `buildLaunchBody` sends no such field and the
 * daemon detaches every run — so the only thing the toggle changed was the
 * preview beside it. Unticked, which it was by default, it described a container
 * with a pty that nobody was going to get.
 */
test("the detach toggle is gone and the preview says detached", async ({
  page,
}) => {
  await page.goto("/launch");
  await page.waitForLoadState("networkidle");

  await expect(page.locator("#detach")).toHaveCount(0);
  await expect(page.getByText("Run detached")).toHaveCount(0);
  await expect(page.getByText(/\bdetached\b/i).first()).toBeVisible();
});
