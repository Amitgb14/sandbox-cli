import { test, expect } from "@playwright/test";

/**
 * A run with a pty has a *screen*, not a log.
 *
 * `docker logs` on a TTY container returns the raw pty stream, and the
 * read-only viewer renders lines with an SGR colour parser. Pointed at that
 * stream it printed the escape sequences verbatim and collapsed the spacing, so
 * a healthy agent looked like corruption — words run together, a spinner drawn
 * one dot per line. This pins that it says what it is instead.
 */
const API = process.env.NEXT_PUBLIC_SANDBOX_API ?? "http://localhost:8787";
const TOK = process.env.SANDBOX_STUDIO_TOKEN;

test("a pty run's Terminal tab does not print escape sequences", async ({
  page,
  request,
}) => {
  let id: string | undefined;
  const res = await request.get(`${API}/v1/runs`, {
    headers: TOK ? { authorization: `Bearer ${TOK}` } : undefined,
  });
  if (res.ok()) {
    const runs = (await res.json())?.runs ?? [];
    id = runs.find(
      (r: { tty: boolean; state: string }) => r.tty && r.state === "running",
    )?.id;
  }
  test.skip(!id, "no pty run to look at");

  if (TOK) {
    await page.goto("/");
    await page.evaluate(
      (t) => localStorage.setItem("sandbox-studio-token", t),
      TOK,
    );
  }

  await page.goto(`/runs/${id}`);
  await page.waitForLoadState("networkidle");
  await page.getByRole("tab", { name: "Terminal" }).click();
  await page.waitForTimeout(2000);

  const body = await page.locator("body").innerText();
  console.log("SAYS SCREEN:", body.includes("terminal, not a log"));

  // The escape character itself, built by code so this file stays plain text.
  const esc = String.fromCharCode(27);
  const leaked = body.includes(esc) || body.includes("[?25l") || body.includes("[?1049h");
  console.log("RAW ESCAPES ON SCREEN:", leaked);
  expect(leaked, "a pty stream must not be rendered as log lines").toBe(false);
});
