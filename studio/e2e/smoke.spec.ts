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
 *
 * Run it twice — once with the daemon down and once with it up. They catch
 * different things: fixture mode exercises rendering, and live mode is the only
 * place a contract mismatch between the two halves of Studio can appear. Every
 * such bug found so far was invisible in fixture mode, because the fixtures are
 * written to the shape the components want rather than the shape the daemon
 * sends.
 */
const API = process.env.NEXT_PUBLIC_SANDBOX_API ?? "http://localhost:8787";

/** Static routes: everything that needs no id. */
const routes = [
  "/",
  "/runs",
  "/agents",
  "/worktrees",
  "/launch",
  "/settings",
  "/settings/doctor",
];

function watch(page: import("@playwright/test").Page) {
  const errors: string[] = [];
  const failed: string[] = [];

  page.on("console", (m) => {
    if (m.type() !== "error") return;
    if (m.text().startsWith("Failed to load resource")) return; // carries no URL
    const at = m.location();
    const where = at.url
      ? ` [${at.url}:${at.lineNumber}:${at.columnNumber}]`
      : "";
    errors.push(m.text() + where);
  });
  page.on("pageerror", (e) => errors.push(`${e.name}: ${e.message}`));
  page.on("requestfailed", (r) => {
    // The health probe failing is not a failure — it is how the UI *detects*
    // that no daemon is running and switches to fixtures. Counting it would
    // make this suite unable to pass in the one mode it must also cover.
    // Everything else that cannot connect still counts, including a CORS block,
    // which never reaches the response listener at all.
    if (r.url().endsWith("/v1/health")) return;
    failed.push(`${r.failure()?.errorText ?? "failed"} ${r.url()}`);
  });
  page.on("response", (r) => {
    // 501 from the history endpoint is an answer, not a fault: it is how a
    // daemon started without -history-db says it has no index, and the
    // dashboard falls back to counting audit records itself. The index is
    // optional by design, so the suite has to pass in both modes — but the
    // exemption is this one route and this one code, because a 501 anywhere
    // else would be a route that was never finished.
    if (r.status() === 501 && new URL(r.url()).pathname === "/v1/stats/history")
      return;
    if (r.status() >= 400) failed.push(`${r.status()} ${r.url()}`);
  });

  return { errors, failed };
}

for (const path of routes) {
  test(`${path} renders cleanly`, async ({ page }) => {
    const { errors, failed } = watch(page);
    await page.goto(path);
    await page.waitForLoadState("networkidle");
    expect(errors, `JS errors on ${path}`).toEqual([]);
    expect(failed, `failed requests on ${path}`).toEqual([]);
  });
}

/**
 * The worktree detail assembles three sources — git for the branch and its
 * commits, docker for the runs — so it is the route most likely to break when
 * any one of them changes shape. Skipped when there is no worktree, for the same
 * reason run detail is: an empty machine is not a broken one.
 */
test("/worktrees/[branch] renders cleanly", async ({ page, request }) => {
  let branch: string | undefined;
  try {
    const res = await request.get(`${API}/v1/worktrees`);
    if (res.ok()) branch = (await res.json())?.worktrees?.[0]?.branch;
  } catch {
    // No daemon; the skip below covers it.
  }
  test.skip(!branch, "no worktree to open");

  const { errors, failed } = watch(page);
  await page.goto(`/worktrees/${encodeURIComponent(branch!)}`);
  await page.waitForLoadState("networkidle");
  expect(errors, `JS errors on /worktrees/${branch}`).toEqual([]);
  expect(failed, `failed requests on /worktrees/${branch}`).toEqual([]);
});

/**
 * Run detail needs a real id, and it is the route that reads the most of the
 * contract — network, security, mounts, logs, metrics, diff, config are all on
 * this one screen. Skipped rather than failed when there is no run to open: an
 * empty machine is not a broken one, and a test that invented an id would only
 * ever exercise the not-found path.
 */
test("/runs/[id] renders cleanly", async ({ page, request }) => {
  let id: string | undefined;
  try {
    const res = await request.get(`${API}/v1/runs?all=1`);
    if (res.ok()) {
      id = (await res.json())?.runs?.[0]?.id;
    }
  } catch {
    // No daemon. Covered by the skip below.
  }
  test.skip(!id, "no run to open — start one, or run this with the daemon up");

  const { errors, failed } = watch(page);
  await page.goto(`/runs/${id}`);
  await page.waitForLoadState("networkidle");
  expect(errors, `JS errors on /runs/${id}`).toEqual([]);
  expect(failed, `failed requests on /runs/${id}`).toEqual([]);
});
