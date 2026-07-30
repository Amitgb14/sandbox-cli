import { test, expect } from "@playwright/test";

/**
 * Every route renders without the browser complaining.
 *
 * Two assertions, kept apart on purpose. A JavaScript error means the page is
 * broken — a hydration mismatch, or reading a field the daemon never sent. A
 * failed request means something the page asked for was not there, which is
 * usually an endpoint with no server side yet. They fail for different reasons
 * and are fixed in different files, so one combined list makes the report
 * useless.
 *
 * The console's own "Failed to load resource" line is dropped, because it does
 * not name the URL. The response listener records status and URL instead, which
 * is the difference between "six 404s" and "six 404s on /v1/doctor".
 */
const routes = ["/", "/runs", "/agents", "/worktrees", "/launch", "/settings"];

for (const path of routes) {
  test(`${path} renders cleanly`, async ({ page }) => {
    const errors: string[] = [];
    const failed: string[] = [];

    page.on("console", (m) => {
      if (m.type() !== "error") return;
      if (m.text().startsWith("Failed to load resource")) return; // carries no URL
      // Location turns "a component did something wrong" into a file and line.
      const at = m.location();
      const where = at.url ? ` [${at.url}:${at.lineNumber}:${at.columnNumber}]` : "";
      errors.push(m.text() + where);
    });
    page.on("pageerror", (e) => errors.push(`${e.name}: ${e.message}`));
    page.on("requestfailed", (r) =>
      failed.push(`${r.failure()?.errorText ?? "failed"} ${r.url()}`),
    );
    page.on("response", (r) => {
      if (r.status() >= 400) failed.push(`${r.status()} ${r.url()}`);
    });

    await page.goto(path);
    await page.waitForLoadState("networkidle");

    expect(errors, `JS errors on ${path}`).toEqual([]);
    expect(failed, `failed requests on ${path}`).toEqual([]);
  });
}
